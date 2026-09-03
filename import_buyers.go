package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// ─── domain types ───────────────────────────────────────────────────────────

type importCSVRow struct {
	RowRef      string
	BuyerName   string
	Phone       string
	Email       string
	PlotNumber  string
	Estate      string
	AgentName   string
	PaymentPlan string
}

type overrideRow struct {
	SourceRowRef string
	BuyerName    string
	Phone        string
	Email        string
	AgentName    string
	PaymentPlan  string
	TargetEstate string
	TargetPlots  []string
}

type dbEstate struct {
	ID   int
	Name string
}

type dbPlot struct {
	ID         int
	EstateID   int
	PlotNumber string
}

// dbSale represents a prop_sales row for a plot that was sold through some
// route that never created a prop_bookings row (legacy/historical sales).
type dbSale struct {
	ID          int
	PlotID      int
	BuyerName   string
	BuyerPhone  string
	BuyerEmail  string
	AgentName   string
	PaymentPlan string
	PlotNumber  string
	EstateName  string
	DepositDoc  string
	IDDoc       string
	KRADoc      string
	Passport    string
	ZohoBooksID string
	ZohoCRMID   string
}

type dbBooking struct {
	ID            int
	PlotID        int
	EstateID      int
	Status        string
	BuyerName     string
	BuyerPhone    string
	BuyerEmail    string
	AgentName     string
	PaymentPlan   string
	PlotNumber    string
	EstateName    string
	DepositRef    string
	IDPhoto       string
	KRA           string
	PassportPhoto string
	SaleAgreement string
	ZohoBooksID   string
	ZohoCRMID     string
}

// estateAliases maps a normalized (uppercase, whitespace-collapsed) Excel
// estate name to the normalized DB estate name, for known spelling drift.
var estateAliases = map[string]string{
	"WEST VIEW GARDENS PHASE 1": "WEST VIEW GARDEN PHASE 1",
}

func normEstate(s string) string {
	return strings.Join(strings.Fields(strings.ToUpper(strings.TrimSpace(s))), " ")
}

func normPlot(s string) string {
	return strings.TrimSpace(s)
}

// normalizePaymentPlan converts free-text plan descriptions like
// "12 MONTHS PLAN" or "7 YEARS" into the app's slug format ("12_months",
// "7_years") so they display correctly in the existing payment-plan dropdown
// wherever the number matches an actual option, and stay consistently
// formatted even where it doesn't (e.g. "36_months" has no dropdown option
// yet, but is still stored in the same style as the ones that do).
func normalizePaymentPlan(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)
	upper = strings.TrimSuffix(strings.TrimSpace(upper), "PLAN")
	upper = strings.TrimSpace(upper)
	fields := strings.Fields(upper)
	if len(fields) == 2 {
		n := fields[0]
		unit := strings.ToLower(fields[1])
		switch unit {
		case "months", "month":
			return n + "_months"
		case "years", "year":
			return n + "_years"
		}
	}
	// Unrecognized shape (doesn't parse as "N unit") — fall back to a
	// lowercased, underscore-joined slug of the original text rather than
	// dropping it silently.
	return strings.ToLower(strings.Join(strings.Fields(s), "_"))
}

// ─── entry point ────────────────────────────────────────────────────────────

