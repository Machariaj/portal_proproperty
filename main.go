package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"marketers_portal/accounts"
	"marketers_portal/legal"
	"marketers_portal/vanbooking"
	"marketers_portal/welfare"
)

// uploadsDir is where uploaded images are stored (same as PHP portal).
// Overridden at startup by the UPLOADS_DIR environment variable.
var uploadsDir = `d:\propropertysolutions\uploads`

// pointXY is a 2D percentage-based coordinate for plot zone polygons
type pointXY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// zoneDisplay carries a plot zone with its current status for rendering
type zoneDisplay struct {
	PlotNumber string    `json:"plot_number"`
	Status     string    `json:"status"`
	Points     []pointXY `json:"points"`
}

const (
	sessionCookie = "pp_session"
	roleCookie    = "pp_role"
	nameCookie    = "pp_name"
	roleAdmin     = "admin"
	roleAgent     = "agent"
	roleAccounts  = "accounts"
	roleLegal     = "legal"
)

var pageTemplates map[string]*template.Template

var tmplFuncs = template.FuncMap{
	"add":   func(a, b int) int { return a + b },
	"inc":   func(i int) int { return i + 1 },
	"upper": strings.ToUpper,
	"lines": func(s string) []string { return strings.Split(s, "\n") },
	"shortRef": func(s string) string {
		if len(s) > 10 {
			return strings.ToUpper(s[:10])
		}
		return strings.ToUpper(s)
	},
	"fmtAmount": func(s string) string {
		var f float64
		fmt.Sscanf(s, "%f", &f)
		// Format with thousands separator
		intPart := int64(f)
		fracPart := int(f*100) % 100
		var result []byte
		str := fmt.Sprintf("%d", intPart)
		for i, c := range str {
			if i > 0 && (len(str)-i)%3 == 0 {
				result = append(result, ',')
			}
			result = append(result, byte(c))
		}
		return fmt.Sprintf("%s.%02d", string(result), fracPart)
	},
}

func mustParse(files ...string) *template.Template {
	t, err := template.New("").Funcs(tmplFuncs).ParseFiles(files...)
	if err != nil {
		log.Fatalf("parse templates %v: %v", files, err)
	}
	return t
}

func main() {
	if loc, err := time.LoadLocation("Africa/Nairobi"); err == nil {
		time.Local = loc
	}

	// Applied before subcommand dispatch below — every subcommand that reads
	// uploaded files (backfill-crm-attachments, etc.) needs the real uploads
	// path, not the fallback default, and each dispatch branch returns before
	// ever reaching the HTTP-server startup code further down.
	if v := os.Getenv("UPLOADS_DIR"); v != "" {
		uploadsDir = v
	}

	if len(os.Args) > 1 && os.Args[1] == "import-buyers" {
		initDB()
		runImportBuyers(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "audit-crm-duplicates" {
		runAuditCRM(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-crm-attachments" {
		runBackfillAttachments(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "audit-books-placeholder" {
		runAuditBooksPlaceholder(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "audit-crm-placeholder" {
		runAuditCRMPlaceholder(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "audit-books-displayname" {
		runAuditBooksDisplayName(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "parse-sale-agreement" {
		runParseSaleAgreement(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "inspect-crm-fields" {
		runInspectCRMFields(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-installments" {
		runBackfillInstallments(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fix-installment-linkage" {
		runFixInstallmentLinkage(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sync-payment-plans" {
		runSyncPaymentPlans(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "reconcile-payment-plan-sold" {
		runReconcilePaymentPlanSold(os.Args[2:])
		return
	}

	initLoggers()
	initDB()
	vanbooking.Init(db, render, getAgentName, pathSegment, RegisterFeature,
		func(r *http.Request) bool { return getRole(r) == roleSystemAdmin }, devMode)
	vanbooking.InitTables()
	welfare.Init(db, render, getAgentName, pathSegment,
		RegisterFeature,
		func(r *http.Request) bool { return getRole(r) == roleSystemAdmin },
		hasPermission,
		getUserID,
	)
	welfare.InitTables()
	accounts.Init(db, render, getAgentName, cancelBooksEstimate, sendReviewOutcomeEmail, createBooksRecordForBookingID, sendAccountsApprovedEmail, sendAccountsApprovedSMS, notifyLawyerOfNewCase)
	legal.Init(db, render, getAgentName, getUserID,
		func(r *http.Request) bool { return getRole(r) == roleSystemAdmin },
		getRole,
		func(r *http.Request) bool { return hasPermission(getUserID(r), "legal.access", "write") },
		saveUploadedFiles, processSignedIntegrations, cancelBooksEstimate, sendReviewOutcomeEmail,
		sendSentForSignatureSMS, sendAgreementSignedSMS)
	initPermissionTables()
	init2FATables()
	initSchedulerTables()
	initAppSettingsTables()
	initPaymentPlanTables()
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN booking_deadline DATE DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_estates ADD COLUMN plot_price DECIMAL(15,2) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN zoho_books_id VARCHAR(64) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN zoho_crm_id VARCHAR(64) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_sales ADD COLUMN zoho_books_id VARCHAR(64) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_sales ADD COLUMN zoho_crm_id VARCHAR(64) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_sales ADD COLUMN letter_of_consent VARCHAR(500) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_sales ADD COLUMN transfer_forms VARCHAR(500) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_sales ADD COLUMN title_deed VARCHAR(500) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN lead_source VARCHAR(50) DEFAULT NULL`)
	// Set by the agent/admin at booking time, shown only to them (never to
	// Accounts or Legal — deliberately excluded from both modules' review
	// queries/templates) and forwarded only as the "Care_Of" field on the
	// Zoho CRM deal, created later at SA Signed — see processSignedIntegrations.
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN care_of VARCHAR(255) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN sale_agreement VARCHAR(500) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN installment_page VARCHAR(20) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN batch_ref VARCHAR(64) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN receipt_number VARCHAR(20) DEFAULT NULL`)
	// Accounts + Legal review workflow: two new intermediate statuses between
	// 'active' and 'sa_signed'.
	db.Exec(`ALTER TABLE prop_bookings MODIFY COLUMN status
		ENUM('active','pending_accounts_review','pending_wakili_review','cancelled','expired','completed','sa_signed')
		DEFAULT 'active'`)
	db.Exec(`ALTER TABLE prop_estates ADD COLUMN deposit_threshold DECIMAL(15,2) DEFAULT NULL`)
	// Each estate's sale agreements are handled by exactly one lawyer — a
	// specific prop_agents row with role='legal'. Loosely referenced (no FK
	// constraint), matching the pattern used elsewhere in this schema.
	db.Exec(`ALTER TABLE prop_estates ADD COLUMN lawyer_id INT DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN accounts_notes TEXT DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN accounts_reviewed_by VARCHAR(255) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN accounts_reviewed_at TIMESTAMP NULL DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN wakili_notes TEXT DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN wakili_reviewed_by VARCHAR(255) DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN wakili_reviewed_at TIMESTAMP NULL DEFAULT NULL`)
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN deposit_topups TEXT DEFAULT NULL`)
	// Legal has three internal stages while status='pending_wakili_review':
	// drafting the sale agreement, then awaiting the client's signature, then
	// (on upload) status flips to 'sa_signed'. legal_stage is meaningless once
	// status has moved past 'pending_wakili_review'.
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN legal_stage ENUM('drafting','awaiting_signature') NOT NULL DEFAULT 'drafting'`)
	// Set when Legal moves a booking from drafting to awaiting_signature —
	// distinct from wakili_reviewed_at, which only fires at the terminal
	// sign/cancel action, not this intermediate stage change.
	db.Exec(`ALTER TABLE prop_bookings ADD COLUMN sent_for_signature_at TIMESTAMP NULL DEFAULT NULL`)
	db.Exec(`CREATE TABLE IF NOT EXISTS prop_booking_receipts (
		id INT AUTO_INCREMENT PRIMARY KEY,
		booking_id INT NOT NULL,
		amount DECIMAL(15,2) NOT NULL DEFAULT 0,
		receipt_number VARCHAR(20) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_br_booking (booking_id)
	)`)
	db.Exec(`ALTER TABLE prop_estates ADD COLUMN is_restricted TINYINT(1) DEFAULT 0`)
	db.Exec(`CREATE TABLE IF NOT EXISTS prop_restricted_access (
		id INT AUTO_INCREMENT PRIMARY KEY,
		estate_id INT NOT NULL,
		user_id VARCHAR(64) NOT NULL,
		granted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uq_ra (estate_id, user_id)
	)`)
	// Backfill: grant private-estate permission to any user already in prop_restricted_access
	db.Exec(`INSERT INTO prop_permissions (user_id, feature, can_read, can_write)
		SELECT CAST(a.id AS CHAR), IF(a.role='agent','agent.private_estates','admin.private_estates'), 1, 0
		FROM prop_agents a
		WHERE a.role != 'system_admin'
		AND EXISTS (SELECT 1 FROM prop_restricted_access ra WHERE ra.user_id = CAST(a.id AS CHAR))
		ON DUPLICATE KEY UPDATE can_read = 1`)
	// Backfill: sa_signed bookings just moved off "My Bookings" onto their
	// own new "My Signed Plots" page (see agentSignedPlotsHandler) — grant
	// every agent who could already see My Bookings the new page too, so
	// nobody loses visibility into their signed plots as a side effect.
	db.Exec(`INSERT INTO prop_permissions (user_id, feature, can_read, can_write)
		SELECT user_id, 'agent.signed_plots', 1, 0
		FROM prop_permissions
		WHERE feature = 'agent.bookings' AND can_read = 1
		ON DUPLICATE KEY UPDATE can_read = 1`)
	initWhatsAppLog()
	initPlotStatusLog()
	startOverdueBookingChecker()
	startPaymentPlanSync()

	pageTemplates = map[string]*template.Template{
		"login":         mustParse("templates/login.html"),
		"login_2fa":     mustParse("templates/login_2fa.html"),
		"hub":           mustParse("templates/hub.html"),
		"access_denied": mustParse("templates/access_denied.html"),
		// admin pages
		"admin_dashboard.html":             mustParse("templates/admin_base.html", "templates/admin_dashboard.html"),
		"admin_estates.html":               mustParse("templates/admin_base.html", "templates/admin_estates.html"),
		"admin_booked_plots.html":          mustParse("templates/admin_base.html", "templates/admin_booked_plots.html"),
		"admin_signed_plots.html":          mustParse("templates/admin_base.html", "templates/admin_signed_plots.html"),
		"admin_sold_plots.html":            mustParse("templates/admin_base.html", "templates/admin_sold_plots.html"),
		"admin_placeholder.html":           mustParse("templates/admin_base.html", "templates/admin_placeholder.html"),
		"admin_estate_detail.html":         mustParse("templates/admin_base.html", "templates/admin_estate_detail.html"),
		"admin_estate_plots.html":          mustParse("templates/admin_base.html", "templates/admin_estate_plots.html"),
		"admin_estate_book.html":           mustParse("templates/admin_base.html", "templates/admin_estate_book.html"),
		"admin_estate_edit.html":           mustParse("templates/admin_base.html", "templates/admin_estate_edit.html"),
		"admin_booking_attachments.html":   mustParse("templates/admin_base.html", "templates/admin_booking_attachments.html"),
		"admin_mark_sold.html":             mustParse("templates/admin_base.html", "templates/admin_mark_sold.html"),
		"admin_plots_overview.html":        mustParse("templates/admin_base.html", "templates/admin_plots_overview.html"),
		"agent_booking_attachments.html":   mustParse("templates/agent_base.html", "templates/agent_booking_attachments.html"),
		"admin_add_estate.html":            mustParse("templates/admin_base.html", "templates/admin_add_estate.html"),
		"admin_pending_projects.html":      mustParse("templates/admin_base.html", "templates/admin_pending_projects.html"),
		"admin_completed_projects.html":    mustParse("templates/admin_base.html", "templates/admin_completed_projects.html"),
		"admin_export_reports.html":        mustParse("templates/admin_base.html", "templates/admin_export_reports.html"),
		"admin_receipts_list.html":         mustParse("templates/admin_base.html", "templates/admin_receipts_list.html"),
		"admin_payment_plans.html":         mustParse("templates/admin_base.html", "templates/admin_payment_plans.html"),
		"admin_payment_plans_list.html":    mustParse("templates/admin_base.html", "templates/admin_payment_plans_list.html"),
		"admin_payment_plans_deal.html":    mustParse("templates/admin_base.html", "templates/admin_payment_plans_deal.html"),
		"admin_van_bookings.html":          mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_bookings.html"),
		"admin_van_bookings_approved.html": mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_bookings_approved.html"),
		"admin_van_bookings_done.html":     mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_bookings_done.html"),
		"admin_vans.html":                  mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_vans.html"),
		"admin_van_book.html":              mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_book.html"),
		"admin_van_my_bookings.html":       mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_my_bookings.html"),
		"admin_van_notifications.html":     mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_notifications.html"),
		"admin_van_maintenance.html":       mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_maintenance.html"),
		"admin_van_availability.html":      mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/admin_van_availability.html"),
		"van_admin_permissions_list.html":  mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/van_admin_permissions_list.html"),
		"van_admin_permissions.html":       mustParse("templates/vanbooking/van_admin_base.html", "templates/vanbooking/van_admin_permissions.html"),
		// agent pages
		"dashboard.html":                   mustParse("templates/agent_base.html", "templates/dashboard.html"),
		"agent_estates.html":               mustParse("templates/agent_base.html", "templates/agent_estates.html"),
		"agent_estate_detail.html":         mustParse("templates/agent_base.html", "templates/agent_estate_detail.html"),
		"agent_estate_plots.html":          mustParse("templates/agent_base.html", "templates/agent_estate_plots.html"),
		"agent_estate_book.html":           mustParse("templates/agent_base.html", "templates/agent_estate_book.html"),
		"agent_cart.html":                  mustParse("templates/agent_base.html", "templates/agent_cart.html"),
		"agent_receipt.html":               mustParse("templates/agent_base.html", "templates/agent_receipt.html"),
		"agent_booking_receipt.html":       mustParse("templates/agent_base.html", "templates/agent_booking_receipt.html"),
		"admin_cart.html":                  mustParse("templates/admin_base.html", "templates/admin_cart.html"),
		"admin_receipt.html":               mustParse("templates/admin_base.html", "templates/admin_receipt.html"),
		"admin_booking_receipt.html":       mustParse("templates/admin_base.html", "templates/admin_booking_receipt.html"),
		"admin_private_estates.html":       mustParse("templates/admin_base.html", "templates/admin_private_estates.html"),
		"admin_private_estate_plots.html":  mustParse("templates/admin_base.html", "templates/admin_private_estate_plots.html"),
		"admin_private_estate_access.html": mustParse("templates/admin_base.html", "templates/admin_private_estate_access.html"),
		"agent_private_estates.html":       mustParse("templates/agent_base.html", "templates/agent_private_estates.html"),
		"agent_private_estate_plots.html":  mustParse("templates/agent_base.html", "templates/agent_private_estate_plots.html"),
		"agent_bookings.html":              mustParse("templates/agent_base.html", "templates/agent_bookings.html"),
		"agent_signed_plots.html":          mustParse("templates/agent_base.html", "templates/agent_signed_plots.html"),
		"agent_sales.html":                 mustParse("templates/agent_base.html", "templates/agent_sales.html"),
		"agent_van_booking.html":           mustParse("templates/vanbooking/van_agent_base.html", "templates/vanbooking/agent_van_booking.html"),
		"agent_van_bookings.html":          mustParse("templates/vanbooking/van_agent_base.html", "templates/vanbooking/agent_van_bookings.html"),
		"agent_van_notifications.html":     mustParse("templates/vanbooking/van_agent_base.html", "templates/vanbooking/agent_van_notifications.html"),
		"driver_sessions.html":             mustParse("templates/vanbooking/van_agent_base.html", "templates/vanbooking/driver_sessions.html"),
		"fleet_approval.html":              mustParse("templates/vanbooking/van_agent_base.html", "templates/vanbooking/fleet_approval.html"),
		// Welfare pages
		"welfare_members.html":          mustParse("templates/welfare/welfare_base.html", "templates/welfare/welfare_members.html"),
		"welfare_expenses.html":         mustParse("templates/welfare/welfare_base.html", "templates/welfare/welfare_expenses.html"),
		"welfare_beneficiaries.html":    mustParse("templates/welfare/welfare_base.html", "templates/welfare/welfare_beneficiaries.html"),
		"welfare_permissions_list.html": mustParse("templates/welfare/welfare_base.html", "templates/welfare/welfare_permissions_list.html"),
		"welfare_permissions.html":      mustParse("templates/welfare/welfare_base.html", "templates/welfare/welfare_permissions.html"),
		// Accounts review pages
		"accounts_queue.html":  mustParse("templates/accounts/accounts_base.html", "templates/accounts/accounts_queue.html"),
		"accounts_review.html": mustParse("templates/accounts/accounts_base.html", "templates/accounts/accounts_review.html"),
		// Legal review pages
		"legal_queue.html":     mustParse("templates/legal/legal_base.html", "templates/legal/legal_queue.html"),
		"legal_awaiting.html":  mustParse("templates/legal/legal_base.html", "templates/legal/legal_awaiting.html"),
		"legal_review.html":    mustParse("templates/legal/legal_base.html", "templates/legal/legal_review.html"),
		"legal_completed.html": mustParse("templates/legal/legal_base.html", "templates/legal/legal_completed.html"),
		// Settings pages (system_admin only)
		"settings_users.html":         mustParse("templates/settings/settings_base.html", "templates/settings/settings_users.html"),
		"settings_edit_user.html":     mustParse("templates/settings/settings_base.html", "templates/settings/settings_edit_user.html"),
		"settings_modules.html":       mustParse("templates/settings/settings_base.html", "templates/settings/settings_modules.html"),
		"settings_notifications.html": mustParse("templates/settings/settings_base.html", "templates/settings/settings_notifications.html"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", oauthCallbackHandler)
	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("/login/verify", login2faHandler)
	mux.HandleFunc("/login/resend-2fa", resend2faHandler)
	mux.HandleFunc("/logout", logoutHandler)
	mux.Handle("/hub", authMiddleware(http.HandlerFunc(hubHandler)))
	mux.Handle("/check-rebook-match", authMiddleware(http.HandlerFunc(checkRebookMatchHandler)))
	// Admin routes
	mux.Handle("/admin/dashboard", authMiddleware(requireRole(roleAdmin, requirePerm("admin.dashboard", "read", http.HandlerFunc(adminDashboardHandler)))))
	mux.Handle("/admin/estates", authMiddleware(requireRole(roleAdmin, requirePerm("admin.estates", "read", http.HandlerFunc(adminEstatesHandler)))))
	mux.Handle("/admin/estate/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.estates", "read", http.HandlerFunc(adminEstateRouter)))))
	mux.Handle("/admin/add-estate", authMiddleware(requireRole(roleAdmin, requirePerm("admin.estates", "write", http.HandlerFunc(adminAddEstateHandler)))))
	mux.Handle("/admin/add-plots", authMiddleware(requireRole(roleAdmin, requirePerm("admin.estates", "write", http.HandlerFunc(adminAddPlotsHandler)))))
	mux.Handle("/admin/booking/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.booked_plots", "write", http.HandlerFunc(adminBookingAttachmentsHandler)))))
	mux.Handle("/admin/mark-sold/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.signed_to_sold", "read", http.HandlerFunc(adminMarkSoldHandler)))))
	// System-admin-only emergency override — bypasses Accounts + Legal review entirely.
	mux.Handle("/admin/force-sa-signed/", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(adminForceSASignedHandler))))
	mux.Handle("/admin/skip-accounts/", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(adminSkipAccountsHandler))))
	mux.Handle("/admin/recheck-accounts/", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminRecheckAccountsHandler))))
	mux.Handle("/admin/booking-extend/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.extend_booking", "read", http.HandlerFunc(extendBookingHandler)))))
	mux.Handle("/admin/booking-retry-zoho/", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminRetryZohoHandler))))
	mux.Handle("/admin/signed-booking-retry-zoho/", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminRetryZohoSignedHandler))))
	mux.Handle("/admin/booking-delete-attachment/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.booked_plots", "write", http.HandlerFunc(bookingAttachmentDeleteHandler)))))
	mux.Handle("/agent/booking-delete-attachment/", authMiddleware(requireRole(roleAgent, requirePerm("agent.bookings", "write", http.HandlerFunc(bookingAttachmentDeleteHandler)))))
	mux.Handle("/agent/booking/", authMiddleware(requireRole(roleAgent, requirePerm("agent.bookings", "write", http.HandlerFunc(agentBookingAttachmentsHandler)))))
	mux.Handle("/admin/booking-receipt", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminBookingReceiptHandler))))
	mux.Handle("/admin/booking-receipt/", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminBookingReceiptViewHandler))))
	mux.Handle("/agent/booking-receipt", authMiddleware(requireRole(roleAgent, http.HandlerFunc(agentBookingReceiptHandler))))
	mux.Handle("/agent/booking-receipt/", authMiddleware(requireRole(roleAgent, http.HandlerFunc(agentBookingReceiptViewHandler))))
	mux.Handle("/admin/booked-plots", authMiddleware(requireRole(roleAdmin, requirePerm("admin.booked_plots", "read", http.HandlerFunc(adminBookedPlotsHandler)))))
	mux.Handle("/admin/signed-plots", authMiddleware(requireRole(roleAdmin, requirePerm("admin.signed_plots", "read", http.HandlerFunc(adminSignedPlotsHandler)))))
	mux.Handle("/admin/sold-plots", authMiddleware(requireRole(roleAdmin, requirePerm("admin.sold_plots", "read", http.HandlerFunc(adminSoldPlotsHandler)))))
	mux.Handle("/admin/payment-plans", authMiddleware(requireRole(roleAdmin, requirePerm("admin.payment_plans", "read", http.HandlerFunc(adminPaymentPlansHandler)))))
	mux.Handle("/admin/payment-plans/deal/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.payment_plans", "read", http.HandlerFunc(paymentPlanDealRouter)))))
	mux.Handle("/admin/payment-plans/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.payment_plans", "read", http.HandlerFunc(paymentPlanListHandler)))))
	mux.Handle("/admin/plots-overview", authMiddleware(requireRole(roleAdmin, requirePerm("admin.plots_overview", "read", http.HandlerFunc(adminPlotsOverviewHandler)))))
	mux.Handle("/admin/plots-overview/delete-attachment", authMiddleware(requireRole(roleAdmin, requirePerm("admin.plots_overview", "write", http.HandlerFunc(adminPlotsOverviewDeleteAttachmentHandler)))))
	mux.Handle("/admin/plots-overview/add-attachment", authMiddleware(requireRole(roleAdmin, requirePerm("admin.plots_overview", "write", http.HandlerFunc(adminPlotsOverviewAddAttachmentHandler)))))
	mux.Handle("/admin/plots-overview/update-buyer-name", authMiddleware(requireRole(roleAdmin, requirePerm("admin.plots_overview", "write", http.HandlerFunc(adminPlotsOverviewUpdateBuyerNameHandler)))))
	mux.Handle("/admin/pending-projects", authMiddleware(requireRole(roleAdmin, requirePerm("admin.pending_projects", "read", http.HandlerFunc(adminPendingProjectsHandler)))))
	mux.Handle("/admin/completed-projects", authMiddleware(requireRole(roleAdmin, requirePerm("admin.completed_projects", "read", http.HandlerFunc(adminCompletedProjectsHandler)))))
	mux.Handle("/admin/export-reports", authMiddleware(requireRole(roleAdmin, requirePerm("admin.export_reports", "read", http.HandlerFunc(adminExportReportsHandler)))))
	mux.Handle("/admin/van-bookings", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanBookingsHandler)))))
	mux.Handle("/admin/van-bookings/approved", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanBookingsApprovedHandler)))))
	mux.Handle("/admin/van-bookings/done", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanBookingsDoneHandler)))))
	mux.Handle("/admin/van-bookings/export", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanExportHandler)))))
	mux.Handle("/admin/van-bookings/export/detailed", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanExportDetailedHandler)))))
	mux.Handle("/admin/van-bookings/export/summary", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanExportSummaryHandler)))))
	mux.Handle("/admin/van-booking/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "write", http.HandlerFunc(vanbooking.AdminVanBookingActionHandler)))))
	mux.Handle("/admin/van-book", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "write", http.HandlerFunc(vanbooking.AdminVanBookHandler)))))
	mux.Handle("/admin/van-my-bookings", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_bookings", "read", http.HandlerFunc(vanbooking.AdminVanMyBookingsHandler)))))
	mux.Handle("/admin/vans", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_manage", "read", http.HandlerFunc(vanbooking.AdminVansHandler)))))
	mux.Handle("/admin/van-maintenance", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_manage", "write", http.HandlerFunc(vanbooking.AdminVanMaintenanceHandler)))))
	mux.Handle("/admin/van-availability", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_manage", "read", http.HandlerFunc(vanbooking.AdminVanAvailabilityHandler)))))
	mux.Handle("/admin/van-session/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_manage", "write", http.HandlerFunc(vanbooking.AdminVanAssignDriverHandler)))))
	mux.Handle("/admin/van-notify", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_manage", "write", http.HandlerFunc(vanbooking.AdminVanNotifyHandler)))))
	mux.Handle("/admin/van-permissions", authMiddleware(http.HandlerFunc(vanPermissionsListHandler)))
	mux.Handle("/admin/van-permissions/", authMiddleware(http.HandlerFunc(vanPermissionsHandler)))
	// Agent routes
	mux.Handle("/dashboard", authMiddleware(requireRole(roleAgent, requirePerm("agent.dashboard", "read", http.HandlerFunc(dashboardHandler)))))
	mux.Handle("/agent/estates", authMiddleware(requireRole(roleAgent, requirePerm("agent.estates", "read", http.HandlerFunc(agentEstatesHandler)))))
	mux.Handle("/agent/estate/", authMiddleware(requireRole(roleAgent, requirePerm("agent.estates", "read", http.HandlerFunc(agentEstateRouter)))))
	mux.Handle("/agent/cart/checkout", authMiddleware(requireRole(roleAgent, requirePerm("agent.bookings", "write", http.HandlerFunc(agentCartCheckoutHandler)))))
	mux.Handle("/agent/cart", authMiddleware(requireRole(roleAgent, requirePerm("agent.estates", "read", http.HandlerFunc(agentCartHandler)))))
	mux.Handle("/agent/receipt/", authMiddleware(requireRole(roleAgent, http.HandlerFunc(agentReceiptHandler))))
	mux.Handle("/admin/cart/checkout", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminCartCheckoutHandler))))
	mux.Handle("/admin/cart", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminCartHandler))))
	mux.Handle("/admin/receipt/", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminReceiptHandler))))
	mux.Handle("/admin/receipts", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(adminReceiptsListHandler))))
	mux.Handle("/admin/private-estates", authMiddleware(requireRole(roleAdmin, requirePerm("admin.private_estates", "read", http.HandlerFunc(adminPrivateEstatesHandler)))))
	mux.Handle("/admin/private-estate/", authMiddleware(requireRole(roleAdmin, requirePerm("admin.private_estates", "read", http.HandlerFunc(adminPrivateEstateRouter)))))
	mux.Handle("/agent/private-estates", authMiddleware(requireRole(roleAgent, requirePerm("agent.private_estates", "read", http.HandlerFunc(agentPrivateEstatesHandler)))))
	mux.Handle("/agent/private-estate/", authMiddleware(requireRole(roleAgent, requirePerm("agent.private_estates", "read", http.HandlerFunc(agentPrivateEstateRouter)))))
	mux.Handle("/agent/bookings/cancel/", authMiddleware(requireRole(roleAgent, requirePerm("agent.bookings", "write", http.HandlerFunc(agentCancelBookingHandler)))))
	mux.Handle("/agent/bookings", authMiddleware(requireRole(roleAgent, requirePerm("agent.bookings", "read", http.HandlerFunc(agentBookingsHandler)))))
	mux.Handle("/agent/signed-plots", authMiddleware(requireRole(roleAgent, requirePerm("agent.signed_plots", "read", http.HandlerFunc(agentSignedPlotsHandler)))))
	mux.Handle("/agent/sales", authMiddleware(requireRole(roleAgent, requirePerm("agent.sales", "read", http.HandlerFunc(agentSalesHandler)))))
	mux.Handle("/agent/van-booking", authMiddleware(requireRole(roleAgent, requirePerm("agent.van_booking", "read", http.HandlerFunc(vanbooking.AgentVanBookingHandler)))))
	mux.Handle("/agent/van-bookings", authMiddleware(requireRole(roleAgent, requirePerm("agent.van_booking", "read", http.HandlerFunc(vanbooking.AgentVanBookingsHandler)))))
	mux.Handle("/agent/van-notifications", authMiddleware(requireRole(roleAgent, requirePerm("agent.van_notifications", "read", http.HandlerFunc(vanbooking.AgentVanNotificationsHandler)))))
	mux.Handle("/agent/van-driver-sessions", authMiddleware(requireRole(roleAgent, http.HandlerFunc(vanbooking.DriverSessionsHandler))))
	mux.Handle("/agent/van-session/", authMiddleware(requireRole(roleAgent, http.HandlerFunc(vanbooking.DriverMarkDoneHandler))))
	mux.Handle("/agent/van-booking/", authMiddleware(requireRole(roleAgent, http.HandlerFunc(vanbooking.AgentVanCancelHandler))))
	mux.Handle("/agent/van-fleet-approval", authMiddleware(requireRole(roleAgent, http.HandlerFunc(vanbooking.FleetApprovalHandler))))
	mux.Handle("/agent/van-fleet-booking/", authMiddleware(requireRole(roleAgent, http.HandlerFunc(vanbooking.FleetBookingActionHandler))))
	mux.Handle("/admin/van-notifications", authMiddleware(requireRole(roleAdmin, requirePerm("admin.van_notifications", "read", http.HandlerFunc(vanbooking.AdminVanNotificationsHandler)))))
	mux.Handle("/admin/test-whatsapp", authMiddleware(requireRole(roleAdmin, http.HandlerFunc(testWhatsAppHandler))))
	// Welfare permissions (system_admin only — registered before /welfare/ catch-all)
	mux.Handle("/welfare/permissions", authMiddleware(http.HandlerFunc(welfarePermissionsListHandler)))
	mux.Handle("/welfare/permissions/", authMiddleware(http.HandlerFunc(welfarePermissionsHandler)))
	// Welfare routes (accessible to any authenticated user)
	mux.Handle("/welfare/", authMiddleware(http.HandlerFunc(welfare.Router)))
	// Accounts + Legal review modules — role-exclusive.
	mux.Handle("/accounts/", authMiddleware(requireAccountsAccess(http.HandlerFunc(accounts.Router))))
	mux.Handle("/legal/", authMiddleware(requireLegalAccess(http.HandlerFunc(legal.Router))))
	// Settings — system_admin only: user creation, password resets, config.
	mux.Handle("/settings", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsRootHandler))))
	mux.Handle("/settings/", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsRootHandler))))
	mux.Handle("/settings/users", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsUsersHandler))))
	mux.Handle("/settings/edit-user/", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsEditUserHandler))))
	mux.Handle("/settings/modules/", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsModulesHandler))))
	mux.Handle("/settings/notifications", authMiddleware(requireRole(roleSystemAdmin, http.HandlerFunc(settingsNotificationsHandler))))
	mux.Handle("/", http.HandlerFunc(rootHandler))

	fs := http.FileServer(http.Dir("static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	uploads := http.FileServer(http.Dir(uploadsDir))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", uploads))

	server := &http.Server{
		Addr:         ":8090",
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Println("Portal running on http://localhost:8090")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func isAuthenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value == "ok"
}

