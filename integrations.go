package main

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"marketers_portal/vanbooking"
)

// ─── Dev Mode ────────────────────────────────────────────────────────────────
// Set to true locally to disable outbound notifications (email, WhatsApp, SMS).
// Zoho Books and CRM integrations still run when devMode is true — so you can
// test Zoho without inbox spam.
// Set to false before deploying to production.
const devMode = true

// ─── Credentials ─────────────────────────────────────────────────────────────

const (
	zohoCRMClientID     = "1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN"
	zohoCRMClientSecret = "3c01794c2d6172f0636d041751d7562157d7e7d885"
	zohoCRMRefreshToken = "1000.3f5e3d4e023da520dc83f4494f81e304.9572365b60f6103d1ec412acd787eef0"

	zohoBooksClientID     = "1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN"
	zohoBooksClientSecret = "3c01794c2d6172f0636d041751d7562157d7e7d885"
	zohoBooksRefreshToken = "1000.0c0038d56d62ccb76729861bfb06fc3f.8ed7ffb6d0ee07362394268e8011e2e3"
	zohoBooksOrgID        = "897770663"

	zohoTokenURL  = "https://accounts.zoho.com/oauth/v2/token"
	zohoCRMBase   = "https://www.zohoapis.com/crm/v8/"
	zohoBooksBase = "https://www.zohoapis.com/books/v3/"

	smtpHost = "smtp.zoho.com"
	smtpPort = 465
	smtpUser = "notifications@proproperty.co.ke"
	smtpPass = "ay.r8iVcay.r8iVc"
	smtpFrom = "notifications@proproperty.co.ke"
)

// bookingPendingRecipients — every new booking regardless of docs status.
var bookingPendingRecipients = []string{
	"info@proproperty.co.ke",
	"systemadmin@proproperty.co.ke",
}

// bookingCompleteRecipients — booking made with all docs uploaded at the time
// of booking. sales@/systemadmin@ deliberately excluded — they no longer get
// a with-attachments notification at doc-completion time; they get a
// separate, attachment-free one once Accounts approves and the booking
// moves to Legal instead (see accountsApprovedRecipients).
var bookingCompleteRecipients = []string{
	"info@proproperty.co.ke",
}

// accountsApprovedRecipients — notified, without attachments, once a
// booking clears Accounts review and moves to Legal (normal approval, or a
// system_admin's skip-Accounts override). Replaces the old
// docs-complete-with-attachments email these two addresses used to get.
var accountsApprovedRecipients = []string{
	"sales@proproperty.co.ke",
	"systemadmin@proproperty.co.ke",
}

// soldRecipients — plot marked as sold.
var soldRecipients = []string{
	"info@proproperty.co.ke",
	"sales@proproperty.co.ke",
	"systemadmin@proproperty.co.ke",
	"sales@proproperty.co.ke",
}

// reviewOutcomeRecipients — a terminal outcome from the Accounts or Legal
// review stage: Accounts cancelling, or Legal signing/cancelling.
var reviewOutcomeRecipients = []string{
	"sales@proproperty.co.ke",
	"accounts@proproperty.co.ke",
}

// ─── Token Cache ──────────────────────────────────────────────────────────────

var (
	crmMu        sync.Mutex
	crmToken     string
	crmExpires   time.Time
	booksMu      sync.Mutex
	booksToken   string
	booksExpires time.Time
)

