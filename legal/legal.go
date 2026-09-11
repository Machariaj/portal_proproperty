// Package legal implements the Legal review module: bookings Accounts has
// approved and forwarded move through three stages — drafting the sale
// agreement, awaiting the client's signature, then completed (signed) — with
// Legal able to cancel (plot -> available) from either of the first two.
package legal

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
)

var (
	db                *sql.DB
	renderFn          func(w http.ResponseWriter, name string, data any)
	getAgentFn        func(r *http.Request) string
	getUserIDFn       func(r *http.Request) string
	isSystemAdminFn   func(r *http.Request) bool
	saveUploadedFiles func(r *http.Request, fieldName string) string
	processSAIntegr   func(plotID int, plotNumber, estateName string)
	cancelBooks       func(estimateID string)
	sendOutcome       func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error
)

// Init wires this package to the host app's shared dependencies. Each
// estate has exactly one assigned lawyer (prop_estates.lawyer_id, a
// Legal-role prop_agents.id) — getUserIDFn identifies which one is logged
// in, so every list/action here is scoped to that lawyer's own estates
// unless isSystemAdminFn(r) is true (system_admin sees everything).
func Init(
	d *sql.DB,
	render func(http.ResponseWriter, string, any),
	agentFn func(*http.Request) string,
	getUserID func(*http.Request) string,
	isSystemAdmin func(*http.Request) bool,
	saveFiles func(r *http.Request, fieldName string) string,
	processSignedIntegrations func(plotID int, plotNumber, estateName string),
	cancelBooksEstimate func(estimateID string),
	sendOutcomeEmail func(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error,
) {
	db = d
	renderFn = render
	getAgentFn = agentFn
	getUserIDFn = getUserID
	isSystemAdminFn = isSystemAdmin
	saveUploadedFiles = saveFiles
	processSAIntegr = processSignedIntegrations
	cancelBooks = cancelBooksEstimate
	sendOutcome = sendOutcomeEmail
}

// scopeToLawyer appends an estate-ownership filter to query/args unless the
// caller is system_admin. Returns the (possibly unchanged) query and args.
func scopeToLawyer(r *http.Request, query string, args []any) (string, []any) {
	if isSystemAdminFn(r) {
		return query, args
	}
	return query + ` AND e.lawyer_id = ?`, append(args, getUserIDFn(r))
}

// isAssignedLawyer reports whether the current user may act on a booking on
// the given estate — either they're system_admin, or the estate's
// lawyer_id matches their own user ID.
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

func renderLegal(w http.ResponseWriter, name string, data map[string]any) {
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

// Router handles all /legal/* requests. Access is gated by role at the mux
// level in main.go (requireRole(roleLegal, ...)).
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
	DateBooked string
}

// queueHandler lists stage 1: bookings Legal has just received from
// Accounts and has not yet drafted a sale agreement for.
func queueHandler(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       DATE_FORMAT(b.date_booked,'%d %b %Y')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status = 'pending_wakili_review' AND b.legal_stage = 'drafting'`
	query, args := scopeToLawyer(r, query, nil)
	query += ` ORDER BY b.date_booked ASC`

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
		if err := rows.Scan(&q.BookingID, &q.BuyerName, &q.AgentName, &q.EstateName, &q.PlotNumber, &q.DateBooked); err == nil {
			queue = append(queue, q)
		}
	}

	renderLegal(w, "legal_queue.html", map[string]any{
		"Title":  "Legal — Pending Drafting Sale Agreement",
		"Active": "queue",
		"Queue":  queue,
		"Error":  r.URL.Query().Get("err"),
	})
}

// awaitingSignatureHandler lists stage 2: bookings whose sale agreement has
// been drafted and sent out, waiting on the client's signature.
func awaitingSignatureHandler(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       DATE_FORMAT(b.date_booked,'%d %b %Y')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status = 'pending_wakili_review' AND b.legal_stage = 'awaiting_signature'`
	query, args := scopeToLawyer(r, query, nil)
	query += ` ORDER BY b.date_booked ASC`

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
		if err := rows.Scan(&q.BookingID, &q.BuyerName, &q.AgentName, &q.EstateName, &q.PlotNumber, &q.DateBooked); err == nil {
			queue = append(queue, q)
		}
	}

	renderLegal(w, "legal_awaiting.html", map[string]any{
		"Title":  "Legal — Pending Client Signature",
		"Active": "awaiting",
		"Queue":  queue,
		"Error":  r.URL.Query().Get("err"),
	})
}

type completedRow struct {
	BookingID  int
	BuyerName  string
	AgentName  string
	EstateName string
	PlotNumber string
	DateSigned string
	PlotStatus string
}

// completedHandler lists every booking whose sale agreement is done —
// currently sa_signed, or sold on top of that (status='completed', per the
// user's explicit ask to keep showing these here even after the plot moves
// on) — optionally narrowed to one month by date_signed.
func completedHandler(w http.ResponseWriter, r *http.Request) {
	month := strings.TrimSpace(r.URL.Query().Get("month")) // "YYYY-MM" or "" for all

	query := `
		SELECT b.id, b.buyer_name, COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'), ''), p.status
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status IN ('sa_signed','completed')`
	var args []any
	if month != "" {
		query += ` AND DATE_FORMAT(b.date_signed,'%Y-%m') = ?`
		args = append(args, month)
	}
	query, args = scopeToLawyer(r, query, args)
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
		if err := rows.Scan(&c.BookingID, &c.BuyerName, &c.AgentName, &c.EstateName, &c.PlotNumber, &c.DateSigned, &c.PlotStatus); err == nil {
			list = append(list, c)
		}
	}

	renderLegal(w, "legal_completed.html", map[string]any{
		"Title":  "Legal — Completed Sale Agreement",
		"Active": "completed",
		"List":   list,
		"Total":  len(list),
		"Month":  month,
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
		       COALESCE(b.notes,''), COALESCE(b.accounts_notes,''), b.legal_stage,
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE b.id = ?`, bookingID).
		Scan(&d.BookingID, &d.BuyerName, &d.BuyerPhone, &d.BuyerEmail,
			&d.AgentName, &d.AgentPhone, &d.AgentEmail,
			&d.EstateID, &d.EstateName, &d.PlotID, &d.PlotNumber,
			&d.Deposit, &d.PaymentPlan,
			&d.Notes, &d.AccountsNotes, &d.LegalStage, &depositRef, &idPhoto, &kra, &passportPhoto)
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
	if !isAssignedLawyer(r, detail.EstateID) {
		http.Error(w, "Access denied — this estate is not assigned to you.", http.StatusForbidden)
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
	if !isAssignedLawyer(r, estateID) {
		http.Error(w, "Access denied — this estate is not assigned to you.", http.StatusForbidden)
		return
	}

	if _, err := db.Exec(`UPDATE prop_bookings SET legal_stage='awaiting_signature'
		WHERE id=? AND status='pending_wakili_review' AND legal_stage='drafting'`, bookingID); err != nil {
		log.Printf("legal send-for-signature: %v", err)
		http.Redirect(w, r, fmt.Sprintf("/legal/review/%s?err=Database+error", bookingID), http.StatusFound)
		return
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
	if !isAssignedLawyer(r, estateIDForCheck) {
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
	if !isAssignedLawyer(r, estateIDForCheck) {
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