func runImportBuyers(args []string) {
	fs := flag.NewFlagSet("import-buyers", flag.ExitOnError)
	csvPath := fs.String("csv", "", "path to buyer data CSV (row_ref,buyer_name,phone,email,plot_number,estate,agent_name)")
	overridesPath := fs.String("overrides", "", "path to overrides CSV (source_row_ref,buyer_name,phone,email,agent_name,target_estate,target_plot_numbers)")
	excludePath := fs.String("exclude", "", "path to a single-column CSV of row_refs to drop entirely from normal matching (e.g. a duplicate/wrong sibling row already resolved via a different row)")
	zohoSweep := fs.String("zoho-sweep", "", "comma-separated prop_bookings statuses (e.g. sa_signed) to sweep DB-wide for fresh Zoho Books/CRM records, using current DB buyer data — independent of CSV matching, covers every row at that status")
	salesOnlyZoho := fs.Bool("sales-only-zoho", false, "also create Zoho Books/CRM records for legacy prop_sales-only matches (default: DB fields only, no Zoho, since these plots never had a prop_bookings row)")
	dryRun := fs.Bool("dry-run", false, "compute matches and print a report only; no DB or Zoho writes")
	resumeLogPath := fs.String("resume-log", "", "path to a log file tracking completed booking IDs; re-running skips rows already logged done")
	fs.Parse(args)

	if *csvPath == "" && *zohoSweep == "" {
		log.Fatal("[import-buyers] at least one of -csv or -zoho-sweep is required")
	}

	var rows []importCSVRow
	var err error
	if *csvPath != "" {
		rows, err = loadImportCSV(*csvPath)
		if err != nil {
			log.Fatalf("[import-buyers] loading csv: %v", err)
		}
	}
	var overrides []overrideRow
	if *overridesPath != "" {
		overrides, err = loadOverridesCSV(*overridesPath)
		if err != nil {
			log.Fatalf("[import-buyers] loading overrides: %v", err)
		}
	}
	excluded := map[string]string{} // row_ref -> reason
	if *excludePath != "" {
		excluded, err = loadExcludeCSV(*excludePath)
		if err != nil {
			log.Fatalf("[import-buyers] loading exclude list: %v", err)
		}
	}

	estates, err := loadEstates()
	if err != nil {
		log.Fatalf("[import-buyers] loading estates: %v", err)
	}
	plots, err := loadPlots()
	if err != nil {
		log.Fatalf("[import-buyers] loading plots: %v", err)
	}

	estateByNorm := map[string]dbEstate{}
	for _, e := range estates {
		estateByNorm[normEstate(e.Name)] = e
	}
	plotByKey := map[string]dbPlot{} // key: estateID|plotNumber
	for _, p := range plots {
		plotByKey[plotKey(p.EstateID, p.PlotNumber)] = p
	}

	overrideSourceRows := map[string]bool{}
	for _, o := range overrides {
		overrideSourceRows[o.SourceRowRef] = true
	}
	for rowRef := range excluded {
		overrideSourceRows[rowRef] = true
	}

	// ── build the list of (target estate/plot, buyer fields, source row) to process ──

	type resolved struct {
		SourceRowRef string
		BuyerName    string
		Phone        string
		Email        string
		AgentName    string
		PaymentPlan  string
		Estate       dbEstate
		Plot         dbPlot
	}

	var toResolve []resolved
	var skipped []string

	// Collision check within the CSV itself (excluding override source rows).
	seenPairs := map[string][]string{}
	for _, r := range rows {
		if overrideSourceRows[r.RowRef] {
			continue
		}
		if r.PlotNumber == "" {
			continue
		}
		key := normEstate(r.Estate) + "|" + normPlot(r.PlotNumber)
		seenPairs[key] = append(seenPairs[key], r.RowRef)
	}
	var collisions []string
	for key, refs := range seenPairs {
		if len(refs) > 1 {
			collisions = append(collisions, fmt.Sprintf("%s -> rows %s", key, strings.Join(refs, ",")))
		}
	}
	if len(collisions) > 0 {
		log.Fatalf("[import-buyers] %d unresolved (estate,plot) collision(s) in the CSV — add an override or fix the source file:\n  %s",
			len(collisions), strings.Join(collisions, "\n  "))
	}

	for _, r := range rows {
		if overrideSourceRows[r.RowRef] {
			continue // handled via overrides below
		}
		if r.PlotNumber == "" {
			skipped = append(skipped, fmt.Sprintf("row %s (%s): no plot number", r.RowRef, r.BuyerName))
			continue
		}
		estKey := normEstate(r.Estate)
		if alias, ok := estateAliases[estKey]; ok {
			estKey = alias
		}
		est, ok := estateByNorm[estKey]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("row %s (%s): estate %q not found in DB", r.RowRef, r.BuyerName, r.Estate))
			continue
		}
		plot, ok := plotByKey[plotKey(est.ID, normPlot(r.PlotNumber))]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("row %s (%s): plot %q not found in estate %q", r.RowRef, r.BuyerName, r.PlotNumber, est.Name))
			continue
		}
		toResolve = append(toResolve, resolved{
			SourceRowRef: r.RowRef, BuyerName: r.BuyerName, Phone: cleanPhone(r.Phone),
			Email: strings.TrimSpace(r.Email), AgentName: strings.TrimSpace(r.AgentName),
			PaymentPlan: normalizePaymentPlan(r.PaymentPlan),
			Estate:      est, Plot: plot,
		})
	}

	for rowRef, reason := range excluded {
		skipped = append(skipped, fmt.Sprintf("row %s: excluded (%s)", rowRef, reason))
	}

	for _, o := range overrides {
		estKey := normEstate(o.TargetEstate)
		if alias, ok := estateAliases[estKey]; ok {
			estKey = alias
		}
		est, ok := estateByNorm[estKey]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("override row %s (%s): target estate %q not found in DB", o.SourceRowRef, o.BuyerName, o.TargetEstate))
			continue
		}
		for _, pn := range o.TargetPlots {
			plot, ok := plotByKey[plotKey(est.ID, normPlot(pn))]
			if !ok {
				skipped = append(skipped, fmt.Sprintf("override row %s (%s): target plot %q not found in estate %q", o.SourceRowRef, o.BuyerName, pn, est.Name))
				continue
			}
			toResolve = append(toResolve, resolved{
				SourceRowRef: o.SourceRowRef + "(override)", BuyerName: o.BuyerName, Phone: cleanPhone(o.Phone),
				Email: strings.TrimSpace(o.Email), AgentName: strings.TrimSpace(o.AgentName),
				PaymentPlan: normalizePaymentPlan(o.PaymentPlan),
				Estate:      est, Plot: plot,
			})
		}
	}

	// ── resolve each target plot to its current prop_bookings row ──

	var toApply []struct {
		resolved
		Booking dbBooking
	}
	var toApplySalesOnly []struct {
		resolved
		Sale dbSale
	}
	for _, r := range toResolve {
		b, ok, err := findCurrentBooking(r.Plot.ID)
		if err != nil {
			log.Fatalf("[import-buyers] querying booking for plot %d: %v", r.Plot.ID, err)
		}
		if ok {
			toApply = append(toApply, struct {
				resolved
				Booking dbBooking
			}{r, b})
			continue
		}

		// No prop_bookings row — check for a legacy sale record instead
		// (plot sold before the app tracked bookings; buyer lives only in prop_sales).
		s, ok, err := findCurrentSaleOnly(r.Plot.ID)
		if err != nil {
			log.Fatalf("[import-buyers] querying sale for plot %d: %v", r.Plot.ID, err)
		}
		if ok {
			toApplySalesOnly = append(toApplySalesOnly, struct {
				resolved
				Sale dbSale
			}{r, s})
			continue
		}

		skipped = append(skipped, fmt.Sprintf("row %s (%s): no active/sa_signed/completed booking or sale record for %s plot %s",
			r.SourceRowRef, r.BuyerName, r.Estate.Name, r.Plot.PlotNumber))
	}

	// ── Zoho sweep: every prop_bookings row at the given status(es), DB-wide,
	// gets fresh Books/CRM records using its current buyer data (already
	// corrected above for any row the CSV matched). Rows already picked up
	// by CSV matching are skipped here to avoid double-processing.
	if *zohoSweep != "" {
		alreadyApplied := map[int]bool{}
		for _, r := range toApply {
			alreadyApplied[r.Booking.ID] = true
		}
		statuses := strings.Split(*zohoSweep, ",")
		for i := range statuses {
			statuses[i] = strings.TrimSpace(statuses[i])
		}
		sweepBookings, err := loadBookingsByStatus(statuses)
		if err != nil {
			log.Fatalf("[import-buyers] loading sweep bookings: %v", err)
		}
		sweepAdded := 0
		for _, b := range sweepBookings {
			if alreadyApplied[b.ID] {
				continue
			}
			toApply = append(toApply, struct {
				resolved
				Booking dbBooking
			}{
				resolved{
					SourceRowRef: "sweep",
					Estate:       dbEstate{ID: b.EstateID, Name: b.EstateName},
					Plot:         dbPlot{ID: b.PlotID, EstateID: b.EstateID, PlotNumber: b.PlotNumber},
				},
				b,
			})
			sweepAdded++
		}
		log.Printf("[import-buyers] zoho-sweep(%s): %d row(s) already covered by CSV matching, %d additional row(s) added for Zoho-only recreation",
			strings.Join(statuses, ","), len(sweepBookings)-sweepAdded, sweepAdded)
	}

	log.Printf("[import-buyers] resolved %d booking row(s) + %d sales-only row(s) to apply, %d row(s) skipped",
		len(toApply), len(toApplySalesOnly), len(skipped))
	for _, s := range skipped {
		log.Printf("[import-buyers] SKIP: %s", s)
	}

	// ── resume log ──
	done := map[int]bool{}
	var resumeFile *os.File
	if *resumeLogPath != "" {
		done = loadResumeLog(*resumeLogPath)
		f, err := os.OpenFile(*resumeLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("[import-buyers] opening resume log: %v", err)
		}
		defer f.Close()
		resumeFile = f
	}

	fmt.Printf("\nrow_ref,booking_id,status,estate,plot,old_buyer_name,new_buyer_name,old_phone,new_phone,old_email,new_email,old_agent,new_agent,old_payment_plan,new_payment_plan,books_action,crm_action\n")
	for _, r := range toApply {
		fmt.Printf("%s,%d,%s,%s,%s,%q,%q,%q,%q,%q,%q,%q,%q,%q,%q,%s,%s\n",
			r.SourceRowRef, r.Booking.ID, r.Booking.Status, r.Estate.Name, r.Plot.PlotNumber,
			r.Booking.BuyerName, firstNonEmpty(r.BuyerName, r.Booking.BuyerName),
			r.Booking.BuyerPhone, firstNonEmpty(r.Phone, r.Booking.BuyerPhone),
			r.Booking.BuyerEmail, firstNonEmpty(r.Email, r.Booking.BuyerEmail),
			r.Booking.AgentName, firstNonEmpty(r.AgentName, r.Booking.AgentName),
			r.Booking.PaymentPlan, firstNonEmpty(r.PaymentPlan, r.Booking.PaymentPlan),
			booksAction(r.Booking), crmAction(r.Booking),
		)
	}
	for _, r := range toApplySalesOnly {
		fmt.Printf("%s,sale:%d,sold(no booking row),%s,%s,%q,%q,%q,%q,%q,%q,%q,%q,%q,%q,%s,%s\n",
			r.SourceRowRef, r.Sale.ID, r.Estate.Name, r.Plot.PlotNumber,
			r.Sale.BuyerName, firstNonEmpty(r.BuyerName, r.Sale.BuyerName),
			r.Sale.BuyerPhone, firstNonEmpty(r.Phone, r.Sale.BuyerPhone),
			r.Sale.BuyerEmail, firstNonEmpty(r.Email, r.Sale.BuyerEmail),
			r.Sale.AgentName, firstNonEmpty(r.AgentName, r.Sale.AgentName),
			r.Sale.PaymentPlan, firstNonEmpty(r.PaymentPlan, r.Sale.PaymentPlan),
			saleBooksAction(r.Sale, *salesOnlyZoho), saleCRMAction(r.Sale, *salesOnlyZoho),
		)
	}

	if *dryRun {
		log.Printf("[import-buyers] dry-run complete — no DB or Zoho changes made")
		return
	}

	successCount, failCount := 0, 0
	for _, r := range toApply {
		if done[r.Booking.ID] {
			log.Printf("[import-buyers] booking %d already processed per resume log — skipping", r.Booking.ID)
			continue
		}
		if err := applyOne(r.Booking, r.BuyerName, r.Phone, r.Email, r.AgentName, r.PaymentPlan); err != nil {
			failCount++
			log.Printf("[import-buyers] FAILED booking %d (row %s, %s): %v", r.Booking.ID, r.SourceRowRef, r.BuyerName, err)
			if resumeFile != nil {
				fmt.Fprintf(resumeFile, "%d,failed,%s\n", r.Booking.ID, err)
			}
			continue
		}
		successCount++
		log.Printf("[import-buyers] OK booking %d (row %s, %s)", r.Booking.ID, r.SourceRowRef, r.BuyerName)
		if resumeFile != nil {
			fmt.Fprintf(resumeFile, "%d,done,\n", r.Booking.ID)
		}
	}

	salesOK, salesFail := 0, 0
	for _, r := range toApplySalesOnly {
		if err := applySaleOnly(r.Sale, r.BuyerName, r.Phone, r.Email, r.AgentName, r.PaymentPlan, *salesOnlyZoho); err != nil {
			salesFail++
			log.Printf("[import-buyers] FAILED sale %d (row %s, %s): %v", r.Sale.ID, r.SourceRowRef, r.BuyerName, err)
			continue
		}
		salesOK++
		log.Printf("[import-buyers] OK sale %d (row %s, %s) — DB only, no Zoho", r.Sale.ID, r.SourceRowRef, r.BuyerName)
	}

	log.Printf("[import-buyers] finished: %d booking(s) succeeded, %d failed; %d sale(s) succeeded, %d failed; %d skipped up front",
		successCount, failCount, salesOK, salesFail, len(skipped))
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func plotKey(estateID int, plotNumber string) string {
	return fmt.Sprintf("%d|%s", estateID, plotNumber)
}

