package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// ─── Payment Plans → conveyancing → auto-sold ────────────────────────────────
//
// Reads Zoho CRM Deals on a schedule (4×/day) and tracks two groups locally:
//   • 3-month payment-plan clients  (Deal field Payment_Plan = "3_months")
//   • every other client once Zoho's Percent_Paid reaches 70+
//
// The three conveyancing documents are uploaded here, in order:
//   1. Approved Letter of Consent — any tracked deal, any payment %
//   2. Signed Transfer Forms      — only once Percent_Paid = 100
//   3. Title Deed                 — only once Percent_Paid = 100 (after transfer)
//
// When all three are on file AND Percent_Paid = 100, the matching plot
// (estate name + plot number, normalized) is flipped sa_signed → sold with
// full parity to the manual Mark Sold flow. Deals whose estate/plot cannot be
// matched keep their uploads but are flagged for manual handling.
//
// Zoho stays the source of truth for Percent_Paid; the sync never touches the
// local document / sold columns.

// Zoho CRM Deals field API names (confirmed via inspect-crm-fields -module=Deals):
//   Payment_Plan  — picklist; booking form posts "3_months".
//   Percent_Paid  — numeric "percent" type (e.g. 70, 100), NOT a picklist.

// threeMonthPlanValues is the set of raw Zoho Payment_Plan strings that count
// as a 3-month plan. "3 Months Plan" / lower-case are kept as fallbacks in
// case the picklist is edited in Zoho.
var threeMonthPlanValues = map[string]bool{
	"3_months":      true,
	"3 Months Plan": true,
	"3 months plan": true,
}

// ppSyncSlots are the wall-clock times (EAT — time.Local is Africa/Nairobi)
// at which the scheduled Zoho sync runs.
var ppSyncSlots = map[string]bool{
	"08:00": true,
	"10:00": true,
	"13:00": true,
	"15:00": true,
}

// ppListViews maps a URL slug to its page title and SQL WHERE clause.
var ppListViews = map[string]struct {
	Title string
	Where string
}{
	"all":                {"All Tracked Deals", "1 = 1"},
	"3m-pending-consent": {"3-Month Plan — Pending Consent", "sold_at IS NULL AND is_three_month = 1 AND COALESCE(consent_file,'') = ''"},
	"70-pending-consent": {"70%+ — Pending Consent", "sold_at IS NULL AND is_three_month = 0 AND percentage_paid >= 70 AND COALESCE(consent_file,'') = ''"},
	"pending-transfer":   {"Pending Signed Transfer Forms", "sold_at IS NULL AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') = ''"},
	"pending-title":      {"Pending Title Deed", "sold_at IS NULL AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') <> '' AND COALESCE(title_file,'') = ''"},
	"fully-paid":         {"Fully Paid (Sold)", "sold_at IS NOT NULL OR (percentage_paid = 100 AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') <> '' AND COALESCE(title_file,'') <> '')"},
}

// ─── Schema ──────────────────────────────────────────────────────────────────

