// Package legal implements the Legal review module: bookings Accounts has
// approved and forwarded move through three stages — drafting the sale
// agreement, awaiting the client's signature, then completed (signed) — with
// Legal able to cancel (plot -> available) from either of the first two.
package legal

import (
	"database/sql"
	"fmt"
	"log"
	"maps"
	"net/http"
	"strings"
)

var (
	db                *sql.DB
	renderFn          func(w http.ResponseWriter, name string, data any)
	getAgentFn        func(r *http.Request) string
	getUserIDFn       func(r *http.Request) string
	isSystemAdminFn   func(r *http.Request) bool
	getRoleFn         func(r *http.Request) string
	hasWritePermFn    func(r *http.Request) bool
	saveUploadedFiles func(r *http.Request, fieldName string) string
	processSAIntegr   func(plotID int, plotNumber, estateName string)
	cancelBooks       func(estimateID string)
	sendOutcome       func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error
	sendSentForSigSMS func(bookingID string)
	sendSignedSMS     func(bookingID string)
)

// Init wires this package to the host app's shared dependencies. Two roles
// can reach /legal/* (agents track their own bookings' stage from their own
// My Bookings page instead — see bookingStageLabel in main.go):
//   - legal:  the assigned lawyer on an estate (prop_estates.lawyer_id) —
//     full access to that estate's bookings, can act on them.
//   - admin:  every booking, every estate — read-only oversight of what's
//     pending and at what stage, with the assigned lawyer shown per row, by
//     default. A system_admin can lift this per-admin via the "Legal
//     Module" write permission (Settings → Users → Permissions), letting a
//     specific admin also draft/send/upload/cancel — see hasWritePermFn.
//   - system_admin: everything, full access, same as legal.
func Init(
	d *sql.DB,
	render func(http.ResponseWriter, string, any),
	agentFn func(*http.Request) string,
	getUserID func(*http.Request) string,
	isSystemAdmin func(*http.Request) bool,
	getRole func(*http.Request) string,
	hasWritePerm func(r *http.Request) bool,
	saveFiles func(r *http.Request, fieldName string) string,
	processSignedIntegrations func(plotID int, plotNumber, estateName string),
	cancelBooksEstimate func(estimateID string),
	sendOutcomeEmail func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error,
	sendSentForSignatureSMS func(bookingID string),
	sendAgreementSignedSMS func(bookingID string),
) {
	db = d
	renderFn = render
	getAgentFn = agentFn
	getUserIDFn = getUserID
	isSystemAdminFn = isSystemAdmin
	getRoleFn = getRole
	hasWritePermFn = hasWritePerm
	saveUploadedFiles = saveFiles
	processSAIntegr = processSignedIntegrations
	cancelBooks = cancelBooksEstimate
	sendOutcome = sendOutcomeEmail
	sendSentForSigSMS = sendSentForSignatureSMS
	sendSignedSMS = sendAgreementSignedSMS
}

// scopeQuery narrows a queue query to what the current viewer is allowed to
// see: a lawyer sees only their assigned estates' bookings; admin/
// system_admin see everything (admin is read-only oversight — see
// isAssignedLawyer's use as the action gate below).
func scopeQuery(r *http.Request, query string, args []any) (string, []any) {
	if getRoleFn(r) == "legal" {
		return query + ` AND e.lawyer_id = ?`, append(args, getUserIDFn(r))
	}
	return query, args // admin, system_admin
}

// isAssignedLawyer reports whether the current user IS the estate's
// assigned lawyer — either they're system_admin, or the estate's
// lawyer_id matches their own user ID. An admin or agent's own ID never
// matches an estate's lawyer_id, so this alone naturally excludes them.
// Use canAct (below), not this, to gate the mutating handlers — it also
// admits an admin explicitly granted the "Legal Module" write permission.
func isAssignedLawyer(r *http.Request, estateID int) bool {
	if isSystemAdminFn(r) {
		return true
	}
	var lawyerID string
	if err := db.QueryRow(`SELECT COALESCE(lawyer_id,0) FROM prop_estates WHERE id=?`, estateID).Scan(&lawyerID); err != nil {
		return false
	}
	return lawyerID == getUserIDFn(r)
}