func getRole(r *http.Request) string {
	c, err := r.Cookie(roleCookie)
	if err != nil {
		return ""
	}
	switch c.Value {
	case roleAdmin, roleAgent, roleSystemAdmin, roleAccounts, roleLegal:
		return c.Value
	}
	return ""
}

func setSession(w http.ResponseWriter, role, name, userID string) {
	exp := time.Now().Add(8 * time.Hour)
	for _, c := range []struct{ k, v string }{
		{sessionCookie, "ok"},
		{roleCookie, role},
		{nameCookie, name},
		{idCookie, userID},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     c.k,
			Value:    c.v,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  exp,
		})
	}
}

func clearSession(w http.ResponseWriter) {
	for _, n := range []string{sessionCookie, roleCookie, nameCookie, idCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     n,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

func getAgentName(r *http.Request) string {
	c, err := r.Cookie(nameCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticated(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userRole := getRole(r)
		// system_admin passes all role checks
		if userRole == roleSystemAdmin {
			next.ServeHTTP(w, r)
			return
		}
		if userRole != role {
			switch userRole {
			case roleAdmin, roleAgent:
				http.Redirect(w, r, "/hub", http.StatusFound)
			case roleAccounts:
				http.Redirect(w, r, "/accounts", http.StatusFound)
			case roleLegal:
				http.Redirect(w, r, "/legal", http.StatusFound)
			default:
				http.Redirect(w, r, "/login", http.StatusFound)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// canAccessAccounts reports whether the current user may use the Accounts
// module: role "accounts" or system_admin (always), an admin with
// admin.accounts_access, or any user explicitly granted accounts.access.
func canAccessAccounts(r *http.Request) bool {
	role := getRole(r)
	if role == roleSystemAdmin || role == roleAccounts {
		return true
	}
	uid := getUserID(r)
	if role == roleAdmin && hasPermission(uid, "admin.accounts_access", "read") {
		return true
	}
	return hasPermission(uid, "accounts.access", "read")
}

// requireAccountsAccess gates /accounts/* to canAccessAccounts, redirecting
// anyone else to their own home rather than erroring — same pattern as
// requireRole.
func requireAccountsAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if canAccessAccounts(r) {
			next.ServeHTTP(w, r)
			return
		}
		switch getRole(r) {
		case roleAdmin, roleAgent:
			http.Redirect(w, r, "/hub", http.StatusFound)
		case roleLegal:
			http.Redirect(w, r, "/legal", http.StatusFound)
		default:
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	})
}

// canAccessLegal reports whether the current user may use the Legal module.
// role "legal" and system_admin get full access (see isAssignedLawyer);
// "admin" gets blanket read-only oversight — every booking at every stage,
// across every estate, with the assigned lawyer shown per row (see
// scopeQuery/canView in legal/legal.go). Agents track their own bookings'
// stage from their own My Bookings page instead (bookingStageLabel below),
// not through this module. Anyone else needs an individual legal.access
// grant via the Modules page.
func canAccessLegal(r *http.Request) bool {
	switch getRole(r) {
	case roleSystemAdmin, roleLegal, roleAdmin:
		return true
	}
	return hasPermission(getUserID(r), "legal.access", "read")
}

// requireLegalAccess gates /legal/* to canAccessLegal.
func requireLegalAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if canAccessLegal(r) {
			next.ServeHTTP(w, r)
			return
		}
		switch getRole(r) {
		case roleAdmin, roleAgent:
			http.Redirect(w, r, "/hub", http.StatusFound)
		case roleAccounts:
			http.Redirect(w, r, "/accounts", http.StatusFound)
		default:
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	})
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	switch getRole(r) {
	case roleAdmin:
		http.Redirect(w, r, "/hub", http.StatusFound)
	case roleAgent:
		http.Redirect(w, r, "/hub", http.StatusFound)
	default:
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

type ctxKey string

const ctxID ctxKey = "id"
const ctxPlotID ctxKey = "plotID"

// pathSegment extracts the first path segment after the given prefix.
func pathSegment(prefix, path string) string {
	s := strings.TrimPrefix(path, prefix)
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

// redirectBack sends the user back to wherever they came from (the Referer
// header) instead of a bare fallback page — so after acting on one row in a
// filtered/paginated list, they land back on that same filtered view rather
// than a reset, top-of-list, no-filters page. Pass anchor (an element id,
// without "#") to also have the browser scroll straight back to that row;
// leave it empty to just preserve the query string. extra, if given, is one
// or more pre-encoded "key=value" pairs (e.g. a one-shot success banner
// flag) merged into the query string regardless of whether the Referer or
// fallbackPath ends up being used.
//
// Only the Referer's path+query is ever used — never its scheme/host — so a
// forged Referer can at most bounce the request back into this same app,
// never off-site (no open-redirect risk). Falls back to fallbackPath if
// there's no usable Referer (e.g. the action was triggered some other way).
func redirectBack(w http.ResponseWriter, r *http.Request, fallbackPath, anchor string, extra ...string) {
	target := fallbackPath
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && strings.HasPrefix(u.Path, "/") {
			target = u.Path
			if u.RawQuery != "" {
				target += "?" + u.RawQuery
			}
		}
	}
	for _, kv := range extra {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + kv
	}
	if anchor != "" {
		target += "#" + anchor
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func withID(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxID, id))
}

func adminEstateRouter(w http.ResponseWriter, r *http.Request) {
	id := pathSegment("/admin/estate/", r.URL.Path)
	suffix := strings.TrimPrefix(r.URL.Path, "/admin/estate/"+id)
	r = withID(r, id)

	// Write-only sub-routes require write permission on admin.estates
	switch suffix {
	case "/edit", "/delete", "/zones":
		if getRole(r) != roleSystemAdmin && !hasPermission(getUserID(r), "admin.estates", "write") {
			w.WriteHeader(http.StatusForbidden)
			render(w, "access_denied", nil)
			return
		}
	}

	switch suffix {
	case "", "/":
		adminEstateDetailHandler(w, r)
	case "/edit":
		adminEstateEditHandler(w, r)
	case "/delete":
		adminEstateDeleteHandler(w, r)
	case "/zones":
		adminSaveZonesHandler(w, r)
	case "/plots":
		adminEstatePlotsHandler(w, r)
	case "/book":
		adminEstateBookHandler(w, r)
	case "/plot-status":
		adminUpdatePlotStatusHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func agentEstateRouter(w http.ResponseWriter, r *http.Request) {
	id := pathSegment("/agent/estate/", r.URL.Path)
	suffix := strings.TrimPrefix(r.URL.Path, "/agent/estate/"+id)
	r = withID(r, id)
	switch suffix {
	case "", "/":
		agentEstateDetailHandler(w, r)
	case "/plots":
		agentEstatePlotsHandler(w, r)
	case "/book":
		agentEstateBookHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func oauthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code parameter", http.StatusBadRequest)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	clientSecret := r.URL.Query().Get("client_secret")
	if clientID == "" {
		clientID = "1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN"
	}
	if clientSecret == "" {
		clientSecret = "3c01794c2d6172f0636d041751d7562157d7e7d885"
	}

	resp, err := http.PostForm("https://accounts.zoho.com/oauth/v2/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"https://portal.proproperty.co.ke/oauth/callback"},
		"code":          {code},
	})
	if err != nil {
		http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:monospace;padding:32px;max-width:800px;">
<h2>Zoho OAuth Token</h2>
<p>HTTP Status: %d</p>
<pre style="background:#f3f4f6;padding:16px;border-radius:8px;white-space:pre-wrap;word-break:break-all;">%s</pre>
<p style="color:#6b7280;font-size:13px;">Copy the <strong>refresh_token</strong> value and paste it to update integrations.go</p>
</body></html>`, resp.StatusCode, string(body))
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if isAuthenticated(r) {
			http.Redirect(w, r, "/hub", http.StatusFound)
			return
		}
		render(w, "login", map[string]any{"Title": "Login"})
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	var id int
	var name, hash, role, phone string
	var blocked int
	err := db.QueryRow(
		`SELECT id, name, password, role, COALESCE(phone,''), COALESCE(blocked,0) FROM prop_agents WHERE email = ? LIMIT 1`, email,
	).Scan(&id, &name, &hash, &role, &phone, &blocked)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		logAccess("LOGIN_FAIL", email, "unknown", clientIP(r), "bad credentials")
		render(w, "login", map[string]any{
			"Title": "Login",
			"Error": "Invalid email or password",
			"Email": email,
		})
		return
	}
	if blocked == 1 {
		logAccess("LOGIN_BLOCKED", email, role, clientIP(r), "blocked account")
		render(w, "login", map[string]any{
			"Title": "Login",
			"Error": "This account has been blocked. Contact your administrator.",
			"Email": email,
		})
		return
	}

	// Check if this device is already trusted
	trustCookieName := fmt.Sprintf("pp_trust_%d", id)
	if tc, tcErr := r.Cookie(trustCookieName); tcErr == nil && tc.Value != "" {
		var count int
		db.QueryRow(
			`SELECT COUNT(*) FROM prop_trusted_devices WHERE token = ? AND user_id = ? AND expires_at > NOW()`,
			tc.Value, id,
		).Scan(&count)
		if count > 0 {
			setSession(w, role, name, fmt.Sprintf("%d", id))
			logAccess("LOGIN_OK", name, role, clientIP(r), "trusted_device email="+email)
			http.Redirect(w, r, "/hub", http.StatusFound)
			return
		}
	}

	// Generate OTP, store it, and send via email + SMS
	otp := generateOTP()
	db.Exec(`DELETE FROM prop_otp_sessions WHERE user_id = ?`, id)
	if _, err := db.Exec(
		`INSERT INTO prop_otp_sessions (user_id, otp_code, expires_at) VALUES (?, ?, DATE_ADD(NOW(), INTERVAL 10 MINUTE))`,
		id, otp,
	); err != nil {
		log.Printf("ERROR inserting OTP for user %d: %v", id, err)
		render(w, "login", map[string]any{
			"Title": "Login",
			"Error": "Could not send verification code. Please try again.",
			"Email": email,
		})
		return
	}
	sendOTP(email, phone, otp)

	// Set short-lived pending cookie to carry user ID to the verify page
	http.SetCookie(w, &http.Cookie{
		Name:     otpPendingCookie,
		Value:    fmt.Sprintf("%d", id),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(15 * time.Minute),
	})

	logAccess("LOGIN_2FA_SENT", name, role, clientIP(r), "email="+email)
	http.Redirect(w, r, "/login/verify", http.StatusSeeOther)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func hubHandler(w http.ResponseWriter, r *http.Request) {
	role := getRole(r)
	isSA := role == roleSystemAdmin
	uid := getUserID(r)

	portalURL := "/dashboard"
	vanBookingURL := "/agent/van-bookings"
	if role == roleAdmin || isSA {
		portalURL = "/admin/dashboard"
		vanBookingURL = "/admin/van-bookings"
	}

	showWelfare := isSA ||
		hasPermission(uid, "welfare.members", "read") ||
		hasPermission(uid, "welfare.expenses", "read") ||
		hasPermission(uid, "welfare.claims", "read")

	// Each module card only appears when the user has at least one granted
	// feature in that module, or is system_admin (who sees everything).
	showPortal := isSA || hasAnyModulePermission(uid, "Marketers Portal")
	showVan := isSA || hasAnyModulePermission(uid, "Van Booking")
	showAsset := isSA || hasAnyModulePermission(uid, "Asset Management")
	showLeave := isSA || hasAnyModulePermission(uid, "Leave Management")
	showHR := isSA || hasAnyModulePermission(uid, "HR Management")
	showTask := isSA || hasAnyModulePermission(uid, "Task Management")

	render(w, "hub", map[string]any{
		"Title":         "Portal Hub",
		"UserName":      getAgentName(r),
		"PortalURL":     portalURL,
		"VanBookingURL": vanBookingURL,
		"IsSystemAdmin": isSA,
		"ShowPortal":    showPortal,
		"ShowVan":       showVan,
		"ShowWelfare":   showWelfare,
		"ShowAccounts":  canAccessAccounts(r),
		"ShowLegal":     canAccessLegal(r),
		"ShowAsset":     showAsset,
		"ShowLeave":     showLeave,
		"ShowHR":        showHR,
		"ShowTask":      showTask,
	})
}

// checkRebookMatchHandler is the client-side pre-check the booking forms call
// before submitting — for each plot being booked, reports whether the buyer
// name/phone matches a prior (cancelled/expired) booking on that same plot,
// so the form can prompt the agent to confirm a rebook before finalizing.
// Read-only; the actual reuse-vs-create decision is re-verified server-side
// in createBooksRecordForBooking using the same helpers — this endpoint only
// drives the popup, it's never trusted as the authoritative answer.
func checkRebookMatchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	plotIDStrs := strings.Split(r.URL.Query().Get("plot_ids"), ",")
	buyerName := strings.TrimSpace(r.URL.Query().Get("buyer_name"))
	buyerPhone := strings.TrimSpace(r.URL.Query().Get("buyer_phone"))

	type match struct {
		PlotID            int    `json:"plot_id"`
		PlotNumber        string `json:"plot_number"`
		EstateName        string `json:"estate_name"`
		PreviousBuyerName string `json:"previous_buyer_name"`
	}
	var matches []match

	for _, idStr := range plotIDStrs {
		pid, err := strconv.Atoi(strings.TrimSpace(idStr))
		if err != nil || pid == 0 {
			continue
		}
		prior, err := findPriorBookingHistory(pid)
		if err != nil || prior == nil {
			continue
		}
		if !buyerMatchesPrior(prior, buyerName, buyerPhone) {
			continue
		}
		var plotNumber, estateName string
		db.QueryRow(`SELECT p.plot_number, e.name FROM prop_plots p JOIN prop_estates e ON e.id = p.estate_id WHERE p.id = ?`, pid).
			Scan(&plotNumber, &estateName)
		matches = append(matches, match{PlotID: pid, PlotNumber: plotNumber, EstateName: estateName, PreviousBuyerName: prior.BuyerName})
	}

	if matches == nil {
		matches = []match{}
	}
	json.NewEncoder(w).Encode(matches)
}

// Admin dashboard: overview with bar charts per estate
func adminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	selectedMonth := r.URL.Query().Get("month")

	// Estate plot counts — total and available always reflect current stock.
	// Booked/SA Signed/Sold are filtered by month when selected.
	rows, err := db.Query(`
		SELECT e.name,
			COUNT(p.id),
			COALESCE(SUM(p.status = 'available'), 0)
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		WHERE COALESCE(e.is_restricted,0)=0
		GROUP BY e.id, e.name
		ORDER BY e.name`)
	if err != nil {
		log.Printf("dashboard query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var estates []string
	var totalPlots, availablePlots []int
	for rows.Next() {
		var name string
		var total, available int
		if err := rows.Scan(&name, &total, &available); err != nil {
			log.Printf("dashboard scan: %v", err)
			continue
		}
		estates = append(estates, name)
		totalPlots = append(totalPlots, total)
		availablePlots = append(availablePlots, available)
	}

	// Build per-estate maps for booked/sa_signed/sold, month-filtered when selected.
	bookedByEstate := map[string]int{}
	saSignedByEstate := map[string]int{}
	soldByEstate := map[string]int{}

	if selectedMonth != "" {
		if br, e := db.Query(`
			SELECT e.name, COUNT(b.id)
			FROM prop_bookings b JOIN prop_estates e ON e.id = b.estate_id
			WHERE DATE_FORMAT(b.date_booked,'%Y-%m') = ?
			  AND COALESCE(e.is_restricted,0)=0
			GROUP BY e.id, e.name`, selectedMonth); e == nil {
			defer br.Close()
			for br.Next() {
				var n string
				var c int
				if br.Scan(&n, &c) == nil {
					bookedByEstate[n] = c
				}
			}
		}
		if sr, e := db.Query(`
			SELECT e.name, COUNT(b.id)
			FROM prop_bookings b JOIN prop_estates e ON e.id = b.estate_id
			WHERE b.status IN ('sa_signed','completed')
			  AND DATE_FORMAT(COALESCE(b.date_signed, b.date_booked),'%Y-%m') = ?
			  AND COALESCE(e.is_restricted,0)=0
			GROUP BY e.id, e.name`, selectedMonth); e == nil {
			defer sr.Close()
			for sr.Next() {
				var n string
				var c int
				if sr.Scan(&n, &c) == nil {
					saSignedByEstate[n] = c
				}
			}
		}
		if vr, e := db.Query(`
			SELECT e.name, COUNT(s.id)
			FROM prop_sales s JOIN prop_estates e ON e.id = s.estate_id
			WHERE DATE_FORMAT(s.sale_date,'%Y-%m') = ?
			  AND COALESCE(e.is_restricted,0)=0
			GROUP BY e.id, e.name`, selectedMonth); e == nil {
			defer vr.Close()
			for vr.Next() {
				var n string
				var c int
				if vr.Scan(&n, &c) == nil {
					soldByEstate[n] = c
				}
			}
		}
	} else {
		if br, e := db.Query(`
			SELECT e.name, SUM(p.status='booked'), SUM(p.status='sa_signed'), SUM(p.status='sold')
			FROM prop_estates e LEFT JOIN prop_plots p ON p.estate_id = e.id
			WHERE COALESCE(e.is_restricted,0)=0
			GROUP BY e.id, e.name ORDER BY e.name`); e == nil {
			defer br.Close()
			for br.Next() {
				var n string
				var b, s, v int
				if br.Scan(&n, &b, &s, &v) == nil {
					bookedByEstate[n] = b
					saSignedByEstate[n] = s
					soldByEstate[n] = v
				}
			}
		}
	}

	var bookedPlots, saSignedPlots, soldPlots []int
	for _, name := range estates {
		bookedPlots = append(bookedPlots, bookedByEstate[name])
		saSignedPlots = append(saSignedPlots, saSignedByEstate[name])
		soldPlots = append(soldPlots, soldByEstate[name])
	}

	// Top agents by value of plots sa_signed, filtered by month when selected.
	// Uses prop_bookings: status 'completed' means the plot later went to sold but was still sa_signed.
	// COALESCE(date_signed, date_booked) handles older bookings where date_signed was not yet recorded.
	agentQuery := `
		SELECT b.agent_name, COALESCE(SUM(e.plot_price), 0) AS total_value
		FROM prop_bookings b
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.status IN ('sa_signed', 'completed')
		  AND b.agent_name IS NOT NULL AND b.agent_name != ''
		  AND b.agent_name NOT IN ('Daniel Mwangi', 'Kevin Magua', 'James Maina')
		GROUP BY b.agent_name
		ORDER BY total_value DESC
		LIMIT 10`
	agentArgs := []any{}
	if selectedMonth != "" {
		agentQuery = `
			SELECT b.agent_name, COALESCE(SUM(e.plot_price), 0) AS total_value
			FROM prop_bookings b
			JOIN prop_estates e ON e.id = b.estate_id
			WHERE b.status IN ('sa_signed', 'completed')
			  AND b.agent_name IS NOT NULL AND b.agent_name != ''
			  AND b.agent_name NOT IN ('Daniel Mwangi', 'Kevin Magua', 'James Maina')
			  AND DATE_FORMAT(COALESCE(b.date_signed, b.date_booked), '%Y-%m') = ?
			GROUP BY b.agent_name
			ORDER BY total_value DESC
			LIMIT 10`
		agentArgs = []any{selectedMonth}
	}
	var agents []string
	var agentValues []float64
	if agentRows, err := db.Query(agentQuery, agentArgs...); err == nil {
		defer agentRows.Close()
		for agentRows.Next() {
			var name string
			var val float64
			if agentRows.Scan(&name, &val) == nil {
				agents = append(agents, name)
				agentValues = append(agentValues, val)
			}
		}
	}

	// Plots fully sold per agent (from prop_sales), always all-time, no month filter.
	soldAgentQ := `
		SELECT s.agent_name, COUNT(*) AS cnt
		FROM prop_sales s
		WHERE s.agent_name IS NOT NULL AND s.agent_name != ''
		  AND s.agent_name NOT IN ('Joseph Maina', 'Lucy Wambui', 'James Maina', 'Daniel Mwangi', 'Kevin Magua')
		GROUP BY s.agent_name ORDER BY cnt DESC LIMIT 10`
	var soldAgents []string
	var soldAgentCounts []int
	if sar, err2 := db.Query(soldAgentQ); err2 == nil {
		defer sar.Close()
		for sar.Next() {
			var n string
			var c int
			if sar.Scan(&n, &c) == nil {
				soldAgents = append(soldAgents, n)
				soldAgentCounts = append(soldAgentCounts, c)
			}
		}
	}

	// Months dropdown — union of booking, sale, and sa_signed transition months
	monthRows, _ := db.Query(`
		SELECT DISTINCT month FROM (
			SELECT DATE_FORMAT(date_booked, '%Y-%m') AS month FROM prop_bookings WHERE date_booked IS NOT NULL
			UNION
			SELECT DATE_FORMAT(sale_date, '%Y-%m') AS month FROM prop_sales WHERE sale_date IS NOT NULL
			UNION
			SELECT DATE_FORMAT(changed_at, '%Y-%m') AS month FROM prop_plot_status_log WHERE new_status = 'sa_signed'
		) t
		ORDER BY month DESC`)
	var months []string
	if monthRows != nil {
		defer monthRows.Close()
		for monthRows.Next() {
			var m string
			if err := monthRows.Scan(&m); err == nil {
				months = append(months, m)
			}
		}
	}

	// Summary stat cards — filter by month when selected
	type statTotals struct {
		Estates       int
		Available     int
		Booked        int
		SaSigned      int
		Sold          int
		MonthFiltered bool
	}
	var stats statTotals
	db.QueryRow(`SELECT COUNT(*) FROM prop_estates WHERE COALESCE(is_restricted,0)=0`).Scan(&stats.Estates)
	db.QueryRow(`SELECT COUNT(*) FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.status='available' AND COALESCE(e.is_restricted,0)=0`).Scan(&stats.Available)
	if selectedMonth != "" {
		stats.MonthFiltered = true
		db.QueryRow(`SELECT COUNT(*) FROM prop_bookings b JOIN prop_estates e ON e.id=b.estate_id WHERE DATE_FORMAT(b.date_booked,'%Y-%m')=? AND b.status IN ('active','sa_signed','completed') AND COALESCE(e.is_restricted,0)=0`, selectedMonth).Scan(&stats.Booked)
		db.QueryRow(`SELECT COUNT(*) FROM prop_bookings b JOIN prop_estates e ON e.id=b.estate_id WHERE DATE_FORMAT(b.date_booked,'%Y-%m')=? AND b.status='sa_signed' AND COALESCE(e.is_restricted,0)=0`, selectedMonth).Scan(&stats.SaSigned)
		db.QueryRow(`SELECT COUNT(*) FROM prop_sales s JOIN prop_estates e ON e.id=s.estate_id WHERE DATE_FORMAT(s.sale_date,'%Y-%m')=? AND COALESCE(e.is_restricted,0)=0`, selectedMonth).Scan(&stats.Sold)
	} else {
		db.QueryRow(`SELECT COUNT(*) FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.status='booked' AND COALESCE(e.is_restricted,0)=0`).Scan(&stats.Booked)
		db.QueryRow(`SELECT COUNT(*) FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.status='sa_signed' AND COALESCE(e.is_restricted,0)=0`).Scan(&stats.SaSigned)
		db.QueryRow(`SELECT COUNT(*) FROM prop_plots p JOIN prop_estates e ON e.id=p.estate_id WHERE p.status='sold' AND COALESCE(e.is_restricted,0)=0`).Scan(&stats.Sold)
	}

	chartData := map[string]any{
		"estates":         estates,
		"totalPlots":      totalPlots,
		"availablePlots":  availablePlots,
		"bookedPlots":     bookedPlots,
		"soldPlots":       soldPlots,
		"saSignedPlots":   saSignedPlots,
		"agents":          agents,
		"agentValues":     agentValues,
		"soldAgents":      soldAgents,
		"soldAgentCounts": soldAgentCounts,
	}
	chartJSON, _ := json.Marshal(chartData)

	renderAdmin(w, r, "admin_dashboard.html", map[string]any{
		"Title":         "Dashboard Overview",
		"Active":        "dashboard",
		"Months":        months,
		"SelectedMonth": selectedMonth,
		"ChartData":     template.JS(chartJSON),
		"Stats":         stats,
	})
}

func adminEstatesHandler(w http.ResponseWriter, r *http.Request) {
	type estateRow struct {
		ID        int
		Name      string
		Total     int
		Available int
		Booked    int
		SaSigned  int
		Sold      int
	}
	rows, err := db.Query(`
		SELECT e.id, e.name,
			COUNT(p.id),
			COALESCE(SUM(p.status = 'available'), 0),
			COALESCE(SUM(p.status = 'booked'), 0),
			COALESCE(SUM(p.status = 'sa_signed'), 0),
			COALESCE(SUM(p.status = 'sold'), 0)
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		WHERE COALESCE(e.is_restricted, 0) = 0
		GROUP BY e.id, e.name
		ORDER BY e.name`)
	if err != nil {
		log.Printf("estates query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var estates []estateRow
	for rows.Next() {
		var e estateRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Total, &e.Available, &e.Booked, &e.SaSigned, &e.Sold); err != nil {
			log.Printf("estates scan: %v", err)
			continue
		}
		estates = append(estates, e)
	}
	renderAdmin(w, r, "admin_estates.html", map[string]any{"Title": "Estates", "Active": "estates", "Estates": estates})
}

func adminEstateDetailHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "available"
	}

	// Estate info
	var estateName, image, mutationImage, plotInfo string
	err := db.QueryRow(`SELECT name, COALESCE(image,''), COALESCE(mutation_image,''), COALESCE(prop_plotinfo,'') FROM prop_estates WHERE id = ?`, id).
		Scan(&estateName, &image, &mutationImage, &plotInfo)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	displayImage := mutationImage
	if displayImage == "" {
		displayImage = image
	}

	// Status counts
	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id)
	if cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}

	// Plot zones with current status
	zones := []zoneDisplay{}
	zRows, _ := db.Query(`
		SELECT pz.plot_number, COALESCE(pz.points_json,'[]'), COALESCE(pp.status,'available')
		FROM prop_plot_zones pz
		LEFT JOIN prop_plots pp ON pp.estate_id = pz.estate_id AND pp.plot_number = pz.plot_number
		WHERE pz.estate_id = ?
		ORDER BY pz.id`, id)
	if zRows != nil {
		defer zRows.Close()
		for zRows.Next() {
			var pn, pj, st string
			if err := zRows.Scan(&pn, &pj, &st); err == nil {
				var pts []pointXY
				json.Unmarshal([]byte(pj), &pts)
				zones = append(zones, zoneDisplay{PlotNumber: pn, Status: st, Points: pts})
			}
		}
	}
	zonesJSON, _ := json.Marshal(zones)

	// Plots filtered by selected status tab
	type plotRow struct {
		ID     int
		Number string
		Status string
	}
	pRows, _ := db.Query(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id=? AND status=? ORDER BY id`, id, status)
	var plots []plotRow
	if pRows != nil {
		defer pRows.Close()
		for pRows.Next() {
			var p plotRow
			if pRows.Scan(&p.ID, &p.Number, &p.Status) == nil {
				plots = append(plots, p)
			}
		}
	}

	plotInfo = strings.ReplaceAll(plotInfo, `\r\n`, "\n")
	plotInfo = strings.ReplaceAll(plotInfo, `\n`, "\n")
	plotInfo = strings.ReplaceAll(plotInfo, "\r", "")

	plotInfoLinesJSON, _ := json.Marshal(strings.Split(plotInfo, "\n"))

	canWrite := getRole(r) == roleSystemAdmin || hasPermission(getUserID(r), "admin.estates", "write")
	renderAdmin(w, r, "admin_estate_detail.html", map[string]any{
		"Title":        estateName,
		"Active":       "estates",
		"EstateName":   estateName,
		"EstateID":     id,
		"DisplayImage": displayImage,
		"PlotInfo":     plotInfo,
		"PlotInfoJSON": template.JS(plotInfoLinesJSON),
		"Plots":        plots,
		"Counts":       counts,
		"Status":       status,
		"ZonesJSON":    template.JS(zonesJSON),
		"CanWrite":     canWrite,
	})
}

// saveUploadedFiles saves multipart uploaded files and returns comma-separated filenames.
func saveUploadedFiles(r *http.Request, fieldName string) string {
	if r.MultipartForm == nil {
		return ""
	}
	var saved []string
	for _, fh := range r.MultipartForm.File[fieldName] {
		if fh.Size == 0 {
			continue // skip empty file parts (common on mobile when no file selected)
		}
		f, err := fh.Open()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		// Mobile browsers (especially iOS camera) often send files with no extension;
		// fall back to Content-Type to determine the correct extension.
		if ext == "" {
			switch fh.Header.Get("Content-Type") {
			case "image/jpeg":
				ext = ".jpg"
			case "image/png":
				ext = ".png"
			case "image/heic", "image/heif":
				ext = ".heic"
			case "image/webp":
				ext = ".webp"
			case "application/pdf":
				ext = ".pdf"
			default:
				ext = ".bin"
			}
		}
		safe := strings.Map(func(ch rune) rune {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
				return ch
			}
			return '_'
		}, strings.TrimSuffix(fh.Filename, filepath.Ext(fh.Filename)))
		if safe == "" {
			safe = fieldName // fallback when browser sends no filename (e.g. camera shot)
		}
		fname := fmt.Sprintf("%s_%d_%s%s", fieldName, time.Now().UnixNano(), safe, ext)
		dst, err := os.Create(filepath.Join(uploadsDir, fname))
		if err != nil {
			f.Close()
			continue
		}
		io.Copy(dst, f)
		dst.Close()
		f.Close()
		saved = append(saved, fname)
	}
	return strings.Join(saved, ",")
}

func adminEstatePlotsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "available"
	}

	var estateName string
	if err := db.QueryRow(`SELECT name FROM prop_estates WHERE id=?`, id).Scan(&estateName); err != nil {
		http.NotFound(w, r)
		return
	}

	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id)
	if cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}

	type plotRow struct {
		ID         int
		Number     string
		Status     string
		BuyerName  string
		BuyerPhone string
		AgentName  string
		DateBooked string
	}
	var plots []plotRow
	if status == "booked" || status == "sa_signed" {
		pRows, _ := db.Query(`
			SELECT p.id, p.plot_number, p.status,
				COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''),
				COALESCE(b.agent_name,''), COALESCE(DATE_FORMAT(b.date_booked,'%d %b %Y %H:%i'),'')
			FROM prop_plots p
			LEFT JOIN prop_bookings b ON b.id = (
				SELECT id FROM prop_bookings
				WHERE plot_id = p.id
				ORDER BY id DESC LIMIT 1
			)
			WHERE p.estate_id=? AND p.status=?
			ORDER BY p.id`, id, status)
		if pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p plotRow
				if pRows.Scan(&p.ID, &p.Number, &p.Status, &p.BuyerName, &p.BuyerPhone, &p.AgentName, &p.DateBooked) == nil {
					plots = append(plots, p)
				}
			}
		}
	} else {
		pRows, _ := db.Query(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id=? AND status=? ORDER BY id`, id, status)
		if pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p plotRow
				if pRows.Scan(&p.ID, &p.Number, &p.Status) == nil {
					plots = append(plots, p)
				}
			}
		}
	}

	renderAdmin(w, r, "admin_estate_plots.html", map[string]any{
		"Title":      estateName + " – Plots",
		"Active":     "estates",
		"EstateName": estateName,
		"EstateID":   id,
		"Status":     status,
		"Plots":      plots,
		"Counts":     counts,
	})
}

func adminUpdatePlotStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/estates", http.StatusFound)
		return
	}
	id := r.Context().Value(ctxID).(string)
	r.ParseMultipartForm(10 << 20)
	plotID := r.FormValue("plot_id")
	newStatus := r.FormValue("status")

	// Allow forward transitions and revert to available. booked -> sa_signed is
	// deliberately NOT allowed here anymore — that transition now only happens
	// via the Accounts + Legal review chain (or the system_admin-only force
	// override), not directly by an admin/agent.
	allowed := map[string]bool{"sold": true, "available": true}
	if !allowed[newStatus] || plotID == "" {
		redirectBack(w, r, "/admin/estate/"+id+"/plots?status=booked", "")
		return
	}

	// Verify plot belongs to this estate and is in a transitionable state
	var currentStatus, plotNumber, plotEstateName string
	var estateID int
	err := db.QueryRow(`SELECT p.status, p.estate_id, p.plot_number, e.name
		FROM prop_plots p JOIN prop_estates e ON e.id = p.estate_id
		WHERE p.id=? AND p.estate_id=?`, plotID, id).
		Scan(&currentStatus, &estateID, &plotNumber, &plotEstateName)
	if err != nil || (currentStatus != "booked" && currentStatus != "sa_signed" && currentStatus != "sold") {
		redirectBack(w, r, "/admin/estate/"+id+"/plots?status=booked", "")
		return
	}

	// Server-side permission check for the specific action
	uid := getUserID(r)
	var requiredFeature string
	switch {
	case currentStatus == "booked" && newStatus == "available":
		requiredFeature = "admin.booked_to_available"
	case currentStatus == "sa_signed" && newStatus == "sold":
		requiredFeature = "admin.signed_to_sold"
		// Permission check happens below; redirect after.
	case currentStatus == "sa_signed" && newStatus == "available":
		requiredFeature = "admin.signed_to_available"
	case currentStatus == "sold" && newStatus == "available":
		requiredFeature = "admin.sold_to_available"
	}
	if requiredFeature != "" && getRole(r) != roleSystemAdmin && !hasPermission(uid, requiredFeature, "read") {
		w.WriteHeader(http.StatusForbidden)
		render(w, "access_denied", nil)
		return
	}

	// sa_signed → sold requires document upload — hand off to dedicated page.
	if currentStatus == "sa_signed" && newStatus == "sold" {
		http.Redirect(w, r, "/admin/mark-sold/"+plotID+"?estate_id="+id, http.StatusFound)
		return
	}

	db.Exec(`UPDATE prop_plots SET status=? WHERE id=?`, newStatus, plotID)
	if plotIDInt, err2 := strconv.Atoi(plotID); err2 == nil {
		logPlotStatus(plotIDInt, plotNumber, plotEstateName, estateID, currentStatus, newStatus, getAgentName(r), "admin action")
	}

	// Sync booking/sales records
	if newStatus == "sold" {
		db.Exec(`UPDATE prop_bookings SET status='completed' WHERE plot_id=? AND status IN ('active','sa_signed')`, plotID)
		// Create a sales record from the booking if one doesn't exist
		var buyerName, buyerPhone, buyerEmail, agentName, paymentPlan, depositRef, idPhoto, kra, passportPhoto string
		var deposit float64
		db.QueryRow(`SELECT buyer_name, COALESCE(buyer_phone,''), COALESCE(buyer_email,''), COALESCE(agent_name,''), COALESCE(deposit,0), COALESCE(payment_plan,''), COALESCE(deposit_ref,''), COALESCE(id_photo,''), COALESCE(kra,''), COALESCE(passport_photo,'') FROM prop_bookings WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).
			Scan(&buyerName, &buyerPhone, &buyerEmail, &agentName, &deposit, &paymentPlan, &depositRef, &idPhoto, &kra, &passportPhoto)
		db.Exec(`INSERT IGNORE INTO prop_sales (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, amount, payment_plan, deposit_doc, id_doc, kra_doc, passport_photo) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			plotID, id, buyerName, buyerPhone, buyerEmail, agentName, deposit, paymentPlan, depositRef, idPhoto, kra, passportPhoto)
	} else if newStatus == "available" {
		// Fetch zoho_books_id before cancelling so we can void the estimate in Zoho Books
		var zohoBookID string
		db.QueryRow(`SELECT COALESCE(zoho_books_id,'') FROM prop_bookings WHERE plot_id=? AND status IN ('active','sa_signed','completed') ORDER BY id DESC LIMIT 1`, plotID).Scan(&zohoBookID)
		db.Exec(`UPDATE prop_bookings SET status='cancelled' WHERE plot_id=? AND status IN ('active','sa_signed','completed')`, plotID)
		db.Exec(`DELETE FROM prop_sales WHERE plot_id=?`, plotID)
		if zohoBookID != "" {
			go cancelBooksEstimate(zohoBookID)
		}
	}

	redirectStatus := newStatus
	if newStatus == "available" {
		redirectStatus = "available"
	}
	redirectBack(w, r, "/admin/estate/"+id+"/plots?status="+redirectStatus, "")
}

// adminForceSASignedHandler is a system_admin-only emergency override that
// jumps a booked plot straight to sa_signed, bypassing the Accounts + Legal
// review chain entirely. Deliberately a separate route and button (not the
// normal SA-signed flow, which now belongs only to Legal) so it can never be
// triggered by mistake, and always leaves a distinct, clearly-labeled log
// entry so the bypass is auditable.
func adminForceSASignedHandler(w http.ResponseWriter, r *http.Request) {
	plotID := strings.TrimPrefix(r.URL.Path, "/admin/force-sa-signed/")
	plotID = strings.Trim(plotID, "/")
	estateID := r.URL.Query().Get("estate_id")
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/estate/"+estateID+"/plots?status=booked", http.StatusFound)
		return
	}
	r.ParseMultipartForm(10 << 20)

	var currentStatus, plotNumber, plotEstateName string
	var plotEstateID int
	err := db.QueryRow(`SELECT p.status, p.estate_id, p.plot_number, e.name
		FROM prop_plots p JOIN prop_estates e ON e.id = p.estate_id
		WHERE p.id=?`, plotID).
		Scan(&currentStatus, &plotEstateID, &plotNumber, &plotEstateName)
	if err != nil {
		redirectBack(w, r, "/admin/estate/"+estateID+"/plots?status=booked", "")
		return
	}

	saleAgreement := saveUploadedFiles(r, "sale_agreement")
	if saleAgreement == "" {
		redirectBack(w, r, "/admin/estate/"+estateID+"/plots?status=booked&err=sale_agreement_required", "")
		return
	}
	installmentPage := strings.TrimSpace(r.FormValue("installment_page"))

	db.Exec(`UPDATE prop_plots SET status='sa_signed' WHERE id=?`, plotID)
	db.Exec(`UPDATE prop_bookings SET status='sa_signed', date_signed=NOW(), sale_agreement=?, installment_page=?
		WHERE plot_id=? AND status IN ('active','pending_accounts_review','pending_wakili_review')`,
		saleAgreement, installmentPage, plotID)

	plotIDInt, _ := strconv.Atoi(plotID)
	logPlotStatus(plotIDInt, plotNumber, plotEstateName, plotEstateID, currentStatus, "sa_signed", getAgentName(r), "SYSTEM ADMIN FORCE OVERRIDE — bypassed Accounts/Legal review")
	logBooking("SYSADMIN_FORCE_SA_SIGNED", getAgentName(r), "", plotNumber+" — "+plotEstateName, "forced sa_signed bypassing review chain")

	if plotIDInt != 0 {
		processSignedIntegrations(plotIDInt, plotNumber, plotEstateName)
	}

	redirectBack(w, r, "/admin/estate/"+estateID+"/plots?status=sa_signed", "")
}

// adminSkipAccountsHandler is a system_admin-only override for buyers who,
// by policy, are exempt from the deposit-threshold check that normally gates
// entry into Accounts review (e.g. an institutional buyer on a separately
// agreed payment arrangement). Pushes the booking straight into Legal's
// queue (pending_wakili_review, stage "drafting"), skipping Accounts
// entirely — but still requires all 4 KYC docs to already be uploaded
// (re-checked here server-side, never just trusted from the UI) since the
// exemption is for the payment threshold only, not identity verification.
// Requires a reason, stored in accounts_notes so Legal can see why this
// booking arrived without going through Accounts — the same field/UI
// surface Accounts' own notes normally use.
func adminSkipAccountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/booked-plots", http.StatusFound)
		return
	}
	bookingID := pathSegment("/admin/skip-accounts/", r.URL.Path)
	r.ParseForm()
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID, "err=A+reason+is+required")
		return
	}
	reviewer := getAgentName(r)

	var plotNumber, estateName string
	var depositRef, idPhoto, kra, passportPhoto string
	if err := db.QueryRow(`SELECT p.plot_number, e.name,
		COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''), COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id=b.plot_id
		JOIN prop_estates e ON e.id=b.estate_id
		WHERE b.id=? AND b.status IN ('active','pending_accounts_review')`, bookingID).
		Scan(&plotNumber, &estateName, &depositRef, &idPhoto, &kra, &passportPhoto); err != nil {
		redirectBack(w, r, "/admin/booked-plots", "", "err=Booking+not+found+or+already+past+Accounts")
		return
	}

	if !hasAllAttachments(bookingInfo{DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto}) {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID, "err=All+4+KYC+documents+must+be+uploaded+first")
		return
	}

	res, err := db.Exec(`UPDATE prop_bookings SET status='pending_wakili_review', legal_stage='drafting',
		accounts_notes=?, accounts_reviewed_by=?, accounts_reviewed_at=NOW()
		WHERE id=? AND status IN ('active','pending_accounts_review')`,
		"SYSTEM ADMIN OVERRIDE (deposit threshold waived — KYC docs confirmed complete): "+reason, reviewer, bookingID)
	if err != nil {
		log.Printf("admin skip-accounts: %v", err)
		redirectBack(w, r, "/admin/booked-plots", "", "err=Database+error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		redirectBack(w, r, "/admin/booked-plots", "", "err=Booking+already+reviewed")
		return
	}

	logBooking("SYSADMIN_SKIP_ACCOUNTS", reviewer, "", plotNumber+" — "+estateName,
		"bypassed Accounts deposit-threshold check (KYC docs confirmed complete), pushed straight to Legal: "+reason)

	// Zoho Books estimate is deliberately deferred until this exact moment —
	// system_admin explicitly skipping Accounts and pushing straight to
	// Legal — rather than at initial booking time.
	go createBooksRecordForBookingID(bookingID)

	// sales@/systemadmin@ are notified (without attachments) here too — this
	// path also results in "booking sent to Legal," just bypassing the
	// normal Accounts approval step.
	go func() {
		if err := sendAccountsApprovedEmail(bookingID); err != nil {
			log.Printf("admin skip-accounts: notification email error: %v", err)
		}
	}()
	// Buyer/agent SMS, per Settings -> Notifications — same "now with Legal" event.
	sendAccountsApprovedSMS(bookingID)
	// Assigned lawyer email + SMS — always fires, not gated by that setting.
	notifyLawyerOfNewCase(bookingID)

	redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID)
}

// adminRecheckAccountsHandler lets any admin manually trigger
// maybeAdvanceToAccountsReview for one booking right away, instead of
// waiting for the next 30-minute scheduler tick (sweepQualifiedBookings in
// scheduler.go covers the same check automatically) — for a booking showing
// "Not Yet Advanced" on Booked Plots. Not a bypass of any kind: it's the
// exact same qualification check every other trigger point already runs,
// just invoked on demand, so it's safe to expose to any admin (not just
// system_admin) and is a no-op if the booking doesn't actually qualify.
func adminRecheckAccountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/booked-plots", http.StatusFound)
		return
	}
	bookingIDStr := pathSegment("/admin/recheck-accounts/", r.URL.Path)
	bookingID, err := strconv.Atoi(bookingIDStr)
	if err != nil {
		redirectBack(w, r, "/admin/booked-plots", "", "err=Invalid+booking")
		return
	}
	advanced, err := maybeAdvanceToAccountsReview(bookingID)
	if err != nil {
		log.Printf("admin recheck-accounts: %v", err)
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingIDStr, "err=Database+error")
		return
	}
	if advanced {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingIDStr, "recheck=advanced")
		return
	}
	redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingIDStr, "err=Still+does+not+qualify+-+check+docs+and+deposit+threshold")
}

func adminMarkSoldHandler(w http.ResponseWriter, r *http.Request) {
	plotID := strings.TrimPrefix(r.URL.Path, "/admin/mark-sold/")
	plotID = strings.TrimSuffix(plotID, "/")
	estateID := r.URL.Query().Get("estate_id")
	if plotID == "" || estateID == "" {
		http.Redirect(w, r, "/admin/signed-plots", http.StatusFound)
		return
	}

	type markSoldInfo struct {
		PlotID               string
		EstateID             string
		PlotNumber           string
		EstateName           string
		BuyerName            string
		BuyerPhone           string
		BuyerEmail           string
		AgentName            string
		LetterOfConsent      string
		TransferForms        string
		TitleDeed            string
		LetterOfConsentFiles []string
		TransferFormsFiles   []string
		TitleDeedFiles       []string
		AllDocsPresent       bool
	}

	var info markSoldInfo
	info.PlotID = plotID
	info.EstateID = estateID
	err := db.QueryRow(`
		SELECT p.plot_number, e.name,
		       COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''), COALESCE(b.agent_name,'')
		FROM prop_plots p
		JOIN prop_estates e ON e.id = p.estate_id
		LEFT JOIN prop_bookings b ON b.plot_id = p.id AND b.status = 'sa_signed'
		WHERE p.id = ?
		ORDER BY b.id DESC LIMIT 1`, plotID).
		Scan(&info.PlotNumber, &info.EstateName, &info.BuyerName, &info.BuyerPhone, &info.BuyerEmail, &info.AgentName)
	if err != nil {
		http.Redirect(w, r, "/admin/signed-plots", http.StatusFound)
		return
	}

	// Helper to split comma-separated filenames
	splitSoldFiles := func(s string) []string {
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

	// Fetch already-saved docs from prop_sales staging row
	loadSoldDocs := func() {
		db.QueryRow(`SELECT COALESCE(letter_of_consent,''), COALESCE(transfer_forms,''), COALESCE(title_deed,'') FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).
			Scan(&info.LetterOfConsent, &info.TransferForms, &info.TitleDeed)
		info.LetterOfConsentFiles = splitSoldFiles(info.LetterOfConsent)
		info.TransferFormsFiles = splitSoldFiles(info.TransferForms)
		info.TitleDeedFiles = splitSoldFiles(info.TitleDeed)
		info.AllDocsPresent = info.LetterOfConsent != "" && info.TransferForms != "" && info.TitleDeed != ""
	}

	// Fetch buyer info from the booking (needed for upsert)
	fetchBuyerInfo := func() (buyerName, buyerPhone, buyerEmail, agentName, paymentPlan, depositRef, idPhoto, kra, passportPhoto string, deposit float64) {
		db.QueryRow(`SELECT buyer_name, COALESCE(buyer_phone,''), COALESCE(buyer_email,''), COALESCE(agent_name,''),
			COALESCE(deposit,0), COALESCE(payment_plan,''), COALESCE(deposit_ref,''), COALESCE(id_photo,''),
			COALESCE(kra,''), COALESCE(passport_photo,'')
			FROM prop_bookings WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).
			Scan(&buyerName, &buyerPhone, &buyerEmail, &agentName, &deposit, &paymentPlan, &depositRef, &idPhoto, &kra, &passportPhoto)
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")

		if action == "upload_doc" {
			r.ParseMultipartForm(32 << 20)
			docField := r.FormValue("doc_field")
			if docField != "letter_of_consent" && docField != "transfer_forms" && docField != "title_deed" {
				http.Error(w, "invalid doc field", http.StatusBadRequest)
				return
			}

			loadSoldDocs()
			newFiles := saveUploadedFiles(r, docField)
			if newFiles == "" {
				http.Redirect(w, r, r.URL.String(), http.StatusFound)
				return
			}

			// Append to existing
			loc := info.LetterOfConsent
			tf := info.TransferForms
			td := info.TitleDeed
			appendTo := func(existing, added string) string {
				if existing == "" {
					return added
				}
				return existing + "," + added
			}
			switch docField {
			case "letter_of_consent":
				loc = appendTo(loc, newFiles)
			case "transfer_forms":
				tf = appendTo(tf, newFiles)
			case "title_deed":
				td = appendTo(td, newFiles)
			}

			buyerName2, buyerPhone2, buyerEmail2, agentName2, paymentPlan, depositRef, idPhoto, kra, passportPhoto, deposit := fetchBuyerInfo()
			var existingSalesID int
			db.QueryRow(`SELECT id FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).Scan(&existingSalesID)
			if existingSalesID > 0 {
				db.Exec(`UPDATE prop_sales SET letter_of_consent=?, transfer_forms=?, title_deed=? WHERE id=?`, loc, tf, td, existingSalesID)
			} else {
				db.Exec(`INSERT INTO prop_sales
					(plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, amount, payment_plan,
					 deposit_doc, id_doc, kra_doc, passport_photo, letter_of_consent, transfer_forms, title_deed)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					plotID, estateID, buyerName2, buyerPhone2, buyerEmail2, agentName2, deposit, paymentPlan,
					depositRef, idPhoto, kra, passportPhoto, loc, tf, td)
			}

			http.Redirect(w, r, r.URL.String()+"&saved="+docField, http.StatusFound)
			return
		}

		if action == "mark_sold" {
			// Re-read docs to confirm all present
			var loc, tf, td string
			db.QueryRow(`SELECT COALESCE(letter_of_consent,''), COALESCE(transfer_forms,''), COALESCE(title_deed,'') FROM prop_sales WHERE plot_id=? ORDER BY id DESC LIMIT 1`, plotID).
				Scan(&loc, &tf, &td)
			if loc == "" || tf == "" || td == "" {
				http.Redirect(w, r, r.URL.String()+"&err=missing_docs", http.StatusFound)
				return
			}

			db.Exec(`UPDATE prop_plots SET status='sold' WHERE id=?`, plotID)
			db.Exec(`UPDATE prop_bookings SET status='completed' WHERE plot_id=? AND status='sa_signed'`, plotID)

			if plotIDInt, err2 := strconv.Atoi(plotID); err2 == nil {
				logPlotStatus(plotIDInt, info.PlotNumber, info.EstateName, 0, "sa_signed", "sold", getAgentName(r), "admin action")
				processSoldAttachments(plotIDInt, loc, tf, td)
			}

			buyerName2, buyerPhone2, buyerEmail2, agentName2, paymentPlan, _, _, _, _, deposit := fetchBuyerInfo()
			go func() {
				b := bookingInfo{
					PlotNumbers: []string{info.PlotNumber},
					EstateName:  info.EstateName,
					BuyerName:   buyerName2,
					BuyerPhone:  buyerPhone2,
					BuyerEmail:  buyerEmail2,
					AgentName:   agentName2,
					Deposit:     fmt.Sprintf("%.2f", deposit),
					PaymentPlan: paymentPlan,
				}
				if err := sendSoldEmail(b); err != nil {
					log.Printf("[sold-email] error: %v", err)
				}
			}()

			http.Redirect(w, r, "/admin/estate/"+estateID+"/plots?status=sold", http.StatusFound)
			return
		}
	}

	loadSoldDocs()
	savedField := r.URL.Query().Get("saved")
	errMsg := r.URL.Query().Get("err")
	renderAdmin(w, r, "admin_mark_sold.html", map[string]any{
		"Title":      "Mark as Sold — " + info.PlotNumber,
		"Info":       info,
		"SavedField": savedField,
		"ErrMsg":     errMsg,
	})
}

func adminEstateBookHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	agentName := getAgentName(r)

	type plotRow struct {
		ID         int
		Number     string
		EstateName string
	}

	fetchPlots := func(ids []string) ([]plotRow, string) {
		var rows []plotRow
		var estateName string
		for _, pid := range ids {
			var p plotRow
			if err := db.QueryRow(`SELECT pp.id, pp.plot_number, e.name FROM prop_plots pp JOIN prop_estates e ON e.id=pp.estate_id WHERE pp.id=? AND pp.status='available'`, pid).
				Scan(&p.ID, &p.Number, &p.EstateName); err == nil {
				rows = append(rows, p)
				estateName = p.EstateName
			}
		}
		return rows, estateName
	}

	renderBookForm := func(plots []plotRow, estateName, formErr string) {
		title := fmt.Sprintf("Book Plot %s", plots[0].Number)
		if len(plots) > 1 {
			title = fmt.Sprintf("Book %d Plots", len(plots))
		}
		renderAdmin(w, r, "admin_estate_book.html", map[string]any{
			"Title":      title,
			"Active":     "estates",
			"Plots":      plots,
			"EstateName": estateName,
			"EstateID":   id,
			"FormError":  formErr,
		})
	}

	if r.Method == http.MethodPost {
		r.ParseMultipartForm(32 << 20)
		plotIDs := r.Form["plot_ids"]
		buyerName := strings.TrimSpace(r.FormValue("buyer_name"))
		buyerPhone := strings.TrimSpace(r.FormValue("buyer_phone"))
		buyerEmail := r.FormValue("buyer_email")
		deposit := strings.TrimSpace(r.FormValue("deposit"))
		paymentPlan := r.FormValue("payment_plan")
		leadSource := r.FormValue("lead_source")
		notes := strings.TrimSpace(r.FormValue("notes"))
		careOf := strings.TrimSpace(r.FormValue("care_of"))
		rebookConfirmed := r.FormValue("rebook_confirmed") == "1"

		plots, estateName := fetchPlots(plotIDs)

		switch {
		case buyerName == "":
			renderBookForm(plots, estateName, "Buyer name is required.")
			return
		case buyerPhone == "":
			renderBookForm(plots, estateName, "Buyer phone number is required.")
			return
		case deposit == "" || deposit == "0":
			renderBookForm(plots, estateName, "Deposit amount is required.")
			return
		case leadSource == "":
			renderBookForm(plots, estateName, "Lead source is required.")
			return
		}

		hasDepositRef := false
		if r.MultipartForm != nil {
			for _, fh := range r.MultipartForm.File["deposit_ref"] {
				if fh.Size > 0 {
					hasDepositRef = true
					break
				}
			}
		}
		if !hasDepositRef {
			renderBookForm(plots, estateName, "Payment reference attachment is required.")
			return
		}

		if len(plots) == 0 {
			http.Redirect(w, r, "/admin/estate/"+id+"/plots?status=available&err=unavailable", http.StatusFound)
			return
		}

		depositRef := saveUploadedFiles(r, "deposit_ref")
		idPhoto := saveUploadedFiles(r, "id_photo")
		kra := saveUploadedFiles(r, "kra")
		passportPhoto := saveUploadedFiles(r, "passport_photo")

		var plotNumbers []string
		var bookedPlotIDs []int
		var newBookingIDs []int
		estateIDInt, _ := strconv.Atoi(id)
		for _, p := range plots {
			if res, err := db.Exec(`INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, deposit, payment_plan, deposit_ref, id_photo, kra, passport_photo, lead_source, notes, care_of, status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'active')`,
				p.ID, id, buyerName, buyerPhone, buyerEmail, agentName, deposit, paymentPlan, depositRef, idPhoto, kra, passportPhoto, leadSource, notes, careOf); err != nil {
				log.Printf("adminBook: booking insert failed for plot %d: %v", p.ID, err)
			} else if bid, err2 := res.LastInsertId(); err2 == nil {
				newBookingIDs = append(newBookingIDs, int(bid))
			}
			if _, err := db.Exec(`UPDATE prop_plots SET status='booked' WHERE id=?`, p.ID); err != nil {
				log.Printf("adminBook: plot status update failed for plot %d: %v", p.ID, err)
			}
			logPlotStatus(p.ID, p.Number, estateName, estateIDInt, "available", "booked", agentName, "booked")
			plotNumbers = append(plotNumbers, p.Number)
			bookedPlotIDs = append(bookedPlotIDs, p.ID)
		}
		log.Printf("adminBook: %d plots booked for %s by %s", len(plots), buyerName, agentName)
		logBooking("PLOT_BOOKED", agentName, buyerName,
			strings.Join(plotNumbers, ", ")+" — "+estateName,
			fmt.Sprintf("Deposit: KES %s | Plan: %s | Docs: %v", deposit, paymentPlan, hasAllAttachments(bookingInfo{DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto})))
		processBookingIntegrations(bookingInfo{
			PlotIDs: bookedPlotIDs, BuyerName: buyerName, BuyerPhone: buyerPhone, BuyerEmail: buyerEmail,
			PlotNumbers: plotNumbers, EstateName: estateName,
			Deposit: deposit, PaymentPlan: paymentPlan, AgentName: agentName,
			DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto,
			Notes: notes, RebookConfirmed: rebookConfirmed,
		})
		for _, bid := range newBookingIDs {
			go maybeAdvanceToAccountsReview(bid)
		}
		http.Redirect(w, r, "/admin/estate/"+id+"/plots?status=booked", http.StatusFound)
		return
	}

	// GET: ?plots=1,2,3
	plotIDStrs := strings.Split(r.URL.Query().Get("plots"), ",")
	plots, estateName := fetchPlots(plotIDStrs)
	if len(plots) == 0 {
		http.Redirect(w, r, "/admin/estate/"+id+"/plots?status=available", http.StatusFound)
		return
	}
	renderBookForm(plots, estateName, "")
}

// lawyerOption is one Legal-role user, selectable as the estate's assigned
// lawyer (each estate's sale agreements are handled by exactly one lawyer).
type lawyerOption struct {
	ID   int
	Name string
}

func loadLawyers() []lawyerOption {
	rows, err := db.Query(`SELECT id, name FROM prop_agents WHERE role='legal' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []lawyerOption
	for rows.Next() {
		var l lawyerOption
		if rows.Scan(&l.ID, &l.Name) == nil {
			out = append(out, l)
		}
	}
	return out
}

func adminAddEstateHandler(w http.ResponseWriter, r *http.Request) {
	var formErr string
	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "form error", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		plotInfo := r.FormValue("prop_plotinfo")
		plotPrice := r.FormValue("plot_price")
		depositThreshold := strings.TrimSpace(r.FormValue("deposit_threshold"))
		lawyerID := strings.TrimSpace(r.FormValue("lawyer_id"))
		if name == "" {
			formErr = "Estate name is required"
		} else if depositThreshold == "" {
			formErr = "Deposit threshold is required"
		} else if lawyerID == "" {
			formErr = "Assigned lawyer is required"
		} else {
			var mutationImage string
			file, header, ferr := r.FormFile("mutation_image")
			if ferr == nil {
				defer file.Close()
				ext := filepath.Ext(header.Filename)
				base := strings.TrimSuffix(header.Filename, ext)
				safe := strings.Map(func(r rune) rune {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
						return r
					}
					return '_'
				}, base)
				filename := fmt.Sprintf("mutation_%d_%s%s", time.Now().Unix(), safe, ext)
				dst, oerr := os.Create(filepath.Join(uploadsDir, filename))
				if oerr == nil {
					io.Copy(dst, file)
					dst.Close()
					mutationImage = filename
				}
			}
			var plotPriceVal interface{}
			if plotPrice != "" {
				plotPriceVal = plotPrice
			}
			isRestricted := 0
			if r.FormValue("visibility") == "hide" {
				isRestricted = 1
			}
			res, ierr := db.Exec(`INSERT INTO prop_estates (name, prop_plotinfo, mutation_image, plot_price, is_restricted, deposit_threshold, lawyer_id) VALUES (?,?,?,?,?,?,?)`,
				name, plotInfo, mutationImage, plotPriceVal, isRestricted, depositThreshold, lawyerID)
			if ierr != nil {
				log.Printf("add estate: %v", ierr)
				formErr = "Database error creating estate"
			} else {
				newID, _ := res.LastInsertId()
				http.Redirect(w, r, fmt.Sprintf("/admin/estate/%d/edit", newID), http.StatusFound)
				return
			}
		}
	}
	renderAdmin(w, r, "admin_add_estate.html", map[string]any{
		"Title":     "Add Estate",
		"Active":    "add-estate",
		"FormError": formErr,
		"Lawyers":   loadLawyers(),
	})
}
func adminAddPlotsHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, r, "admin_placeholder.html", map[string]any{"Title": "Add Plots", "Active": "add-plots", "Message": "Add Plots form (placeholder)."})
}