func initPaymentPlanTables() {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_payment_plan_deals (
		deal_id           VARCHAR(64) PRIMARY KEY,
		deal_name         VARCHAR(255) DEFAULT '',
		phone             VARCHAR(64)  DEFAULT '',
		email             VARCHAR(191) DEFAULT '',
		estate            VARCHAR(255) DEFAULT '',
		plot              VARCHAR(255) DEFAULT '',
		payment_plan      VARCHAR(64)  DEFAULT '',
		is_three_month    TINYINT(1)   DEFAULT 0,
		percentage_paid   INT          DEFAULT 0,
		zoho_stage        VARCHAR(120) DEFAULT '',
		books_ref         VARCHAR(64)  DEFAULT '',
		closing_date      VARCHAR(32)  DEFAULT '',
		consent_file  VARCHAR(500) DEFAULT NULL, consent_at  DATETIME NULL, consent_by  VARCHAR(120) DEFAULT '',
		transfer_file VARCHAR(500) DEFAULT NULL, transfer_at DATETIME NULL, transfer_by VARCHAR(120) DEFAULT '',
		title_file    VARCHAR(500) DEFAULT NULL, title_at    DATETIME NULL, title_by    VARCHAR(120) DEFAULT '',
		sold_at       DATETIME     NULL,
		sold_plot_id  INT          NULL,
		sold_note     VARCHAR(255) DEFAULT NULL,
		first_synced_at DATETIME NULL,
		last_synced_at  DATETIME NULL,
		INDEX idx_ppd_track (is_three_month),
		INDEX idx_ppd_pct (percentage_paid)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[payment-plans] ERROR creating prop_payment_plan_deals: %v", err)
	}

	// Additive migrations for deployments created before the conveyancing
	// columns existed. Errors ignored (column already present) — same pattern
	// as the ALTER block in main().
	for _, stmt := range []string{
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN consent_file  VARCHAR(500) DEFAULT NULL`,
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN transfer_file VARCHAR(500) DEFAULT NULL`,
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN title_file    VARCHAR(500) DEFAULT NULL`,
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN sold_at       DATETIME     NULL`,
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN sold_plot_id  INT          NULL`,
		`ALTER TABLE prop_payment_plan_deals ADD COLUMN sold_note     VARCHAR(255) DEFAULT NULL`,
	} {
		db.Exec(stmt)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_payment_plan_sync_log (
		id       INT AUTO_INCREMENT PRIMARY KEY,
		run_date DATE        NOT NULL,
		slot     VARCHAR(5)  NOT NULL,
		ran_at   TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
		deal_count INT       DEFAULT 0,
		UNIQUE KEY uk_ppsync (run_date, slot)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[payment-plans] ERROR creating prop_payment_plan_sync_log: %v", err)
	}

	// Lightweight snapshot of EVERY Zoho CRM Deal (not just the qualifying
	// subset) — feeds the visualisations on the Payment Plans landing page.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_crm_deal_snapshot (
		deal_id      VARCHAR(64) PRIMARY KEY,
		payment_plan VARCHAR(64)  DEFAULT '',
		percent_paid INT          DEFAULT 0,
		stage        VARCHAR(120) DEFAULT '',
		estate       VARCHAR(255) DEFAULT '',
		deal_month   VARCHAR(7)   DEFAULT '',
		last_seen_at DATETIME     NULL,
		INDEX idx_cds_plan (payment_plan),
		INDEX idx_cds_month (deal_month),
		INDEX idx_cds_pct (percent_paid)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[payment-plans] ERROR creating prop_crm_deal_snapshot: %v", err)
	}
}

// ─── Zoho sync ───────────────────────────────────────────────────────────────

// percentToInt rounds Zoho's Percent_Paid value (a JSON number, or null → 0)
// to the nearest whole percent.
func percentToInt(f float64) int {
	if f <= 0 {
		return 0
	}
	return int(f + 0.5)
}

// syncPaymentPlanDeals pages through every Zoho CRM Deal and upserts the ones
// that qualify (3-month plan, or ≥70% paid) into prop_payment_plan_deals.
// Returns the number of qualifying deals seen. Local document / sold columns
// are never touched by the upsert.
func syncPaymentPlanDeals() (int, error) {
	token, err := getCRMToken()
	if err != nil {
		return 0, fmt.Errorf("token: %w", err)
	}

	const fields = "Deal_Name,Phone_Number,Buyer_Email,Estates,Plot,Payment_Plan,Percent_Paid,Stage,Books_Ref_Number,Closing_Date,Created_Time"
	qualifying := 0
	page := 1
	for {
		url := fmt.Sprintf("%sDeals?fields=%s&per_page=200&page=%d", zohoCRMBase, fields, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return qualifying, fmt.Errorf("page %d: %w", page, err)
		}
		if resp.StatusCode == 204 {
			resp.Body.Close()
			break
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return qualifying, fmt.Errorf("page %d HTTP %d: %s", page, resp.StatusCode, string(b))
		}

		var result struct {
			Data []struct {
				ID          string  `json:"id"`
				DealName    string  `json:"Deal_Name"`
				Phone       string  `json:"Phone_Number"`
				Email       string  `json:"Buyer_Email"`
				Estates     string  `json:"Estates"`
				Plot        string  `json:"Plot"`
				PaymentPlan string  `json:"Payment_Plan"`
				PercentPaid float64 `json:"Percent_Paid"`
				Stage       string  `json:"Stage"`
				BooksRef    string  `json:"Books_Ref_Number"`
				ClosingDate string  `json:"Closing_Date"`
				CreatedTime string  `json:"Created_Time"`
			} `json:"data"`
			Info struct {
				MoreRecords bool `json:"more_records"`
			} `json:"info"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return qualifying, fmt.Errorf("page %d decode: %w", page, err)
		}
		resp.Body.Close()

		for _, d := range result.Data {
			plan := strings.TrimSpace(d.PaymentPlan)
			isThree := threeMonthPlanValues[plan]
			pct := percentToInt(d.PercentPaid)

			// Snapshot EVERY deal for the landing-page charts. Zoho does not
			// expose Created_Time for this org, so deal_month is the
			// Closing_Date (expected final-payment / completion month).
			month := ""
			if len(d.ClosingDate) >= 7 {
				month = d.ClosingDate[:7]
			} else if len(d.CreatedTime) >= 7 {
				month = d.CreatedTime[:7]
			}
			db.Exec(`INSERT INTO prop_crm_deal_snapshot
				(deal_id, payment_plan, percent_paid, stage, estate, deal_month, last_seen_at)
				VALUES (?,?,?,?,?,?,NOW())
				ON DUPLICATE KEY UPDATE payment_plan=VALUES(payment_plan), percent_paid=VALUES(percent_paid),
				  stage=VALUES(stage), estate=VALUES(estate), deal_month=VALUES(deal_month), last_seen_at=NOW()`,
				d.ID, plan, pct, d.Stage, d.Estates, month)

			if !isThree && pct < 70 {
				continue
			}
			qualifying++
			threeInt := 0
			if isThree {
				threeInt = 1
			}
			if _, err := db.Exec(`INSERT INTO prop_payment_plan_deals
				(deal_id, deal_name, phone, email, estate, plot, payment_plan, is_three_month,
				 percentage_paid, zoho_stage, books_ref, closing_date, first_synced_at, last_synced_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NOW(),NOW())
				ON DUPLICATE KEY UPDATE
				  deal_name=VALUES(deal_name), phone=VALUES(phone), email=VALUES(email),
				  estate=VALUES(estate), plot=VALUES(plot), payment_plan=VALUES(payment_plan),
				  is_three_month=VALUES(is_three_month), percentage_paid=VALUES(percentage_paid),
				  zoho_stage=VALUES(zoho_stage), books_ref=VALUES(books_ref),
				  closing_date=VALUES(closing_date), last_synced_at=NOW()`,
				d.ID, d.DealName, d.Phone, d.Email, d.Estates, d.Plot, plan, threeInt,
				pct, d.Stage, d.BooksRef, d.ClosingDate); err != nil {
				log.Printf("[payment-plans] upsert deal %s: %v", d.ID, err)
			}
		}

		if !result.Info.MoreRecords {
			break
		}
		page++
	}

	// Drop snapshot rows for deals that vanished from the CRM (full sweep only).
	if res, err := db.Exec(`DELETE FROM prop_crm_deal_snapshot WHERE last_seen_at < (NOW() - INTERVAL 2 DAY)`); err == nil {
		if nd, _ := res.RowsAffected(); nd > 0 {
			log.Printf("[payment-plans] snapshot: pruned %d stale deal row(s)", nd)
		}
	}

	if n := reconcileSoldDeals(); n > 0 {
		log.Printf("[payment-plans] reconciled %d deal(s) whose plot is already sold", n)
	}
	if n := reconcileConveyancingDocs(); n > 0 {
		log.Printf("[payment-plans] pulled Mark-Sold-page documents into %d deal(s)", n)
	}
	return qualifying, nil
}