// canAct reports whether the current user may act on (not just view) a
// booking on the given estate — the assigned lawyer/system_admin (see
// isAssignedLawyer), or an admin a system_admin has explicitly granted the
// "Legal Module" write permission (e.g. so a specific admin can upload sale
// agreements directly, without becoming that estate's assigned lawyer).
// This is the action gate for the three mutating handlers below and for the
// CanAct template flag.
func canAct(r *http.Request, estateID int) bool {
	if isAssignedLawyer(r, estateID) {
		return true
	}
	return hasWritePermFn != nil && hasWritePermFn(r)
}

// canView reports whether the current viewer may see a booking on the given
// estate — the assigned lawyer, system_admin, or any admin (full oversight).
func canView(r *http.Request, estateID int) bool {
	switch getRoleFn(r) {
	case "system_admin", "admin":
		return true
	case "legal":
		return isAssignedLawyer(r, estateID)
	default:
		return false
	}
}

func renderLegal(w http.ResponseWriter, name string, data map[string]any) {
	renderFn(w, name, data)
}

// filterOption is a simple {ID, Name} pair for the filter-bar dropdowns.
type filterOption struct {
	ID   int
	Name string
}

// loadFilterEstates returns the estates selectable in the Estate filter —
// only the viewer's own assigned estates for a lawyer (matching what
// scopeQuery already restricts them to; showing every estate in the
// dropdown would just let them pick one that always returns nothing),
// every estate for admin/system_admin.
func loadFilterEstates(r *http.Request) []filterOption {
	query := `SELECT id, name FROM prop_estates`
	var args []any
	if getRoleFn(r) == "legal" {
		query += ` WHERE lawyer_id = ?`
		args = append(args, getUserIDFn(r))
	}
	query += ` ORDER BY name`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []filterOption
	for rows.Next() {
		var o filterOption
		if rows.Scan(&o.ID, &o.Name) == nil {
			out = append(out, o)
		}
	}
	return out
}