func refreshToken(clientID, clientSecret, refreshTok string) (string, time.Time, error) {
	resp, err := http.PostForm(zohoTokenURL, url.Values{
		"refresh_token": {refreshTok},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if r.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("zoho token refresh failed: %s", r.Error)
	}
	exp := time.Now().Add(time.Duration(r.ExpiresIn-300) * time.Second)
	return r.AccessToken, exp, nil
}

func getCRMToken() (string, error) {
	crmMu.Lock()
	defer crmMu.Unlock()
	if time.Now().Before(crmExpires) {
		return crmToken, nil
	}
	t, exp, err := refreshToken(zohoCRMClientID, zohoCRMClientSecret, zohoCRMRefreshToken)
	if err != nil {
		return "", err
	}
	crmToken, crmExpires = t, exp
	return t, nil
}

func getBooksToken() (string, error) {
	booksMu.Lock()
	defer booksMu.Unlock()
	if time.Now().Before(booksExpires) {
		return booksToken, nil
	}
	t, exp, err := refreshToken(zohoBooksClientID, zohoBooksClientSecret, zohoBooksRefreshToken)
	if err != nil {
		return "", err
	}
	booksToken, booksExpires = t, exp
	return t, nil
}

// ─── Booking Data ─────────────────────────────────────────────────────────────

type bookingInfo struct {
	PlotIDs         []int // database plot IDs — used to store zoho_books_id after creation
	BuyerName       string
	BuyerPhone      string
	BuyerEmail      string
	PlotNumbers     []string
	EstateName      string
	Deposit         string
	PaymentPlan     string
	AgentName       string
	PlotPrice       float64
	DepositRef      string // comma-separated filenames
	IDPhoto         string
	KRA             string
	PassportPhoto   string
	SaleAgreement   string
	Notes           string
	CareOf          string // agent/admin-entered at booking time; shown only to them, forwarded only to the Zoho CRM deal's Care_Of field (see createCRMDeal) — never to Accounts, Legal, or any email/SMS
	RebookConfirmed bool   // agent confirmed this is the same client rebooking a plot they'd previously booked
}

// hasAllAttachments returns true only when all four document fields are filled.
func hasAllAttachments(b bookingInfo) bool {
	return b.DepositRef != "" && b.IDPhoto != "" && b.KRA != "" && b.PassportPhoto != ""
}

// maybeAdvanceToAccountsReview checks whether a booking now satisfies both
// entry conditions for the Accounts review queue — all four KYC docs present,
// and the deposit paid at or above the estate's deposit_threshold — and, if
// so, flips it from 'active' to 'pending_accounts_review'. No-ops (returns
// false, nil) if the booking isn't currently 'active', so it's safe to call
// redundantly from every path that can change docs or deposit (booking
// creation, doc upload, deposit top-up).
func maybeAdvanceToAccountsReview(bookingID int) (bool, error) {
	var status, depositRef, idPhoto, kra, passportPhoto, depositStr string
	var threshold sql.NullFloat64
	err := db.QueryRow(`
		SELECT b.status, COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
		       COALESCE(b.kra,''), COALESCE(b.passport_photo,''),
		       COALESCE(CAST(b.deposit AS CHAR),'0'), e.deposit_threshold
		FROM prop_bookings b
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.id = ?`, bookingID).
		Scan(&status, &depositRef, &idPhoto, &kra, &passportPhoto, &depositStr, &threshold)
	if err != nil {
		return false, err
	}
	if status != "active" {
		return false, nil
	}
	if !hasAllAttachments(bookingInfo{DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto}) {
		return false, nil
	}
	var deposit float64
	fmt.Sscanf(depositStr, "%f", &deposit)
	if threshold.Valid && deposit < threshold.Float64 {
		return false, nil
	}
	res, err := db.Exec(`UPDATE prop_bookings SET status='pending_accounts_review' WHERE id=? AND status='active'`, bookingID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	logBooking("ACCOUNTS_REVIEW_QUEUED", "", "", fmt.Sprintf("booking %d", bookingID), "docs complete and deposit threshold met")
	return true, nil
}

// sendReviewOutcomeEmail notifies sales+accounts of a terminal outcome from
// the Accounts or Legal review stage — outcome is one of "accounts_cancelled",
// "legal_signed", "legal_cancelled". devMode-gated internally via sendPlainEmail.
func sendReviewOutcomeEmail(outcome, plotNumber, estateName, buyerName, buyerPhone, agentName, notes string) error {
	subjectByOutcome := map[string]string{
		"accounts_cancelled": "Booking Cancelled by Accounts",
		"legal_signed":       "Sale Agreement Signed",
		"legal_cancelled":    "Booking Cancelled by Legal",
	}
	label := subjectByOutcome[outcome]
	if label == "" {
		label = "Booking Review Outcome"
	}
	subject := fmt.Sprintf("%s — %s — Plot %s", label, estateName, plotNumber)
	body := fmt.Sprintf(
		"Outcome:        %s\r\n\r\n"+
			"Estate:         %s\r\n"+
			"Plot:           %s\r\n"+
			"Buyer Name:     %s\r\n"+
			"Phone:          %s\r\n"+
			"Agent:          %s\r\n"+
			"Date:           %s\r\n",
		label, estateName, plotNumber, buyerName, buyerPhone, agentName,
		time.Now().Format("02 Jan 2006 15:04"),
	)
	if strings.TrimSpace(notes) != "" {
		body += fmt.Sprintf("\r\nNotes:\r\n%s\r\n", notes)
	}
	return sendPlainEmail(reviewOutcomeRecipients, subject, body)
}

// processBookingIntegrations runs booking-time integrations asynchronously.
// Zoho Books estimate creation does NOT happen here — per policy, a plot
// shouldn't get a Books record just for being booked. It's deferred until
// Accounts actually approves the booking (accounts.approveHandler) or a
// system_admin explicitly skips Accounts and pushes it straight to Legal
// (adminSkipAccountsHandler) — see createBooksRecordForBookingID, called
// from both of those.
// Zoho CRM fires at SA Signed (see processSignedIntegrations).
func processBookingIntegrations(b bookingInfo) {
	// Booking-confirmation SMS (buyer and/or agent, per Settings ->
	// Notifications) fires on every booking, regardless of whether docs/
	// deposit are already complete — it's the start of the 14-day KYC/
	// deposit clock, not a completion notice. vanbooking.SendSMS self-gates
	// on devMode, safe to call unconditionally.
	go sendBuyerBookingConfirmationSMS(b)
	go sendAgentBookingConfirmationSMS(b)

	if devMode {
		log.Printf("[devMode] notifications skipped for %s — email and SMS disabled", b.BuyerName)
		return
	}

	if !hasAllAttachments(b) {
		log.Printf("[integrations] attachments incomplete for %s — sending internal alert", b.BuyerName)
		go func() {
			if err := sendPendingDocsAlert(b); err != nil {
				log.Printf("[integrations] pending docs alert error: %v", err)
			}
		}()
		return
	}
	go func() {
		// 1. Email notification
		if err := sendBookingEmail(b, bookingCompleteRecipients, true); err != nil {
			log.Printf("[email] send error: %v", err)
		} else {
			plotStr := strings.Join(b.PlotNumbers, ", ")
			logBooking("EMAIL_SENT", b.AgentName, b.BuyerName,
				plotStr+" — "+b.EstateName, "email="+b.BuyerEmail)
		}

		// 2. SMS alert to management
		plotStr := strings.Join(b.PlotNumbers, ", ")
		smsMsg := fmt.Sprintf(
			"BOOKING COMPLETE: %s has booked Plot(s) %s at %s. Agent: %s. Deposit: KES %s. All docs uploaded.",
			b.BuyerName, plotStr, b.EstateName, b.AgentName, b.Deposit,
		)
		go vanbooking.SendSMS("254721866681", smsMsg)
		go vanbooking.SendSMS("254798811426", smsMsg)
	}()
}

// sendBuyerBookingConfirmationSMS sends the buyer the initial "thank you for
// booking" SMS: which plot(s)/estate, the 14-day window to pay the
// outstanding balance against the estate's deposit threshold and submit KYC
// docs (ID copy, KRA PIN, passport photo) to their agent, and that the plot
// reverts to available if either isn't met in time. Only mentions whichever
// of the two (balance, docs) is actually still outstanding at booking time —
// a rebooking or an agent who captured an initial deposit/docs on the spot
// may already have one or both covered. Skipped entirely if the estate has
// no deposit_threshold configured — the 14-day payment/release premise
// doesn't apply without one. Fired once per booking from
// processBookingIntegrations regardless of completeness — see
// checkClientReminderSMS (day 10-14 follow-ups) and checkOverdueBookings'
// release notice (scheduler.go) for the rest of this SMS sequence.
func sendBuyerBookingConfirmationSMS(b bookingInfo) {
	if b.BuyerPhone == "" || !notifyBuyerEnabled() {
		return
	}
	msg, ok := buildBuyerBookingConfirmationSMS(b)
	if !ok {
		return
	}
	vanbooking.SendSMS(b.BuyerPhone, msg)
}

// buildBuyerBookingConfirmationSMS builds the message text for
// sendBuyerBookingConfirmationSMS, split out so the wording can be unit
// tested without actually sending an SMS. ok is false when the estate has no
// deposit_threshold configured, meaning no SMS should be sent at all.
func buildBuyerBookingConfirmationSMS(b bookingInfo) (msg string, ok bool) {
	balanceMsg, balanceOwed, configured := depositBalanceMsg(b)
	if !configured {
		return "", false
	}
	plotStr := strings.Join(b.PlotNumbers, ", ")
	docsOwed := !hasAllAttachments(b)

	var body string
	switch {
	case balanceOwed && docsOwed:
		body = fmt.Sprintf(
			"You have 14 days from today to pay %s and share your ID copy, KRA PIN and passport-size photo (soft copy) with %s. "+
				"If either is not done within 14 days, the plot will be released back to available.",
			balanceMsg, b.AgentName)
	case balanceOwed:
		body = fmt.Sprintf(
			"You have 14 days from today to pay %s. If this is not done within 14 days, the plot will be released back to available.",
			balanceMsg)
	case docsOwed:
		body = fmt.Sprintf(
			"You have 14 days from today to share your ID copy, KRA PIN and passport-size photo (soft copy) with %s. "+
				"If this is not done within 14 days, the plot will be released back to available.",
			b.AgentName)
	default:
		body = "Your deposit and KYC documents are already on file — thank you!"
	}

	return fmt.Sprintf("Dear %s, Thank you for booking Plot %s at %s. %s - Pro-Property",
		b.BuyerName, plotStr, b.EstateName, body), true
}

// depositBalanceMsg returns the phrase for the buyer SMS's "pay X" clause —
// the estate's deposit_threshold minus what's already recorded as paid on
// this booking (b.Deposit) — whether any balance is actually owed, and
// whether the estate has a threshold configured at all. configured is false
// (and msg/owed meaningless) when there's no plot to look up or the estate
// has no deposit_threshold set — callers should skip sending in that case
// rather than reference a vague, unconfigured amount.
func depositBalanceMsg(b bookingInfo) (msg string, owed bool, configured bool) {
	if len(b.PlotIDs) == 0 {
		return "", false, false
	}
	var threshold sql.NullFloat64
	db.QueryRow(`SELECT e.deposit_threshold FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.id=?`,
		b.PlotIDs[0]).Scan(&threshold)
	if !threshold.Valid || threshold.Float64 <= 0 {
		return "", false, false
	}
	paid, _ := strconv.ParseFloat(b.Deposit, 64)
	balance := threshold.Float64 - paid
	if balance <= 0 {
		return "", false, true
	}
	return fmt.Sprintf("the balance of KES %.0f", balance), true, true
}

// sendAgentBookingConfirmationSMS sends the booking agent the same
// booking-confirmation information as the buyer's SMS — which client, plot,
// estate, the 14-day deadline, and the deposit threshold — so they can
// follow up with their client. Needs its own phone lookup since bookingInfo
// only carries the agent's name, not their phone.
func sendAgentBookingConfirmationSMS(b bookingInfo) {
	if b.AgentName == "" || !notifyAgentEnabled() {
		return
	}
	var agentPhone string
	db.QueryRow(`SELECT COALESCE(phone,'') FROM prop_agents WHERE name=? LIMIT 1`, b.AgentName).Scan(&agentPhone)
	if agentPhone == "" {
		return
	}
	plotStr := strings.Join(b.PlotNumbers, ", ")
	msg := fmt.Sprintf(
		"Booking confirmed: %s booked Plot %s at %s. They have 14 days from today to pay %s and submit ID copy, KRA PIN and passport-size photo, "+
			"or the plot will be released back to available. - Pro-Property",
		b.BuyerName, plotStr, b.EstateName, depositThresholdMsg(b),
	)
	vanbooking.SendSMS(agentPhone, msg)
}

// depositThresholdMsg returns a human-readable phrase for the estate's
// deposit threshold ("the deposit threshold of KES 50000"), falling back to
// a generic phrase if the estate has none configured. Shared by the buyer
// and agent booking-confirmation SMS above.
func depositThresholdMsg(b bookingInfo) string {
	if len(b.PlotIDs) == 0 {
		return "the set deposit threshold"
	}
	var threshold sql.NullFloat64
	db.QueryRow(`SELECT e.deposit_threshold FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.id=?`,
		b.PlotIDs[0]).Scan(&threshold)
	if threshold.Valid && threshold.Float64 > 0 {
		return fmt.Sprintf("the deposit threshold of KES %.0f", threshold.Float64)
	}
	return "the set deposit threshold"
}

// sendPendingDocsAlert notifies bookingPendingRecipients that a booking was made
// with missing documents, attaching the payment reference file if it was uploaded.
func sendPendingDocsAlert(b bookingInfo) error {
	if devMode {
		log.Printf("[devMode] skipping pending docs alert for %s", b.BuyerName)
		return nil
	}
	plots := strings.Join(b.PlotNumbers, ", ")
	subject := fmt.Sprintf("New Booking — %s (Docs Pending)", b.BuyerName)
	notesLine := ""
	if b.Notes != "" {
		notesLine = fmt.Sprintf("Notes:        %s\r\n", b.Notes)
	}
	body := fmt.Sprintf(
		"A new plot booking has been recorded but documents have not yet been fully uploaded.\r\n\r\n"+
			"Estate:       %s\r\n"+
			"Plot(s):      %s\r\n"+
			"Buyer Name:   %s\r\n"+
			"Phone:        %s\r\n"+
			"Deposit:      KES %s\r\n"+
			"Payment Plan: %s\r\n"+
			"Agent:        %s\r\n"+
			"Date:         %s\r\n"+
			"%s\r\n"+
			"Please follow up with the agent to collect and upload the remaining documents.\r\n",
		b.EstateName, plots, b.BuyerName, b.BuyerPhone,
		b.Deposit, b.PaymentPlan, b.AgentName,
		time.Now().Format("02 Jan 2006 15:04"),
		notesLine,
	)

	var msg bytes.Buffer
	rootWriter := multipart.NewWriter(&msg)

	header := fmt.Sprintf(
		"From: Marketing Portal <%s>\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n",
		smtpFrom, strings.Join(bookingPendingRecipients, ", "), subject, rootWriter.Boundary(),
	)
	msg.WriteString(header)

	// Text part
	th := make(textproto.MIMEHeader)
	th.Set("Content-Type", "text/plain; charset=utf-8")
	tw, _ := rootWriter.CreatePart(th)
	tw.Write([]byte(body))

	// Attach payment reference if uploaded
	if b.DepositRef != "" {
		for _, fname := range strings.Split(b.DepositRef, ",") {
			fname = strings.TrimSpace(fname)
			if fname == "" {
				continue
			}
			fullPath := filepath.Join(uploadsDir, fname)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				log.Printf("[pendingDocs] skip attachment %s: %v", fname, err)
				continue
			}
			ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(fname)))
			if ct == "" {
				ct = "application/octet-stream"
			}
			ah := make(textproto.MIMEHeader)
			ah.Set("Content-Type", ct)
			ah.Set("Content-Transfer-Encoding", "base64")
			ah.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(fname)))
			aw, _ := rootWriter.CreatePart(ah)
			enc := base64.StdEncoding.EncodeToString(data)
			for len(enc) > 76 {
				aw.Write([]byte(enc[:76] + "\r\n"))
				enc = enc[76:]
			}
			aw.Write([]byte(enc + "\r\n"))
		}
	}
	rootWriter.Close()

	tlsCfg := &tls.Config{ServerName: smtpHost}
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", smtpHost, smtpPort), tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(smtpFrom); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, r := range bookingPendingRecipients {
		if err := client.Rcpt(r); err != nil {
			log.Printf("[pendingDocs] rcpt %s: %v", r, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	w.Write(msg.Bytes())
	w.Close()
	return client.Quit()
}

// processSignedIntegrations fires when a plot is switched to SA Signed.
// It fetches the booking details + zoho_books_id from the DB, creates the CRM deal
// referencing the Books estimate, and sends an SA Signed notification email.
func processSignedIntegrations(plotID int, plotNumber, estateName string) {
	go func() {
		var b bookingInfo
		b.PlotNumbers = []string{plotNumber}
		b.EstateName = estateName
		var zohoBookID, existingCRMID, installmentPage string
		err := db.QueryRow(`
			SELECT COALESCE(buyer_name,''), COALESCE(buyer_phone,''), COALESCE(buyer_email,''),
			       COALESCE(agent_name,''), COALESCE(CAST(deposit AS CHAR),'0'),
			       COALESCE(payment_plan,''),
			       COALESCE(deposit_ref,''), COALESCE(id_photo,''),
			       COALESCE(kra,''), COALESCE(passport_photo,''),
			       COALESCE(sale_agreement,''), COALESCE(notes,''), COALESCE(care_of,''),
			       COALESCE(zoho_books_id,''), COALESCE(zoho_crm_id,''),
			       COALESCE(installment_page,'')
			FROM prop_bookings
			WHERE plot_id = ? ORDER BY id DESC LIMIT 1`, plotID).
			Scan(&b.BuyerName, &b.BuyerPhone, &b.BuyerEmail,
				&b.AgentName, &b.Deposit, &b.PaymentPlan,
				&b.DepositRef, &b.IDPhoto, &b.KRA, &b.PassportPhoto,
				&b.SaleAgreement, &b.Notes, &b.CareOf,
				&zohoBookID, &existingCRMID, &installmentPage)
		if err != nil {
			log.Printf("[signed-integrations] fetch booking for plot %d: %v", plotID, err)
			return
		}
		if existingCRMID != "" {
			log.Printf("[zoho-crm] plot %s already has CRM deal %s — skipping duplicate creation (likely a repeat form submission)", plotNumber, existingCRMID)
			return
		}

		// Zoho CRM deal — always runs, devMode only blocks email below.
		dealID, err := createCRMDeal(b, zohoBookID)
		if err != nil {
			log.Printf("[zoho-crm] deal error (plot %s): %v", plotNumber, err)
		} else {
			log.Printf("[zoho-crm] deal created for plot %s: %s", plotNumber, dealID)
			// Persist the deal ID so sold-stage uploads can target the same deal.
			db.Exec(`UPDATE prop_bookings SET zoho_crm_id=? WHERE plot_id=? AND status='sa_signed' ORDER BY id DESC LIMIT 1`, dealID, plotID)
			files := splitFiles(b.DepositRef, b.IDPhoto, b.KRA, b.PassportPhoto, b.SaleAgreement)
			log.Printf("[zoho-crm] uploading %d attachment(s) for plot %s: %v", len(files), plotNumber, files)
			for _, f := range files {
				fullPath := filepath.Join(uploadsDir, f)
				log.Printf("[zoho-crm] checking file: %s", fullPath)
				if err := uploadCRMAttachment(dealID, f); err != nil {
					log.Printf("[zoho-crm] attachment %s error: %v", f, err)
				} else {
					log.Printf("[zoho-crm] attachment %s uploaded OK", f)
				}
			}

			// Best-effort: extract the payment schedule from the sale agreement
			// PDF and push it into the deal's Installment related list. A parse
			// failure or zero-row result just means manual entry is needed — it
			// never blocks deal creation or attachment upload above.
			if b.SaleAgreement != "" {
				pushInstallmentsFromSaleAgreement(dealID, plotNumber, b.SaleAgreement, installmentPage)
			}
		}

		// SA Signed email — skipped in devMode.
		if devMode {
			log.Printf("[devMode] SA signed email skipped for plot %s", plotNumber)
			return
		}
		subject := fmt.Sprintf("SA Signed — %s — Plot %s", estateName, plotNumber)
		booksRef := zohoBookID
		if booksRef == "" {
			booksRef = "N/A (no Books estimate — use Retry Zoho on the admin panel)"
		}
		notesLine := ""
		if b.Notes != "" {
			notesLine = fmt.Sprintf("Notes:          %s\r\n", b.Notes)
		}
		body := fmt.Sprintf(
			"A plot has been switched to SA Signed.\r\n\r\n"+
				"Estate:         %s\r\n"+
				"Plot:           %s\r\n"+
				"Buyer Name:     %s\r\n"+
				"Phone:          %s\r\n"+
				"Email:          %s\r\n"+
				"Deposit:        KES %s\r\n"+
				"Payment Plan:   %s\r\n"+
				"Agent:          %s\r\n"+
				"Zoho Books Ref: %s\r\n"+
				"Date:           %s\r\n"+
				"%s",
			estateName, plotNumber,
			b.BuyerName, b.BuyerPhone, b.BuyerEmail,
			b.Deposit, b.PaymentPlan, b.AgentName, booksRef,
			time.Now().Format("02 Jan 2006 15:04"),
			notesLine,
		)
		saSignedRecipients := []string{"systemadmin@proproperty.co.ke", "accounts@proproperty.co.ke"}
		if err := sendPlainEmail(saSignedRecipients, subject, body); err != nil {
			log.Printf("[sa-signed-email] send error (plot %s): %v", plotNumber, err)
		} else {
			log.Printf("[sa-signed-email] sent for plot %s", plotNumber)
		}
	}()
}

// ─── Zoho CRM ─────────────────────────────────────────────────────────────────

func createCRMDeal(b bookingInfo, zohoBookID string) (string, error) {
	token, err := getCRMToken()
	if err != nil {
		return "", err
	}

	plotStr := strings.Join(b.PlotNumbers, ", ")
	payload := map[string]any{
		"data": []map[string]any{{
			"Deal_Name":        b.BuyerName,
			"Phone_Number":     b.BuyerPhone,
			"Buyer_Email":      b.BuyerEmail,
			"Deposit":          b.Deposit,
			"Payment_Plan":     b.PaymentPlan,
			"Agent_Name":       b.AgentName,
			"Estates":          b.EstateName,
			"Plot":             plotStr,
			"Stage":            "Sale Agreement Signed",
			"Closing_Date":     time.Now().Format("2006-01-02"),
			"Description":      fmt.Sprintf("Plot(s): %s | Estate: %s | Agent: %s", plotStr, b.EstateName, b.AgentName),
			"Books_Ref_Number": zohoBookID,
			"Care_Of":          b.CareOf,
		}},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", zohoCRMBase+"Deals", bytes.NewReader(body))
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Details struct {
				ID string `json:"id"`
			} `json:"details"`
			Status string `json:"status"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Data) > 0 && result.Data[0].Details.ID != "" {
		return result.Data[0].Details.ID, nil
	}
	return "", fmt.Errorf("CRM deal creation failed (status %d)", resp.StatusCode)
}

func uploadCRMAttachment(dealID, filename string) error {
	fullPath := filepath.Join(uploadsDir, filename)
	info, err := os.Stat(fullPath)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("file not found or empty: %s", filename)
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Retry up to 3 times
	for attempt := 0; attempt < 3; attempt++ {
		f.Seek(0, io.SeekStart)
		token, err := getCRMToken()
		if err != nil {
			return err
		}

		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		h := make(textproto.MIMEHeader)
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
		h.Set("Content-Type", ct)
		part, _ := w.CreatePart(h)
		io.Copy(part, f)
		w.Close()

		req, _ := http.NewRequest("POST", zohoCRMBase+"Deals/"+dealID+"/Attachments", &buf)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
		req.Header.Set("Content-Type", w.FormDataContentType())

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		rb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 401 {
			// Force token refresh on next attempt
			crmMu.Lock()
			crmExpires = time.Time{}
			crmMu.Unlock()
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return fmt.Errorf("attachment upload HTTP %d: %s", resp.StatusCode, string(rb))
	}
	return fmt.Errorf("attachment upload failed after retries: %s", filename)
}

// ─── Zoho Books ───────────────────────────────────────────────────────────────

// syncBooksCustomer creates a new customer contact in Zoho Books for this booking.
// Each booking gets its own entry (name includes estate + plot) so repeat buyers
// are always distinguishable and never overwrite each other.
func syncBooksCustomer(b bookingInfo) (string, error) {
	token, err := getBooksToken()
	if err != nil {
		return "", err
	}

	plotStr := strings.Join(b.PlotNumbers, ", ")
	// Unique customer name per booking: "John Doe — Golden Hills (Plot 5)"
	contactName := fmt.Sprintf("%s — %s (Plot %s)", b.BuyerName, b.EstateName, plotStr)
	notes := fmt.Sprintf("Estate: %s | Plot(s): %s | Agent: %s | Date: %s",
		b.EstateName, plotStr, b.AgentName, time.Now().Format("02 Jan 2006"))

	firstName, lastName := splitName(b.BuyerName)
	agentFirst, agentLast := splitName(b.AgentName)

	payload := map[string]any{
		"contact_name": contactName,
		"contact_type": "customer",
		"notes":        notes,
		"contact_persons": []map[string]any{
			{
				"first_name":         firstName,
				"last_name":          lastName,
				"email":              b.BuyerEmail,
				"phone":              b.BuyerPhone,
				"is_primary_contact": true,
			},
			{
				"first_name": agentFirst,
				"last_name":  agentLast,
				"salutation": "Agent",
			},
		},
	}
	body, _ := json.Marshal(payload)

	apiURL := fmt.Sprintf("%scontacts?organization_id=%s", zohoBooksBase, zohoBooksOrgID)
	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var cr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Contact struct {
			ContactID string `json:"contact_id"`
		} `json:"contact"`
	}
	json.Unmarshal(respBody, &cr)

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("zoho books POST %d: %s", resp.StatusCode, cr.Message)
	}
	if cr.Contact.ContactID == "" {
		return "", fmt.Errorf("zoho books: no contact_id returned")
	}
	return cr.Contact.ContactID, nil
}

// splitName splits a full name into first and last name.
func splitName(full string) (first, last string) {
	parts := strings.SplitN(strings.TrimSpace(full), " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return full, ""
}

// createBooksEstimate creates an estimate in Zoho Books for a plot booking.
// Returns the estimate_id (internal ID used as the shared reference with CRM).
func createBooksEstimate(b bookingInfo, customerID string) (string, error) {
	token, err := getBooksToken()
	if err != nil {
		return "", err
	}

	plotStr := strings.Join(b.PlotNumbers, ", ")
	var depositAmt float64
	fmt.Sscanf(b.Deposit, "%f", &depositAmt)

	payload := map[string]any{
		"customer_id": customerID,
		"date":        time.Now().Format("2006-01-02"),
		"line_items": []map[string]any{{
			"name":        fmt.Sprintf("Plot Booking — %s", plotStr),
			"description": fmt.Sprintf("Estate: %s | Payment Plan: %s", b.EstateName, b.PaymentPlan),
			"quantity":    1,
			"rate":        depositAmt,
		}},
		"notes": fmt.Sprintf("Agent: %s | Deposit: KES %s | Plot(s): %s | Estate: %s",
			b.AgentName, b.Deposit, plotStr, b.EstateName),
	}
	body, _ := json.Marshal(payload)

	apiURL := fmt.Sprintf("%sestimates?organization_id=%s", zohoBooksBase, zohoBooksOrgID)
	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code     int    `json:"code"`
		Message  string `json:"message"`
		Estimate struct {
			EstimateID     string `json:"estimate_id"`
			EstimateNumber string `json:"estimate_number"`
		} `json:"estimate"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Estimate.EstimateID == "" {
		return "", fmt.Errorf("books estimate creation failed (HTTP %d): %s", resp.StatusCode, result.Message)
	}
	estimateID := result.Estimate.EstimateID
	log.Printf("[zoho-books] estimate %s (id=%s) created", result.Estimate.EstimateNumber, estimateID)

	// Update the estimate to set reference_number (visible on quote header).
	// Zoho Books PUT requires the full estimate payload (customer_id + line_items are mandatory).
	token, _ = getBooksToken()
	updatePayload := map[string]any{
		"customer_id":      customerID,
		"reference_number": estimateID,
		"date":             time.Now().Format("2006-01-02"),
		"line_items": []map[string]any{{
			"name":        fmt.Sprintf("Plot Booking — %s", plotStr),
			"description": fmt.Sprintf("Estate: %s | Payment Plan: %s", b.EstateName, b.PaymentPlan),
			"quantity":    1,
			"rate":        depositAmt,
		}},
		"notes": fmt.Sprintf("Agent: %s | Deposit: KES %s | Plot(s): %s | Estate: %s",
			b.AgentName, b.Deposit, plotStr, b.EstateName),
	}
	updateBody, _ := json.Marshal(updatePayload)
	updateURL := fmt.Sprintf("%sestimates/%s?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
	ureq, _ := http.NewRequest("PUT", updateURL, bytes.NewReader(updateBody))
	ureq.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	ureq.Header.Set("Content-Type", "application/json")
	if uresp, uerr := http.DefaultClient.Do(ureq); uerr != nil {
		log.Printf("[zoho-books] reference update failed for %s: %v", estimateID, uerr)
	} else {
		rb, _ := io.ReadAll(uresp.Body)
		uresp.Body.Close()
		if uresp.StatusCode != 200 {
			log.Printf("[zoho-books] reference update HTTP %d for %s: %s", uresp.StatusCode, estimateID, string(rb))
		} else {
			log.Printf("[zoho-books] reference number set on estimate %s", estimateID)
		}
	}

	// Set cf_books_ref_number on the customer record so Zoho Flow can match it with the CRM deal.
	token, _ = getBooksToken()
	custPayload := map[string]any{
		"custom_fields": []map[string]any{{
			"api_name": "cf_books_ref_number",
			"value":    estimateID,
		}},
	}
	custBody, _ := json.Marshal(custPayload)
	custURL := fmt.Sprintf("%scontacts/%s?organization_id=%s", zohoBooksBase, customerID, zohoBooksOrgID)
	creq, _ := http.NewRequest("PUT", custURL, bytes.NewReader(custBody))
	creq.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	creq.Header.Set("Content-Type", "application/json")
	if cresp, cerr := http.DefaultClient.Do(creq); cerr != nil {
		log.Printf("[zoho-books] customer ref update failed for %s: %v", customerID, cerr)
	} else {
		cb, _ := io.ReadAll(cresp.Body)
		cresp.Body.Close()
		if cresp.StatusCode != 200 {
			log.Printf("[zoho-books] customer ref update HTTP %d for %s: %s", cresp.StatusCode, customerID, string(cb))
		} else {
			log.Printf("[zoho-books] cf_books_ref_number set on customer %s = %s", customerID, estimateID)
		}
	}

	return estimateID, nil
}

// createBooksRecord syncs the customer then creates the estimate, returning the estimate_id.
func createBooksRecord(b bookingInfo) (string, error) {
	customerID, err := syncBooksCustomer(b)
	if err != nil {
		return "", fmt.Errorf("customer sync: %w", err)
	}
	return createBooksEstimate(b, customerID)
}

// priorBooking is a past (cancelled/expired) booking on a plot that still
// has a Zoho Books estimate on record — a candidate for reuse if the same
// buyer rebooks, or for an accounts@ cleanup notice if a different buyer does.
type priorBooking struct {
	BookingID   int
	BuyerName   string
	BuyerPhone  string
	ZohoBooksID string
}

// normalizePhoneDigits strips everything but digits and keeps the last 9 —
// Kenyan mobile numbers vary wildly in the prefix we've seen this project
// (+254722..., 254722..., 0722..., 722..., with spaces/dashes), but the
// trailing 9 digits are stable regardless of formatting.
func normalizePhoneDigits(s string) string {
	var digits []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits = append(digits, s[i])
		}
	}
	if len(digits) > 9 {
		digits = digits[len(digits)-9:]
	}
	return string(digits)
}

// findPriorBookingHistory returns the most recent cancelled/expired booking
// for a plot that actually has a Zoho Books estimate on record — no
// zoho_books_id means there's nothing to reuse or clean up, so it doesn't
// count as "history" for this purpose.
func findPriorBookingHistory(plotID int) (*priorBooking, error) {
	var p priorBooking
	err := db.QueryRow(`
		SELECT id, COALESCE(buyer_name,''), COALESCE(buyer_phone,''), zoho_books_id
		FROM prop_bookings
		WHERE plot_id = ? AND status IN ('cancelled','expired')
		  AND zoho_books_id IS NOT NULL AND zoho_books_id <> ''
		ORDER BY id DESC LIMIT 1`, plotID).
		Scan(&p.BookingID, &p.BuyerName, &p.BuyerPhone, &p.ZohoBooksID)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// buyerMatchesPrior reports whether a new booking's buyer looks like the
// same person as a prior booking on the same plot — name or phone match,
// per the confirmed matching rule (names get retyped/reformatted often, so
// either signal alone is treated as enough to prompt the agent).
func buyerMatchesPrior(prior *priorBooking, buyerName, buyerPhone string) bool {
	if prior == nil {
		return false
	}
	if strings.TrimSpace(prior.BuyerName) != "" && strings.EqualFold(strings.TrimSpace(prior.BuyerName), strings.TrimSpace(buyerName)) {
		return true
	}
	pd, bd := normalizePhoneDigits(prior.BuyerPhone), normalizePhoneDigits(buyerPhone)
	return pd != "" && pd == bd
}

// createBooksRecordForBooking is the goroutine entry point.
// Each plot gets its own customer entry and estimate in Zoho Books.
// Idempotent per plot — skips any plot that already has a zoho_books_id.
func createBooksRecordForBooking(b bookingInfo) {
	if len(b.PlotIDs) == 0 {
		return
	}
	for i, pid := range b.PlotIDs {
		var existing string
		// Matches whichever booking row is currently "live" for this plot —
		// not just 'active': this now also runs at Accounts-approval and
		// system_admin skip-to-legal time, by which point the row's status
		// is already 'pending_wakili_review'. A plot can only have one
		// non-cancelled/expired row at a time (rebooking requires the prior
		// one to reach a terminal status first), so this still identifies
		// exactly the row this call is for.
		db.QueryRow(
			`SELECT COALESCE(zoho_books_id,'') FROM prop_bookings WHERE plot_id=? AND status NOT IN ('cancelled','expired') ORDER BY id DESC LIMIT 1`,
			pid,
		).Scan(&existing)
		if existing != "" {
			log.Printf("[zoho-books] estimate already exists for plot %d (%s) — skipping", pid, existing)
			continue
		}

		// Build a single-plot bookingInfo so each plot gets its own customer + estimate
		plotNum := ""
		if i < len(b.PlotNumbers) {
			plotNum = b.PlotNumbers[i]
		}
		single := b
		single.PlotIDs = []int{pid}
		single.PlotNumbers = []string{plotNum}

		// Check for prior booking history on this plot — if the same buyer is
		// rebooking (name or phone matches a previous cancelled/expired
		// booking there) and the agent confirmed it, reuse and refresh that
		// estimate instead of creating a new one. Otherwise, if any prior
		// history exists, notify accounts that a fresh estimate is being
		// created for a plot that's had one before, so the old one can be
		// manually deactivated in Zoho.
		prior, priorErr := findPriorBookingHistory(pid)
		if priorErr != nil {
			log.Printf("[zoho-books] prior-history lookup error for plot %d: %v", pid, priorErr)
		}
		if prior != nil && buyerMatchesPrior(prior, single.BuyerName, single.BuyerPhone) && b.RebookConfirmed {
			if err := refreshBooksEstimate(prior.ZohoBooksID, single); err != nil {
				log.Printf("[zoho-books] refresh estimate %s for plot %d failed: %v", prior.ZohoBooksID, pid, err)
			}
			db.Exec(
				`UPDATE prop_bookings SET zoho_books_id=? WHERE plot_id=? AND status NOT IN ('cancelled','expired') AND zoho_books_id IS NULL`,
				prior.ZohoBooksID, pid,
			)
			log.Printf("[zoho-books] reused estimate %s for plot %d (%s) — confirmed rebook", prior.ZohoBooksID, pid, plotNum)
			continue
		}

		booksID, err := createBooksRecord(single)
		if err != nil {
			log.Printf("[zoho-books] estimate error for %s plot %s: %v", b.BuyerName, plotNum, err)
			continue
		}
		db.Exec(
			`UPDATE prop_bookings SET zoho_books_id=? WHERE plot_id=? AND status NOT IN ('cancelled','expired') AND zoho_books_id IS NULL`,
			booksID, pid,
		)
		log.Printf("[zoho-books] stored estimate %s for plot %d (%s)", booksID, pid, plotNum)
		if prior != nil {
			go sendRebookAccountsEmail(plotNum, single.EstateName, single.BuyerName, prior.ZohoBooksID)
		}
	}
}

// createBooksRecordForBookingID fetches one booking's data and creates its
// Zoho Books estimate — the actual trigger point for Books creation now
// that it's deferred past initial booking time (see processBookingIntegrations).
// Called from accounts.approveHandler (via the createBooksFn injected into
// accounts.Init) once Accounts approves, and from adminSkipAccountsHandler
// when a system_admin bypasses Accounts entirely. Safe to call even if a
// record already exists — createBooksRecordForBooking is idempotent per plot.
func createBooksRecordForBookingID(bookingID string) {
	var b bookingInfo
	var plotID int
	var deposit float64
	var plotNumber string
	err := db.QueryRow(`
		SELECT p.id, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       COALESCE(b.agent_name,''), COALESCE(b.deposit,0), COALESCE(b.payment_plan,''),
		       p.plot_number, e.name
		FROM prop_bookings b
		JOIN prop_plots p ON b.plot_id = p.id
		JOIN prop_estates e ON b.estate_id = e.id
		WHERE b.id = ?`, bookingID).Scan(
		&plotID, &b.BuyerName, &b.BuyerPhone, &b.BuyerEmail,
		&b.AgentName, &deposit, &b.PaymentPlan,
		&plotNumber, &b.EstateName)
	if err != nil {
		log.Printf("[zoho-books] createBooksRecordForBookingID fetch error booking=%s: %v", bookingID, err)
		return
	}
	b.PlotIDs = []int{plotID}
	b.PlotNumbers = []string{plotNumber}
	b.Deposit = fmt.Sprintf("%.2f", deposit)
	createBooksRecordForBooking(b)
}

// updateBooksContact fetches the customer_id from the stored estimate, then
// updates the customer's name, phone and email in Zoho Books. contact_name is
// rebuilt in the same "Name — Estate (Plot N)" format syncBooksCustomer uses,
// so a correction never strips the estate/plot suffix from the display name.
func updateBooksContact(estimateID, buyerName, phone, email, estateName, plotStr string) error {
	token, err := getBooksToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	// Step 1: GET the estimate to retrieve customer_id
	getURL := fmt.Sprintf("%sestimates/%s?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
	req, _ := http.NewRequest("GET", getURL, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("get estimate: %w", err)
	}
	defer resp.Body.Close()
	var estResp struct {
		Estimate struct {
			CustomerID string `json:"customer_id"`
		} `json:"estimate"`
	}
	if b, _ := io.ReadAll(resp.Body); resp.StatusCode == 200 {
		json.Unmarshal(b, &estResp)
	}
	customerID := estResp.Estimate.CustomerID
	if customerID == "" {
		return fmt.Errorf("no customer_id found on estimate %s", estimateID)
	}

	// Step 2: PUT updated contact info
	token, err = getBooksToken()
	if err != nil {
		return fmt.Errorf("token refresh: %w", err)
	}
	firstName, lastName := splitName(buyerName)
	contactName := buyerName
	if estateName != "" {
		contactName = fmt.Sprintf("%s — %s (Plot %s)", buyerName, estateName, plotStr)
	}
	payload := map[string]any{
		"contact_name": contactName,
		"contact_persons": []map[string]any{{
			"first_name":         firstName,
			"last_name":          lastName,
			"email":              email,
			"phone":              phone,
			"is_primary_contact": true,
		}},
	}
	body, _ := json.Marshal(payload)
	putURL := fmt.Sprintf("%scontacts/%s?organization_id=%s", zohoBooksBase, customerID, zohoBooksOrgID)
	putReq, _ := http.NewRequest("PUT", putURL, bytes.NewReader(body))
	putReq.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("put contact: %w", err)
	}
	defer putResp.Body.Close()
	rb, _ := io.ReadAll(putResp.Body)
	if putResp.StatusCode != 200 {
		return fmt.Errorf("put contact HTTP %d: %s", putResp.StatusCode, string(rb))
	}
	log.Printf("[zoho-books] contact %s updated — name=%s phone=%s email=%s", customerID, contactName, phone, email)
	return nil
}

// refreshBooksEstimate updates an existing estimate's date/deposit/notes to
// reflect a new booking cycle (used when a confirmed rebook reuses a prior
// estimate instead of creating a new one), and refreshes the linked
// contact's details via updateBooksContact.
func refreshBooksEstimate(estimateID string, b bookingInfo) error {
	token, err := getBooksToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	getURL := fmt.Sprintf("%sestimates/%s?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
	req, _ := http.NewRequest("GET", getURL, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("get estimate: %w", err)
	}
	defer resp.Body.Close()
	var estResp struct {
		Estimate struct {
			CustomerID string `json:"customer_id"`
		} `json:"estimate"`
	}
	if rb, _ := io.ReadAll(resp.Body); resp.StatusCode == 200 {
		json.Unmarshal(rb, &estResp)
	}
	customerID := estResp.Estimate.CustomerID
	if customerID == "" {
		return fmt.Errorf("no customer_id found on estimate %s", estimateID)
	}

	plotStr := strings.Join(b.PlotNumbers, ", ")
	var depositAmt float64
	fmt.Sscanf(b.Deposit, "%f", &depositAmt)

	token, _ = getBooksToken()
	updatePayload := map[string]any{
		"customer_id":      customerID,
		"reference_number": estimateID,
		"date":             time.Now().Format("2006-01-02"),
		"line_items": []map[string]any{{
			"name":        fmt.Sprintf("Plot Booking — %s", plotStr),
			"description": fmt.Sprintf("Estate: %s | Payment Plan: %s", b.EstateName, b.PaymentPlan),
			"quantity":    1,
			"rate":        depositAmt,
		}},
		"notes": fmt.Sprintf("Rebooking — Agent: %s | Deposit: KES %s | Plot(s): %s | Estate: %s",
			b.AgentName, b.Deposit, plotStr, b.EstateName),
	}
	body, _ := json.Marshal(updatePayload)
	putURL := fmt.Sprintf("%sestimates/%s?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
	putReq, _ := http.NewRequest("PUT", putURL, bytes.NewReader(body))
	putReq.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("put estimate: %w", err)
	}
	defer putResp.Body.Close()
	rb, _ := io.ReadAll(putResp.Body)
	if putResp.StatusCode != 200 {
		return fmt.Errorf("put estimate HTTP %d: %s", putResp.StatusCode, string(rb))
	}
	log.Printf("[zoho-books] estimate %s refreshed for rebooking — deposit=%s plot(s)=%s", estimateID, b.Deposit, plotStr)

	if err := updateBooksContact(estimateID, b.BuyerName, b.BuyerPhone, b.BuyerEmail, b.EstateName, plotStr); err != nil {
		log.Printf("[zoho-books] contact refresh during rebook failed for %s: %v", estimateID, err)
	}
	return nil
}

// cancelBooksEstimate marks a Zoho Books estimate as cancelled so it doesn't
// appear as an active quote when a plot is re-booked after expiry.
func cancelBooksEstimate(estimateID string) {
	token, err := getBooksToken()
	if err != nil {
		log.Printf("[zoho-books] cancel estimate %s: token error: %v", estimateID, err)
		return
	}
	url := fmt.Sprintf("%sestimates/%s/status/cancelled?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[zoho-books] cancel estimate %s: %v", estimateID, err)
		return
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[zoho-books] cancel estimate %s HTTP %d: %s", estimateID, resp.StatusCode, string(rb))
	} else {
		log.Printf("[zoho-books] estimate %s cancelled", estimateID)
	}
}

// sendRebookAccountsEmail notifies accounts that a fresh Zoho Books estimate
// was created for a plot that already had one from a prior booking cycle —
// the prior estimate is left active in Zoho (not auto-voided, see
// checkOverdueBookings) so accounts needs to manually deactivate it.
func sendRebookAccountsEmail(plotNumber, estateName, buyerName, priorEstimateID string) {
	if devMode {
		log.Printf("[devMode] rebook accounts email skipped for plot %s", plotNumber)
		return
	}
	subject := fmt.Sprintf("Plot %s (%s) rebooked — please deactivate previous Zoho Books estimate", plotNumber, estateName)
	body := fmt.Sprintf(
		"Plot %s at %s has just been booked by %s, and a new Zoho Books estimate has been created for it.\r\n\r\n"+
			"This plot has a previous booking on record with its own estimate (%s), which is still active in Zoho Books.\r\n"+
			"Please review and deactivate/void that previous estimate if it's no longer needed.\r\n\r\n"+
			"Regards,\r\nPro-Property System",
		plotNumber, estateName, buyerName, priorEstimateID,
	)
	if err := sendPlainEmail([]string{"accounts@proproperty.co.ke"}, subject, body); err != nil {
		log.Printf("[zoho-books] rebook accounts email error (plot %s): %v", plotNumber, err)
	} else {
		log.Printf("[zoho-books] rebook accounts email sent for plot %s (prior estimate %s)", plotNumber, priorEstimateID)
	}
}

// ─── Email ────────────────────────────────────────────────────────────────────

func sendBookingEmail(b bookingInfo, recipients []string, withAttachments bool) error {
	if devMode {
		log.Printf("[devMode] skipping booking email for %s", b.EstateName)
		return nil
	}
	plotStr := strings.Join(b.PlotNumbers, ", ")
	subject := fmt.Sprintf("New Booking – %s – Plot(s) %s", b.EstateName, plotStr)

	textBody := fmt.Sprintf(
		"A new booking has been confirmed.\r\n\r\n"+
			"Estate:       %s\r\n"+
			"Plot(s):      %s\r\n"+
			"Buyer Name:   %s\r\n"+
			"Phone:        %s\r\n"+
			"Email:        %s\r\n"+
			"Deposit:      Ksh %s\r\n"+
			"Payment Plan: %s\r\n"+
			"Agent:        %s\r\n"+
			"Date:         %s\r\n",
		b.EstateName, plotStr,
		b.BuyerName, b.BuyerPhone, b.BuyerEmail,
		b.Deposit, b.PaymentPlan, b.AgentName,
		time.Now().Format("02 Jan 2006 15:04"),
	)
	if b.Notes != "" {
		textBody += fmt.Sprintf("Notes:        %s\r\n", b.Notes)
	}

	var attachFiles []string
	if withAttachments {
		attachFiles = splitFiles(b.DepositRef, b.IDPhoto, b.KRA, b.PassportPhoto)
	}

	// Build MIME message
	var msg bytes.Buffer
	rootWriter := multipart.NewWriter(&msg)

	header := fmt.Sprintf(
		"From: Marketing Portal <%s>\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n",
		smtpFrom, strings.Join(recipients, ", "), subject, rootWriter.Boundary(),
	)
	msg.WriteString(header)

	// Text part
	th := make(textproto.MIMEHeader)
	th.Set("Content-Type", "text/plain; charset=utf-8")
	tw, _ := rootWriter.CreatePart(th)
	tw.Write([]byte(textBody))

	// Attachment parts
	for _, fname := range attachFiles {
		fullPath := filepath.Join(uploadsDir, fname)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("[email] skip attachment %s: %v", fname, err)
			continue
		}
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(fname)))
		if ct == "" {
			ct = "application/octet-stream"
		}
		ah := make(textproto.MIMEHeader)
		ah.Set("Content-Type", ct)
		ah.Set("Content-Transfer-Encoding", "base64")
		ah.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(fname)))
		aw, _ := rootWriter.CreatePart(ah)
		enc := base64.StdEncoding.EncodeToString(data)
		// Wrap at 76 chars per RFC 2045
		for len(enc) > 76 {
			aw.Write([]byte(enc[:76] + "\r\n"))
			enc = enc[76:]
		}
		aw.Write([]byte(enc + "\r\n"))
	}
	rootWriter.Close()

	// Connect via TLS on port 465 (SMTPS)
	tlsCfg := &tls.Config{ServerName: smtpHost}
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", smtpHost, smtpPort), tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(smtpFrom); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, r := range recipients {
		if err := client.Rcpt(r); err != nil {
			log.Printf("[email] rcpt %s: %v", r, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	w.Write(msg.Bytes())
	w.Close()
	return client.Quit()
}

// sendPlainEmail sends a plain-text email to the given recipients using the
// same SMTP credentials as the booking notification emails.
func sendPlainEmail(to []string, subject, body string) error {
	if devMode {
		log.Printf("[devMode] skipping email to %v", to)
		return nil
	}
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf(
		"From: Marketing Portal <%s>\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n",
		smtpFrom, strings.Join(to, ", "), subject,
	))
	msg.WriteString(body)

	tlsCfg := &tls.Config{ServerName: smtpHost}
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", smtpHost, smtpPort), tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(smtpFrom); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, r := range to {
		client.Rcpt(r)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	w.Write(msg.Bytes())
	w.Close()
	return client.Quit()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func splitFiles(fields ...string) []string {
	var out []string
	for _, f := range fields {
		for _, name := range strings.Split(f, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// processSoldAttachments uploads the three sold-stage documents (letter of consent,
// transfer forms, title deed) to the Zoho CRM deal that was created at SA signed time.
func processSoldAttachments(plotID int, letterOfConsent, transferForms, titleDeed string) {
	go func() {
		var crmID string
		db.QueryRow(`SELECT COALESCE(zoho_crm_id,'') FROM prop_bookings WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&crmID)
		if crmID == "" {
			log.Printf("[sold-attachments] no zoho_crm_id for plot %d — skipping CRM upload", plotID)
			return
		}
		for _, f := range splitFiles(letterOfConsent, transferForms, titleDeed) {
			if err := uploadCRMAttachment(crmID, f); err != nil {
				log.Printf("[sold-attachments] attachment %s error: %v", f, err)
			} else {
				log.Printf("[sold-attachments] attachment %s uploaded to deal %s", f, crmID)
			}
		}
	}()
}

// sendAccountsApprovedEmail notifies accountsApprovedRecipients, without
// attachments, once a booking has cleared Accounts review and moved to
// Legal. Fetches the booking fresh from the DB since callers only have a
// booking ID at that point: accounts.approveHandler (via the createBooksFn-
// style function injected into accounts.Init) on normal approval, and
// adminSkipAccountsHandler on a system_admin's skip-Accounts override.
func sendAccountsApprovedEmail(bookingID string) error {
	if devMode {
		log.Printf("[devMode] skipping accounts-approved email for booking %s", bookingID)
		return nil
	}
	var b bookingInfo
	var plotNumber string
	err := db.QueryRow(`
		SELECT b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       COALESCE(b.agent_name,''), COALESCE(CAST(b.deposit AS CHAR),'0'),
		       COALESCE(b.payment_plan,''), COALESCE(b.notes,''),
		       e.name, p.plot_number
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.id = ?`, bookingID).
		Scan(&b.BuyerName, &b.BuyerPhone, &b.BuyerEmail,
			&b.AgentName, &b.Deposit, &b.PaymentPlan, &b.Notes,
			&b.EstateName, &plotNumber)
	if err != nil {
		return fmt.Errorf("fetch booking %s: %w", bookingID, err)
	}
	b.PlotNumbers = []string{plotNumber}
	return sendBookingEmail(b, accountsApprovedRecipients, false)
}

// fetchNotifySMSInfo loads the buyer/agent contact and plot/estate details
// needed for the Accounts/Legal progress SMS below. Shared by all three
// since each caller only has a booking ID at its trigger point.
func fetchNotifySMSInfo(bookingID string) (buyerName, buyerPhone, agentName, agentPhone, plotNumber, estateName string, err error) {
	err = db.QueryRow(`
		SELECT b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.agent_name,''),
		       COALESCE(a.phone,''), p.plot_number, e.name
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE b.id = ?`, bookingID).
		Scan(&buyerName, &buyerPhone, &agentName, &agentPhone, &plotNumber, &estateName)
	return
}

// sendAccountsApprovedSMS notifies the buyer and/or agent — per Settings ->
// Notifications (notifyBuyerEnabled/notifyAgentEnabled, the same toggle that
// already gates the booking-confirmation and 5-day reminder SMS) — that a
// booking has cleared Accounts and moved to Legal for sale agreement
// drafting. Fired from accounts.approveHandler (normal approval) and
// adminSkipAccountsHandler (system_admin's skip-Accounts override) — both
// land the booking at the same "now with Legal" point.
func sendAccountsApprovedSMS(bookingID string) {
	if !notifyBuyerEnabled() && !notifyAgentEnabled() {
		return
	}
	buyerName, buyerPhone, _, agentPhone, plotNumber, estateName, err := fetchNotifySMSInfo(bookingID)
	if err != nil {
		log.Printf("[sms] accounts-approved fetch error booking=%s: %v", bookingID, err)
		return
	}
	if notifyBuyerEnabled() && buyerPhone != "" {
		msg := fmt.Sprintf(
			"Dear %s, good news — your payment and documents for Plot %s at %s have been verified. Your booking now moves to our legal team for sale agreement preparation. - Pro-Property",
			buyerName, plotNumber, estateName,
		)
		go vanbooking.SendSMS(buyerPhone, msg)
	}
	if notifyAgentEnabled() && agentPhone != "" {
		msg := fmt.Sprintf(
			"Your client %s's booking for Plot %s at %s has passed Accounts verification and moved to Legal for sale agreement drafting. - Pro-Property",
			buyerName, plotNumber, estateName,
		)
		go vanbooking.SendSMS(agentPhone, msg)
	}
}

// sendSentForSignatureSMS notifies the buyer and/or agent that the sale
// agreement has been drafted and sent for the client's signature. Fired
// from legal.sendForSignatureHandler.
func sendSentForSignatureSMS(bookingID string) {
	if !notifyBuyerEnabled() && !notifyAgentEnabled() {
		return
	}
	buyerName, buyerPhone, agentName, agentPhone, plotNumber, estateName, err := fetchNotifySMSInfo(bookingID)
	if err != nil {
		log.Printf("[sms] sent-for-signature fetch error booking=%s: %v", bookingID, err)
		return
	}
	if notifyBuyerEnabled() && buyerPhone != "" {
		msg := fmt.Sprintf(
			"Dear %s, your sale agreement for Plot %s at %s has been prepared and sent for your signature. Please contact %s to sign and return it. - Pro-Property",
			buyerName, plotNumber, estateName, agentName,
		)
		go vanbooking.SendSMS(buyerPhone, msg)
	}
	if notifyAgentEnabled() && agentPhone != "" {
		msg := fmt.Sprintf(
			"The sale agreement for %s's Plot %s at %s is ready for signature — please coordinate with the client. - Pro-Property",
			buyerName, plotNumber, estateName,
		)
		go vanbooking.SendSMS(agentPhone, msg)
	}
}

// sendAgreementSignedSMS notifies the buyer and/or agent once the signed
// sale agreement has been uploaded — the final step of this pipeline
// (status sa_signed). Fired from legal.uploadAgreementHandler.
func sendAgreementSignedSMS(bookingID string) {
	if !notifyBuyerEnabled() && !notifyAgentEnabled() {
		return
	}
	buyerName, buyerPhone, _, agentPhone, plotNumber, estateName, err := fetchNotifySMSInfo(bookingID)
	if err != nil {
		log.Printf("[sms] agreement-signed fetch error booking=%s: %v", bookingID, err)
		return
	}
	if notifyBuyerEnabled() && buyerPhone != "" {
		msg := fmt.Sprintf(
			"Dear %s, congratulations! Your sale agreement for Plot %s at %s has been signed and finalized. - Pro-Property",
			buyerName, plotNumber, estateName,
		)
		go vanbooking.SendSMS(buyerPhone, msg)
	}
	if notifyAgentEnabled() && agentPhone != "" {
		msg := fmt.Sprintf(
			"The sale agreement for %s's Plot %s at %s has been signed — booking complete. - Pro-Property",
			buyerName, plotNumber, estateName,
		)
		go vanbooking.SendSMS(agentPhone, msg)
	}
}

// sendSoldEmail notifies soldRecipients when a plot is marked as sold.
func sendSoldEmail(b bookingInfo) error {
	plotStr := strings.Join(b.PlotNumbers, ", ")
	subject := fmt.Sprintf("Plot Sold — %s — Plot %s", b.EstateName, plotStr)
	body := fmt.Sprintf(
		"A plot has been marked as SOLD.\r\n\r\n"+
			"Estate:       %s\r\n"+
			"Plot(s):      %s\r\n"+
			"Buyer Name:   %s\r\n"+
			"Phone:        %s\r\n"+
			"Email:        %s\r\n"+
			"Deposit:      Ksh %s\r\n"+
			"Payment Plan: %s\r\n"+
			"Agent:        %s\r\n"+
			"Date:         %s\r\n",
		b.EstateName, plotStr,
		b.BuyerName, b.BuyerPhone, b.BuyerEmail,
		b.Deposit, b.PaymentPlan, b.AgentName,
		time.Now().Format("02 Jan 2006 15:04"),
	)
	return sendPlainEmail(soldRecipients, subject, body)
}
