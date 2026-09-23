// Package accounts implements the Accounts review module: a queue of
// bookings that have met both entry conditions (KYC docs complete, deposit
// at/above the estate's threshold) and are waiting for Accounts to approve
// (forwarding to Legal) or cancel (releasing the plot).
package accounts

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
)

var (
	db              *sql.DB
	renderFn        func(w http.ResponseWriter, name string, data any)
	getAgentFn      func(r *http.Request) string
	cancelBooks     func(estimateID string)
	sendOutcome     func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error
	createBooks     func(bookingID string)
	sendApproved    func(bookingID string) error
	sendApprovedSMS func(bookingID string)
	notifyLawyer    func(bookingID string)
)

// Init wires this package to the host app's shared dependencies. cancelBooks,
// sendOutcomeEmail, createBooks, sendApprovedEmail, sendApprovedSMSFn and
// notifyLawyerFn are injected rather than imported directly since they live
// in package main (cancelBooksEstimate, sendReviewOutcomeEmail,
// createBooksRecordForBookingID, sendAccountsApprovedEmail,
// sendAccountsApprovedSMS, notifyLawyerOfNewCase).
func Init(
	d *sql.DB,
	render func(http.ResponseWriter, string, any),
	agentFn func(*http.Request) string,
	cancelBooksEstimate func(estimateID string),
	sendOutcomeEmail func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error,
	createBooksRecord func(bookingID string),
	sendApprovedEmail func(bookingID string) error,
	sendApprovedSMSFn func(bookingID string),
	notifyLawyerFn func(bookingID string),
) {
	db = d
	renderFn = render
	getAgentFn = agentFn
	cancelBooks = cancelBooksEstimate
	sendOutcome = sendOutcomeEmail
	createBooks = createBooksRecord
	sendApprovedSMS = sendApprovedSMSFn
	sendApproved = sendApprovedEmail
	notifyLawyer = notifyLawyerFn
}

func renderAccounts(w http.ResponseWriter, name string, data map[string]any) {
	renderFn(w, name, data)
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

// Router handles all /accounts/* requests. Access is gated by role at the
// mux level in main.go (requireRole(roleAccounts, ...)) — everything reaching
// here is already a confirmed Accounts (or system_admin) session.
func Router(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/accounts" || p == "/accounts/":
		http.Redirect(w, r, "/accounts/queue", http.StatusFound)
	case p == "/accounts/queue":
		queueHandler(w, r)
	case strings.HasPrefix(p, "/accounts/review/") && strings.HasSuffix(p, "/approve"):
		approveHandler(w, r)
	case strings.HasPrefix(p, "/accounts/review/") && strings.HasSuffix(p, "/cancel"):
		cancelHandler(w, r)
	case strings.HasPrefix(p, "/accounts/review/"):
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
	Deposit    float64
	Threshold  float64
	DateBooked string
}

func queueHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(b.deposit,0), COALESCE(e.deposit_threshold,0),
		       DATE_FORMAT(b.date_booked,'%d %b %Y')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status = 'pending_accounts_review'
		ORDER BY b.date_booked ASC`)
	if err != nil {
		log.Printf("accounts queue: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var queue []queueRow
	for rows.Next() {
		var q queueRow
		if err := rows.Scan(&q.BookingID, &q.BuyerName, &q.AgentName, &q.EstateName, &q.PlotNumber,
			&q.Deposit, &q.Threshold, &q.DateBooked); err == nil {
			queue = append(queue, q)
		}
	}

	renderAccounts(w, "accounts_queue.html", map[string]any{
		"Title":  "Accounts — Review Queue",
		"Active": "queue",
		"Queue":  queue,
		"Error":  r.URL.Query().Get("err"),
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
	EstateName         string
	PlotNumber         string
	PlotID             int
	Deposit            float64
	Threshold          float64
	PaymentPlan        string
	Notes              string
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
		       e.name, p.plot_number, b.plot_id,
		       COALESCE(b.deposit,0), COALESCE(e.deposit_threshold,0), COALESCE(b.payment_plan,''),
		       COALESCE(b.notes,''),
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE b.id = ?`, bookingID).
		Scan(&d.BookingID, &d.BuyerName, &d.BuyerPhone, &d.BuyerEmail,
			&d.AgentName, &d.AgentPhone, &d.AgentEmail,
			&d.EstateName, &d.PlotNumber, &d.PlotID,
			&d.Deposit, &d.Threshold, &d.PaymentPlan,
			&d.Notes, &depositRef, &idPhoto, &kra, &passportPhoto)
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
	bookingID := bookingIDFromPath("/accounts/review/", r.URL.Path)
	detail, err := loadReviewDetail(bookingID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	type receiptRow struct {
		ReceiptNumber string
		Amount        string
		CreatedAt     string
	}
	var receipts []receiptRow
	if rrows, rerr := db.Query(`SELECT receipt_number, CAST(amount AS CHAR), DATE_FORMAT(created_at,'%d %b %Y %H:%i') FROM prop_booking_receipts WHERE booking_id=? ORDER BY id DESC`, bookingID); rerr == nil {
		defer rrows.Close()
		for rrows.Next() {
			var rr receiptRow
			if rrows.Scan(&rr.ReceiptNumber, &rr.Amount, &rr.CreatedAt) == nil {
				receipts = append(receipts, rr)
			}
		}
	}

	renderAccounts(w, "accounts_review.html", map[string]any{
		"Title":    "Review — " + detail.BuyerName,
		"Active":   "queue",
		"Info":     detail,
		"Receipts": receipts,
		"Error":    r.URL.Query().Get("err"),
	})
}

func approveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/accounts/queue", http.StatusFound)
		return
	}
	bookingID := bookingIDFromPath("/accounts/review/", strings.TrimSuffix(r.URL.Path, "/approve"))
	r.ParseForm()
	notes := strings.TrimSpace(r.FormValue("notes"))
	reviewer := getAgentFn(r)

	res, err := db.Exec(`UPDATE prop_bookings SET status='pending_wakili_review', accounts_notes=?, accounts_reviewed_by=?, accounts_reviewed_at=NOW()
		WHERE id=? AND status='pending_accounts_review'`, notes, reviewer, bookingID)
	if err != nil {
		log.Printf("accounts approve: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/accounts/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Redirect(w, r, "/accounts/queue?err=Booking+already+reviewed", http.StatusFound)
		return
	}

	// Zoho Books estimate is deliberately deferred until this exact moment —
	// Accounts approval — rather than at initial booking time.
	if createBooks != nil {
		go createBooks(bookingID)
	}

	// sales@/systemadmin@ are notified (without attachments) here, once the
	// booking has actually cleared Accounts and moved to Legal — not at
	// doc-completion time anymore.
	if sendApproved != nil {
		go func() {
			if err := sendApproved(bookingID); err != nil {
				log.Printf("accounts approve: notification email error: %v", err)
			}
		}()
	}
	// Buyer/agent SMS, per Settings -> Notifications — same "now with Legal" event.
	if sendApprovedSMS != nil {
		sendApprovedSMS(bookingID)
	}
	// Assigned lawyer email + SMS — always fires, not gated by that setting.
	if notifyLawyer != nil {
		notifyLawyer(bookingID)
	}

	http.Redirect(w, r, "/accounts/queue", http.StatusFound)
}

func cancelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/accounts/queue", http.StatusFound)
		return
	}
	bookingID := bookingIDFromPath("/accounts/review/", strings.TrimSuffix(r.URL.Path, "/cancel"))
	r.ParseForm()
	notes := strings.TrimSpace(r.FormValue("notes"))
	if notes == "" {
		http.Redirect(w, r, fmt.Sprintf("/accounts/review/%s?err=A+reason+is+required+to+cancel", bookingID), http.StatusFound)
		return
	}
	reviewer := getAgentFn(r)

	var plotID int
	var zohoBooksID string
	if err := db.QueryRow(`SELECT plot_id, COALESCE(zoho_books_id,'') FROM prop_bookings WHERE id=? AND status='pending_accounts_review'`, bookingID).
		Scan(&plotID, &zohoBooksID); err != nil {
		http.Redirect(w, r, "/accounts/queue?err=Booking+already+reviewed", http.StatusFound)
		return
	}

	if _, err := db.Exec(`UPDATE prop_bookings SET status='cancelled', accounts_notes=?, accounts_reviewed_by=?, accounts_reviewed_at=NOW()
		WHERE id=? AND status='pending_accounts_review'`, notes, reviewer, bookingID); err != nil {
		log.Printf("accounts cancel: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/accounts/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
	}
	db.Exec(`UPDATE prop_plots SET status='available' WHERE id=?`, plotID)
	if zohoBooksID != "" && cancelBooks != nil {
		go cancelBooks(zohoBooksID)
	}

	detail, derr := loadReviewDetail(bookingID)
	if derr == nil && sendOutcome != nil {
		go sendOutcome("accounts_cancelled", detail.PlotNumber, detail.EstateName, detail.BuyerName, detail.BuyerPhone, detail.AgentName, notes)
	}

	http.Redirect(w, r, "/accounts/queue", http.StatusFound)
}