// User creation, editing, and password resets moved to settings.go under
// /settings/* — see settingsUsersHandler / settingsEditUserHandler.

// filterAgentsAndEstates loads dropdown data for the filter bar.
type filterOption struct {
	ID   string
	Name string
}

func loadFilterOptions() (agents []filterOption, estates []filterOption) {
	ar, err := db.Query(`SELECT DISTINCT COALESCE(agent_name,'') FROM prop_bookings WHERE agent_name != '' ORDER BY COALESCE(agent_name,'')`)
	if err != nil {
		log.Printf("loadFilterOptions: agent query error: %v", err)
	} else {
		defer ar.Close()
		for ar.Next() {
			var n string
			ar.Scan(&n)
			agents = append(agents, filterOption{ID: n, Name: n})
		}
	}
	er, err := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`)
	if err != nil {
		log.Printf("loadFilterOptions: estate query error: %v", err)
	} else {
		defer er.Close()
		for er.Next() {
			var id int
			var name string
			er.Scan(&id, &name)
			estates = append(estates, filterOption{ID: fmt.Sprintf("%d", id), Name: name})
		}
	}
	return
}

// docsCompleteSQL / thresholdMetSQL are the shared boolean expressions
// defining when a booking is "fully baked" — all 4 KYC docs present, and
// the deposit paid meets the estate's threshold (or the estate has no
// threshold set, matching maybeAdvanceToAccountsReview's own definition of
// "qualifies for Accounts review"). Used in both SELECT and WHERE below, so
// kept as constants to avoid the two drifting apart.
const (
	docsCompleteSQL = `(COALESCE(b.deposit_ref,'')!='' AND COALESCE(b.id_photo,'')!='' AND COALESCE(b.kra,'')!='' AND COALESCE(b.passport_photo,'')!='')`
	thresholdMetSQL = `(e.deposit_threshold IS NULL OR COALESCE(b.deposit,0) >= e.deposit_threshold)`
	fullyBakedSQL   = "(" + docsCompleteSQL + " AND " + thresholdMetSQL + ")"
)

// bookingStageLabel names a booking's review stage once it's past 'active'
// — "Accounts Stage", "Legal Stage — Drafting", or "Legal Stage — Awaiting
// Signature" — shared between the admin Booked Plots page and the agent's
// My Bookings page so the two never describe the same status differently.
// Returns "" for 'active' or anything else, since what that should say
// depends on the caller's own context (admin distinguishes "not yet swept"
// from plain "active"; agent just shows "Active").
func bookingStageLabel(status, legalStage string) string {
	switch {
	case status == "pending_accounts_review":
		return "Accounts Stage"
	case status == "pending_wakili_review" && legalStage == "awaiting_signature":
		return "Legal Stage — Awaiting Signature"
	case status == "pending_wakili_review":
		return "Legal Stage — Drafting"
	default:
		return ""
	}
}

func adminBookedPlotsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fAgent := q.Get("agent")
	fEstate := q.Get("estate")
	fFrom := q.Get("date_from")
	fTo := q.Get("date_to")
	fLawyer := q.Get("lawyer")
	fReadiness := q.Get("readiness") // "baked", "unbaked", or "" for all

	query := `
		SELECT b.id, p.id, e.id, b.buyer_name, COALESCE(b.buyer_phone,''), e.name, p.plot_number,
			COALESCE(b.agent_name,''), DATE_FORMAT(b.date_booked,'%d %b %Y %H:%i'), b.status,
			DATEDIFF(NOW(), b.date_booked),
			COALESCE(DATE_FORMAT(b.booking_deadline,'%d %b %Y'),
			         DATE_FORMAT(DATE_ADD(b.date_booked, INTERVAL 14 DAY),'%d %b %Y')),
			DATEDIFF(COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)), NOW()),
			COALESCE(b.zoho_books_id,''), COALESCE(b.batch_ref,''), COALESCE(lw.name,''),
			` + docsCompleteSQL + `, ` + thresholdMetSQL + `, b.legal_stage
		FROM prop_bookings b
		JOIN prop_estates e ON b.estate_id = e.id
		JOIN prop_plots p ON b.plot_id = p.id
		LEFT JOIN prop_agents lw ON lw.id = e.lawyer_id
		JOIN (
			SELECT plot_id, MAX(id) AS latest_id
			FROM prop_bookings
			GROUP BY plot_id
		) latest ON b.id = latest.latest_id
		WHERE p.status = 'booked'`
	var args []any
	if fAgent != "" {
		query += " AND b.agent_name = ?"
		args = append(args, fAgent)
	}
	if fEstate != "" {
		query += " AND e.id = ?"
		args = append(args, fEstate)
	}
	if fFrom != "" {
		query += " AND DATE(b.date_booked) >= ?"
		args = append(args, fFrom)
	}
	if fTo != "" {
		query += " AND DATE(b.date_booked) <= ?"
		args = append(args, fTo)
	}
	if fLawyer != "" {
		query += " AND e.lawyer_id = ?"
		args = append(args, fLawyer)
	}
	if fReadiness == "baked" {
		query += " AND " + fullyBakedSQL
	} else if fReadiness == "unbaked" {
		query += " AND NOT " + fullyBakedSQL
	}
	query += " ORDER BY b.date_booked DESC"

	type bookingRow struct {
		BookingID     int
		PlotID        int
		EstateID      int
		BuyerName     string
		BuyerPhone    string
		EstateName    string
		PlotNumber    string
		AgentName     string
		DateBooked    string
		Status        string
		DaysBooked    int
		Deadline      string
		DaysRemaining int
		ZohoBooksID   string
		BatchRef      string
		LawyerName    string
		DocsComplete  bool
		ThresholdMet  bool
		LegalStage    string
		StageLabel    string // human-readable review stage, for "fully baked" rows
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("booked plots query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var bookings []bookingRow
	for rows.Next() {
		var b bookingRow
		if err := rows.Scan(&b.BookingID, &b.PlotID, &b.EstateID, &b.BuyerName, &b.BuyerPhone, &b.EstateName, &b.PlotNumber, &b.AgentName, &b.DateBooked, &b.Status, &b.DaysBooked, &b.Deadline, &b.DaysRemaining, &b.ZohoBooksID, &b.BatchRef, &b.LawyerName, &b.DocsComplete, &b.ThresholdMet, &b.LegalStage); err != nil {
			log.Printf("booked scan: %v", err)
			continue
		}
		if b.Status == "active" {
			// Qualifies (docs + deposit both check out) but hasn't been swept
			// into Accounts yet — worth flagging distinctly since it means
			// maybeAdvanceToAccountsReview hasn't fired for this booking yet.
			b.StageLabel = "Not Yet Advanced"
		} else if label := bookingStageLabel(b.Status, b.LegalStage); label != "" {
			b.StageLabel = label
		} else {
			b.StageLabel = b.Status
		}
		bookings = append(bookings, b)
	}
	agents, estates := loadFilterOptions()
	uid := getUserID(r)
	isSA := getRole(r) == roleSystemAdmin
	renderAdmin(w, r, "admin_booked_plots.html", map[string]any{
		"Title":                "Booked Plots",
		"Active":               "booked-plots",
		"Bookings":             bookings,
		"Agents":               agents,
		"Estates":              estates,
		"Lawyers":              loadLawyers(),
		"Error":                r.URL.Query().Get("err"),
		"FAgent":               fAgent,
		"FEstate":              fEstate,
		"FFrom":                fFrom,
		"FTo":                  fTo,
		"FLawyer":              fLawyer,
		"FReadiness":           fReadiness,
		"CanBookedToAvailable": isSA || hasPermission(uid, "admin.booked_to_available", "read"),
		"CanExtendBooking":     isSA || hasPermission(uid, "admin.extend_booking", "read"),
		"Success":              r.URL.Query().Get("extended"),
		"ZohoRetry":            r.URL.Query().Get("zoho_retry"),
		"Recheck":              r.URL.Query().Get("recheck"),
	})
}

// adminRetryZohoHandler manually retries Zoho Books estimate creation for one
// booking (e.g. the first attempt errored). Refuses to run for a booking
// that hasn't been approved by Accounts (or skipped straight to Legal) yet —
// no Books record is expected to exist before then, so there's nothing to
// "retry" (see createBooksRecordForBookingID and where it's actually
// triggered: accounts.approveHandler, adminSkipAccountsHandler).
func adminRetryZohoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bookingID := pathSegment("/admin/booking-retry-zoho/", r.URL.Path)
	if bookingID == "" {
		http.NotFound(w, r)
		return
	}

	var status, zohoBookID string
	if err := db.QueryRow(`SELECT status, COALESCE(zoho_books_id,'') FROM prop_bookings WHERE id=?`, bookingID).
		Scan(&status, &zohoBookID); err != nil {
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	}
	if zohoBookID != "" {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID)
		return
	}
	if status == "active" || status == "pending_accounts_review" {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID,
			"err=No+Books+record+expected+yet+%E2%80%94+this+booking+hasn%27t+been+approved+by+Accounts")
		return
	}
	go createBooksRecordForBookingID(bookingID)
	redirectBack(w, r, "/admin/booked-plots?zoho_retry=1", "booking-"+bookingID, "zoho_retry=1")
}

// adminRetryZohoSignedHandler retries Zoho Books creation for SA Signed plots
// where zoho_books_id is NULL (e.g. old bookings predating the integration).
func adminRetryZohoSignedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bookingID := pathSegment("/admin/signed-booking-retry-zoho/", r.URL.Path)
	if bookingID == "" {
		http.NotFound(w, r)
		return
	}

	var plotID int
	var deposit float64
	var zohoBookID string
	var b bookingInfo
	var plotNumber string
	err := db.QueryRow(`
		SELECT p.id, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       COALESCE(b.agent_name,''), COALESCE(b.deposit,0), COALESCE(b.payment_plan,''),
		       p.plot_number, e.name, COALESCE(b.zoho_books_id,'')
		FROM prop_bookings b
		JOIN prop_plots p ON b.plot_id = p.id
		JOIN prop_estates e ON b.estate_id = e.id
		WHERE b.id = ? AND b.status = 'sa_signed'`, bookingID).Scan(
		&plotID, &b.BuyerName, &b.BuyerPhone, &b.BuyerEmail,
		&b.AgentName, &deposit, &b.PaymentPlan,
		&plotNumber, &b.EstateName, &zohoBookID)
	if err != nil {
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	}
	if zohoBookID != "" {
		redirectBack(w, r, "/admin/signed-plots", "booking-"+bookingID)
		return
	}
	b.PlotIDs = []int{plotID}
	b.PlotNumbers = []string{plotNumber}
	b.Deposit = fmt.Sprintf("%.0f", deposit)

	go func() {
		booksID, err := createBooksRecord(b)
		if err != nil {
			log.Printf("[zoho-books] signed retry error for %s plot %s: %v", b.BuyerName, plotNumber, err)
			return
		}
		db.Exec(`UPDATE prop_bookings SET zoho_books_id=? WHERE id=? AND zoho_books_id IS NULL`, booksID, bookingID)
		log.Printf("[zoho-books] signed retry stored estimate %s for booking %s", booksID, bookingID)
	}()

	redirectBack(w, r, "/admin/signed-plots?zoho_retry=1", "booking-"+bookingID, "zoho_retry=1")
}

// extendBookingHandler handles POST /admin/booking-extend/{id}
// It adds the requested number of days to the booking deadline.
func extendBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bookingID := pathSegment("/admin/booking-extend/", r.URL.Path)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("days")))
	if days <= 0 {
		redirectBack(w, r, "/admin/booked-plots", "booking-"+bookingID)
		return
	}
	// Extend: move deadline forward by N days from current deadline (or date_booked+14 if none set)
	db.Exec(`
		UPDATE prop_bookings
		SET booking_deadline = DATE_ADD(
			COALESCE(booking_deadline, DATE_ADD(date_booked, INTERVAL 14 DAY)),
			INTERVAL ? DAY
		)
		WHERE id = ?`, days, bookingID)

	redirectBack(w, r, "/admin/booked-plots?extended=1", "booking-"+bookingID, "extended=1")
}

func adminSignedPlotsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fAgent := q.Get("agent")
	fEstate := q.Get("estate")
	fFrom := q.Get("date_from")
	fTo := q.Get("date_to")

	query := `
		SELECT b.id, p.id, b.estate_id, b.buyer_name, COALESCE(b.buyer_phone,''), e.name, p.plot_number,
			COALESCE(b.agent_name,''), DATE_FORMAT(b.date_booked,'%d %b %Y'),
			COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'),'—'),
			COALESCE(b.zoho_books_id,'')
		FROM prop_bookings b
		JOIN prop_estates e ON b.estate_id = e.id
		JOIN prop_plots p ON b.plot_id = p.id
		JOIN (
			SELECT plot_id, MAX(id) AS latest_id
			FROM prop_bookings
			GROUP BY plot_id
		) latest ON b.id = latest.latest_id
		WHERE p.status = 'sa_signed'`
	var args []any
	if fAgent != "" {
		query += " AND b.agent_name = ?"
		args = append(args, fAgent)
	}
	if fEstate != "" {
		query += " AND b.estate_id = ?"
		args = append(args, fEstate)
	}
	if fFrom != "" {
		query += " AND DATE(b.date_signed) >= ?"
		args = append(args, fFrom)
	}
	if fTo != "" {
		query += " AND DATE(b.date_signed) <= ?"
		args = append(args, fTo)
	}
	query += " ORDER BY b.date_signed DESC"

	type bookingRow struct {
		BookingID   int
		PlotID      int
		EstateID    int
		BuyerName   string
		BuyerPhone  string
		EstateName  string
		PlotNumber  string
		AgentName   string
		DateBooked  string
		DateSigned  string
		ZohoBooksID string
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("signed plots query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var bookings []bookingRow
	for rows.Next() {
		var b bookingRow
		if err := rows.Scan(&b.BookingID, &b.PlotID, &b.EstateID, &b.BuyerName, &b.BuyerPhone, &b.EstateName, &b.PlotNumber, &b.AgentName, &b.DateBooked, &b.DateSigned, &b.ZohoBooksID); err != nil {
			log.Printf("signed scan: %v", err)
			continue
		}
		bookings = append(bookings, b)
	}
	agents, estates := loadFilterOptions()
	uid2 := getUserID(r)
	isSA2 := getRole(r) == roleSystemAdmin
	renderAdmin(w, r, "admin_signed_plots.html", map[string]any{
		"Title":                "Signed Plots",
		"Active":               "signed-plots",
		"Bookings":             bookings,
		"Agents":               agents,
		"Estates":              estates,
		"FAgent":               fAgent,
		"FEstate":              fEstate,
		"FFrom":                fFrom,
		"FTo":                  fTo,
		"CanSignedToSold":      isSA2 || hasPermission(uid2, "admin.signed_to_sold", "read"),
		"CanSignedToAvailable": isSA2 || hasPermission(uid2, "admin.signed_to_available", "read"),
		"CanRetryZoho":         isSA2,
		"Success":              r.URL.Query().Get("zoho_retry"),
	})
}

func adminSoldPlotsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fAgent := q.Get("agent")
	fEstate := q.Get("estate")
	fFrom := q.Get("date_from")
	fTo := q.Get("date_to")

	query := `
		SELECT s.plot_id, s.estate_id, s.buyer_name, COALESCE(s.buyer_phone,''), e.name, p.plot_number,
			COALESCE(s.agent_name,''), COALESCE(s.amount,0),
			COALESCE(s.payment_plan,''), DATE_FORMAT(s.date_sold,'%d %b %Y')
		FROM prop_sales s
		JOIN prop_estates e ON s.estate_id = e.id
		JOIN prop_plots p ON s.plot_id = p.id
		WHERE 1=1`
	var args []any
	if fAgent != "" {
		query += " AND s.agent_name = ?"
		args = append(args, fAgent)
	}
	if fEstate != "" {
		query += " AND s.estate_id = ?"
		args = append(args, fEstate)
	}
	if fFrom != "" {
		query += " AND DATE(s.date_sold) >= ?"
		args = append(args, fFrom)
	}
	if fTo != "" {
		query += " AND DATE(s.date_sold) <= ?"
		args = append(args, fTo)
	}
	query += " ORDER BY s.date_sold DESC"

	type saleRow struct {
		PlotID      int
		EstateID    int
		BuyerName   string
		BuyerPhone  string
		EstateName  string
		PlotNumber  string
		AgentName   string
		Amount      float64
		PaymentPlan string
		DateSold    string
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("sold plots query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var sales []saleRow
	for rows.Next() {
		var s saleRow
		if err := rows.Scan(&s.PlotID, &s.EstateID, &s.BuyerName, &s.BuyerPhone, &s.EstateName, &s.PlotNumber, &s.AgentName, &s.Amount, &s.PaymentPlan, &s.DateSold); err != nil {
			log.Printf("sold scan: %v", err)
			continue
		}
		sales = append(sales, s)
	}
	agents, estates := loadFilterOptions()
	uid3 := getUserID(r)
	isSA3 := getRole(r) == roleSystemAdmin
	renderAdmin(w, r, "admin_sold_plots.html", map[string]any{
		"Title":              "Sold Plots",
		"Active":             "sold-plots",
		"Sales":              sales,
		"Agents":             agents,
		"Estates":            estates,
		"FAgent":             fAgent,
		"FEstate":            fEstate,
		"FFrom":              fFrom,
		"FTo":                fTo,
		"CanSoldToAvailable": isSA3 || hasPermission(uid3, "admin.sold_to_available", "read"),
	})
}
func adminPlotsOverviewHandler(w http.ResponseWriter, r *http.Request) {
	// AJAX search endpoint
	if r.URL.Query().Get("api") == "1" {
		adminPlotsOverviewSearchHandler(w, r)
		return
	}

	// Count totals for tab badges. Counted from prop_plots.status (the
	// authoritative current state of the plot) rather than
	// prop_bookings.status — a plot's most recent booking can be sitting in
	// any of active/pending_accounts_review/pending_wakili_review while
	// still plainly "booked", and a stale, superseded prop_bookings row
	// left over from an earlier booking attempt on the same plot must not
	// be counted once the plot itself has moved on (see the matching fix
	// in adminPlotsOverviewSearchHandler below).
	var cntBooked, cntSigned, cntSold int
	db.QueryRow(`SELECT COUNT(*) FROM prop_plots WHERE status='booked'`).Scan(&cntBooked)
	db.QueryRow(`SELECT COUNT(*) FROM prop_plots WHERE status='sa_signed'`).Scan(&cntSigned)
	db.QueryRow(`SELECT COUNT(*) FROM prop_sales`).Scan(&cntSold)

	renderAdmin(w, r, "admin_plots_overview.html", map[string]any{
		"Title":     "Plots Overview",
		"Active":    "plots-overview",
		"CntBooked": cntBooked,
		"CntSigned": cntSigned,
		"CntSold":   cntSold,
	})
}

func adminPlotsOverviewSearchHandler(w http.ResponseWriter, r *http.Request) {
	type plotEntry struct {
		ID            int
		PlotNumber    string
		EstateName    string
		AgentName     string
		BuyerName     string
		BuyerPhone    string
		BuyerEmail    string
		DateBooked    string
		DepositRef    string
		IDPhoto       string
		KRA           string
		Passport      string
		SaleAgree     string
		LetterConsent string
		TransferForms string
		TitleDeed     string
	}

	tab := r.URL.Query().Get("tab")
	rawQ := strings.TrimSpace(r.URL.Query().Get("q"))
	hasSearch := rawQ != ""
	q := "%" + rawQ + "%"

	w.Header().Set("Content-Type", "application/json")

	var out []plotEntry

	queryAndScan := func(query string, args ...any) {
		rows, err := db.Query(query, args...)
		if err != nil {
			log.Printf("plots-overview query error (tab=%s): %v", tab, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var pe plotEntry
			switch tab {
			case "signed":
				rows.Scan(&pe.ID, &pe.PlotNumber, &pe.EstateName, &pe.AgentName, &pe.BuyerName,
					&pe.BuyerPhone, &pe.BuyerEmail, &pe.DateBooked,
					&pe.DepositRef, &pe.IDPhoto, &pe.KRA, &pe.Passport, &pe.SaleAgree,
					&pe.LetterConsent, &pe.TransferForms, &pe.TitleDeed)
			case "sold":
				rows.Scan(&pe.ID, &pe.PlotNumber, &pe.EstateName, &pe.AgentName, &pe.BuyerName,
					&pe.BuyerPhone, &pe.BuyerEmail, &pe.DateBooked,
					&pe.DepositRef, &pe.IDPhoto, &pe.KRA, &pe.Passport,
					&pe.LetterConsent, &pe.TransferForms, &pe.TitleDeed)
			default:
				rows.Scan(&pe.ID, &pe.PlotNumber, &pe.EstateName, &pe.AgentName, &pe.BuyerName,
					&pe.BuyerPhone, &pe.BuyerEmail, &pe.DateBooked,
					&pe.DepositRef, &pe.IDPhoto, &pe.KRA, &pe.Passport)
			}
			out = append(out, pe)
		}
	}

	switch tab {
	case "signed":
		// LEFT JOINs from prop_plots (not an INNER JOIN starting from
		// prop_bookings) so a plot whose status is sa_signed but which has
		// no non-cancelled/expired booking row to match — common for
		// older/imported records — still shows here instead of silently
		// vanishing while still counted in the tab's badge total.
		if hasSearch {
			queryAndScan(`
				SELECT COALESCE(b.id,0), p.plot_number, e.name,
				       COALESCE(b.agent_name,''), COALESCE(b.buyer_name,''),
				       COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
				       COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'),''),
				       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
				       COALESCE(b.kra,''), COALESCE(b.passport_photo,''),
				       COALESCE(b.sale_agreement,''),
				       COALESCE(dl.consent_file,''), COALESCE(dl.transfer_file,''), COALESCE(dl.title_file,'')
				FROM prop_plots p
				JOIN prop_estates e ON e.id = p.estate_id
				LEFT JOIN (
					SELECT plot_id, MAX(id) AS latest_id
					FROM prop_bookings
					GROUP BY plot_id
				) latest ON latest.plot_id = p.id
				LEFT JOIN prop_bookings b ON b.id = latest.latest_id
				LEFT JOIN (
					SELECT * FROM (
						SELECT d.*, ROW_NUMBER() OVER (
							PARTITION BY estate, plot ORDER BY last_synced_at DESC, deal_id DESC
						) AS rn
						FROM prop_payment_plan_deals d WHERE sold_at IS NULL
					) ranked WHERE rn = 1
				) dl ON dl.estate COLLATE utf8mb4_general_ci = e.name COLLATE utf8mb4_general_ci AND TRIM(dl.plot) COLLATE utf8mb4_general_ci = p.plot_number COLLATE utf8mb4_general_ci
				WHERE p.status = 'sa_signed'
				  AND (e.name LIKE ? OR p.plot_number LIKE ? OR COALESCE(b.agent_name,'') LIKE ? OR COALESCE(b.buyer_name,'') LIKE ?)
				ORDER BY (b.deposit_ref IS NOT NULL AND b.deposit_ref != '') DESC, b.date_signed DESC`, q, q, q, q)
		} else {
			queryAndScan(`
				SELECT COALESCE(b.id,0), p.plot_number, e.name,
				       COALESCE(b.agent_name,''), COALESCE(b.buyer_name,''),
				       COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
				       COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'),''),
				       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
				       COALESCE(b.kra,''), COALESCE(b.passport_photo,''),
				       COALESCE(b.sale_agreement,''),
				       COALESCE(dl.consent_file,''), COALESCE(dl.transfer_file,''), COALESCE(dl.title_file,'')
				FROM prop_plots p
				JOIN prop_estates e ON e.id = p.estate_id
				LEFT JOIN (
					SELECT plot_id, MAX(id) AS latest_id
					FROM prop_bookings
					GROUP BY plot_id
				) latest ON latest.plot_id = p.id
				LEFT JOIN prop_bookings b ON b.id = latest.latest_id
				LEFT JOIN (
					SELECT * FROM (
						SELECT d.*, ROW_NUMBER() OVER (
							PARTITION BY estate, plot ORDER BY last_synced_at DESC, deal_id DESC
						) AS rn
						FROM prop_payment_plan_deals d WHERE sold_at IS NULL
					) ranked WHERE rn = 1
				) dl ON dl.estate COLLATE utf8mb4_general_ci = e.name COLLATE utf8mb4_general_ci AND TRIM(dl.plot) COLLATE utf8mb4_general_ci = p.plot_number COLLATE utf8mb4_general_ci
				WHERE p.status = 'sa_signed'
				ORDER BY (b.deposit_ref IS NOT NULL AND b.deposit_ref != '') DESC, b.date_signed DESC`)
		}
	case "sold":
		if hasSearch {
			queryAndScan(`
				SELECT s.id, p.plot_number, e.name,
				       COALESCE(s.agent_name,''), COALESCE(s.buyer_name,''),
				       COALESCE(s.buyer_phone,''), COALESCE(s.buyer_email,''),
				       COALESCE(DATE_FORMAT(s.date_sold,'%d %b %Y'),''),
				       COALESCE(s.deposit_doc,''), COALESCE(s.id_doc,''),
				       COALESCE(s.kra_doc,''), COALESCE(s.passport_photo,''),
				       COALESCE(s.letter_of_consent,''), COALESCE(s.transfer_forms,''),
				       COALESCE(s.title_deed,'')
				FROM prop_sales s
				JOIN prop_plots p ON p.id = s.plot_id
				JOIN prop_estates e ON e.id = s.estate_id
				WHERE (e.name LIKE ? OR p.plot_number LIKE ? OR s.agent_name LIKE ? OR s.buyer_name LIKE ?)
				ORDER BY s.date_sold DESC`, q, q, q, q)
		} else {
			queryAndScan(`
				SELECT s.id, p.plot_number, e.name,
				       COALESCE(s.agent_name,''), COALESCE(s.buyer_name,''),
				       COALESCE(s.buyer_phone,''), COALESCE(s.buyer_email,''),
				       COALESCE(DATE_FORMAT(s.date_sold,'%d %b %Y'),''),
				       COALESCE(s.deposit_doc,''), COALESCE(s.id_doc,''),
				       COALESCE(s.kra_doc,''), COALESCE(s.passport_photo,''),
				       COALESCE(s.letter_of_consent,''), COALESCE(s.transfer_forms,''),
				       COALESCE(s.title_deed,'')
				FROM prop_sales s
				JOIN prop_plots p ON p.id = s.plot_id
				JOIN prop_estates e ON e.id = s.estate_id
				ORDER BY s.date_sold DESC`)
		}
	default: // booked
		// Filters on p.status (the plot's actual current status) rather
		// than b.status='active' — a booking still legitimately "booked"
		// can be sitting in pending_accounts_review or
		// pending_wakili_review, not just 'active', and the latest-booking
		// join keeps a stale, superseded prop_bookings row (e.g. from an
		// earlier booking attempt on a plot that has since been re-booked,
		// signed, or sold through a later row) from being picked up instead
		// of the plot's actual current booking. Mirrors the same join used
		// by adminBookedPlotsHandler (the Booked Plots page).
		// LEFT JOINs from prop_plots (not an INNER JOIN starting from
		// prop_bookings) so a plot whose status is booked but which has no
		// non-cancelled/expired booking row to match — common for
		// older/imported records — still shows here instead of silently
		// vanishing while still counted in the tab's badge total.
		if hasSearch {
			queryAndScan(`
				SELECT COALESCE(b.id,0), p.plot_number, e.name,
				       COALESCE(b.agent_name,''), COALESCE(b.buyer_name,''),
				       COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
				       COALESCE(DATE_FORMAT(b.date_booked,'%d %b %Y'),''),
				       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
				       COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
				FROM prop_plots p
				JOIN prop_estates e ON e.id = p.estate_id
				LEFT JOIN (
					SELECT plot_id, MAX(id) AS latest_id
					FROM prop_bookings
					GROUP BY plot_id
				) latest ON latest.plot_id = p.id
				LEFT JOIN prop_bookings b ON b.id = latest.latest_id
				WHERE p.status = 'booked'
				  AND (e.name LIKE ? OR p.plot_number LIKE ? OR COALESCE(b.agent_name,'') LIKE ? OR COALESCE(b.buyer_name,'') LIKE ?)
				ORDER BY (b.deposit_ref IS NOT NULL AND b.deposit_ref != '') DESC, b.date_booked DESC`, q, q, q, q)
		} else {
			queryAndScan(`
				SELECT COALESCE(b.id,0), p.plot_number, e.name,
				       COALESCE(b.agent_name,''), COALESCE(b.buyer_name,''),
				       COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
				       COALESCE(DATE_FORMAT(b.date_booked,'%d %b %Y'),''),
				       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
				       COALESCE(b.kra,''), COALESCE(b.passport_photo,'')
				FROM prop_plots p
				JOIN prop_estates e ON e.id = p.estate_id
				LEFT JOIN (
					SELECT plot_id, MAX(id) AS latest_id
					FROM prop_bookings
					GROUP BY plot_id
				) latest ON latest.plot_id = p.id
				LEFT JOIN prop_bookings b ON b.id = latest.latest_id
				WHERE p.status = 'booked'
				ORDER BY (b.deposit_ref IS NOT NULL AND b.deposit_ref != '') DESC, b.date_booked DESC`)
		}
	}

	if out == nil {
		out = []plotEntry{}
	}
	json.NewEncoder(w).Encode(out)
}

// docFieldColumn maps a Plots Overview (tab, field) pair to the actual
// table/column it lives in — prop_bookings for booked/signed, prop_sales for
// sold (which uses different column names for the same documents). Column
// names can't be parameterized in SQL, so this whitelist is what keeps the
// delete/add-attachment endpoints below safe from arbitrary column access.
func docFieldColumn(tab, field string) (table, column string, ok bool) {
	switch tab {
	case "booked", "signed":
		switch field {
		case "deposit_ref":
			return "prop_bookings", "deposit_ref", true
		case "id_photo":
			return "prop_bookings", "id_photo", true
		case "kra":
			return "prop_bookings", "kra", true
		case "passport_photo":
			return "prop_bookings", "passport_photo", true
		case "sale_agreement":
			if tab == "signed" {
				return "prop_bookings", "sale_agreement", true
			}
		}
	case "sold":
		switch field {
		case "deposit_ref":
			return "prop_sales", "deposit_doc", true
		case "id_photo":
			return "prop_sales", "id_doc", true
		case "kra":
			return "prop_sales", "kra_doc", true
		case "passport_photo":
			return "prop_sales", "passport_photo", true
		case "letter_of_consent":
			return "prop_sales", "letter_of_consent", true
		case "transfer_forms":
			return "prop_sales", "transfer_forms", true
		case "title_deed":
			return "prop_sales", "title_deed", true
		}
	}
	return "", "", false
}

// adminPlotsOverviewDeleteAttachmentHandler removes a single filename from a
// (possibly multi-file, comma-separated) document field and deletes the file
// from disk — for correcting a wrongly-uploaded document from the Plots
// Overview detail view.
func adminPlotsOverviewDeleteAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tab := r.FormValue("tab")
	id := r.FormValue("id")
	field := r.FormValue("field")
	filename := strings.TrimSpace(r.FormValue("filename"))
	table, column, ok := docFieldColumn(tab, field)
	if !ok || id == "" || filename == "" {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid request"})
		return
	}

	var current string
	if err := db.QueryRow(fmt.Sprintf("SELECT COALESCE(%s,'') FROM %s WHERE id=?", column, table), id).Scan(&current); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "record not found"})
		return
	}

	var remaining []string
	found := false
	for _, f := range strings.Split(current, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f == filename {
			found = true
			continue
		}
		remaining = append(remaining, f)
	}
	if !found {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file not found on this record"})
		return
	}

	if _, err := db.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE id=?", table, column), strings.Join(remaining, ","), id); err != nil {
		log.Printf("delete-attachment: update failed: %v", err)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "database update failed"})
		return
	}

	if err := os.Remove(filepath.Join(uploadsDir, filename)); err != nil && !os.IsNotExist(err) {
		log.Printf("delete-attachment: file remove warning for %s: %v", filename, err)
	}

	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// adminPlotsOverviewAddAttachmentHandler appends a newly uploaded file to a
// document field and, if the record has a zoho_crm_id, pushes the file to
// that Zoho CRM deal as an attachment.
func adminPlotsOverviewAddAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(32 << 20)
	tab := r.FormValue("tab")
	id := r.FormValue("id")
	field := r.FormValue("field")
	table, column, ok := docFieldColumn(tab, field)
	if !ok || id == "" {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid request"})
		return
	}

	newFiles := saveUploadedFiles(r, "file")
	if newFiles == "" {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no file uploaded"})
		return
	}

	var current string
	db.QueryRow(fmt.Sprintf("SELECT COALESCE(%s,'') FROM %s WHERE id=?", column, table), id).Scan(&current)

	merged := newFiles
	if current != "" {
		merged = current + "," + newFiles
	}
	if _, err := db.Exec(fmt.Sprintf("UPDATE %s SET %s=? WHERE id=?", table, column), merged, id); err != nil {
		log.Printf("add-attachment: update failed: %v", err)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "database update failed"})
		return
	}

	// Push new file(s) to the Zoho CRM deal if one exists for this record.
	var zohoQueued bool
	var crmID string
	switch tab {
	case "booked", "signed":
		db.QueryRow(`SELECT COALESCE(zoho_crm_id,'') FROM prop_bookings WHERE id=?`, id).Scan(&crmID)
	case "sold":
		db.QueryRow(`SELECT COALESCE(zoho_crm_id,'') FROM prop_sales WHERE id=?`, id).Scan(&crmID)
	}
	if crmID != "" {
		zohoQueued = true
		go func(dealID string, files []string) {
			for _, f := range files {
				if f == "" {
					continue
				}
				if err := uploadCRMAttachment(dealID, f); err != nil {
					log.Printf("[plots-overview] zoho attachment %s → deal %s: %v", f, dealID, err)
				} else {
					log.Printf("[plots-overview] zoho attachment %s → deal %s: OK", f, dealID)
				}
			}
		}(crmID, strings.Split(newFiles, ","))
	}

	json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"files":       strings.Split(newFiles, ","),
		"zoho_queued": zohoQueued,
	})
}

func adminPlotsOverviewUpdateBuyerNameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(32 << 20)
	recordID := strings.TrimSpace(r.FormValue("record_id"))
	tab := strings.TrimSpace(r.FormValue("tab"))
	newName := strings.TrimSpace(r.FormValue("buyer_name"))
	w.Header().Set("Content-Type", "application/json")
	if recordID == "" || newName == "" {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing fields"})
		return
	}
	var execErr error
	if tab == "sold" {
		_, execErr = db.Exec(`UPDATE prop_sales SET buyer_name=? WHERE id=?`, newName, recordID)
	} else {
		_, execErr = db.Exec(`UPDATE prop_bookings SET buyer_name=? WHERE id=?`, newName, recordID)
	}
	if execErr != nil {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": execErr.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func adminPendingProjectsHandler(w http.ResponseWriter, r *http.Request) {
	type estateRow struct {
		ID        int
		Name      string
		Total     int
		Available int
		Booked    int
		SaSigned  int
		Sold      int
	}
	rows, err := db.Query(`
		SELECT e.id, e.name,
			COUNT(p.id),
			COALESCE(SUM(p.status = 'available'),0),
			COALESCE(SUM(p.status = 'booked'),0),
			COALESCE(SUM(p.status = 'sa_signed'),0),
			COALESCE(SUM(p.status = 'sold'),0)
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		GROUP BY e.id, e.name
		HAVING COUNT(p.id) = 0
			OR COALESCE(SUM(p.status = 'available'),0) > 0
			OR COALESCE(SUM(p.status = 'booked'),0) > 0
			OR COALESCE(SUM(p.status = 'sa_signed'),0) > 0
		ORDER BY e.name`)
	if err != nil {
		log.Printf("pending projects query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var estates []estateRow
	for rows.Next() {
		var e estateRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Total, &e.Available, &e.Booked, &e.SaSigned, &e.Sold); err != nil {
			continue
		}
		estates = append(estates, e)
	}
	renderAdmin(w, r, "admin_pending_projects.html", map[string]any{
		"Title":   "Pending Projects",
		"Active":  "pending-projects",
		"Estates": estates,
	})
}
func adminCompletedProjectsHandler(w http.ResponseWriter, r *http.Request) {
	type estateRow struct {
		ID       int
		Name     string
		Total    int
		Sold     int
		LastSale string
	}
	rows, err := db.Query(`
		SELECT e.id, e.name,
			COUNT(p.id),
			COALESCE(SUM(p.status = 'sold'),0),
			COALESCE((SELECT DATE_FORMAT(MAX(s.date_sold),'%d %b %Y') FROM prop_sales s WHERE s.estate_id = e.id),'—')
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		GROUP BY e.id, e.name
		HAVING COUNT(p.id) > 0
			AND COALESCE(SUM(p.status = 'available'),0) = 0
			AND COALESCE(SUM(p.status = 'booked'),0) = 0
			AND COALESCE(SUM(p.status = 'sa_signed'),0) = 0
		ORDER BY e.name`)
	if err != nil {
		log.Printf("completed projects query: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var estates []estateRow
	for rows.Next() {
		var e estateRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Total, &e.Sold, &e.LastSale); err != nil {
			continue
		}
		estates = append(estates, e)
	}
	renderAdmin(w, r, "admin_completed_projects.html", map[string]any{
		"Title":   "Completed Projects",
		"Active":  "completed-projects",
		"Estates": estates,
	})
}
func adminExportReportsHandler(w http.ResponseWriter, r *http.Request) {
	tab := r.URL.Query().Get("tab")
	if tab != "signed" && tab != "sold" && tab != "salesleader" && tab != "soldsummary" && tab != "sasigned" {
		tab = "booked"
	}
	agent := r.URL.Query().Get("agent")
	estateID := r.URL.Query().Get("estate")
	fromDate := r.URL.Query().Get("from")
	toDate := r.URL.Query().Get("to")
	doExport := r.URL.Query().Get("export") == "1"

	type option struct{ Value, Label string }

	// Agent dropdown options
	var agentOptions []option
	if agRows, _ := db.Query(
		`SELECT DISTINCT agent_name FROM prop_bookings
		 WHERE agent_name IS NOT NULL AND agent_name != ''
		 ORDER BY agent_name`); agRows != nil {
		defer agRows.Close()
		for agRows.Next() {
			var v string
			if agRows.Scan(&v) == nil {
				agentOptions = append(agentOptions, option{v, v})
			}
		}
	}

	// Estate dropdown options
	var estateOptions []option
	if esRows, _ := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`); esRows != nil {
		defer esRows.Close()
		for esRows.Next() {
			var id int
			var name string
			if esRows.Scan(&id, &name) == nil {
				estateOptions = append(estateOptions, option{fmt.Sprintf("%d", id), name})
			}
		}
	}

	type exportRow struct {
		Plot, Estate, Client, Phone, Agent, Date string
		Days                                     int
		Amount, PaymentPlan, Source              string
	}
	type leaderRow struct {
		Month     string
		Agent     string
		Count     int
		SoldCount int
		Value     string
	}
	type agentRow struct {
		Agent string
		Count int
		Value string
	}

	var rows []exportRow
	var leaderRows []leaderRow
	var soldSummaryRows []agentRow
	var saSignedRows []agentRow
	var args []any
	var baseQuery, filename string

	switch tab {
	case "soldsummary":
		filename = "fully_sold_by_agent.csv"
		ssQ := `
			SELECT a.agent_name, COUNT(s.id) AS cnt,
			       COALESCE(SUM(e.plot_price), 0) AS total
			FROM (
				SELECT DISTINCT agent_name FROM prop_bookings
				WHERE agent_name IS NOT NULL AND agent_name != ''
				  AND agent_name NOT IN ('Joseph Maina', 'Lucy Wambui', 'Daniel Mwangi', 'Kevin Magua', 'James Maina')
			) a
			LEFT JOIN prop_sales s ON s.agent_name = a.agent_name
			LEFT JOIN prop_estates e ON e.id = s.estate_id
			GROUP BY a.agent_name
			ORDER BY cnt DESC, a.agent_name`
		if ss, _ := db.Query(ssQ); ss != nil {
			defer ss.Close()
			for ss.Next() {
				var row agentRow
				var val float64
				if ss.Scan(&row.Agent, &row.Count, &val) == nil {
					row.Value = fmt.Sprintf("%.0f", val)
					soldSummaryRows = append(soldSummaryRows, row)
				}
			}
		}

	case "sasigned":
		filename = "sa_signed_by_agent.csv"
		saQ := `
			SELECT b.agent_name, COUNT(*) AS cnt,
			       COALESCE(SUM(e.plot_price), 0) AS total
			FROM prop_bookings b
			JOIN prop_estates e ON e.id = b.estate_id
			WHERE b.status IN ('sa_signed', 'completed')
			  AND b.agent_name IS NOT NULL AND b.agent_name != ''
			  AND b.agent_name NOT IN ('Daniel Mwangi', 'Kevin Magua', 'James Maina', 'Joseph Maina', 'Lucy Wambui')
			GROUP BY b.agent_name ORDER BY cnt DESC`
		if sa, _ := db.Query(saQ); sa != nil {
			defer sa.Close()
			for sa.Next() {
				var row agentRow
				var val float64
				if sa.Scan(&row.Agent, &row.Count, &val) == nil {
					row.Value = fmt.Sprintf("%.0f", val)
					saSignedRows = append(saSignedRows, row)
				}
			}
		}

	case "salesleader":
		filename = "best_salespeople_by_month.csv"
		leaderQ := `
			SELECT DATE_FORMAT(COALESCE(b.date_signed, b.date_booked), '%Y-%m') AS month,
			       b.agent_name, COUNT(*) AS cnt,
			       SUM(CASE WHEN b.status = 'completed' THEN 1 ELSE 0 END) AS sold_cnt,
			       COALESCE(SUM(e.plot_price), 0) AS total
			FROM prop_bookings b
			JOIN prop_estates e ON e.id = b.estate_id
			WHERE b.status IN ('sa_signed', 'completed')
			  AND b.agent_name IS NOT NULL AND b.agent_name != ''
			  AND b.agent_name NOT IN ('Daniel Mwangi', 'Kevin Magua', 'James Maina', 'Joseph Maina')`
		var lArgs []any
		if fromDate != "" {
			leaderQ += ` AND COALESCE(b.date_signed, b.date_booked) >= ?`
			lArgs = append(lArgs, fromDate)
		}
		if toDate != "" {
			leaderQ += ` AND COALESCE(b.date_signed, b.date_booked) <= ?`
			lArgs = append(lArgs, toDate+" 23:59:59")
		}
		leaderQ += ` GROUP BY DATE_FORMAT(COALESCE(b.date_signed, b.date_booked), '%Y-%m'), b.agent_name ORDER BY month DESC, total DESC`
		if lr, _ := db.Query(leaderQ, lArgs...); lr != nil {
			defer lr.Close()
			for lr.Next() {
				var row leaderRow
				var val float64
				if lr.Scan(&row.Month, &row.Agent, &row.Count, &row.SoldCount, &val) == nil {
					row.Value = fmt.Sprintf("%.0f", val)
					leaderRows = append(leaderRows, row)
				}
			}
		}

	case "booked":
		filename = "booked_plots.csv"
		baseQuery = `
			SELECT p.plot_number, e.name, COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''),
			       COALESCE(b.agent_name,''), DATE_FORMAT(b.date_booked,'%d %b %Y'),
			       DATEDIFF(NOW(), b.date_booked), COALESCE(b.lead_source,'')
			FROM prop_bookings b
			JOIN prop_plots   p ON p.id = b.plot_id
			JOIN prop_estates e ON e.id = b.estate_id
			WHERE b.status = 'active' AND p.status = 'booked'`
		if agent != "" {
			baseQuery += ` AND b.agent_name = ?`
			args = append(args, agent)
		}
		if estateID != "" {
			baseQuery += ` AND b.estate_id = ?`
			args = append(args, estateID)
		}
		if fromDate != "" {
			baseQuery += ` AND b.date_booked >= ?`
			args = append(args, fromDate)
		}
		if toDate != "" {
			baseQuery += ` AND b.date_booked <= ?`
			args = append(args, toDate+" 23:59:59")
		}
		baseQuery += ` ORDER BY b.date_booked DESC`
		if dbRows, _ := db.Query(baseQuery, args...); dbRows != nil {
			defer dbRows.Close()
			for dbRows.Next() {
				var r exportRow
				if dbRows.Scan(&r.Plot, &r.Estate, &r.Client, &r.Phone, &r.Agent, &r.Date, &r.Days, &r.Source) == nil {
					rows = append(rows, r)
				}
			}
		}

	case "signed":
		filename = "signed_plots.csv"
		baseQuery = `
			SELECT p.plot_number, COALESCE(e.name,''), COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''),
			       COALESCE(b.agent_name,''), COALESCE(DATE_FORMAT(COALESCE(b.date_signed, b.date_booked),'%d %b %Y'),''),
			       COALESCE(b.lead_source,'')
			FROM prop_plots p
			LEFT JOIN prop_estates e ON e.id = p.estate_id
			LEFT JOIN prop_bookings b ON b.id = (
				SELECT id FROM prop_bookings
				WHERE plot_id = p.id
				ORDER BY id DESC LIMIT 1
			)
			WHERE p.status = 'sa_signed'`
		if agent != "" {
			baseQuery += ` AND b.agent_name = ?`
			args = append(args, agent)
		}
		if estateID != "" {
			baseQuery += ` AND p.estate_id = ?`
			args = append(args, estateID)
		}
		if fromDate != "" {
			baseQuery += ` AND COALESCE(b.date_signed, b.date_booked) >= ?`
			args = append(args, fromDate)
		}
		if toDate != "" {
			baseQuery += ` AND COALESCE(b.date_signed, b.date_booked) <= ?`
			args = append(args, toDate+" 23:59:59")
		}
		baseQuery += ` ORDER BY COALESCE(b.date_signed, b.date_booked) DESC`
		if dbRows, _ := db.Query(baseQuery, args...); dbRows != nil {
			defer dbRows.Close()
			for dbRows.Next() {
				var r exportRow
				if dbRows.Scan(&r.Plot, &r.Estate, &r.Client, &r.Phone, &r.Agent, &r.Date, &r.Source) == nil {
					rows = append(rows, r)
				}
			}
		}

	case "sold":
		filename = "sold_plots.csv"
		baseQuery = `
			SELECT p.plot_number, COALESCE(e.name,''), COALESCE(s.buyer_name,''), COALESCE(s.buyer_phone,''),
			       COALESCE(s.agent_name,''), COALESCE(CAST(s.amount AS CHAR),'0'),
			       COALESCE(s.payment_plan,''), COALESCE(DATE_FORMAT(s.date_sold,'%d %b %Y'),''),
			       COALESCE(b.lead_source,'')
			FROM prop_plots p
			LEFT JOIN prop_estates e ON e.id = p.estate_id
			LEFT JOIN prop_sales s ON s.id = (
				SELECT id FROM prop_sales
				WHERE plot_id = p.id
				ORDER BY id DESC LIMIT 1
			)
			LEFT JOIN prop_bookings b ON b.id = (
				SELECT id FROM prop_bookings
				WHERE plot_id = p.id
				ORDER BY id DESC LIMIT 1
			)
			WHERE p.status = 'sold'`
		if agent != "" {
			baseQuery += ` AND s.agent_name = ?`
			args = append(args, agent)
		}
		if estateID != "" {
			baseQuery += ` AND p.estate_id = ?`
			args = append(args, estateID)
		}
		if fromDate != "" {
			baseQuery += ` AND s.date_sold >= ?`
			args = append(args, fromDate)
		}
		if toDate != "" {
			baseQuery += ` AND s.date_sold <= ?`
			args = append(args, toDate+" 23:59:59")
		}
		baseQuery += ` ORDER BY s.date_sold DESC`
		if dbRows, _ := db.Query(baseQuery, args...); dbRows != nil {
			defer dbRows.Close()
			for dbRows.Next() {
				var r exportRow
				if dbRows.Scan(&r.Plot, &r.Estate, &r.Client, &r.Phone, &r.Agent, &r.Amount, &r.PaymentPlan, &r.Date, &r.Source) == nil {
					rows = append(rows, r)
				}
			}
		}
	}

	// CSV export
	if doExport {
		var buf bytes.Buffer
		switch tab {
		case "booked":
			buf.WriteString("Plot,Estate,Client,Phone,Agent,Date Booked,Days Booked,Source\n")
			for _, r := range rows {
				fmt.Fprintf(&buf, "%s,%s,%s,%s,%s,%s,%d,%s\n",
					csvEscape(r.Plot), csvEscape(r.Estate), csvEscape(r.Client),
					csvEscape(r.Phone), csvEscape(r.Agent), csvEscape(r.Date), r.Days, csvEscape(r.Source))
			}
		case "signed":
			buf.WriteString("Plot,Estate,Client,Phone,Agent,Date Signed,Source\n")
			for _, r := range rows {
				fmt.Fprintf(&buf, "%s,%s,%s,%s,%s,%s,%s\n",
					csvEscape(r.Plot), csvEscape(r.Estate), csvEscape(r.Client),
					csvEscape(r.Phone), csvEscape(r.Agent), csvEscape(r.Date), csvEscape(r.Source))
			}
		case "sold":
			buf.WriteString("Plot,Estate,Client,Phone,Agent,Amount,Payment Plan,Date Sold,Source\n")
			for _, r := range rows {
				fmt.Fprintf(&buf, "%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
					csvEscape(r.Plot), csvEscape(r.Estate), csvEscape(r.Client),
					csvEscape(r.Phone), csvEscape(r.Agent), csvEscape(r.Amount),
					csvEscape(r.PaymentPlan), csvEscape(r.Date), csvEscape(r.Source))
			}
		case "salesleader":
			buf.WriteString("Month,Agent,Plots SA Signed,Plots Fully Sold,Total Value (KES)\n")
			for _, r := range leaderRows {
				fmt.Fprintf(&buf, "%s,%s,%d,%d,%s\n",
					r.Month, csvEscape(r.Agent), r.Count, r.SoldCount, r.Value)
			}
		case "soldsummary":
			buf.WriteString("Agent,Plots Fully Sold,Total Value (KES)\n")
			for _, r := range soldSummaryRows {
				fmt.Fprintf(&buf, "%s,%d,%s\n",
					csvEscape(r.Agent), r.Count, r.Value)
			}
		case "sasigned":
			buf.WriteString("Agent,Plots SA Signed,Total Value (KES)\n")
			for _, r := range saSignedRows {
				fmt.Fprintf(&buf, "%s,%d,%s\n",
					csvEscape(r.Agent), r.Count, r.Value)
			}
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Write(buf.Bytes())
		return
	}

	count := len(rows)
	if tab == "salesleader" {
		count = len(leaderRows)
	} else if tab == "soldsummary" {
		count = len(soldSummaryRows)
	} else if tab == "sasigned" {
		count = len(saSignedRows)
	}
	renderAdmin(w, r, "admin_export_reports.html", map[string]any{
		"Title":           "Export Reports",
		"Active":          "export-reports",
		"Tab":             tab,
		"Rows":            rows,
		"LeaderRows":      leaderRows,
		"SoldSummaryRows": soldSummaryRows,
		"SASignedRows":    saSignedRows,
		"Count":           count,
		"AgentOptions":    agentOptions,
		"EstateOptions":   estateOptions,
		"FAgent":          agent,
		"FEstate":         estateID,
		"FFrom":           fromDate,
		"FTo":             toDate,
	})
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// ── Agent handlers ──────────────────────────────────────────────────────────

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgentName(r)

	var totalEstates, totalAvailable, agentBooked, agentSaSigned, agentSold int
	db.QueryRow(`SELECT COUNT(*) FROM prop_estates WHERE COALESCE(is_restricted,0)=0`).Scan(&totalEstates)
	db.QueryRow(`
		SELECT COUNT(*) FROM prop_plots p
		JOIN prop_estates e ON e.id = p.estate_id
		WHERE p.status='available' AND COALESCE(e.is_restricted,0)=0`).Scan(&totalAvailable)
	db.QueryRow(`SELECT COUNT(*) FROM prop_bookings WHERE agent_name=? AND status='active'`, agentName).Scan(&agentBooked)
	db.QueryRow(`SELECT COUNT(*) FROM prop_bookings WHERE agent_name=? AND status='sa_signed'`, agentName).Scan(&agentSaSigned)
	db.QueryRow(`SELECT COUNT(*) FROM prop_sales WHERE agent_name=?`, agentName).Scan(&agentSold)

	// Per-estate chart data — private (is_restricted) estates are excluded,
	// same as agentEstatesHandler and the admin dashboard; they have their
	// own dedicated Private Estates page instead.
	rows, _ := db.Query(`
		SELECT e.name,
			SUM(p.status='available'),
			SUM(p.status='booked'),
			SUM(p.status='sold')
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		WHERE COALESCE(e.is_restricted,0)=0
		GROUP BY e.id, e.name ORDER BY e.name`)
	var estates []string
	var avail, booked, sold []int
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var n string
			var a, b, s int
			if rows.Scan(&n, &a, &b, &s) == nil {
				estates = append(estates, n)
				avail = append(avail, a)
				booked = append(booked, b)
				sold = append(sold, s)
			}
		}
	}
	chartData := map[string]any{
		"estates":        estates,
		"availablePlots": avail,
		"bookedPlots":    booked,
		"soldPlots":      sold,
	}
	chartJSON, _ := json.Marshal(chartData)

	render(w, "dashboard.html", map[string]any{
		"Title":          "Dashboard",
		"Active":         "dashboard",
		"AgentName":      agentName,
		"TotalEstates":   totalEstates,
		"TotalAvailable": totalAvailable,
		"AgentBooked":    agentBooked,
		"AgentSaSigned":  agentSaSigned,
		"AgentSold":      agentSold,
		"ChartData":      template.JS(chartJSON),
	})
}