// cleanPhone strips a stray leading `"` artifact seen in some cells (Excel
// forced-text marker) and trims to fit buyer_phone VARCHAR(20).
func cleanPhone(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, `"`)
	if len(s) > 20 {
		s = s[:20]
	}
	return s
}

// ─── DB loading ─────────────────────────────────────────────────────────────

func loadEstates() ([]dbEstate, error) {
	rows, err := db.Query(`SELECT id, name FROM prop_estates`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbEstate
	for rows.Next() {
		var e dbEstate
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func loadPlots() ([]dbPlot, error) {
	rows, err := db.Query(`SELECT id, estate_id, plot_number FROM prop_plots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbPlot
	for rows.Next() {
		var p dbPlot
		if err := rows.Scan(&p.ID, &p.EstateID, &p.PlotNumber); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func findCurrentBooking(plotID int) (dbBooking, bool, error) {
	var b dbBooking
	b.PlotID = plotID
	err := db.QueryRow(`
		SELECT b.id, b.estate_id, b.status, b.buyer_name, COALESCE(b.buyer_phone,''),
		       COALESCE(b.buyer_email,''), COALESCE(b.agent_name,''), COALESCE(b.payment_plan,''), p.plot_number, e.name,
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,''), COALESCE(b.sale_agreement,''),
		       COALESCE(b.zoho_books_id,''), COALESCE(b.zoho_crm_id,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.plot_id = ? AND b.status IN ('active','sa_signed','completed')
		ORDER BY b.id DESC LIMIT 1`, plotID).
		Scan(&b.ID, &b.EstateID, &b.Status, &b.BuyerName, &b.BuyerPhone, &b.BuyerEmail, &b.AgentName, &b.PaymentPlan, &b.PlotNumber, &b.EstateName,
			&b.DepositRef, &b.IDPhoto, &b.KRA, &b.PassportPhoto, &b.SaleAgreement, &b.ZohoBooksID, &b.ZohoCRMID)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return dbBooking{}, false, nil
		}
		return dbBooking{}, false, err
	}
	return b, true, nil
}

// loadBookingsByStatus returns every prop_bookings row at the given
// status(es), DB-wide — used by the Zoho sweep to recreate records for every
// booking at a status, not just ones matched from a CSV.
func loadBookingsByStatus(statuses []string) ([]dbBooking, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, s := range statuses {
		placeholders[i] = "?"
		args[i] = s
	}
	query := fmt.Sprintf(`
		SELECT b.id, b.plot_id, b.estate_id, b.status, b.buyer_name, COALESCE(b.buyer_phone,''),
		       COALESCE(b.buyer_email,''), COALESCE(b.agent_name,''), COALESCE(b.payment_plan,''), p.plot_number, e.name,
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,''), COALESCE(b.sale_agreement,''),
		       COALESCE(b.zoho_books_id,''), COALESCE(b.zoho_crm_id,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status IN (%s)`, strings.Join(placeholders, ","))
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbBooking
	for rows.Next() {
		var b dbBooking
		if err := rows.Scan(&b.ID, &b.PlotID, &b.EstateID, &b.Status, &b.BuyerName, &b.BuyerPhone,
			&b.BuyerEmail, &b.AgentName, &b.PaymentPlan, &b.PlotNumber, &b.EstateName,
			&b.DepositRef, &b.IDPhoto, &b.KRA, &b.PassportPhoto, &b.SaleAgreement,
			&b.ZohoBooksID, &b.ZohoCRMID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// findCurrentSaleOnly handles plots that were sold through some route that
// never created a prop_bookings row (legacy/historical sales) — the only
// buyer record for them lives directly in prop_sales.
func findCurrentSaleOnly(plotID int) (dbSale, bool, error) {
	var s dbSale
	s.PlotID = plotID
	err := db.QueryRow(`
		SELECT s.id, s.buyer_name, COALESCE(s.buyer_phone,''), COALESCE(s.buyer_email,''),
		       COALESCE(s.agent_name,''), COALESCE(s.payment_plan,''), p.plot_number, e.name,
		       COALESCE(s.deposit_doc,''), COALESCE(s.id_doc,''), COALESCE(s.kra_doc,''), COALESCE(s.passport_photo,''),
		       COALESCE(s.zoho_books_id,''), COALESCE(s.zoho_crm_id,'')
		FROM prop_sales s
		JOIN prop_plots p ON p.id = s.plot_id
		JOIN prop_estates e ON e.id = s.estate_id
		WHERE s.plot_id = ? AND p.status = 'sold'
		ORDER BY s.id DESC LIMIT 1`, plotID).
		Scan(&s.ID, &s.BuyerName, &s.BuyerPhone, &s.BuyerEmail, &s.AgentName, &s.PaymentPlan, &s.PlotNumber, &s.EstateName,
			&s.DepositDoc, &s.IDDoc, &s.KRADoc, &s.Passport, &s.ZohoBooksID, &s.ZohoCRMID)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return dbSale{}, false, nil
		}
		return dbSale{}, false, err
	}
	return s, true, nil
}

// applySaleOnly corrects buyer/agent fields directly on a prop_sales row for
// a plot with no prop_bookings row. DB-only — no Zoho records are created for
// these (historical sales, confirmed with user).
func applySaleOnly(s dbSale, buyerName, phone, email, agentName, paymentPlan string, createZoho bool) error {
	newName := firstNonEmpty(buyerName, s.BuyerName)
	newPhone := firstNonEmpty(phone, s.BuyerPhone)
	newEmail := firstNonEmpty(email, s.BuyerEmail)
	newAgent := firstNonEmpty(agentName, s.AgentName)
	newPlan := firstNonEmpty(paymentPlan, s.PaymentPlan)
	if _, err := db.Exec(`UPDATE prop_sales SET buyer_name=?, buyer_phone=?, buyer_email=?, agent_name=?, payment_plan=? WHERE id=?`,
		newName, newPhone, newEmail, newAgent, newPlan, s.ID); err != nil {
		return fmt.Errorf("update prop_sales: %w", err)
	}
	if !createZoho {
		return nil
	}

	bi := bookingInfo{
		PlotIDs:       []int{s.PlotID},
		PlotNumbers:   []string{s.PlotNumber},
		BuyerName:     newName,
		BuyerPhone:    newPhone,
		BuyerEmail:    newEmail,
		AgentName:     newAgent,
		PaymentPlan:   newPlan,
		EstateName:    s.EstateName,
		DepositRef:    s.DepositDoc,
		IDPhoto:       s.IDDoc,
		KRA:           s.KRADoc,
		PassportPhoto: s.Passport,
	}

	booksID := s.ZohoBooksID
	if booksID != "" {
		log.Printf("[import-buyers] sale %d: already has Books estimate %s — skipping Books creation", s.ID, booksID)
	} else {
		var err error
		booksID, err = createBooksRecord(bi)
		if err != nil {
			return fmt.Errorf("zoho books: %w", err)
		}
		if _, err := db.Exec(`UPDATE prop_sales SET zoho_books_id=? WHERE id=?`, booksID, s.ID); err != nil {
			return fmt.Errorf("save zoho_books_id: %w", err)
		}
	}

	if s.ZohoCRMID != "" {
		log.Printf("[import-buyers] sale %d: already has CRM deal %s — skipping CRM creation", s.ID, s.ZohoCRMID)
		return nil
	}
	dealID, err := createCRMDeal(bi, booksID)
	if err != nil {
		return fmt.Errorf("zoho crm (books estimate %s was created): %w", booksID, err)
	}
	if _, err := db.Exec(`UPDATE prop_sales SET zoho_crm_id=? WHERE id=?`, dealID, s.ID); err != nil {
		return fmt.Errorf("save zoho_crm_id: %w", err)
	}
	for _, f := range splitFiles(bi.DepositRef, bi.IDPhoto, bi.KRA, bi.PassportPhoto) {
		if err := uploadCRMAttachment(dealID, f); err != nil {
			log.Printf("[import-buyers] sale %d: CRM attachment %q upload failed (non-fatal): %v", s.ID, f, err)
		}
	}
	return nil
}

// saleBooksAction and saleCRMAction mirror booksAction/crmAction for
// prop_sales-only rows, for the dry-run report.
func saleBooksAction(s dbSale, createZoho bool) string {
	if !createZoho {
		return "n/a (legacy sale, no Zoho by design)"
	}
	if s.ZohoBooksID != "" {
		return "skip (already has " + s.ZohoBooksID + ")"
	}
	return "CREATE"
}

func saleCRMAction(s dbSale, createZoho bool) string {
	if !createZoho {
		return "n/a (legacy sale, no Zoho by design)"
	}
	if s.ZohoCRMID != "" {
		return "skip (already has " + s.ZohoCRMID + ")"
	}
	return "CREATE"
}

// ─── per-row apply ──────────────────────────────────────────────────────────

// booksAction and crmAction preview what applyOne will actually do for a
// given booking — mirroring its skip-if-already-present logic — so the
// dry-run report shows exactly which rows are missing from Books/CRM instead
// of requiring that to be tracked manually in the source spreadsheet.
func booksAction(b dbBooking) string {
	if b.ZohoBooksID != "" {
		return "skip (already has " + b.ZohoBooksID + ")"
	}
	return "CREATE"
}

func crmAction(b dbBooking) string {
	if b.Status != "sa_signed" && b.Status != "completed" {
		return "n/a (status=" + b.Status + ")"
	}
	if b.ZohoCRMID != "" {
		return "skip (already has " + b.ZohoCRMID + ")"
	}
	return "CREATE"
}

func applyOne(b dbBooking, buyerName, phone, email, agentName, paymentPlan string) error {
	newName := firstNonEmpty(buyerName, b.BuyerName)
	newPhone := firstNonEmpty(phone, b.BuyerPhone)
	newEmail := firstNonEmpty(email, b.BuyerEmail)
	newAgent := firstNonEmpty(agentName, b.AgentName)
	newPlan := firstNonEmpty(paymentPlan, b.PaymentPlan)

	if _, err := db.Exec(`UPDATE prop_bookings SET buyer_name=?, buyer_phone=?, buyer_email=?, agent_name=?, payment_plan=? WHERE id=?`,
		newName, newPhone, newEmail, newAgent, newPlan, b.ID); err != nil {
		return fmt.Errorf("update prop_bookings: %w", err)
	}

	if b.Status == "completed" {
		if _, err := db.Exec(`UPDATE prop_sales SET buyer_name=?, buyer_phone=?, buyer_email=?, agent_name=?, payment_plan=? WHERE plot_id=?`,
			newName, newPhone, newEmail, newAgent, newPlan, b.PlotID); err != nil {
			log.Printf("[import-buyers] booking %d: prop_sales sync failed (non-fatal): %v", b.ID, err)
		}
	}

	bi := bookingInfo{
		PlotIDs:       []int{b.PlotID},
		PlotNumbers:   []string{b.PlotNumber},
		BuyerName:     newName,
		BuyerPhone:    newPhone,
		BuyerEmail:    newEmail,
		AgentName:     newAgent,
		PaymentPlan:   newPlan,
		EstateName:    b.EstateName,
		DepositRef:    b.DepositRef,
		IDPhoto:       b.IDPhoto,
		KRA:           b.KRA,
		PassportPhoto: b.PassportPhoto,
		SaleAgreement: b.SaleAgreement,
	}

	booksID := b.ZohoBooksID
	if booksID != "" {
		log.Printf("[import-buyers] booking %d: already has Books estimate %s — skipping Books creation", b.ID, booksID)
	} else {
		var err error
		booksID, err = createBooksRecord(bi)
		if err != nil {
			return fmt.Errorf("zoho books: %w", err)
		}
		if _, err := db.Exec(`UPDATE prop_bookings SET zoho_books_id=? WHERE id=?`, booksID, b.ID); err != nil {
			return fmt.Errorf("save zoho_books_id: %w", err)
		}
	}

	if b.Status == "sa_signed" || b.Status == "completed" {
		if b.ZohoCRMID != "" {
			log.Printf("[import-buyers] booking %d: already has CRM deal %s — skipping CRM creation", b.ID, b.ZohoCRMID)
			return nil
		}
		dealID, err := createCRMDeal(bi, booksID)
		if err != nil {
			return fmt.Errorf("zoho crm (books estimate %s was created): %w", booksID, err)
		}
		if _, err := db.Exec(`UPDATE prop_bookings SET zoho_crm_id=? WHERE id=?`, dealID, b.ID); err != nil {
			return fmt.Errorf("save zoho_crm_id: %w", err)
		}

		// Attach whatever documents already exist on this booking — mirrors
		// processSignedIntegrations' attachment step for the normal booking flow.
		for _, f := range splitFiles(bi.DepositRef, bi.IDPhoto, bi.KRA, bi.PassportPhoto, bi.SaleAgreement) {
			if err := uploadCRMAttachment(dealID, f); err != nil {
				log.Printf("[import-buyers] booking %d: CRM attachment %q upload failed (non-fatal): %v", b.ID, f, err)
			}
		}
	}

	return nil
}

// ─── resume log ─────────────────────────────────────────────────────────────

func loadResumeLog(path string) map[int]bool {
	done := map[int]bool{}
	f, err := os.Open(path)
	if err != nil {
		return done // doesn't exist yet — fine, nothing done so far
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, _ := r.ReadAll()
	for _, rec := range records {
		if len(rec) < 2 || rec[1] != "done" {
			continue
		}
		if id, err := strconv.Atoi(rec[0]); err == nil {
			done[id] = true
		}
	}
	return done
}

// ─── CSV loading ────────────────────────────────────────────────────────────

func loadImportCSV(path string) ([]importCSVRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("empty csv")
	}
	var out []importCSVRow
	for _, rec := range records[1:] { // skip header
		if len(rec) < 7 {
			continue
		}
		paymentPlan := ""
		if len(rec) > 7 {
			paymentPlan = strings.TrimSpace(rec[7])
		}
		out = append(out, importCSVRow{
			RowRef: strings.TrimSpace(rec[0]), BuyerName: strings.TrimSpace(rec[1]),
			Phone: strings.TrimSpace(rec[2]), Email: strings.TrimSpace(rec[3]),
			PlotNumber: strings.TrimSpace(rec[4]), Estate: strings.TrimSpace(rec[5]),
			AgentName: strings.TrimSpace(rec[6]), PaymentPlan: paymentPlan,
		})
	}
	return out, nil
}

func loadOverridesCSV(path string) ([]overrideRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("empty overrides csv")
	}
	var out []overrideRow
	for _, rec := range records[1:] {
		if len(rec) < 8 {
			continue
		}
		plots := strings.Split(rec[7], ",")
		for i := range plots {
			plots[i] = strings.TrimSpace(plots[i])
		}
		out = append(out, overrideRow{
			SourceRowRef: strings.TrimSpace(rec[0]), BuyerName: strings.TrimSpace(rec[1]),
			Phone: strings.TrimSpace(rec[2]), Email: strings.TrimSpace(rec[3]),
			AgentName: strings.TrimSpace(rec[4]), PaymentPlan: strings.TrimSpace(rec[5]),
			TargetEstate: strings.TrimSpace(rec[6]), TargetPlots: plots,
		})
	}
	return out, nil
}

// loadExcludeCSV reads a two-column CSV (row_ref,reason) of rows to drop
// entirely from normal matching (e.g. a duplicate/wrong sibling row already
// resolved via a different row for the same plot).
func loadExcludeCSV(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(records) == 0 {
		return out, nil
	}
	for _, rec := range records[1:] {
		if len(rec) < 1 || strings.TrimSpace(rec[0]) == "" {
			continue
		}
		reason := ""
		if len(rec) > 1 {
			reason = strings.TrimSpace(rec[1])
		}
		out[strings.TrimSpace(rec[0])] = reason
	}
	return out, nil
}