// reconcileConveyancingDocs pulls conveyancing documents that were uploaded
// against a plot through the SA-Signed → Mark Sold page (prop_sales) into the
// matching Payment-Plan deal's *_file columns — so a deal doesn't sit in
// "Pending Transfer Forms" / "Pending Title Deed" here when the document is
// already on file elsewhere. Only fills blanks (a doc uploaded on the Payment
// Plans page is never overwritten). Runs ppMaybeComplete after, so a deal that
// becomes complete this way is auto-sold. Returns the number of deals touched.
func reconcileConveyancingDocs() int {
	type d struct{ id, estate, plot, consent, transfer, title string }
	var deals []d
	rows, err := db.Query(`SELECT deal_id, estate, plot,
		COALESCE(consent_file,''), COALESCE(transfer_file,''), COALESCE(title_file,'')
		FROM prop_payment_plan_deals
		WHERE sold_at IS NULL AND (COALESCE(consent_file,'')='' OR COALESCE(transfer_file,'')='' OR COALESCE(title_file,'')='')`)
	if err != nil {
		return 0
	}
	for rows.Next() {
		var x d
		if rows.Scan(&x.id, &x.estate, &x.plot, &x.consent, &x.transfer, &x.title) == nil {
			deals = append(deals, x)
		}
	}
	rows.Close()

	n := 0
	for _, x := range deals {
		plotID, _, _, _, _, ok := resolveDealPlot(x.estate, x.plot)
		if !ok {
			continue
		}
		var loc, tf, td string
		db.QueryRow(`SELECT COALESCE(letter_of_consent,''), COALESCE(transfer_forms,''), COALESCE(title_deed,'')
			FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&loc, &tf, &td)

		var filledSteps []string
		var pushFiles []string
		fill := func(step, local, fromSales string) {
			if local != "" || fromSales == "" {
				return
			}
			if _, err := db.Exec(`UPDATE prop_payment_plan_deals
				SET `+step+`_file=?, `+step+`_at=NOW(), `+step+`_by='Mark Sold page'
				WHERE deal_id=? AND COALESCE(`+step+`_file,'')=''`, fromSales, x.id); err == nil {
				filledSteps = append(filledSteps, step)
				pushFiles = append(pushFiles, splitFiles(fromSales)...)
			}
		}
		fill("consent", x.consent, loc)
		fill("transfer", x.transfer, tf)
		fill("title", x.title, td)
		if len(filledSteps) == 0 {
			continue
		}
		n++
		log.Printf("[payment-plans] deal %s: pulled %v from Mark Sold page (plot %d)", x.id, filledSteps, plotID)

		// Push the newly-pulled file(s) to the Zoho CRM deal.
		go func(dealID string, files []string) {
			for _, one := range files {
				if one == "" {
					continue
				}
				if err := uploadCRMAttachment(dealID, one); err != nil {
					log.Printf("[payment-plans] CRM attach %s → deal %s: FAILED: %v", one, dealID, err)
				}
			}
		}(x.id, pushFiles)

		// Auto-sell if the deal is now complete + 100% paid.
		ppMaybeComplete(x.id, "Mark Sold reconcile")
	}
	return n
}

// reconcileSoldDeals catches up deals whose plot was already marked sold
// through the legacy SA-Signed → Mark Sold flow (before conveyancing moved
// here). For each not-yet-completed deal that resolves to a plot already in
// 'sold' status, it copies any conveyancing filenames from prop_sales into the
// deal's *_file columns and stamps sold_at, so the deal shows as Sold here
// instead of sitting in a "pending consent" list. Returns the number updated.
func reconcileSoldDeals() int {
	type d struct{ id, estate, plot string }
	var deals []d
	rows, err := db.Query(`SELECT deal_id, estate, plot FROM prop_payment_plan_deals WHERE sold_at IS NULL`)
	if err != nil {
		return 0
	}
	for rows.Next() {
		var x d
		if rows.Scan(&x.id, &x.estate, &x.plot) == nil {
			deals = append(deals, x)
		}
	}
	rows.Close()

	n := 0
	for _, x := range deals {
		plotID, _, _, _, status, ok := resolveDealPlot(x.estate, x.plot)
		if !ok || status != "sold" {
			continue
		}
		var loc, tf, td string
		db.QueryRow(`SELECT COALESCE(letter_of_consent,''), COALESCE(transfer_forms,''), COALESCE(title_deed,'')
			FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&loc, &tf, &td)
		note := ""
		if loc == "" || tf == "" || td == "" {
			note = "plot already sold via Mark Sold — some conveyancing docs not on file here"
		}
		if _, err := db.Exec(`UPDATE prop_payment_plan_deals
			SET consent_file  = COALESCE(NULLIF(?,''), consent_file),
			    transfer_file = COALESCE(NULLIF(?,''), transfer_file),
			    title_file    = COALESCE(NULLIF(?,''), title_file),
			    sold_at = NOW(), sold_plot_id = ?, sold_note = NULLIF(?, '')
			WHERE deal_id = ? AND sold_at IS NULL`,
			loc, tf, td, plotID, note, x.id); err == nil {
			n++
			log.Printf("[payment-plans] reconcile: deal %s → plot %d already sold, stamped (note=%q)", x.id, plotID, note)
		} else {
			log.Printf("[payment-plans] reconcile: deal %s update failed: %v", x.id, err)
		}
	}
	return n
}

// startPaymentPlanSync launches the background scheduler: a 1-minute ticker
// that runs syncPaymentPlanDeals() once per slot per day (08:00/10:00/13:00/
// 15:00 EAT), guarded against duplicates by the UNIQUE KEY on
// prop_payment_plan_sync_log. Also runs once at startup if the last sync is
// stale (>6h) or has never happened.
func startPaymentPlanSync() {
	go func() {
		var last sql.NullTime
		db.QueryRow(`SELECT MAX(ran_at) FROM prop_payment_plan_sync_log`).Scan(&last)
		if !last.Valid || time.Since(last.Time) > 6*time.Hour {
			if n, err := syncPaymentPlanDeals(); err != nil {
				log.Printf("[payment-plans] startup sync error: %v", err)
			} else {
				log.Printf("[payment-plans] startup sync ok — %d qualifying deals", n)
			}
		}

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			slot := time.Now().Format("15:04")
			if !ppSyncSlots[slot] {
				continue
			}
			res, err := db.Exec(
				`INSERT IGNORE INTO prop_payment_plan_sync_log (run_date, slot) VALUES (CURDATE(), ?)`, slot)
			if err != nil {
				log.Printf("[payment-plans] sync-log insert (%s): %v", slot, err)
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // this slot already ran today
			}
			count, err := syncPaymentPlanDeals()
			if err != nil {
				log.Printf("[payment-plans] scheduled sync (%s) error: %v", slot, err)
				continue
			}
			db.Exec(`UPDATE prop_payment_plan_sync_log SET deal_count=? WHERE run_date=CURDATE() AND slot=?`,
				count, slot)
			log.Printf("[payment-plans] scheduled sync (%s) ok — %d qualifying deals", slot, count)
		}
	}()
}