func agentEstatesHandler(w http.ResponseWriter, r *http.Request) {
	type estateRow struct {
		ID        int
		Name      string
		Total     int
		Available int
		Booked    int
		SaSigned  int
		Sold      int
	}
	rows, err := db.Query(`
		SELECT e.id, e.name,
			COUNT(p.id),
			SUM(p.status='available'),
			SUM(p.status='booked'),
			SUM(p.status='sa_signed'),
			SUM(p.status='sold')
		FROM prop_estates e
		LEFT JOIN prop_plots p ON p.estate_id = e.id
		WHERE COALESCE(e.is_restricted, 0) = 0
		GROUP BY e.id, e.name ORDER BY e.name`)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var estates []estateRow
	for rows.Next() {
		var e estateRow
		if rows.Scan(&e.ID, &e.Name, &e.Total, &e.Available, &e.Booked, &e.SaSigned, &e.Sold) == nil {
			estates = append(estates, e)
		}
	}
	renderAgent(w, r, "agent_estates.html", map[string]any{
		"Title":   "Estates",
		"Active":  "estates",
		"Estates": estates,
	})
}

func agentEstateDetailHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)

	var estateName, image, mutationImage, plotInfo string
	if err := db.QueryRow(`SELECT name, COALESCE(image,''), COALESCE(mutation_image,''), COALESCE(prop_plotinfo,'') FROM prop_estates WHERE id=?`, id).
		Scan(&estateName, &image, &mutationImage, &plotInfo); err != nil {
		http.NotFound(w, r)
		return
	}
	displayImage := mutationImage
	if displayImage == "" {
		displayImage = image
	}

	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id)
	if cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}

	zones := []zoneDisplay{}
	zRows, _ := db.Query(`
		SELECT pz.plot_number, COALESCE(pz.points_json,'[]'), COALESCE(pp.status,'available')
		FROM prop_plot_zones pz
		LEFT JOIN prop_plots pp ON pp.estate_id = pz.estate_id AND pp.plot_number = pz.plot_number
		WHERE pz.estate_id = ?
		ORDER BY pz.id`, id)
	if zRows != nil {
		defer zRows.Close()
		for zRows.Next() {
			var pn, pj, st string
			if zRows.Scan(&pn, &pj, &st) == nil {
				var pts []pointXY
				json.Unmarshal([]byte(pj), &pts)
				zones = append(zones, zoneDisplay{PlotNumber: pn, Status: st, Points: pts})
			}
		}
	}
	zonesJSON, _ := json.Marshal(zones)

	plotInfo = strings.ReplaceAll(plotInfo, `\r\n`, "\n")
	plotInfo = strings.ReplaceAll(plotInfo, `\n`, "\n")
	plotInfo = strings.ReplaceAll(plotInfo, "\r", "")

	plotInfoLinesJSON, _ := json.Marshal(strings.Split(plotInfo, "\n"))

	renderAgent(w, r, "agent_estate_detail.html", map[string]any{
		"Title":        estateName,
		"Active":       "estates",
		"EstateID":     id,
		"EstateName":   estateName,
		"DisplayImage": displayImage,
		"PlotInfo":     plotInfo,
		"PlotInfoJSON": template.JS(plotInfoLinesJSON),
		"Counts":       counts,
		"ZonesJSON":    template.JS(zonesJSON),
	})
}

func agentEstatePlotsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "available"
	}

	var estateName string
	if err := db.QueryRow(`SELECT name FROM prop_estates WHERE id=?`, id).Scan(&estateName); err != nil {
		http.NotFound(w, r)
		return
	}

	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id)
	if cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}

	type plotRow struct {
		ID     int
		Number string
		Status string
	}
	pRows, _ := db.Query(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id=? AND status=? ORDER BY id`, id, status)
	var plots []plotRow
	if pRows != nil {
		defer pRows.Close()
		for pRows.Next() {
			var p plotRow
			if pRows.Scan(&p.ID, &p.Number, &p.Status) == nil {
				plots = append(plots, p)
			}
		}
	}

	renderAgent(w, r, "agent_estate_plots.html", map[string]any{
		"Title":          estateName + " – Plots",
		"Active":         "estates",
		"EstateName":     estateName,
		"EstateID":       id,
		"Status":         status,
		"Plots":          plots,
		"Counts":         counts,
		"BookingSuccess": r.URL.Query().Get("booked") == "1",
	})
}

func agentEstateBookHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	agentName := getAgentName(r)

	type plotRow struct {
		ID         int
		Number     string
		EstateName string
	}

	fetchPlots := func(ids []string) ([]plotRow, string) {
		var rows []plotRow
		var estateName string
		for _, pid := range ids {
			var p plotRow
			if err := db.QueryRow(`SELECT pp.id, pp.plot_number, e.name FROM prop_plots pp JOIN prop_estates e ON e.id=pp.estate_id WHERE pp.id=? AND pp.status='available'`, pid).
				Scan(&p.ID, &p.Number, &p.EstateName); err == nil {
				rows = append(rows, p)
				estateName = p.EstateName
			}
		}
		return rows, estateName
	}

	renderAgentBookForm := func(plots []plotRow, estateName, formErr string) {
		title := fmt.Sprintf("Book Plot %s", plots[0].Number)
		if len(plots) > 1 {
			title = fmt.Sprintf("Book %d Plots", len(plots))
		}
		renderAgent(w, r, "agent_estate_book.html", map[string]any{
			"Title":      title,
			"Active":     "estates",
			"Plots":      plots,
			"EstateName": estateName,
			"EstateID":   id,
			"FormError":  formErr,
		})
	}

	if r.Method == http.MethodPost {
		r.ParseMultipartForm(32 << 20)
		plotIDs := r.Form["plot_ids"]
		buyerName := strings.TrimSpace(r.FormValue("buyer_name"))
		buyerPhone := strings.TrimSpace(r.FormValue("buyer_phone"))
		buyerEmail := r.FormValue("buyer_email")
		deposit := strings.TrimSpace(r.FormValue("deposit"))
		paymentPlan := r.FormValue("payment_plan")
		leadSource := r.FormValue("lead_source")
		notes := strings.TrimSpace(r.FormValue("notes"))
		careOf := strings.TrimSpace(r.FormValue("care_of"))
		rebookConfirmed := r.FormValue("rebook_confirmed") == "1"

		plots, estateName := fetchPlots(plotIDs)

		switch {
		case buyerName == "":
			renderAgentBookForm(plots, estateName, "Buyer name is required.")
			return
		case buyerPhone == "":
			renderAgentBookForm(plots, estateName, "Buyer phone number is required.")
			return
		case deposit == "" || deposit == "0":
			renderAgentBookForm(plots, estateName, "Deposit amount is required.")
			return
		case leadSource == "":
			renderAgentBookForm(plots, estateName, "Lead source is required.")
			return
		}

		hasDepositRef := false
		if r.MultipartForm != nil {
			for _, fh := range r.MultipartForm.File["deposit_ref"] {
				if fh.Size > 0 {
					hasDepositRef = true
					break
				}
			}
		}
		if !hasDepositRef {
			renderAgentBookForm(plots, estateName, "Payment reference attachment is required.")
			return
		}

		if len(plots) == 0 {
			http.Redirect(w, r, "/agent/estate/"+id+"/plots?status=available&err=unavailable", http.StatusFound)
			return
		}

		depositRef := saveUploadedFiles(r, "deposit_ref")
		idPhoto := saveUploadedFiles(r, "id_photo")
		kra := saveUploadedFiles(r, "kra")
		passportPhoto := saveUploadedFiles(r, "passport_photo")

		var plotNumbers []string
		var bookedPlotIDs []int
		var newBookingIDs []int
		agentEstateIDInt, _ := strconv.Atoi(id)
		for _, p := range plots {
			if res, err := db.Exec(`INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, deposit, payment_plan, deposit_ref, id_photo, kra, passport_photo, lead_source, notes, care_of, status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'active')`,
				p.ID, id, buyerName, buyerPhone, buyerEmail, agentName, deposit, paymentPlan, depositRef, idPhoto, kra, passportPhoto, leadSource, notes, careOf); err != nil {
				log.Printf("agentBook: booking insert failed for plot %d: %v", p.ID, err)
			} else if bid, err2 := res.LastInsertId(); err2 == nil {
				newBookingIDs = append(newBookingIDs, int(bid))
			}
			if _, err := db.Exec(`UPDATE prop_plots SET status='booked' WHERE id=?`, p.ID); err != nil {
				log.Printf("agentBook: plot status update failed for plot %d: %v", p.ID, err)
			}
			logPlotStatus(p.ID, p.Number, estateName, agentEstateIDInt, "available", "booked", agentName, "booked")
			plotNumbers = append(plotNumbers, p.Number)
			bookedPlotIDs = append(bookedPlotIDs, p.ID)
		}
		log.Printf("agentBook: %d plots booked for %s by %s", len(plots), buyerName, agentName)
		logBooking("PLOT_BOOKED", agentName, buyerName,
			strings.Join(plotNumbers, ", ")+" — "+estateName,
			fmt.Sprintf("Deposit: KES %s | Plan: %s | Docs: %v", deposit, paymentPlan, hasAllAttachments(bookingInfo{DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto})))
		processBookingIntegrations(bookingInfo{
			PlotIDs: bookedPlotIDs, BuyerName: buyerName, BuyerPhone: buyerPhone, BuyerEmail: buyerEmail,
			PlotNumbers: plotNumbers, EstateName: estateName,
			Deposit: deposit, PaymentPlan: paymentPlan, AgentName: agentName,
			DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto,
			Notes: notes, RebookConfirmed: rebookConfirmed,
		})
		for _, bid := range newBookingIDs {
			go maybeAdvanceToAccountsReview(bid)
		}
		http.Redirect(w, r, "/agent/estate/"+id+"/plots?status=booked&booked=1", http.StatusFound)
		return
	}

	// GET: ?plots=1,2,3
	plotIDStrs := strings.Split(r.URL.Query().Get("plots"), ",")
	plots, estateName := fetchPlots(plotIDStrs)
	if len(plots) == 0 {
		http.Redirect(w, r, "/agent/estate/"+id+"/plots?status=available", http.StatusFound)
		return
	}

	renderAgentBookForm(plots, estateName, "")
}

func agentCartHandler(w http.ResponseWriter, r *http.Request) {
	renderAgent(w, r, "agent_cart.html", map[string]any{"Title": "My Cart", "Active": "cart"})
}

func adminCartHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, r, "admin_cart.html", map[string]any{"Title": "Booking Cart", "Active": "cart"})
}

func agentCartCheckoutHandler(w http.ResponseWriter, r *http.Request) {
	cartCheckoutHandler(w, r, "/agent/cart", "/agent/receipt/")
}

func adminCartCheckoutHandler(w http.ResponseWriter, r *http.Request) {
	cartCheckoutHandler(w, r, "/admin/cart", "/admin/receipt/")
}

func cartCheckoutHandler(w http.ResponseWriter, r *http.Request, cartPath, receiptBasePath string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, cartPath, http.StatusFound)
		return
	}
	agentName := getAgentName(r)
	r.ParseMultipartForm(32 << 20)

	plotIDStrs := r.Form["plot_ids"]
	buyerName := strings.TrimSpace(r.FormValue("buyer_name"))
	buyerPhone := strings.TrimSpace(r.FormValue("buyer_phone"))
	buyerEmail := r.FormValue("buyer_email")
	deposit := strings.TrimSpace(r.FormValue("deposit"))
	paymentPlan := r.FormValue("payment_plan")
	leadSource := r.FormValue("lead_source")
	notes := strings.TrimSpace(r.FormValue("notes"))
	careOf := strings.TrimSpace(r.FormValue("care_of"))
	rebookConfirmed := r.FormValue("rebook_confirmed") == "1"

	switch {
	case buyerName == "":
		http.Redirect(w, r, cartPath+"?err=name", http.StatusFound)
		return
	case buyerPhone == "":
		http.Redirect(w, r, cartPath+"?err=phone", http.StatusFound)
		return
	case deposit == "" || deposit == "0":
		http.Redirect(w, r, cartPath+"?err=deposit_amt", http.StatusFound)
		return
	case leadSource == "":
		http.Redirect(w, r, cartPath+"?err=source", http.StatusFound)
		return
	}

	hasDepositRef := false
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["deposit_ref"] {
			if fh.Size > 0 {
				hasDepositRef = true
				break
			}
		}
	}
	if !hasDepositRef {
		http.Redirect(w, r, cartPath+"?err=deposit_ref", http.StatusFound)
		return
	}

	type cartPlot struct {
		ID         int
		Number     string
		EstateID   int
		EstateName string
	}

	var plots []cartPlot
	for _, idStr := range plotIDStrs {
		pid, _ := strconv.Atoi(idStr)
		if pid == 0 {
			continue
		}
		var cp cartPlot
		cp.ID = pid
		if err := db.QueryRow(`SELECT pp.plot_number, pp.estate_id, e.name FROM prop_plots pp JOIN prop_estates e ON e.id=pp.estate_id WHERE pp.id=? AND pp.status='available'`, pid).
			Scan(&cp.Number, &cp.EstateID, &cp.EstateName); err != nil {
			continue
		}
		plots = append(plots, cp)
	}

	if len(plots) == 0 {
		http.Redirect(w, r, cartPath+"?err=unavailable", http.StatusFound)
		return
	}

	depositRef := saveUploadedFiles(r, "deposit_ref")
	idPhoto := saveUploadedFiles(r, "id_photo")
	kra := saveUploadedFiles(r, "kra")
	passportPhoto := saveUploadedFiles(r, "passport_photo")

	batchRef := generateToken()[:20]

	// Generate sequential receipt number: PPSL{year}{padded count}
	year := time.Now().Year()
	var receiptSeq int
	db.QueryRow(`SELECT COUNT(DISTINCT batch_ref) FROM prop_bookings WHERE batch_ref IS NOT NULL AND YEAR(date_booked) = ?`, year).Scan(&receiptSeq)
	receiptNumber := fmt.Sprintf("PPSL%d%03d", year, receiptSeq+1)

	type estateGroup struct {
		name     string
		plotIDs  []int
		plotNums []string
	}
	groups := map[int]*estateGroup{}

	var newBookingIDs []int
	for _, cp := range plots {
		if res, err := db.Exec(`INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, deposit, payment_plan, deposit_ref, id_photo, kra, passport_photo, lead_source, notes, care_of, batch_ref, receipt_number, status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'active')`,
			cp.ID, cp.EstateID, buyerName, buyerPhone, buyerEmail, agentName, deposit, paymentPlan, depositRef, idPhoto, kra, passportPhoto, leadSource, notes, careOf, batchRef, receiptNumber); err == nil {
			if bid, err2 := res.LastInsertId(); err2 == nil {
				newBookingIDs = append(newBookingIDs, int(bid))
			}
		}
		db.Exec(`UPDATE prop_plots SET status='booked' WHERE id=?`, cp.ID)
		logPlotStatus(cp.ID, cp.Number, cp.EstateName, cp.EstateID, "available", "booked", agentName, "booked")

		if groups[cp.EstateID] == nil {
			groups[cp.EstateID] = &estateGroup{name: cp.EstateName}
		}
		groups[cp.EstateID].plotIDs = append(groups[cp.EstateID].plotIDs, cp.ID)
		groups[cp.EstateID].plotNums = append(groups[cp.EstateID].plotNums, cp.Number)
	}

	for _, eg := range groups {
		processBookingIntegrations(bookingInfo{
			PlotIDs: eg.plotIDs, BuyerName: buyerName, BuyerPhone: buyerPhone, BuyerEmail: buyerEmail,
			PlotNumbers: eg.plotNums, EstateName: eg.name,
			Deposit: deposit, PaymentPlan: paymentPlan, AgentName: agentName,
			DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto,
			Notes: notes, RebookConfirmed: rebookConfirmed,
		})
	}

	var allNums []string
	for _, cp := range plots {
		allNums = append(allNums, cp.Number)
	}
	logBooking("PLOT_BOOKED", agentName, buyerName,
		strings.Join(allNums, ", "),
		fmt.Sprintf("Deposit: KES %s | Plan: %s | Batch: %s", deposit, paymentPlan, batchRef))

	for _, bid := range newBookingIDs {
		go maybeAdvanceToAccountsReview(bid)
	}

	http.Redirect(w, r, receiptBasePath+batchRef, http.StatusFound)
}

func agentReceiptHandler(w http.ResponseWriter, r *http.Request) {
	renderReceiptPage(w, r, strings.Trim(strings.TrimPrefix(r.URL.Path, "/agent/receipt/"), "/"), "agent_receipt.html")
}

func adminReceiptHandler(w http.ResponseWriter, r *http.Request) {
	renderReceiptPage(w, r, strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/receipt/"), "/"), "admin_receipt.html")
}

func adminReceiptsListHandler(w http.ResponseWriter, r *http.Request) {
	type receiptRow struct {
		BatchRef      string
		ReceiptNumber string
		BuyerName     string
		BuyerPhone    string
		AgentName     string
		DateBooked    string
		PlotCount     int
		Plots         string
		Status        string
	}

	search := strings.TrimSpace(r.URL.Query().Get("q"))

	q := `
		SELECT b.batch_ref,
		       COALESCE(MAX(b.receipt_number),'—'),
		       COALESCE(MAX(b.buyer_name),''),
		       COALESCE(MAX(b.buyer_phone),''),
		       COALESCE(MAX(b.agent_name),''),
		       DATE_FORMAT(MAX(b.date_booked),'%d %b %Y'),
		       COUNT(*) AS plot_count,
		       GROUP_CONCAT(pp.plot_number ORDER BY pp.plot_number SEPARATOR ', '),
		       MAX(b.status)
		FROM prop_bookings b
		JOIN prop_plots pp ON pp.id = b.plot_id
		WHERE b.batch_ref IS NOT NULL AND b.batch_ref != ''`
	var args []any
	if search != "" {
		q += ` AND (b.buyer_name LIKE ? OR b.buyer_phone LIKE ? OR b.receipt_number LIKE ? OR b.agent_name LIKE ?)`
		like := "%" + search + "%"
		args = append(args, like, like, like, like)
	}
	q += ` GROUP BY b.batch_ref ORDER BY MAX(b.date_booked) DESC`

	var rows []receiptRow
	if dbRows, err := db.Query(q, args...); err == nil {
		defer dbRows.Close()
		for dbRows.Next() {
			var row receiptRow
			if dbRows.Scan(&row.BatchRef, &row.ReceiptNumber, &row.BuyerName, &row.BuyerPhone,
				&row.AgentName, &row.DateBooked, &row.PlotCount, &row.Plots, &row.Status) == nil {
				rows = append(rows, row)
			}
		}
	}

	renderAdmin(w, r, "admin_receipts_list.html", map[string]any{
		"Title":  "All Receipts",
		"Active": "receipts",
		"Rows":   rows,
		"Search": search,
		"Count":  len(rows),
	})
}

func renderReceiptPage(w http.ResponseWriter, r *http.Request, batchRef, tmplName string) {
	_ = r // satisfies signature; render/renderAdmin called below via tmplName
	if batchRef == "" {
		http.NotFound(w, r)
		return
	}

	type receiptPlot struct {
		PlotNumber string
		EstateName string
	}
	type receiptData struct {
		BuyerName     string
		BuyerPhone    string
		BuyerEmail    string
		AgentName     string
		Deposit       string
		PaymentPlan   string
		LeadSource    string
		DateBooked    string
		BatchRef      string
		ReceiptNumber string
		Plots         []receiptPlot
	}

	rows, err := db.Query(`
		SELECT pp.plot_number, e.name,
		       b.buyer_name, b.buyer_phone, COALESCE(b.buyer_email,''),
		       b.agent_name, COALESCE(CAST(b.deposit AS CHAR),'0'),
		       COALESCE(b.payment_plan,''), COALESCE(b.lead_source,''),
		       DATE_FORMAT(b.date_booked,'%d %b %Y %H:%i'),
		       COALESCE(b.receipt_number,'')
		FROM prop_bookings b
		JOIN prop_plots pp ON pp.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.batch_ref = ?
		ORDER BY b.id`, batchRef)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rows.Close()

	var data receiptData
	data.BatchRef = batchRef
	first := true
	for rows.Next() {
		var rp receiptPlot
		var buyerName, buyerPhone, buyerEmail, agentName, deposit, paymentPlan, leadSource, dateBooked, receiptNumber string
		rows.Scan(&rp.PlotNumber, &rp.EstateName, &buyerName, &buyerPhone, &buyerEmail, &agentName, &deposit, &paymentPlan, &leadSource, &dateBooked, &receiptNumber)
		if first {
			data.BuyerName = buyerName
			data.BuyerPhone = buyerPhone
			data.BuyerEmail = buyerEmail
			data.AgentName = agentName
			data.Deposit = deposit
			data.PaymentPlan = paymentPlan
			data.LeadSource = leadSource
			data.DateBooked = dateBooked
			data.ReceiptNumber = receiptNumber
			first = false
		}
		data.Plots = append(data.Plots, rp)
	}
	if len(data.Plots) == 0 {
		http.NotFound(w, r)
		return
	}

	pageData := map[string]any{
		"Title":   "Booking Receipt",
		"Active":  "bookings",
		"Receipt": data,
	}
	if tmplName == "admin_receipt.html" {
		renderAdmin(w, r, tmplName, pageData)
	} else {
		renderAgent(w, r, tmplName, pageData)
	}
}

func agentBookingsHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgentName(r)
	estateFilter := r.URL.Query().Get("estate_id")
	search := r.URL.Query().Get("search")

	type bookingRow struct {
		BookingID  int
		PlotID     int
		PlotNumber string
		EstateName string
		BuyerName  string
		BuyerPhone string
		BuyerEmail string
		Notes      string
		DateBooked string
		BatchRef   string
		Status     string
		LegalStage string
		StageLabel string
	}

	// Covers the pipeline up to (not including) SA Signed — active ->
	// Accounts -> Legal — so a booking never vanishes from the agent's own
	// view partway through review. Once signed it moves to its own
	// dedicated "My Signed Plots" page (agentSignedPlotsHandler) instead of
	// staying listed here; cancelled/expired (dead ends) and completed/sold
	// (My Sales) are left out same as before.
	query := `SELECT b.id, p.id, p.plot_number, e.name, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''), COALESCE(b.notes,''), DATE_FORMAT(b.date_booked,'%d %b %Y'), COALESCE(b.batch_ref,''), b.status, b.legal_stage
		FROM prop_bookings b
		JOIN prop_plots p ON p.id=b.plot_id
		JOIN prop_estates e ON e.id=p.estate_id
		WHERE b.status IN ('active','pending_accounts_review','pending_wakili_review') AND b.agent_name=?`
	args := []any{agentName}

	if estateFilter != "" && estateFilter != "0" {
		query += " AND p.estate_id=?"
		args = append(args, estateFilter)
	}
	if search != "" {
		query += " AND (b.buyer_name LIKE ? OR p.plot_number LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	query += " ORDER BY b.date_booked DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("agentBookings: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var bookings []bookingRow
	for rows.Next() {
		var b bookingRow
		if rows.Scan(&b.BookingID, &b.PlotID, &b.PlotNumber, &b.EstateName, &b.BuyerName, &b.BuyerPhone, &b.BuyerEmail, &b.Notes, &b.DateBooked, &b.BatchRef, &b.Status, &b.LegalStage) == nil {
			if label := bookingStageLabel(b.Status, b.LegalStage); label != "" {
				b.StageLabel = label
			} else {
				b.StageLabel = "Active"
			}
			bookings = append(bookings, b)
		}
	}

	// Estates for filter dropdown
	eRows, _ := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`)
	type estOption struct {
		ID   int
		Name string
	}
	var estateOptions []estOption
	if eRows != nil {
		defer eRows.Close()
		for eRows.Next() {
			var e estOption
			if eRows.Scan(&e.ID, &e.Name) == nil {
				estateOptions = append(estateOptions, e)
			}
		}
	}

	renderAgent(w, r, "agent_bookings.html", map[string]any{
		"Title":        "My Bookings",
		"Active":       "bookings",
		"Bookings":     bookings,
		"Estates":      estateOptions,
		"EstateFilter": estateFilter,
		"Search":       search,
	})
}