// loadFilterLawyers returns every Legal-role user, for the admin-only
// Lawyer filter dropdown — a lawyer viewing their own queue never needs to
// filter by lawyer, since it's always just them.
func loadFilterLawyers() []filterOption {
	rows, err := db.Query(`SELECT id, name FROM prop_agents WHERE role='legal' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []filterOption
	for rows.Next() {
		var o filterOption
		if rows.Scan(&o.ID, &o.Name) == nil {
			out = append(out, o)
		}
	}
	return out
}

// applyListFilters appends the shared Estate/Lawyer/date-range filters (read
// from the request's query string) to a list query. dateCol is the column
// to range-filter on — "b.date_booked" for the two queues, "b.date_signed"
// for Completed. The lawyer filter is applied regardless of viewer role:
// for a lawyer it's redundant with scopeQuery's own restriction and fails
// safe to zero rows if mismatched (never broadens what they can see), so
// there's no need to special-case it away — only the UI hides the dropdown
// for non-admins.
func applyListFilters(r *http.Request, dateCol, query string, args []any) (string, []any) {
	q := r.URL.Query()
	if fEstate := q.Get("estate"); fEstate != "" {
		query += ` AND e.id = ?`
		args = append(args, fEstate)
	}
	if fLawyer := q.Get("lawyer"); fLawyer != "" {
		query += ` AND e.lawyer_id = ?`
		args = append(args, fLawyer)
	}
	if fFrom := q.Get("date_from"); fFrom != "" {
		query += ` AND DATE(` + dateCol + `) >= ?`
		args = append(args, fFrom)
	}
	if fTo := q.Get("date_to"); fTo != "" {
		query += ` AND DATE(` + dateCol + `) <= ?`
		args = append(args, fTo)
	}
	return query, args
}

// filterRenderData returns the common filter-bar fields every list template
// needs — current selections plus the dropdown options.
func filterRenderData(r *http.Request) map[string]any {
	q := r.URL.Query()
	return map[string]any{
		"Estates":          loadFilterEstates(r),
		"Lawyers":          loadFilterLawyers(),
		"ShowLawyerFilter": getRoleFn(r) == "admin" || getRoleFn(r) == "system_admin",
		"FEstate":          q.Get("estate"),
		"FLawyer":          q.Get("lawyer"),
		"FFrom":            q.Get("date_from"),
		"FTo":              q.Get("date_to"),
	}
}

func splitFiles(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func bookingIDFromPath(prefix, p string) string {
	s := strings.TrimPrefix(p, prefix)
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

// Router handles all /legal/* requests. Access is gated by role at the mux
// level in main.go (requireLegalAccess — legal, admin, agent, system_admin);
// what each of those actually gets to see/do is scoped inside this package
// (scopeQuery, canView, isAssignedLawyer).
func Router(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/legal" || p == "/legal/":
		http.Redirect(w, r, "/legal/queue", http.StatusFound)
	case p == "/legal/queue":
		queueHandler(w, r)
	case p == "/legal/awaiting-signature":
		awaitingSignatureHandler(w, r)
	case p == "/legal/completed":
		completedHandler(w, r)
	case p == "/legal/estates":
		estatesHandler(w, r)
	case strings.HasPrefix(p, "/legal/review/") && strings.HasSuffix(p, "/send-for-signature"):
		sendForSignatureHandler(w, r)
	case strings.HasPrefix(p, "/legal/review/") && strings.HasSuffix(p, "/upload-agreement"):
		uploadAgreementHandler(w, r)
	case strings.HasPrefix(p, "/legal/review/") && strings.HasSuffix(p, "/cancel"):
		cancelHandler(w, r)
	case strings.HasPrefix(p, "/legal/review/"):
		reviewDetailHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

type queueRow struct {
	BookingID  int
	BuyerName  string
	AgentName  string
	EstateName string
	PlotNumber string
	StageDate  string
	LawyerName string
}

// queueHandler lists stage 1: bookings Legal has just received from
// Accounts and has not yet drafted a sale agreement for. StageDate here is
// accounts_reviewed_at — when the booking actually moved into Legal's
// queue, not date_booked (which can be days/weeks earlier, back when the
// buyer first booked the plot).
func queueHandler(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(DATE_FORMAT(b.accounts_reviewed_at,'%d %b %Y %h:%i %p'),'—'), COALESCE(lw.name,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents lw ON lw.id = e.lawyer_id
		WHERE b.status = 'pending_wakili_review' AND b.legal_stage = 'drafting'`
	query, args := scopeQuery(r, query, nil)
	query, args = applyListFilters(r, "b.accounts_reviewed_at", query, args)
	query += ` ORDER BY b.accounts_reviewed_at ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("legal queue: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var queue []queueRow
	for rows.Next() {
		var q queueRow
		if err := rows.Scan(&q.BookingID, &q.BuyerName, &q.AgentName, &q.EstateName, &q.PlotNumber, &q.StageDate, &q.LawyerName); err == nil {
			queue = append(queue, q)
		}
	}

	data := map[string]any{
		"Title":  "Legal — Pending Drafting Sale Agreement",
		"Active": "queue",
		"Queue":  queue,
		"Error":  r.URL.Query().Get("err"),
	}
	maps.Copy(data, filterRenderData(r))
	renderLegal(w, "legal_queue.html", data)
}

// awaitingSignatureHandler lists stage 2: bookings whose sale agreement has
// been drafted and sent out, waiting on the client's signature. StageDate
// here is sent_for_signature_at — when it was sent to the client — not
// date_booked.
func awaitingSignatureHandler(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(DATE_FORMAT(b.sent_for_signature_at,'%d %b %Y %h:%i %p'),'—'), COALESCE(lw.name,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents lw ON lw.id = e.lawyer_id
		WHERE b.status = 'pending_wakili_review' AND b.legal_stage = 'awaiting_signature'`
	query, args := scopeQuery(r, query, nil)
	query, args = applyListFilters(r, "b.sent_for_signature_at", query, args)
	query += ` ORDER BY b.sent_for_signature_at ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("legal awaiting-signature: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var queue []queueRow
	for rows.Next() {
		var q queueRow
		if err := rows.Scan(&q.BookingID, &q.BuyerName, &q.AgentName, &q.EstateName, &q.PlotNumber, &q.StageDate, &q.LawyerName); err == nil {
			queue = append(queue, q)
		}
	}

	data := map[string]any{
		"Title":  "Legal — Pending Client Signature",
		"Active": "awaiting",
		"Queue":  queue,
		"Error":  r.URL.Query().Get("err"),
	}
	maps.Copy(data, filterRenderData(r))
	renderLegal(w, "legal_awaiting.html", data)
}

type completedRow struct {
	BookingID  int
	BuyerName  string
	AgentName  string
	EstateName string
	PlotNumber string
	DateSigned string
	PlotStatus string
	LawyerName string
}

// completedHandler lists every booking whose sale agreement is done —
// currently sa_signed, or sold on top of that (status='completed', per the
// user's explicit ask to keep showing these here even after the plot moves
// on) — optionally narrowed to one month by date_signed.
func completedHandler(w http.ResponseWriter, r *http.Request) {
	month := strings.TrimSpace(r.URL.Query().Get("month")) // "YYYY-MM" or "" for all

	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'), ''), p.status, COALESCE(lw.name,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents lw ON lw.id = e.lawyer_id
		WHERE b.status IN ('sa_signed','completed')`
	var args []any
	if month != "" {
		query += ` AND DATE_FORMAT(b.date_signed,'%Y-%m') = ?`
		args = append(args, month)
	}
	query, args = scopeQuery(r, query, args)
	query, args = applyListFilters(r, "b.date_signed", query, args)
	query += ` ORDER BY b.date_signed DESC`

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("legal completed: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []completedRow
	for rows.Next() {
		var c completedRow
		if err := rows.Scan(&c.BookingID, &c.BuyerName, &c.AgentName, &c.EstateName, &c.PlotNumber, &c.DateSigned, &c.PlotStatus, &c.LawyerName); err == nil {
			list = append(list, c)
		}
	}

	data := map[string]any{
		"Title":  "Legal — Completed Sale Agreement",
		"Active": "completed",
		"List":   list,
		"Total":  len(list),
		"Month":  month,
	}
	maps.Copy(data, filterRenderData(r))
	renderLegal(w, "legal_completed.html", data)
}

type estateRow struct {
	ID   int
	Name string
	Info string
}

// estatesHandler lists just the estates this viewer has access to — the
// same scope as everywhere else in this module (their own assigned estates
// for role "legal", every estate for admin/system_admin). Deliberately
// minimal: name and the estate's own info text only, nothing else (no
// image, mutation document, price, or plot counts) — those live on the
// booking-review pages already, this is purely "what estates am I on."
func estatesHandler(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id, name, COALESCE(prop_plotinfo,'') FROM prop_estates e WHERE 1=1`
	query, args := scopeQuery(r, query, nil)
	query += ` ORDER BY name`

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("legal estates: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var estates []estateRow
	for rows.Next() {
		var e estateRow
		if rows.Scan(&e.ID, &e.Name, &e.Info) == nil {
			estates = append(estates, e)
		}
	}

	renderLegal(w, "legal_estates.html", map[string]any{
		"Title":   "Legal — My Estates",
		"Active":  "estates",
		"Estates": estates,
	})
}

type reviewDetail struct {
	BookingID          int
	BuyerName          string
	BuyerPhone         string
	BuyerEmail         string
	AgentName          string
	AgentPhone         string
	AgentEmail         string
	EstateID           int
	EstateName         string
	PlotID             int
	PlotNumber         string
	Deposit            float64
	PaymentPlan        string
	Notes              string
	AccountsNotes      string
	LegalStage         string
	LawyerName         string
	DepositRefFiles    []string
	IDPhotoFiles       []string
	KRAFiles           []string
	PassportPhotoFiles []string
}

func loadReviewDetail(bookingID string) (reviewDetail, error) {
	var d reviewDetail
	var depositRef, idPhoto, kra, passportPhoto string
	err := db.QueryRow(`
		SELECT b.id, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       COALESCE(b.agent_name,''), COALESCE(a.phone,''), COALESCE(a.email,''),
		       e.id, e.name, b.plot_id, p.plot_number,
		       COALESCE(b.deposit,0), COALESCE(b.payment_plan,''),
		       COALESCE(b.notes,''), COALESCE(b.accounts_notes,''), b.legal_stage, COALESCE(lw.name,''),
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		LEFT JOIN prop_agents lw ON lw.id = e.lawyer_id
		WHERE b.id = ?`, bookingID).
		Scan(&d.BookingID, &d.BuyerName, &d.BuyerPhone, &d.BuyerEmail,
			&d.AgentName, &d.AgentPhone, &d.AgentEmail,
			&d.EstateID, &d.EstateName, &d.PlotID, &d.PlotNumber,
			&d.Deposit, &d.PaymentPlan,
			&d.Notes, &d.AccountsNotes, &d.LegalStage, &d.LawyerName, &depositRef, &idPhoto, &kra, &passportPhoto)
	if err != nil {
		return d, err
	}
	d.DepositRefFiles = splitFiles(depositRef)
	d.IDPhotoFiles = splitFiles(idPhoto)
	d.KRAFiles = splitFiles(kra)
	d.PassportPhotoFiles = splitFiles(passportPhoto)
	return d, nil
}

func reviewDetailHandler(w http.ResponseWriter, r *http.Request) {
	bookingID := bookingIDFromPath("/legal/review/", r.URL.Path)
	detail, err := loadReviewDetail(bookingID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !canView(r, detail.EstateID) {
		http.Error(w, "Access denied — you don't have visibility into this booking.", http.StatusForbidden)
		return
	}
	active := "queue"
	if detail.LegalStage == "awaiting_signature" {
		active = "awaiting"
	}
	renderLegal(w, "legal_review.html", map[string]any{
		"Title":  "Review — " + detail.BuyerName,
		"Active": active,
		"Info":   detail,
		"Error":  r.URL.Query().Get("err"),
		// Admin/agent get read-only visibility by default — the action forms
		// below are hidden for them, not just blocked server-side (canAct
		// already rejects the POST regardless, but showing a button that
		// would just 403 on click is bad UX) — unless this admin has been
		// explicitly granted the Legal Module write permission.
		"CanAct": canAct(r, detail.EstateID),
	})
}

// sendForSignatureHandler moves a booking from stage 1 (drafting) to stage 2
// (awaiting the client's signature). Purely an internal Legal-side workflow
// marker — status stays 'pending_wakili_review' throughout.
func sendForSignatureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/legal/queue", http.StatusFound)
		return
	}
	bookingID := bookingIDFromPath("/legal/review/", strings.TrimSuffix(r.URL.Path, "/send-for-signature"))

	var estateID int
	if err := db.QueryRow(`SELECT estate_id FROM prop_bookings WHERE id=? AND status='pending_wakili_review' AND legal_stage='drafting'`, bookingID).
		Scan(&estateID); err != nil {
		http.Redirect(w, r, "/legal/queue?err=Booking+already+moved", http.StatusFound)
		return
	}
	if !canAct(r, estateID) {
		http.Error(w, "Access denied — this estate is not assigned to you.", http.StatusForbidden)
		return
	}

	if _, err := db.Exec(`UPDATE prop_bookings SET legal_stage='awaiting_signature', sent_for_signature_at=NOW()
		WHERE id=? AND status='pending_wakili_review' AND legal_stage='drafting'`, bookingID); err != nil {
		log.Printf("legal send-for-signature: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
	}
	if sendSentForSigSMS != nil {
		sendSentForSigSMS(bookingID)
	}
	http.Redirect(w, r, "/legal/queue", http.StatusFound)
}

func uploadAgreementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/legal/queue", http.StatusFound)
		return
	}
	bookingID := bookingIDFromPath("/legal/review/", strings.TrimSuffix(r.URL.Path, "/upload-agreement"))
	r.ParseMultipartForm(32 << 20)

	saleAgreement := saveUploadedFiles(r, "sale_agreement")
	if saleAgreement == "" {
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=A+signed+sale+agreement+file+is+required", bookingID), http.StatusFound)
		return
	}
	installmentPage := strings.TrimSpace(r.FormValue("installment_page"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	reviewer := getAgentFn(r)

	var plotID int
	if err := db.QueryRow(`SELECT plot_id FROM prop_bookings WHERE id=? AND status='pending_wakili_review' AND legal_stage='awaiting_signature'`, bookingID).Scan(&plotID); err != nil {
		http.Redirect(w, r, "/legal/queue?err=Booking+already+reviewed", http.StatusFound)
		return
	}
	var estateIDForCheck int
	db.QueryRow(`SELECT estate_id FROM prop_bookings WHERE id=?`, bookingID).Scan(&estateIDForCheck)
	if !canAct(r, estateIDForCheck) {
		http.Error(w, "Access denied — this estate is not assigned to you.", http.StatusForbidden)
		return
	}

	res, err := db.Exec(`UPDATE prop_bookings SET status='sa_signed', date_signed=NOW(), sale_agreement=?, installment_page=?,
		wakili_notes=?, wakili_reviewed_by=?, wakili_reviewed_at=NOW()
		WHERE id=? AND status='pending_wakili_review' AND legal_stage='awaiting_signature'`, saleAgreement, installmentPage, notes, reviewer, bookingID)
	if err != nil {
		log.Printf("legal upload-agreement: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Redirect(w, r, "/legal/queue?err=Booking+already+reviewed", http.StatusFound)
		return
	}
	db.Exec(`UPDATE prop_plots SET status='sa_signed' WHERE id=?`, plotID)

	detail, derr := loadReviewDetail(bookingID)
	if derr == nil {
		if processSAIntegr != nil {
			go processSAIntegr(plotID, detail.PlotNumber, detail.EstateName)
		}
		if sendOutcome != nil {
			go sendOutcome("legal_signed", detail.PlotNumber, detail.EstateName, detail.BuyerName, detail.BuyerPhone, detail.AgentName, notes)
		}
	}
	if sendSignedSMS != nil {
		sendSignedSMS(bookingID)
	}

	http.Redirect(w, r, "/legal/queue", http.StatusFound)
}

func cancelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/legal/queue", http.StatusFound)
		return
	}
	bookingID := bookingIDFromPath("/legal/review/", strings.TrimSuffix(r.URL.Path, "/cancel"))
	r.ParseForm()
	notes := strings.TrimSpace(r.FormValue("notes"))
	if notes == "" {
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=A+reason+is+required+to+cancel", bookingID), http.StatusFound)
		return
	}
	reviewer := getAgentFn(r)

	var plotID, estateIDForCheck int
	var zohoBooksID string
	if err := db.QueryRow(`SELECT plot_id, estate_id, COALESCE(zoho_books_id,'') FROM prop_bookings WHERE id=? AND status='pending_wakili_review'`, bookingID).
		Scan(&plotID, &estateIDForCheck, &zohoBooksID); err != nil {
		http.Redirect(w, r, "/legal/queue?err=Booking+already+reviewed", http.StatusFound)
		return
	}
	if !canAct(r, estateIDForCheck) {
		http.Error(w, "Access denied — this estate is not assigned to you.", http.StatusForbidden)
		return
	}

	if _, err := db.Exec(`UPDATE prop_bookings SET status='cancelled', wakili_notes=?, wakili_reviewed_by=?, wakili_reviewed_at=NOW()
		WHERE id=? AND status='pending_wakili_review'`, notes, reviewer, bookingID); err != nil {
		log.Printf("legal cancel: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
	}
	db.Exec(`UPDATE prop_plots SET status='available' WHERE id=?`, plotID)
	if zohoBooksID != "" && cancelBooks != nil {
		go cancelBooks(zohoBooksID)
	}

	detail, derr := loadReviewDetail(bookingID)
	if derr == nil && sendOutcome != nil {
		go sendOutcome("legal_cancelled", detail.PlotNumber, detail.EstateName, detail.BuyerName, detail.BuyerPhone, detail.AgentName, notes)
	}

	http.Redirect(w, r, "/legal/queue", http.StatusFound)
}