// runSyncPaymentPlans is the `sync-payment-plans` CLI subcommand — a one-shot
// manual sync for testing / backfilling.
func runSyncPaymentPlans(args []string) {
	initDB()
	initPaymentPlanTables()
	n, err := syncPaymentPlanDeals()
	if err != nil {
		log.Fatalf("[payment-plans] sync failed: %v", err)
	}
	log.Printf("[payment-plans] sync complete — %d qualifying deals", n)
	os.Exit(0)
}

// runReconcilePaymentPlanSold is the `reconcile-payment-plan-sold` CLI
// subcommand — a one-shot pass that marks Payment-Plan deals as Sold when
// their plot was already sold through the legacy Mark Sold page. (The
// scheduled sync also does this automatically.)
func runReconcilePaymentPlanSold(args []string) {
	initDB()
	initPaymentPlanTables()
	n1 := reconcileSoldDeals()
	n2 := reconcileConveyancingDocs()
	log.Printf("[payment-plans] reconcile complete — %d deal(s) marked sold from existing sale records, %d deal(s) pulled Mark-Sold-page documents", n1, n2)
	os.Exit(0)
}

// ─── Pages ───────────────────────────────────────────────────────────────────

func ppCount(where string) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM prop_payment_plan_deals WHERE ` + where).Scan(&n)
	return n
}

// adminPaymentPlansHandler renders the landing page: stat tiles, filtered-view
// links, and the CRM-deal visualisations.
func adminPaymentPlansHandler(w http.ResponseWriter, r *http.Request) {
	var lastSync sql.NullString
	db.QueryRow(`SELECT DATE_FORMAT(MAX(last_synced_at),'%d %b %Y %H:%i') FROM prop_payment_plan_deals`).Scan(&lastSync)

	chartJSON, crmTotal := ppBuildChartJSON()

	renderAdmin(w, r, "admin_payment_plans.html", map[string]any{
		"Title":                "Conveyancing",
		"Active":               "payment-plans",
		"LastSync":             lastSync.String,
		"CrmTotal":             crmTotal,
		"ChartData":            template.JS(chartJSON),
		"StatTotal":            ppCount("1 = 1"),
		"Stat3mPendingConsent": ppCount("sold_at IS NULL AND is_three_month = 1 AND COALESCE(consent_file,'') = ''"),
		"Stat70PendingConsent": ppCount("sold_at IS NULL AND is_three_month = 0 AND percentage_paid >= 70 AND COALESCE(consent_file,'') = ''"),
		"StatPendingTransfer":  ppCount("sold_at IS NULL AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') = ''"),
		"StatPendingTitle":     ppCount("sold_at IS NULL AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') <> '' AND COALESCE(title_file,'') = ''"),
		"StatFullyPaid":        ppCount("sold_at IS NOT NULL OR (percentage_paid = 100 AND COALESCE(consent_file,'') <> '' AND COALESCE(transfer_file,'') <> '' AND COALESCE(title_file,'') <> '')"),
	})
}

// ppPlanBucket folds a raw Zoho Payment_Plan value into a chart series label.
func ppPlanBucket(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "3_months", "3 months plan", "3 months", "3month":
		return "3 Months"
	case "6_months", "6 months plan", "6 months":
		return "6 Months"
	case "12_months", "12 months plan", "12 months":
		return "12 Months"
	default:
		return "Other"
	}
}

// ppBuildChartJSON assembles the data for the landing-page charts from the
// full CRM-deal snapshot. Returns the JSON blob and the total deal count.
func ppBuildChartJSON() ([]byte, int) {
	var total int
	db.QueryRow(`SELECT COUNT(*) FROM prop_crm_deal_snapshot`).Scan(&total)

	planOrder := []string{"3 Months", "6 Months", "12 Months", "Other"}
	planIdx := map[string]int{"3 Months": 0, "6 Months": 1, "12 Months": 2, "Other": 3}
	planTotals := make([]int, len(planOrder))

	pctLabels := []string{"0%", "1–25%", "26–50%", "51–70%", "71–90%", "91–99%", "100%"}
	pctCounts := make([]int, len(pctLabels))

	var signedTotal, fullyPaidTotal int

	if rs, err := db.Query(`SELECT payment_plan, percent_paid FROM prop_crm_deal_snapshot`); err == nil {
		for rs.Next() {
			var plan string
			var p int
			rs.Scan(&plan, &p)
			planTotals[planIdx[ppPlanBucket(plan)]]++
			if p >= 100 {
				fullyPaidTotal++
			} else {
				signedTotal++
			}
			switch {
			case p <= 0:
				pctCounts[0]++
			case p <= 25:
				pctCounts[1]++
			case p <= 50:
				pctCounts[2]++
			case p <= 70:
				pctCounts[3]++
			case p <= 90:
				pctCounts[4]++
			case p < 100:
				pctCounts[5]++
			default:
				pctCounts[6]++
			}
		}
		rs.Close()
	}

	// ── top estates by deal count ──
	var estates []string
	var estateCounts []int
	if rs, err := db.Query(`SELECT estate, COUNT(*) c FROM prop_crm_deal_snapshot
		WHERE estate <> '' GROUP BY estate ORDER BY c DESC LIMIT 10`); err == nil {
		for rs.Next() {
			var e string
			var c int
			rs.Scan(&e, &c)
			estates = append(estates, e)
			estateCounts = append(estateCounts, c)
		}
		rs.Close()
	}

	b, _ := json.Marshal(map[string]any{
		"planOrder":      planOrder,
		"planTotals":     planTotals,
		"signedTotal":    signedTotal,
		"fullyPaidTotal": fullyPaidTotal,
		"pctLabels":      pctLabels,
		"pctCounts":      pctCounts,
		"estates":        estates,
		"estateCounts":   estateCounts,
	})
	return b, total
}

// ppRow is one deal as shown on a list page.
type ppRow struct {
	DealID         string
	DealName       string
	Phone          string
	Estate         string
	Plot           string
	PaymentPlan    string
	IsThreeMonth   bool
	PercentagePaid int
	ZohoStage      string

	ConsentFile   string
	TransferFile  string
	TitleFile     string
	ConsentFiles  []string
	TransferFiles []string
	TitleFiles    []string
	ConsentBy     string
	ConsentAt     string
	TransferBy    string
	TransferAt    string
	TitleBy       string
	TitleAt       string

	SoldAt   string
	SoldNote string

	LastSynced string

	// Computed / template gating.
	Stage       string
	StageRank   int
	CanTransfer bool // percentage_paid == 100 && consent on file
	CanTitle    bool // percentage_paid == 100 && consent + transfer on file
}

// computeStage collapses a row's document state into one stage label. A record
// advances the moment the previous document is uploaded — the payment %
// only governs whether the next upload is enabled (see CanTransfer/CanTitle),
// not which stage the record sits in.
func computeStage(x *ppRow) {
	if x.SoldAt != "" {
		x.Stage, x.StageRank = "Sold", 5
		return
	}
	switch {
	case x.ConsentFile == "":
		x.Stage, x.StageRank = "Pending Consent", 1
	case x.TransferFile == "":
		x.Stage, x.StageRank = "Pending Transfer Forms", 2
	case x.TitleFile == "":
		x.Stage, x.StageRank = "Pending Title Deed", 3
	default:
		x.Stage, x.StageRank = "Documents Complete", 4
	}
	if x.PercentagePaid < 100 && x.StageRank >= 2 && x.StageRank <= 3 {
		x.Stage += " (awaiting 100% payment)"
	}
}

// paymentPlanListHandler renders one filtered list view (slug after
// /admin/payment-plans/).
func paymentPlanListHandler(w http.ResponseWriter, r *http.Request) {
	view := pathSegment("/admin/payment-plans/", r.URL.Path)
	cfg, ok := ppListViews[view]
	if !ok {
		http.NotFound(w, r)
		return
	}

	// ── filters (estate / plan dropdowns + free-text search) ──
	q := r.URL.Query()
	fEstate := strings.TrimSpace(q.Get("estate"))
	fPlan := strings.TrimSpace(q.Get("plan"))
	fSearch := strings.TrimSpace(q.Get("q"))

	where := "(" + cfg.Where + ")"
	var args []any
	if fEstate != "" {
		where += " AND estate = ?"
		args = append(args, fEstate)
	}
	if fPlan != "" {
		where += " AND payment_plan = ?"
		args = append(args, fPlan)
	}
	if fSearch != "" {
		where += " AND (deal_name LIKE ? OR phone LIKE ? OR plot LIKE ? OR estate LIKE ?)"
		like := "%" + fSearch + "%"
		args = append(args, like, like, like, like)
	}

	// ── payment-% sub-tabs on the transfer / title pending pages ──
	hasTabs := view == "pending-transfer" || view == "pending-title"
	tab := q.Get("tab")
	var tabLt, tabEq int
	if hasTabs {
		db.QueryRow(`SELECT COUNT(*) FROM prop_payment_plan_deals WHERE (` + cfg.Where + `) AND percentage_paid < 100`).Scan(&tabLt)
		db.QueryRow(`SELECT COUNT(*) FROM prop_payment_plan_deals WHERE (` + cfg.Where + `) AND percentage_paid = 100`).Scan(&tabEq)
		if tab != "eq100" {
			tab = "lt100"
			where += " AND percentage_paid < 100"
		} else {
			where += " AND percentage_paid = 100"
		}
	}

	rows, err := db.Query(`
		SELECT deal_id, deal_name, phone, estate, plot, payment_plan, is_three_month, percentage_paid, zoho_stage,
		       COALESCE(consent_file,''),  COALESCE(transfer_file,''), COALESCE(title_file,''),
		       COALESCE(consent_by,''),  COALESCE(DATE_FORMAT(consent_at,'%d %b %Y %H:%i'),''),
		       COALESCE(transfer_by,''), COALESCE(DATE_FORMAT(transfer_at,'%d %b %Y %H:%i'),''),
		       COALESCE(title_by,''),    COALESCE(DATE_FORMAT(title_at,'%d %b %Y %H:%i'),''),
		       COALESCE(DATE_FORMAT(sold_at,'%d %b %Y %H:%i'),''), COALESCE(sold_note,''),
		       COALESCE(DATE_FORMAT(last_synced_at,'%d %b %Y %H:%i'),'')
		FROM prop_payment_plan_deals
		WHERE `+where+`
		ORDER BY percentage_paid DESC, deal_name ASC`, args...)
	if err != nil {
		log.Printf("[payment-plans] list query (%s): %v", view, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var out []ppRow
	for rows.Next() {
		var x ppRow
		var isThree int
		if err := rows.Scan(&x.DealID, &x.DealName, &x.Phone, &x.Estate, &x.Plot, &x.PaymentPlan,
			&isThree, &x.PercentagePaid, &x.ZohoStage,
			&x.ConsentFile, &x.TransferFile, &x.TitleFile,
			&x.ConsentBy, &x.ConsentAt,
			&x.TransferBy, &x.TransferAt,
			&x.TitleBy, &x.TitleAt,
			&x.SoldAt, &x.SoldNote,
			&x.LastSynced); err != nil {
			log.Printf("[payment-plans] list scan (%s): %v", view, err)
			continue
		}
		x.IsThreeMonth = isThree == 1
		x.ConsentFiles = splitFiles(x.ConsentFile)
		x.TransferFiles = splitFiles(x.TransferFile)
		x.TitleFiles = splitFiles(x.TitleFile)
		x.CanTransfer = x.PercentagePaid == 100 && x.ConsentFile != ""
		x.CanTitle = x.PercentagePaid == 100 && x.ConsentFile != "" && x.TransferFile != ""
		computeStage(&x)
		out = append(out, x)
	}

	// Order by pipeline stage, then most-paid, then name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StageRank != out[j].StageRank {
			return out[i].StageRank < out[j].StageRank
		}
		if out[i].PercentagePaid != out[j].PercentagePaid {
			return out[i].PercentagePaid > out[j].PercentagePaid
		}
		return out[i].DealName < out[j].DealName
	})

	uid := getUserID(r)
	isSA := getRole(r) == roleSystemAdmin
	renderAdmin(w, r, "admin_payment_plans_list.html", map[string]any{
		"Title":         cfg.Title,
		"Active":        "payment-plans",
		"View":          view,
		"Path":          r.URL.Path,
		"Rows":          out,
		"Ok":            q.Get("ok") == "1",
		"ErrLocked":     q.Get("err") == "locked",
		"EstateOptions": ppDistinct("estate"),
		"PlanOptions":   ppDistinct("payment_plan"),
		"FEstate":       fEstate,
		"FPlan":         fPlan,
		"FSearch":       fSearch,
		"HasFilter":     fEstate != "" || fPlan != "" || fSearch != "",
		"HasTabs":       hasTabs,
		"Tab":           tab,
		"TabLt":         tabLt,
		"TabEq":         tabEq,
		"CanConvey": isSA || (hasPermission(uid, "admin.payment_plans_conveyancing", "write") &&
			hasPermission(uid, "admin.signed_to_sold", "read")),
	})
}

// ppDistinct returns the sorted distinct non-empty values of a column, for
// populating a filter dropdown. col must be a trusted literal.
func ppDistinct(col string) []string {
	rows, err := db.Query(`SELECT DISTINCT ` + col + ` FROM prop_payment_plan_deals WHERE ` + col + ` <> '' ORDER BY ` + col)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// ─── Per-deal page + document uploads + auto-sold ────────────────────────────

// ppConveyPerm reports whether the request may upload conveyancing docs and
// trigger the auto-sold: system_admin, or both payment_plans_conveyancing
// (write) and signed_to_sold (read).
func ppConveyPerm(r *http.Request) bool {
	if getRole(r) == roleSystemAdmin {
		return true
	}
	uid := getUserID(r)
	return hasPermission(uid, "admin.payment_plans_conveyancing", "write") &&
		hasPermission(uid, "admin.signed_to_sold", "read")
}

// loadPPRow fetches one deal as a ppRow (same columns as the list query).
func loadPPRow(dealID string) (ppRow, bool) {
	var x ppRow
	var isThree int
	err := db.QueryRow(`
		SELECT deal_id, deal_name, phone, estate, plot, payment_plan, is_three_month, percentage_paid, zoho_stage,
		       COALESCE(consent_file,''),  COALESCE(transfer_file,''), COALESCE(title_file,''),
		       COALESCE(consent_by,''),  COALESCE(DATE_FORMAT(consent_at,'%d %b %Y %H:%i'),''),
		       COALESCE(transfer_by,''), COALESCE(DATE_FORMAT(transfer_at,'%d %b %Y %H:%i'),''),
		       COALESCE(title_by,''),    COALESCE(DATE_FORMAT(title_at,'%d %b %Y %H:%i'),''),
		       COALESCE(DATE_FORMAT(sold_at,'%d %b %Y %H:%i'),''), COALESCE(sold_note,''),
		       COALESCE(DATE_FORMAT(last_synced_at,'%d %b %Y %H:%i'),'')
		FROM prop_payment_plan_deals WHERE deal_id=?`, dealID).
		Scan(&x.DealID, &x.DealName, &x.Phone, &x.Estate, &x.Plot, &x.PaymentPlan,
			&isThree, &x.PercentagePaid, &x.ZohoStage,
			&x.ConsentFile, &x.TransferFile, &x.TitleFile,
			&x.ConsentBy, &x.ConsentAt, &x.TransferBy, &x.TransferAt, &x.TitleBy, &x.TitleAt,
			&x.SoldAt, &x.SoldNote, &x.LastSynced)
	if err != nil {
		return x, false
	}
	x.IsThreeMonth = isThree == 1
	x.ConsentFiles = splitFiles(x.ConsentFile)
	x.TransferFiles = splitFiles(x.TransferFile)
	x.TitleFiles = splitFiles(x.TitleFile)
	x.CanTransfer = x.PercentagePaid == 100 && x.ConsentFile != ""
	x.CanTitle = x.PercentagePaid == 100 && x.ConsentFile != "" && x.TransferFile != ""
	computeStage(&x)
	return x, true
}

// paymentPlanDealRouter serves /admin/payment-plans/deal/{dealID}:
//
//	(no suffix)     → the single-deal document page (GET) — the uploader stays
//	                  here after each save to confirm, and the next stage
//	                  unlocks in place.
//	/upload-<step>  → a multipart upload (POST), then back to the same page.
func paymentPlanDealRouter(w http.ResponseWriter, r *http.Request) {
	dealID := pathSegment("/admin/payment-plans/deal/", r.URL.Path)
	suffix := strings.TrimPrefix(r.URL.Path, "/admin/payment-plans/deal/"+dealID)
	switch suffix {
	case "", "/":
		paymentPlanDealPage(w, r, dealID)
	case "/upload-consent":
		paymentPlanUploadStep(w, r, dealID, "consent")
	case "/upload-transfer":
		paymentPlanUploadStep(w, r, dealID, "transfer")
	case "/upload-title":
		paymentPlanUploadStep(w, r, dealID, "title")
	default:
		http.NotFound(w, r)
	}
}

// paymentPlanDealPage renders the focused single-deal document page.
func paymentPlanDealPage(w http.ResponseWriter, r *http.Request, dealID string) {
	row, ok := loadPPRow(dealID)
	if !ok {
		log.Printf("[payment-plans] deal page: %s not found", dealID)
		http.NotFound(w, r)
		return
	}

	from := r.URL.Query().Get("from")
	backURL := "/admin/payment-plans"
	if _, okv := ppListViews[from]; okv {
		backURL = "/admin/payment-plans/" + from
	}

	pid, _, ename, pnum, pstatus, matched := resolveDealPlot(row.Estate, row.Plot)
	log.Printf("[payment-plans] deal page %s (%q) stage=%q pct=%d docs[c=%t t=%t d=%t] matched_plot=%t id=%d status=%q",
		dealID, row.DealName, row.Stage, row.PercentagePaid,
		row.ConsentFile != "", row.TransferFile != "", row.TitleFile != "",
		matched, pid, pstatus)

	renderAdmin(w, r, "admin_payment_plans_deal.html", map[string]any{
		"Title":       "Documents — " + row.DealName,
		"Active":      "payment-plans",
		"D":           row,
		"From":        from,
		"BackURL":     backURL,
		"CanConvey":   ppConveyPerm(r),
		"OkStep":      r.URL.Query().Get("ok"), // consent | transfer | title
		"ErrLocked":   r.URL.Query().Get("err") == "locked",
		"MatchedPlot": matched,
		"PlotNumber":  pnum,
		"PlotEstate":  ename,
		"PlotStatus":  pstatus,
	})
}

// paymentPlanUploadStep handles one multipart document upload and redirects
// back to the deal page (not the list) so the uploader can confirm the file
// landed and move straight on to the next stage.
func paymentPlanUploadStep(w http.ResponseWriter, r *http.Request, dealID, step string) {
	who := getAgentName(r)
	from := r.FormValue("from")
	back := "/admin/payment-plans/deal/" + dealID
	sep := "?"
	if from != "" {
		back += "?from=" + url.QueryEscape(from)
		sep = "&"
	}

	if r.Method != http.MethodPost {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if !ppConveyPerm(r) {
		log.Printf("[payment-plans] upload %s deal %s DENIED for user %q", step, dealID, who)
		w.WriteHeader(http.StatusForbidden)
		render(w, "access_denied", nil)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		log.Printf("[payment-plans] upload %s deal %s: ParseMultipartForm: %v", step, dealID, err)
	}

	var pct int
	var consent, transfer, title string
	if err := db.QueryRow(`SELECT percentage_paid, COALESCE(consent_file,''), COALESCE(transfer_file,''), COALESCE(title_file,'')
		FROM prop_payment_plan_deals WHERE deal_id=?`, dealID).Scan(&pct, &consent, &transfer, &title); err != nil {
		log.Printf("[payment-plans] upload %s: deal %s not found: %v", step, dealID, err)
		http.NotFound(w, r)
		return
	}
	log.Printf("[payment-plans] upload %s received — deal=%s by=%q pct=%d have[c=%t t=%t d=%t]",
		step, dealID, who, pct, consent != "", transfer != "", title != "")

	switch step {
	case "transfer":
		if pct != 100 || consent == "" {
			log.Printf("[payment-plans] upload transfer deal %s BLOCKED (pct=%d consent=%t)", dealID, pct, consent != "")
			http.Redirect(w, r, back+sep+"err=locked", http.StatusSeeOther)
			return
		}
	case "title":
		if pct != 100 || consent == "" || transfer == "" {
			log.Printf("[payment-plans] upload title deal %s BLOCKED (pct=%d consent=%t transfer=%t)",
				dealID, pct, consent != "", transfer != "")
			http.Redirect(w, r, back+sep+"err=locked", http.StatusSeeOther)
			return
		}
	}

	added := saveUploadedFiles(r, "docfile")
	if added == "" {
		log.Printf("[payment-plans] upload %s deal %s — no file received", step, dealID)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	newFiles := splitFiles(added)
	log.Printf("[payment-plans] upload %s deal %s — saved %d file(s): %v", step, dealID, len(newFiles), newFiles)

	existing := map[string]string{"consent": consent, "transfer": transfer, "title": title}[step]
	merged := added
	if existing != "" {
		merged = existing + "," + added
	}
	if _, err := db.Exec(`UPDATE prop_payment_plan_deals SET `+step+`_file=?, `+step+`_at=NOW(), `+step+`_by=? WHERE deal_id=?`,
		merged, who, dealID); err != nil {
		log.Printf("[payment-plans] upload %s deal %s — DB update FAILED: %v", step, dealID, err)
	} else {
		log.Printf("[payment-plans] upload %s deal %s — %s_file now %q", step, dealID, step, merged)
	}

	// Push the just-uploaded file(s) straight to the Zoho CRM deal.
	go func(files []string) {
		log.Printf("[payment-plans] CRM push: %d %s file(s) → deal %s", len(files), step, dealID)
		for _, f := range files {
			if err := uploadCRMAttachment(dealID, f); err != nil {
				log.Printf("[payment-plans] CRM attach %s → deal %s: FAILED: %v", f, dealID, err)
			} else {
				log.Printf("[payment-plans] CRM attach %s → deal %s: ok", f, dealID)
			}
		}
	}(newFiles)

	ppMaybeComplete(dealID, who)

	http.Redirect(w, r, back+sep+"ok="+step, http.StatusSeeOther)
}

// ppUpsertSalesDocs writes the three conveyancing filenames onto the plot's
// prop_sales row, creating it from the latest booking if none exists (same
// columns as adminMarkSoldHandler's upload_doc branch).
func ppUpsertSalesDocs(plotID, estateID int, consent, transfer, title string) {
	var salesID int
	db.QueryRow(`SELECT id FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&salesID)
	if salesID > 0 {
		db.Exec(`UPDATE prop_sales SET letter_of_consent=?, transfer_forms=?, title_deed=? WHERE id=?`,
			consent, transfer, title, salesID)
		return
	}
	var bn, bp, be, an, pplan, dep, idp, kra, pass string
	var amt float64
	db.QueryRow(`SELECT COALESCE(buyer_name,''), COALESCE(buyer_phone,''), COALESCE(buyer_email,''), COALESCE(agent_name,''),
		COALESCE(deposit,0), COALESCE(payment_plan,''), COALESCE(deposit_ref,''), COALESCE(id_photo,''), COALESCE(kra,''), COALESCE(passport_photo,'')
		FROM prop_bookings WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).
		Scan(&bn, &bp, &be, &an, &amt, &pplan, &dep, &idp, &kra, &pass)
	db.Exec(`INSERT INTO prop_sales
		(plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, amount, payment_plan,
		 deposit_doc, id_doc, kra_doc, passport_photo, letter_of_consent, transfer_forms, title_deed)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		plotID, estateID, bn, bp, be, an, amt, pplan, dep, idp, kra, pass, consent, transfer, title)
}