// agentSignedPlotsHandler lists the agent's own bookings that have reached
// sa_signed — split out from My Bookings (agentBookingsHandler), which now
// stops at pending_wakili_review, so a signed plot has one dedicated place
// to show up instead of sitting mixed in with still-in-review bookings.
func agentSignedPlotsHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgentName(r)
	estateFilter := r.URL.Query().Get("estate_id")
	search := r.URL.Query().Get("search")

	type signedRow struct {
		BookingID  int
		PlotNumber string
		EstateName string
		BuyerName  string
		BuyerPhone string
		BuyerEmail string
		Deposit    float64
		DateSigned string
	}

	// Filters on p.status (the plot's actual current status) rather than
	// b.status='sa_signed' alone, joined to only the latest non-cancelled/
	// expired booking per plot — same fix as Plots Overview's booked/signed
	// tabs: a stale, superseded prop_bookings row left stuck at sa_signed
	// from an earlier pass on the same plot must not show here once the
	// plot itself has moved on (e.g. to sold).
	query := `SELECT b.id, p.plot_number, e.name, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
			COALESCE(b.deposit,0), COALESCE(DATE_FORMAT(b.date_signed,'%d %b %Y'),'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id=b.plot_id
		JOIN prop_estates e ON e.id=p.estate_id
		JOIN (
			SELECT plot_id, MAX(id) AS latest_id
			FROM prop_bookings
			GROUP BY plot_id
		) latest ON b.id = latest.latest_id
		WHERE p.status='sa_signed' AND b.agent_name=?`
	args := []any{agentName}

	if estateFilter != "" && estateFilter != "0" {
		query += " AND p.estate_id=?"
		args = append(args, estateFilter)
	}
	if search != "" {
		query += " AND (b.buyer_name LIKE ? OR p.plot_number LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	query += " ORDER BY b.date_signed DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("agentSignedPlots: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var signed []signedRow
	for rows.Next() {
		var s signedRow
		if rows.Scan(&s.BookingID, &s.PlotNumber, &s.EstateName, &s.BuyerName, &s.BuyerPhone, &s.BuyerEmail, &s.Deposit, &s.DateSigned) == nil {
			signed = append(signed, s)
		}
	}

	eRows, _ := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`)
	type estOption struct {
		ID   int
		Name string
	}
	var estateOptions []estOption
	if eRows != nil {
		defer eRows.Close()
		for eRows.Next() {
			var e estOption
			if eRows.Scan(&e.ID, &e.Name) == nil {
				estateOptions = append(estateOptions, e)
			}
		}
	}

	renderAgent(w, r, "agent_signed_plots.html", map[string]any{
		"Title":        "My Signed Plots",
		"Active":       "signed-plots",
		"Signed":       signed,
		"Estates":      estateOptions,
		"EstateFilter": estateFilter,
		"Search":       search,
	})
}

func bookingAttachmentsHandler(w http.ResponseWriter, r *http.Request, tmplName, backURL string, isAdmin bool) {
	// Extract booking ID from URL: /admin/booking/{id}/attachments or /agent/booking/{id}/attachments
	var prefix string
	if isAdmin {
		prefix = "/admin/booking/"
	} else {
		prefix = "/agent/booking/"
	}
	bookingID := strings.TrimPrefix(r.URL.Path, prefix)
	bookingID = strings.TrimSuffix(bookingID, "/attachments")
	bookingID = strings.Trim(bookingID, "/")
	if bookingID == "" {
		http.Redirect(w, r, backURL, http.StatusFound)
		return
	}

	type attachInfo struct {
		BookingID          int
		BuyerName          string
		BuyerPhone         string
		BuyerEmail         string
		EstateName         string
		PlotNumber         string
		DepositRef         string
		IDPhoto            string
		KRA                string
		PassportPhoto      string
		PlotID             int
		EstateID           int
		AgentName          string
		PaymentPlan        string
		Deposit            string
		Notes              string
		CareOf             string
		ZohoBooksID        string
		DepositRefFiles    []string
		IDPhotoFiles       []string
		KRAFiles           []string
		PassportPhotoFiles []string
	}
	var info attachInfo
	err := db.QueryRow(`
		SELECT b.id, b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       e.name, p.plot_number,
		       COALESCE(b.deposit_ref,''), COALESCE(b.id_photo,''),
		       COALESCE(b.kra,''), COALESCE(b.passport_photo,''),
		       b.plot_id, b.estate_id, COALESCE(b.agent_name,''),
		       COALESCE(b.payment_plan,''), COALESCE(CAST(b.deposit AS CHAR),'0'),
		       COALESCE(b.notes,''), COALESCE(b.care_of,''), COALESCE(b.zoho_books_id,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.id = ?`, bookingID).
		Scan(&info.BookingID, &info.BuyerName, &info.BuyerPhone, &info.BuyerEmail,
			&info.EstateName, &info.PlotNumber,
			&info.DepositRef, &info.IDPhoto, &info.KRA, &info.PassportPhoto,
			&info.PlotID, &info.EstateID, &info.AgentName, &info.PaymentPlan, &info.Deposit,
			&info.Notes, &info.CareOf, &info.ZohoBooksID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Populate file lists for template display
	splitFiles := func(s string) []string {
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
	info.DepositRefFiles = splitFiles(info.DepositRef)
	info.IDPhotoFiles = splitFiles(info.IDPhoto)
	info.KRAFiles = splitFiles(info.KRA)
	info.PassportPhotoFiles = splitFiles(info.PassportPhoto)

	// appendFiles joins new uploads with existing ones
	appendFiles := func(newFiles, existing string) string {
		if newFiles == "" {
			return existing
		}
		if existing == "" {
			return newFiles
		}
		return existing + "," + newFiles
	}

	if r.Method == http.MethodPost {
		r.ParseMultipartForm(32 << 20)

		// Allow buyer name/phone to be corrected during doc upload
		if name := strings.TrimSpace(r.FormValue("buyer_name")); name != "" {
			info.BuyerName = name
		}
		if phone := strings.TrimSpace(r.FormValue("buyer_phone")); phone != "" {
			info.BuyerPhone = phone
		}
		if email := strings.TrimSpace(r.FormValue("buyer_email")); email != "" {
			info.BuyerEmail = email
		}
		// Care Of: agent/admin-entered, shown only to them and forwarded only
		// to the Zoho CRM deal's Care_Of field — see integrations.go. Allowed
		// blank here (unlike name/phone/email above) so it can be cleared.
		info.CareOf = strings.TrimSpace(r.FormValue("care_of"))

		// Append new files to existing ones for each field
		depositRef := appendFiles(saveUploadedFiles(r, "deposit_ref"), info.DepositRef)
		idPhoto := appendFiles(saveUploadedFiles(r, "id_photo"), info.IDPhoto)
		kra := appendFiles(saveUploadedFiles(r, "kra"), info.KRA)
		passportPhoto := appendFiles(saveUploadedFiles(r, "passport_photo"), info.PassportPhoto)

		notes := strings.TrimSpace(r.FormValue("notes"))
		if pp := r.FormValue("payment_plan"); pp != "" {
			info.PaymentPlan = pp
		}

		// Deposit here is a direct edit of the running total (distinct from
		// "Generate Receipt" below, which only adds an increment) — lets
		// admin/agent correct or set the figure to what the client has
		// actually paid. Blank or unparseable input leaves it unchanged
		// rather than silently zeroing a real balance.
		oldDeposit := info.Deposit
		newDeposit := info.Deposit
		if depositStr := strings.TrimSpace(r.FormValue("deposit")); depositStr != "" {
			if depVal, perr := strconv.ParseFloat(depositStr, 64); perr == nil && depVal >= 0 {
				newDeposit = fmt.Sprintf("%.2f", depVal)
			}
		}
		info.Deposit = newDeposit

		db.Exec(`UPDATE prop_bookings SET buyer_name=?, buyer_phone=?, buyer_email=?, payment_plan=?, deposit=?, deposit_ref=?, id_photo=?, kra=?, passport_photo=?, notes=?, care_of=? WHERE id=?`,
			info.BuyerName, info.BuyerPhone, info.BuyerEmail, info.PaymentPlan, newDeposit, depositRef, idPhoto, kra, passportPhoto, notes, info.CareOf, bookingID)
		if newDeposit != oldDeposit {
			logBooking("DEPOSIT_UPDATED", info.AgentName, info.BuyerName, info.PlotNumber+" — "+info.EstateName,
				fmt.Sprintf("deposit changed from KES %s to KES %s by %s", oldDeposit, newDeposit, getAgentName(r)))
		}

		// Push buyer info update to Zoho Books if a Books record exists
		if info.ZohoBooksID != "" {
			go func(estimateID, name, phone, email, estateName, plotNumber string) {
				if err := updateBooksContact(estimateID, name, phone, email, estateName, plotNumber); err != nil {
					log.Printf("[zoho-books] contact update error: %v", err)
				}
			}(info.ZohoBooksID, info.BuyerName, info.BuyerPhone, info.BuyerEmail, info.EstateName, info.PlotNumber)
		}

		allDocs := hasAllAttachments(bookingInfo{DepositRef: depositRef, IDPhoto: idPhoto, KRA: kra, PassportPhoto: passportPhoto})
		docStatus := "incomplete"
		if allDocs {
			docStatus = "all 4 docs present — integrations triggered"
		}
		logBooking("DOCS_UPLOADED", info.AgentName, info.BuyerName,
			info.PlotNumber+" — "+info.EstateName, docStatus)

		// When all docs are now present, check whether the deposit already meets
		// the estate's threshold too — if so, this booking moves straight to
		// Accounts review. No email fires here either way anymore — sales@/
		// systemadmin@ are notified once (without attachments) at Accounts
		// approval instead (sendAccountsApprovedEmail), not at doc-completion.
		if allDocs {
			go maybeAdvanceToAccountsReview(info.BookingID)
		}
		http.Redirect(w, r, r.URL.Path+"?uploaded=1", http.StatusFound)
		return
	}

	// Fetch existing booking receipts for this booking
	type bookingReceipt struct {
		ID            int
		ReceiptNumber string
		Amount        string
		CreatedAt     string
	}
	var receipts []bookingReceipt
	if rrows, rerr := db.Query(`SELECT id, receipt_number, CAST(amount AS CHAR), DATE_FORMAT(created_at,'%d %b %Y %H:%i') FROM prop_booking_receipts WHERE booking_id=? ORDER BY id DESC`, info.BookingID); rerr == nil {
		defer rrows.Close()
		for rrows.Next() {
			var br bookingReceipt
			rrows.Scan(&br.ID, &br.ReceiptNumber, &br.Amount, &br.CreatedAt)
			receipts = append(receipts, br)
		}
	}

	receiptPrefix := "/agent"
	if isAdmin {
		receiptPrefix = "/admin"
	}

	data := map[string]any{
		"Title":  "Upload Documents — " + info.BuyerName,
		"Active": "booked-plots",
		"Info":   info,
		// Anchored so "← Back" lands back on this exact row instead of the
		// top of the list — same pattern as the row-action redirects.
		"BackURL":       fmt.Sprintf("%s#booking-%d", backURL, info.BookingID),
		"SaveOK":        r.URL.Query().Get("uploaded") == "1",
		"Receipts":      receipts,
		"ReceiptPrefix": receiptPrefix,
	}
	if isAdmin {
		renderAdmin(w, r, tmplName, data)
	} else {
		renderAgent(w, r, tmplName, data)
	}
}

// bookingAttachmentDeleteHandler removes a single filename from one of a
// booking's KYC document fields (deposit_ref, id_photo, kra, passport_photo)
// and deletes the file from disk — lets admin or agent correct a wrongly-
// uploaded document from the booking-attachments page instead of it staying
// stuck alongside the correct one forever. Shared by both
// /admin/booking-delete-attachment/{id} and
// /agent/booking-delete-attachment/{id}; reuses docFieldColumn's existing
// field whitelist (the same one Plots Overview's delete-attachment endpoint
// uses) via the "booked" tab, which maps to prop_bookings' four KYC columns.
func bookingAttachmentDeleteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var bookingID string
	switch {
	case strings.HasPrefix(r.URL.Path, "/admin/booking-delete-attachment/"):
		bookingID = pathSegment("/admin/booking-delete-attachment/", r.URL.Path)
	case strings.HasPrefix(r.URL.Path, "/agent/booking-delete-attachment/"):
		bookingID = pathSegment("/agent/booking-delete-attachment/", r.URL.Path)
	}
	r.ParseForm()
	field := r.FormValue("field")
	filename := strings.TrimSpace(r.FormValue("filename"))

	_, column, ok := docFieldColumn("booked", field)
	if !ok || bookingID == "" || filename == "" {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid request"})
		return
	}

	var current string
	if err := db.QueryRow(fmt.Sprintf("SELECT COALESCE(%s,'') FROM prop_bookings WHERE id=?", column), bookingID).Scan(&current); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "booking not found"})
		return
	}

	var remaining []string
	found := false
	for _, f := range strings.Split(current, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f == filename {
			found = true
			continue
		}
		remaining = append(remaining, f)
	}
	if !found {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file not found on this booking"})
		return
	}

	if _, err := db.Exec(fmt.Sprintf("UPDATE prop_bookings SET %s=? WHERE id=?", column), strings.Join(remaining, ","), bookingID); err != nil {
		log.Printf("booking attachment delete: update failed: %v", err)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "database update failed"})
		return
	}
	if err := os.Remove(filepath.Join(uploadsDir, filename)); err != nil && !os.IsNotExist(err) {
		log.Printf("booking attachment delete: file remove warning for %s: %v", filename, err)
	}
	logBooking("ATTACHMENT_REMOVED", getAgentName(r), "", fmt.Sprintf("booking %s", bookingID), fmt.Sprintf("removed %s from %s", filename, field))

	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func adminBookingAttachmentsHandler(w http.ResponseWriter, r *http.Request) {
	bookingAttachmentsHandler(w, r, "admin_booking_attachments.html", "/admin/booked-plots", true)
}

func agentBookingAttachmentsHandler(w http.ResponseWriter, r *http.Request) {
	bookingAttachmentsHandler(w, r, "agent_booking_attachments.html", "/agent/bookings", false)
}

// bookingReceiptGenerateHandler creates a new booking confirmation receipt record.
// bookingReceiptGenerateHandler both records a printable payment receipt AND
// tops up the booking's running deposit total by the same amount — a receipt
// generated here is the "add a payment reference + add the deposit amount"
// action, since the two are the same real-world event (a payment came in).
// An optional payment-reference file gets appended to deposit_ref alongside
// the initial deposit proof, and the accounts-review trigger is re-checked
// afterward in case this top-up pushes the deposit to/above the estate's
// threshold.
func bookingReceiptGenerateHandler(w http.ResponseWriter, r *http.Request, isAdmin bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(32 << 20)
	bookingIDStr := strings.TrimSpace(r.FormValue("booking_id"))
	amountStr := strings.TrimSpace(r.FormValue("amount"))
	if bookingIDStr == "" || amountStr == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	// Verify booking exists
	var bookingID int
	var existingDepositRef string
	if err := db.QueryRow(`SELECT id, COALESCE(deposit_ref,'') FROM prop_bookings WHERE id=?`, bookingIDStr).Scan(&bookingID, &existingDepositRef); err != nil {
		http.NotFound(w, r)
		return
	}

	// Generate sequential receipt number: PPL{year}{padded_seq}
	year := time.Now().Year()
	var seq int
	db.QueryRow(`SELECT COUNT(*) FROM prop_booking_receipts WHERE YEAR(created_at)=?`, year).Scan(&seq)
	receiptNumber := fmt.Sprintf("PPSL%d%03d", year, seq+1)

	var newID int64
	res, err := db.Exec(`INSERT INTO prop_booking_receipts (booking_id, amount, receipt_number) VALUES (?,?,?)`,
		bookingID, amountStr, receiptNumber)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	newID, _ = res.LastInsertId()

	// Payment reference file is optional — the amount alone still tops up the deposit.
	if newRef := saveUploadedFiles(r, "payment_reference"); newRef != "" {
		depositRef := newRef
		if existingDepositRef != "" {
			depositRef = existingDepositRef + "," + newRef
		}
		db.Exec(`UPDATE prop_bookings SET deposit_ref=? WHERE id=?`, depositRef, bookingID)
	}
	db.Exec(`UPDATE prop_bookings SET deposit = COALESCE(deposit,0) + ? WHERE id=?`, amountStr, bookingID)

	go maybeAdvanceToAccountsReview(bookingID)

	prefix := "/agent"
	if isAdmin {
		prefix = "/admin"
	}
	http.Redirect(w, r, fmt.Sprintf("%s/booking-receipt/%d", prefix, newID), http.StatusFound)
}

// bookingReceiptViewHandler renders a previously generated booking receipt.
func bookingReceiptViewHandler(w http.ResponseWriter, r *http.Request, tmplName, prefix string, isAdmin bool) {
	receiptID := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix+"/booking-receipt/"), "/")
	if receiptID == "" {
		http.NotFound(w, r)
		return
	}

	type receiptView struct {
		ReceiptID     int
		ReceiptNumber string
		Amount        string
		CreatedAt     string
		BuyerName     string
		BuyerPhone    string
		BuyerEmail    string
		AgentName     string
		EstateName    string
		PlotNumber    string
		PaymentPlan   string
		BookingID     int
	}
	var rv receiptView
	err := db.QueryRow(`
		SELECT br.id, br.receipt_number, CAST(br.amount AS CHAR), DATE_FORMAT(br.created_at,'%d %b %Y %H:%i'),
		       b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       COALESCE(b.agent_name,''), e.name, p.plot_number,
		       COALESCE(b.payment_plan,''), b.id
		FROM prop_booking_receipts br
		JOIN prop_bookings b ON b.id = br.booking_id
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE br.id = ?`, receiptID).
		Scan(&rv.ReceiptID, &rv.ReceiptNumber, &rv.Amount, &rv.CreatedAt,
			&rv.BuyerName, &rv.BuyerPhone, &rv.BuyerEmail,
			&rv.AgentName, &rv.EstateName, &rv.PlotNumber,
			&rv.PaymentPlan, &rv.BookingID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	backURL := fmt.Sprintf("%s/booking/%d/attachments", prefix, rv.BookingID)
	pageData := map[string]any{
		"Title":   "Booking Receipt " + rv.ReceiptNumber,
		"Active":  "booked-plots",
		"Receipt": rv,
		"BackURL": backURL,
	}
	if isAdmin {
		renderAdmin(w, r, tmplName, pageData)
	} else {
		renderAgent(w, r, tmplName, pageData)
	}
}

func adminBookingReceiptHandler(w http.ResponseWriter, r *http.Request) {
	bookingReceiptGenerateHandler(w, r, true)
}

func adminBookingReceiptViewHandler(w http.ResponseWriter, r *http.Request) {
	bookingReceiptViewHandler(w, r, "admin_booking_receipt.html", "/admin", true)
}

func agentBookingReceiptHandler(w http.ResponseWriter, r *http.Request) {
	bookingReceiptGenerateHandler(w, r, false)
}

func agentBookingReceiptViewHandler(w http.ResponseWriter, r *http.Request) {
	bookingReceiptViewHandler(w, r, "agent_booking_receipt.html", "/agent", false)
}

func agentCancelBookingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/agent/bookings", http.StatusFound)
		return
	}
	plotID := pathSegment("/agent/bookings/cancel/", r.URL.Path)
	agentName := getAgentName(r)

	// Verify ownership before cancelling
	var cnt int
	db.QueryRow(`SELECT COUNT(*) FROM prop_bookings WHERE plot_id=? AND agent_name=? AND status='active'`, plotID, agentName).Scan(&cnt)
	if cnt > 0 {
		db.Exec(`UPDATE prop_bookings SET status='cancelled' WHERE plot_id=? AND agent_name=? AND status='active'`, plotID, agentName)
		db.Exec(`UPDATE prop_plots SET status='available' WHERE id=?`, plotID)
		var cancelPlotNumber, cancelEstateName string
		var cancelEstateID int
		if db.QueryRow(`SELECT p.plot_number, p.estate_id, e.name FROM prop_plots p JOIN prop_estates e ON e.id = p.estate_id WHERE p.id=?`, plotID).
			Scan(&cancelPlotNumber, &cancelEstateID, &cancelEstateName) == nil {
			if cancelPlotIDInt, err2 := strconv.Atoi(plotID); err2 == nil {
				logPlotStatus(cancelPlotIDInt, cancelPlotNumber, cancelEstateName, cancelEstateID, "booked", "available", agentName, "cancelled by agent")
			}
		}
	}
	http.Redirect(w, r, "/agent/bookings", http.StatusFound)
}

func agentSalesHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgentName(r)
	estateFilter := r.URL.Query().Get("estate_id")
	search := r.URL.Query().Get("search")

	type saleRow struct {
		PlotNumber string
		EstateName string
		BuyerName  string
		BuyerPhone string
		BuyerEmail string
		Amount     float64
		DateSold   string
	}

	query := `SELECT p.plot_number, e.name, s.buyer_name, COALESCE(s.buyer_phone,''), COALESCE(s.buyer_email,''), COALESCE(s.amount,0), DATE_FORMAT(s.date_sold,'%d %b %Y')
		FROM prop_sales s
		JOIN prop_plots p ON p.id=s.plot_id
		JOIN prop_estates e ON e.id=p.estate_id
		WHERE s.agent_name=?`
	args := []any{agentName}

	if estateFilter != "" && estateFilter != "0" {
		query += " AND p.estate_id=?"
		args = append(args, estateFilter)
	}
	if search != "" {
		query += " AND (s.buyer_name LIKE ? OR p.plot_number LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	query += " ORDER BY s.date_sold DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("agentSales: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var sales []saleRow
	for rows.Next() {
		var s saleRow
		if rows.Scan(&s.PlotNumber, &s.EstateName, &s.BuyerName, &s.BuyerPhone, &s.BuyerEmail, &s.Amount, &s.DateSold) == nil {
			sales = append(sales, s)
		}
	}

	eRows, _ := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`)
	type estOption struct {
		ID   int
		Name string
	}
	var estateOptions []estOption
	if eRows != nil {
		defer eRows.Close()
		for eRows.Next() {
			var e estOption
			if eRows.Scan(&e.ID, &e.Name) == nil {
				estateOptions = append(estateOptions, e)
			}
		}
	}

	renderAgent(w, r, "agent_sales.html", map[string]any{
		"Title":        "My Sales",
		"Active":       "sales",
		"Sales":        sales,
		"Estates":      estateOptions,
		"EstateFilter": estateFilter,
		"Search":       search,
	})
}

// ── Estate Edit ────────────────────────────────────────────────────────────

func adminEstateEditHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)

	var estateName, image, mutationImage, plotInfo string
	var plotPrice, depositThreshold float64
	var isRestricted, lawyerID int
	err := db.QueryRow(`SELECT name, COALESCE(image,''), COALESCE(mutation_image,''), COALESCE(prop_plotinfo,''), COALESCE(plot_price,0), COALESCE(is_restricted,0), COALESCE(deposit_threshold,0), COALESCE(lawyer_id,0) FROM prop_estates WHERE id = ?`, id).
		Scan(&estateName, &image, &mutationImage, &plotInfo, &plotPrice, &isRestricted, &depositThreshold, &lawyerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			log.Printf("estate edit: parse multipart: %v", err)
			http.Error(w, "Upload failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		newPlotInfo := r.FormValue("prop_plotinfo")
		newPlotPrice := r.FormValue("plot_price")
		newDepositThreshold := strings.TrimSpace(r.FormValue("deposit_threshold"))
		newLawyerID := strings.TrimSpace(r.FormValue("lawyer_id"))
		newIsRestricted := 0
		if r.FormValue("visibility") == "hide" {
			newIsRestricted = 1
		}

		newMutationImage := mutationImage
		saveErr := ""
		imageUpdated := false
		file, header, ferr := r.FormFile("mutation_image")
		if ferr == nil {
			defer file.Close()
			log.Printf("estate edit: received file %q size=%d", header.Filename, header.Size)
			ext := filepath.Ext(header.Filename)
			base := strings.TrimSuffix(header.Filename, ext)
			safe := strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
					return r
				}
				return '_'
			}, base)
			filename := fmt.Sprintf("mutation_%d_%s%s", time.Now().Unix(), safe, ext)
			dst, oerr := os.Create(filepath.Join(uploadsDir, filename))
			if oerr != nil {
				log.Printf("estate edit: create file %s: %v", filepath.Join(uploadsDir, filename), oerr)
				saveErr = fmt.Sprintf("Could not save image file: %v", oerr)
			} else {
				if _, cerr := io.Copy(dst, file); cerr != nil {
					log.Printf("estate edit: copy file: %v", cerr)
					saveErr = fmt.Sprintf("Image upload incomplete: %v", cerr)
				} else {
					newMutationImage = filename
					imageUpdated = true
					log.Printf("estate edit: saved mutation image %s", filename)
				}
				dst.Close()
			}
		} else if ferr != http.ErrMissingFile {
			log.Printf("estate edit: FormFile error: %v", ferr)
		}

		// Always update the text fields; only update mutation_image if a new one was saved.
		var dbErr string
		if newDepositThreshold == "" {
			dbErr = "Deposit threshold is required"
		} else if newLawyerID == "" {
			dbErr = "Assigned lawyer is required"
		} else if imageUpdated {
			if _, uerr := db.Exec(`UPDATE prop_estates SET name=?, prop_plotinfo=?, mutation_image=?, plot_price=?, is_restricted=?, deposit_threshold=?, lawyer_id=? WHERE id=?`,
				name, newPlotInfo, newMutationImage, newPlotPrice, newIsRestricted, newDepositThreshold, newLawyerID, id); uerr != nil {
				log.Printf("estate edit update (with image): %v", uerr)
				dbErr = fmt.Sprintf("Database error: %v", uerr)
			}
		} else {
			if _, uerr := db.Exec(`UPDATE prop_estates SET name=?, prop_plotinfo=?, plot_price=?, is_restricted=?, deposit_threshold=?, lawyer_id=? WHERE id=?`,
				name, newPlotInfo, newPlotPrice, newIsRestricted, newDepositThreshold, newLawyerID, id); uerr != nil {
				log.Printf("estate edit update: %v", uerr)
				dbErr = fmt.Sprintf("Database error: %v", uerr)
			}
		}
		if dbErr != "" && saveErr == "" {
			saveErr = dbErr
		}

		if saveErr != "" {
			displayImage := mutationImage
			if displayImage == "" {
				displayImage = image
			}
			// Reload zones for re-render
			zonesFallback := []zoneDisplay{}
			zonesJSONFallback, _ := json.Marshal(zonesFallback)
			renderAdmin(w, r, "admin_estate_edit.html", map[string]any{
				"Title":            "Edit " + estateName,
				"Active":           "estates",
				"EstateName":       name,
				"EstateID":         id,
				"PlotInfo":         strings.ReplaceAll(newPlotInfo, "\r", ""),
				"PlotPrice":        newPlotPrice,
				"DepositThreshold": newDepositThreshold,
				"LawyerID":         newLawyerID,
				"Lawyers":          loadLawyers(),
				"DisplayImage":     displayImage,
				"ZonesJSON":        template.JS(zonesJSONFallback),
				"SaveError":        saveErr,
			})
			return
		}

		redirect := "/admin/estate/" + id + "/edit?saved=1"
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}

	// GET: load zones for editor
	zones := []zoneDisplay{}
	zRows, _ := db.Query(`
		SELECT pz.plot_number, COALESCE(pz.points_json,'[]'), COALESCE(pp.status,'available')
		FROM prop_plot_zones pz
		LEFT JOIN prop_plots pp ON pp.estate_id = pz.estate_id AND pp.plot_number = pz.plot_number
		WHERE pz.estate_id = ?
		ORDER BY pz.id`, id)
	if zRows != nil {
		defer zRows.Close()
		for zRows.Next() {
			var pn, pj, st string
			if err := zRows.Scan(&pn, &pj, &st); err == nil {
				var pts []pointXY
				json.Unmarshal([]byte(pj), &pts)
				zones = append(zones, zoneDisplay{PlotNumber: pn, Status: st, Points: pts})
			}
		}
	}
	zonesJSON, _ := json.Marshal(zones)

	plotInfo = strings.ReplaceAll(plotInfo, `\r\n`, "\n")
	plotInfo = strings.ReplaceAll(plotInfo, `\n`, "\n")

	displayImage := mutationImage
	if displayImage == "" {
		displayImage = image
	}

	renderAdmin(w, r, "admin_estate_edit.html", map[string]any{
		"Title":            "Edit " + estateName,
		"Active":           "estates",
		"EstateName":       estateName,
		"EstateID":         id,
		"PlotInfo":         strings.ReplaceAll(plotInfo, "\r", ""),
		"PlotPrice":        plotPrice,
		"DepositThreshold": depositThreshold,
		"LawyerID":         strconv.Itoa(lawyerID),
		"Lawyers":          loadLawyers(),
		"IsRestricted":     isRestricted == 1,
		"DisplayImage":     displayImage,
		"ZonesJSON":        template.JS(zonesJSON),
		"SaveOK":           r.URL.Query().Get("saved") == "1",
	})
}

// ── Estate Delete ───────────────────────────────────────────────────────────

func adminEstateDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/estates", http.StatusFound)
		return
	}
	id := r.Context().Value(ctxID).(string)
	db.Exec(`DELETE FROM prop_plot_zones WHERE estate_id=?`, id)
	db.Exec(`DELETE FROM prop_bookings WHERE estate_id=?`, id)
	db.Exec(`DELETE FROM prop_sales WHERE estate_id=?`, id)
	db.Exec(`DELETE FROM prop_plots WHERE estate_id=?`, id)
	db.Exec(`DELETE FROM prop_estates WHERE id=?`, id)
	http.Redirect(w, r, "/admin/estates", http.StatusFound)
}

// ── Save Plot Zones (JSON API) ──────────────────────────────────────────────

func adminSaveZonesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := r.Context().Value(ctxID).(string)

	var zones []struct {
		PlotNumber string    `json:"plot_number"`
		Points     []pointXY `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&zones); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	db.Exec(`DELETE FROM prop_plot_zones WHERE estate_id=?`, id)
	for _, z := range zones {
		ptsJSON, _ := json.Marshal(z.Points)
		db.Exec(`INSERT INTO prop_plot_zones (estate_id, plot_number, points_json) VALUES (?,?,?)`,
			id, z.PlotNumber, string(ptsJSON))
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// ── Private Estates ─────────────────────────────────────────────────────────

func adminPrivateEstatesHandler(w http.ResponseWriter, r *http.Request) {
	isSA := getRole(r) == roleSystemAdmin

	var formErr string
	if r.Method == http.MethodPost && isSA {
		r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			formErr = "Estate name is required"
		} else {
			res, err := db.Exec(`INSERT INTO prop_estates (name, is_restricted) VALUES (?, 1)`, name)
			if err != nil {
				formErr = "Database error creating estate"
			} else {
				newID, _ := res.LastInsertId()
				http.Redirect(w, r, fmt.Sprintf("/admin/estate/%d/edit", newID), http.StatusFound)
				return
			}
		}
	}

	type estateRow struct {
		ID        int
		Name      string
		Total     int
		Available int
		Booked    int
		SaSigned  int
		Sold      int
	}
	// Access to the Private Estates module is already gated at the route
	// level (requirePerm admin.private_estates), so anyone who can reach
	// this page sees every private estate — not just estates they have an
	// individual prop_restricted_access grant for. That per-estate ACL
	// predates the page-level permission and is only still used by the
	// legacy grant/revoke screen (see adminPrivateEstateAccessHandler); a
	// newly added private estate has no rows there yet, which was silently
	// hiding it from anyone but system_admin.
	q := `SELECT e.id, e.name, COUNT(p.id),
		COALESCE(SUM(p.status='available'),0), COALESCE(SUM(p.status='booked'),0),
		COALESCE(SUM(p.status='sa_signed'),0), COALESCE(SUM(p.status='sold'),0)
		FROM prop_estates e LEFT JOIN prop_plots p ON p.estate_id=e.id
		WHERE COALESCE(e.is_restricted,0)=1
		GROUP BY e.id, e.name ORDER BY e.name`
	var qArgs []any
	type privateStats struct {
		TotalEstates int
		Available    int
		Booked       int
		SaSigned     int
		Sold         int
	}
	// Stats cover ALL estates regardless of visibility
	var stats privateStats
	db.QueryRow(`SELECT COUNT(*) FROM prop_estates`).Scan(&stats.TotalEstates)
	db.QueryRow(`SELECT COALESCE(SUM(status='available'),0), COALESCE(SUM(status='booked'),0), COALESCE(SUM(status='sa_signed'),0), COALESCE(SUM(status='sold'),0) FROM prop_plots`).
		Scan(&stats.Available, &stats.Booked, &stats.SaSigned, &stats.Sold)

	var estates []estateRow
	if rows, _ := db.Query(q, qArgs...); rows != nil {
		defer rows.Close()
		for rows.Next() {
			var e estateRow
			if rows.Scan(&e.ID, &e.Name, &e.Total, &e.Available, &e.Booked, &e.SaSigned, &e.Sold) == nil {
				estates = append(estates, e)
			}
		}
	}
	renderAdmin(w, r, "admin_private_estates.html", map[string]any{
		"Title":     "Private Estates",
		"Active":    "private-estates",
		"Estates":   estates,
		"Stats":     stats,
		"IsSA":      isSA,
		"FormError": formErr,
	})
}

func adminPrivateEstateRouter(w http.ResponseWriter, r *http.Request) {
	estateID := pathSegment("/admin/private-estate/", r.URL.Path)
	suffix := strings.TrimPrefix(r.URL.Path, "/admin/private-estate/"+estateID)
	isSA := getRole(r) == roleSystemAdmin

	// Viewing an individual private estate is gated the same way as the
	// listing page: admin.private_estates read access (already enforced by
	// requirePerm at the mux level) is enough — no per-estate ACL check.
	// Granting/revoking that per-estate ACL, and adding plots, stay
	// system_admin-only below.
	r = withID(r, estateID)
	switch suffix {
	case "", "/", "/plots":
		adminPrivateEstatePlotsHandler(w, r)
	case "/access", "/access/grant", "/access/revoke":
		if !isSA {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		adminPrivateEstateAccessHandler(w, r, suffix)
	case "/add-plots":
		if !isSA {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		adminPrivateEstateAddPlotsHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func adminPrivateEstatePlotsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "available"
	}
	var estateName string
	if err := db.QueryRow(`SELECT name FROM prop_estates WHERE id=?`, id).Scan(&estateName); err != nil {
		http.NotFound(w, r)
		return
	}
	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	if cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id); cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}
	type plotRow struct {
		ID         int
		Number     string
		Status     string
		BuyerName  string
		BuyerPhone string
		AgentName  string
		DateBooked string
	}
	var plots []plotRow
	if status == "booked" || status == "sa_signed" {
		if pRows, _ := db.Query(`
			SELECT p.id, p.plot_number, p.status,
				COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''),
				COALESCE(b.agent_name,''), COALESCE(DATE_FORMAT(b.date_booked,'%d %b %Y %H:%i'),'')
			FROM prop_plots p
			LEFT JOIN prop_bookings b ON b.id = (
				SELECT id FROM prop_bookings WHERE plot_id=p.id
				ORDER BY id DESC LIMIT 1
			)
			WHERE p.estate_id=? AND p.status=? ORDER BY p.id`, id, status); pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p plotRow
				if pRows.Scan(&p.ID, &p.Number, &p.Status, &p.BuyerName, &p.BuyerPhone, &p.AgentName, &p.DateBooked) == nil {
					plots = append(plots, p)
				}
			}
		}
	} else {
		if pRows, _ := db.Query(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id=? AND status=? ORDER BY id`, id, status); pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p plotRow
				if pRows.Scan(&p.ID, &p.Number, &p.Status) == nil {
					plots = append(plots, p)
				}
			}
		}
	}
	isSA := getRole(r) == roleSystemAdmin
	renderAdmin(w, r, "admin_private_estate_plots.html", map[string]any{
		"Title":      estateName + " – Plots",
		"Active":     "private-estates",
		"EstateName": estateName,
		"EstateID":   id,
		"Status":     status,
		"Plots":      plots,
		"Counts":     counts,
		"IsSA":       isSA,
	})
}

func adminPrivateEstateAddPlotsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := r.Context().Value(ctxID).(string)
	r.ParseForm()
	plotNumbers := strings.Split(r.FormValue("plot_numbers"), "\n")
	for _, raw := range plotNumbers {
		pn := strings.TrimSpace(raw)
		if pn == "" {
			continue
		}
		db.Exec(`INSERT IGNORE INTO prop_plots (estate_id, plot_number, status) VALUES (?,?,'available')`, id, pn)
	}
	http.Redirect(w, r, "/admin/private-estate/"+id+"/plots", http.StatusFound)
}

func adminPrivateEstateAccessHandler(w http.ResponseWriter, r *http.Request, suffix string) {
	id := r.Context().Value(ctxID).(string)

	var estateName string
	if err := db.QueryRow(`SELECT name FROM prop_estates WHERE id=?`, id).Scan(&estateName); err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		targetUserID := r.FormValue("user_id")
		if suffix == "/access/grant" && targetUserID != "" {
			db.Exec(`INSERT IGNORE INTO prop_restricted_access (estate_id, user_id) VALUES (?,?)`, id, targetUserID)
		} else if suffix == "/access/revoke" && targetUserID != "" {
			db.Exec(`DELETE FROM prop_restricted_access WHERE estate_id=? AND user_id=?`, id, targetUserID)
		}
		http.Redirect(w, r, "/admin/private-estate/"+id+"/access", http.StatusFound)
		return
	}

	type accessUser struct {
		UserID string
		Name   string
		Email  string
		Role   string
	}
	var grantedUsers []accessUser
	if rows, _ := db.Query(`
		SELECT a.id, a.name, a.email, a.role FROM prop_agents a
		JOIN prop_restricted_access ra ON ra.user_id=CAST(a.id AS CHAR)
		WHERE ra.estate_id=? ORDER BY a.name`, id); rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u accessUser
			if rows.Scan(&u.UserID, &u.Name, &u.Email, &u.Role) == nil {
				grantedUsers = append(grantedUsers, u)
			}
		}
	}
	var availableUsers []accessUser
	if rows, _ := db.Query(`
		SELECT id, name, email, role FROM prop_agents
		WHERE role != 'system_admin'
		AND CAST(id AS CHAR) NOT IN (
			SELECT user_id FROM prop_restricted_access WHERE estate_id=?
		)
		ORDER BY role, name`, id); rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u accessUser
			if rows.Scan(&u.UserID, &u.Name, &u.Email, &u.Role) == nil {
				availableUsers = append(availableUsers, u)
			}
		}
	}
	renderAdmin(w, r, "admin_private_estate_access.html", map[string]any{
		"Title":          "Access – " + estateName,
		"Active":         "private-estates",
		"EstateName":     estateName,
		"EstateID":       id,
		"GrantedUsers":   grantedUsers,
		"AvailableUsers": availableUsers,
	})
}

func agentPrivateEstatesHandler(w http.ResponseWriter, r *http.Request) {
	type estateRow struct {
		ID        int
		Name      string
		Total     int
		Available int
	}
	var estates []estateRow
	// Same rationale as adminPrivateEstatesHandler: agent.private_estates
	// read access (already enforced at the route level) is the sole gate —
	// not an individual prop_restricted_access grant per estate, which was
	// leaving newly added private estates invisible to anyone but
	// system_admin.
	if rows, _ := db.Query(`
		SELECT e.id, e.name, COUNT(p.id), COALESCE(SUM(p.status='available'),0)
		FROM prop_estates e LEFT JOIN prop_plots p ON p.estate_id=e.id
		WHERE COALESCE(e.is_restricted,0)=1
		GROUP BY e.id, e.name ORDER BY e.name`); rows != nil {
		defer rows.Close()
		for rows.Next() {
			var e estateRow
			if rows.Scan(&e.ID, &e.Name, &e.Total, &e.Available) == nil {
				estates = append(estates, e)
			}
		}
	}
	renderAgent(w, r, "agent_private_estates.html", map[string]any{
		"Title":   "Private Estates",
		"Active":  "private-estates",
		"Estates": estates,
	})
}

func agentPrivateEstateRouter(w http.ResponseWriter, r *http.Request) {
	estateID := pathSegment("/agent/private-estate/", r.URL.Path)
	// agent.private_estates read access (already enforced at the route
	// level) is the sole gate here too — see agentPrivateEstatesHandler.
	r = withID(r, estateID)
	agentPrivateEstatePlotsHandler(w, r)
}

func agentPrivateEstatePlotsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxID).(string)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "available"
	}
	var estateName string
	if err := db.QueryRow(`SELECT name FROM prop_estates WHERE id=?`, id).Scan(&estateName); err != nil {
		http.NotFound(w, r)
		return
	}
	counts := map[string]int{"available": 0, "booked": 0, "sa_signed": 0, "sold": 0}
	if cRows, _ := db.Query(`SELECT status, COUNT(*) FROM prop_plots WHERE estate_id=? GROUP BY status`, id); cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var s string
			var c int
			if cRows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}
	type plotRow struct {
		ID     int
		Number string
		Status string
	}
	var plots []plotRow
	if pRows, _ := db.Query(`SELECT id, plot_number, status FROM prop_plots WHERE estate_id=? AND status=? ORDER BY id`, id, status); pRows != nil {
		defer pRows.Close()
		for pRows.Next() {
			var p plotRow
			if pRows.Scan(&p.ID, &p.Number, &p.Status) == nil {
				plots = append(plots, p)
			}
		}
	}
	renderAgent(w, r, "agent_private_estate_plots.html", map[string]any{
		"Title":      estateName + " – Plots",
		"Active":     "private-estates",
		"EstateName": estateName,
		"EstateID":   id,
		"Status":     status,
		"Plots":      plots,
		"Counts":     counts,
	})
}

// ── Render helper ───────────────────────────────────────────────────────────

func render(w http.ResponseWriter, name string, data any) {
	t, ok := pageTemplates[name]
	if !ok {
		log.Printf("render: no template registered for %q", name)
		http.Error(w, "Page not found.", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "Something went wrong. Check server logs.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}