// resolveDealPlot maps a deal's Zoho estate/plot strings to a prop_plots row:
// exact estate-name match first, then a normalized (upper + whitespace-collapsed,
// via normEstate + estateAliases) comparison. Plot number is matched trimmed.
func resolveDealPlot(estate, plot string) (plotID, estateID int, estateName, plotNumber, plotStatus string, ok bool) {
	plot = strings.TrimSpace(plot)

	err := db.QueryRow(`SELECT p.id, e.id, e.name, p.plot_number, p.status
		FROM prop_estates e JOIN prop_plots p ON p.estate_id = e.id
		WHERE e.name = ? AND TRIM(p.plot_number) = ? LIMIT 1`, strings.TrimSpace(estate), plot).
		Scan(&plotID, &estateID, &estateName, &plotNumber, &plotStatus)
	if err == nil {
		return plotID, estateID, estateName, plotNumber, plotStatus, true
	}

	want := normEstate(estate)
	if alias := estateAliases[want]; alias != "" {
		want = alias
	}
	erows, e2 := db.Query(`SELECT id, name FROM prop_estates`)
	if e2 != nil {
		return 0, 0, "", "", "", false
	}
	matchedID, matchedName := 0, ""
	for erows.Next() {
		var id int
		var nm string
		if erows.Scan(&id, &nm) != nil {
			continue
		}
		n := normEstate(nm)
		if alias := estateAliases[n]; alias != "" {
			n = alias
		}
		if n == want {
			matchedID, matchedName = id, nm
			break
		}
	}
	erows.Close()
	if matchedID == 0 {
		return 0, 0, "", "", "", false
	}
	if err := db.QueryRow(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id = ? AND TRIM(plot_number) = ? LIMIT 1`,
		matchedID, plot).Scan(&plotID, &plotNumber, &plotStatus); err != nil {
		return 0, 0, "", "", "", false
	}
	return plotID, matchedID, matchedName, plotNumber, plotStatus, true
}

// ppMaybeComplete is called after each upload. When all three documents are on
// file and Percent_Paid = 100 (and the deal isn't already completed), it flips
// the matched plot sa_signed → sold with full parity to the manual Mark Sold
// flow. Unmatched or non-sa_signed plots are flagged via sold_note instead.
func ppMaybeComplete(dealID, who string) {
	var estate, plot, consent, transfer, title, pplan string
	var pct int
	var soldAt sql.NullString
	if err := db.QueryRow(`SELECT estate, plot, COALESCE(consent_file,''), COALESCE(transfer_file,''), COALESCE(title_file,''),
		percentage_paid, payment_plan, sold_at
		FROM prop_payment_plan_deals WHERE deal_id=?`, dealID).
		Scan(&estate, &plot, &consent, &transfer, &title, &pct, &pplan, &soldAt); err != nil {
		return
	}
	if soldAt.Valid && soldAt.String != "" {
		log.Printf("[payment-plans] ppMaybeComplete deal %s — already completed (%s), nothing to do", dealID, soldAt.String)
		return
	}
	if consent == "" || transfer == "" || title == "" || pct != 100 {
		log.Printf("[payment-plans] ppMaybeComplete deal %s — not yet complete (consent=%t transfer=%t title=%t pct=%d)",
			dealID, consent != "", transfer != "", title != "", pct)
		return
	}
	log.Printf("[payment-plans] ppMaybeComplete deal %s — all 3 docs in at 100%%, resolving plot for %q / %q", dealID, estate, plot)

	plotID, estateID, estateName, plotNumber, plotStatus, ok := resolveDealPlot(estate, plot)
	log.Printf("[payment-plans] ppMaybeComplete deal %s — resolve: matched=%t plotID=%d status=%q", dealID, ok, plotID, plotStatus)
	// (The three documents were already pushed to the Zoho deal as they were
	// uploaded — see paymentPlanUploadStep.)

	if !ok {
		note := fmt.Sprintf("no matching signed plot for %s / %s — mark sold manually", estate, plot)
		db.Exec(`UPDATE prop_payment_plan_deals SET sold_at=NOW(), sold_note=? WHERE deal_id=?`, note, dealID)
		log.Printf("[payment-plans] deal %s docs complete but unmatched (%s / %s)", dealID, estate, plot)
		return
	}

	if plotStatus != "sa_signed" {
		note := fmt.Sprintf("matched plot %s is '%s', not sa_signed — not auto-marked sold", plotNumber, plotStatus)
		if plotStatus == "sold" {
			note = "matched plot already sold — documents recorded"
			ppUpsertSalesDocs(plotID, estateID, consent, transfer, title)
		}
		db.Exec(`UPDATE prop_payment_plan_deals SET sold_at=NOW(), sold_plot_id=?, sold_note=? WHERE deal_id=?`,
			plotID, note, dealID)
		log.Printf("[payment-plans] deal %s: matched plot %d status=%s — %s", dealID, plotID, plotStatus, note)
		return
	}

	// sa_signed → sold, mirroring adminMarkSoldHandler's mark_sold branch.
	db.Exec(`UPDATE prop_plots SET status='sold' WHERE id=?`, plotID)
	db.Exec(`UPDATE prop_bookings SET status='completed' WHERE plot_id=? AND status IN ('active','sa_signed')`, plotID)
	ppUpsertSalesDocs(plotID, estateID, consent, transfer, title)
	logPlotStatus(plotID, plotNumber, estateName, estateID, "sa_signed", "sold", who, "payment-plans conveyancing complete")

	var bn, bp, be, an string
	var amt float64
	db.QueryRow(`SELECT COALESCE(buyer_name,''), COALESCE(buyer_phone,''), COALESCE(buyer_email,''), COALESCE(agent_name,''), COALESCE(deposit,0)
		FROM prop_bookings WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&bn, &bp, &be, &an, &amt)
	go func() {
		if err := sendSoldEmail(bookingInfo{
			PlotNumbers: []string{plotNumber}, EstateName: estateName,
			BuyerName: bn, BuyerPhone: bp, BuyerEmail: be, AgentName: an,
			Deposit: fmt.Sprintf("%.2f", amt), PaymentPlan: pplan,
		}); err != nil {
			log.Printf("[payment-plans] sold email (deal %s): %v", dealID, err)
		}
	}()

	db.Exec(`UPDATE prop_payment_plan_deals SET sold_at=NOW(), sold_plot_id=?, sold_note=NULL WHERE deal_id=?`, plotID, dealID)
	log.Printf("[payment-plans] deal %s: plot %d (%s / %s) marked SOLD via conveyancing by %s",
		dealID, plotID, estateName, plotNumber, who)
}
