package main

import (
	"net/http"
	"strings"
)

const roleSystemAdmin = "system_admin"
const idCookie = "pp_id"

// ── Feature catalogue ─────────────────────────────────────────────────────────

type featureDef struct {
	Key    string
	Label  string
	Group  string // "admin" or "agent"
	Module string // display section, e.g. "Marketers Portal"
}

// featureRegistry is the global list of all registered features.
// Core features are initialised here; sub-packages call RegisterFeature
// during their Init() to add their own.
var featureRegistry = []featureDef{
	// Marketers Portal – Admin
	{"admin.dashboard", "Dashboard", "admin", "Marketers Portal"},
	{"admin.estates", "Estates", "admin", "Marketers Portal"},
	{"admin.booked_plots", "Booked Plots", "admin", "Marketers Portal"},
	{"admin.signed_plots", "SA Signed Plots", "admin", "Marketers Portal"},
	{"admin.sold_plots", "Sold Plots", "admin", "Marketers Portal"},
	{"admin.pending_projects", "Pending Projects", "admin", "Marketers Portal"},
	{"admin.completed_projects", "Completed Projects", "admin", "Marketers Portal"},
	{"admin.export_reports", "Export Reports", "admin", "Marketers Portal"},
	// (admin.create_user removed: user management moved to the system_admin-only Settings module.)
	{"admin.private_estates", "Private Estates", "admin", "Marketers Portal"},
	{"admin.plots_overview", "Plots Overview", "admin", "Marketers Portal"},
	// Lets a system_admin grant an admin staff member access to the
	// Accounts review module (see canAccessAccounts in main.go) without
	// creating them a separate accounts-only login.
	{"admin.accounts_access", "Access Accounts Module", "admin", "Marketers Portal"},
	// Plot status transitions
	// (booked -> sa_signed removed: that transition now only happens via the
	// Accounts + Legal review chain, or the system_admin-only force override.)
	{"admin.booked_to_available", "Booked → Available", "admin", "Marketers Portal"},
	{"admin.extend_booking", "Extend Booking Deadline", "admin", "Marketers Portal"},
	{"admin.signed_to_sold", "Signed → Sold", "admin", "Marketers Portal"},
	{"admin.signed_to_available", "Signed → Available", "admin", "Marketers Portal"},
	{"admin.sold_to_available", "Sold → Available", "admin", "Marketers Portal"},
	// Conveyancing (uploading + auto-sold also needs admin.signed_to_sold)
	{"admin.payment_plans", "Conveyancing", "admin", "Marketers Portal"},
	{"admin.payment_plans_conveyancing", "Conveyancing: Upload Consent/Transfer/Title", "admin", "Marketers Portal"},
	// Marketers Portal – Agent
	{"agent.dashboard", "Dashboard", "agent", "Marketers Portal"},
	{"agent.estates", "Browse Estates", "agent", "Marketers Portal"},
	{"agent.bookings", "My Bookings", "agent", "Marketers Portal"},
	{"agent.signed_plots", "My Signed Plots", "agent", "Marketers Portal"},
	{"agent.sales", "My Sales", "agent", "Marketers Portal"},
	{"agent.private_estates", "Private Estates", "agent", "Marketers Portal"},
	// Accounts Module
	{"accounts.access", "Accounts Module", "admin", "Accounts"},
	// Legal Module
	{"legal.access", "Legal Module", "admin", "Legal"},
	// Coming-soon modules — register now so they can be pre-granted via Modules page
	{"asset_mgmt.access", "Asset Management", "admin", "Asset Management"},
	{"leave_mgmt.access", "Leave Management", "admin", "Leave Management"},
	{"hr_mgmt.access", "HR Management", "admin", "HR Management"},
	{"task_mgmt.access", "Task Management", "admin", "Task Management"},
}

// RegisterFeature lets sub-packages (e.g. vanbooking) add their own features
// to the global registry. Call this inside the sub-package's Init().
func RegisterFeature(key, label, group, module string) {
	featureRegistry = append(featureRegistry, featureDef{key, label, group, module})
}

// ── DB init ───────────────────────────────────────────────────────────────────

func initPermissionTables() {
	db.Exec(`CREATE TABLE IF NOT EXISTS prop_permissions (
		id        INT AUTO_INCREMENT PRIMARY KEY,
		user_id   INT NOT NULL,
		feature   VARCHAR(100) NOT NULL,
		can_read  TINYINT(1) DEFAULT 0,
		can_write TINYINT(1) DEFAULT 0,
		UNIQUE KEY uq_user_feature (user_id, feature)
	)`)

	// Ensure the role column accepts every role currently in use (it's an ENUM).
	db.Exec(`ALTER TABLE prop_agents MODIFY COLUMN role ENUM('admin','agent','system_admin','accounts','legal') DEFAULT 'agent'`)

	// Ensure the system admin account has the correct role.
	db.Exec(`UPDATE prop_agents SET role='system_admin' WHERE email='systemadmin@proproperty.co.ke'`)

	// Blocked accounts are rejected at login (see loginHandler) — set/cleared
	// from Settings → Users, either the quick row toggle or the Edit page.
	db.Exec(`ALTER TABLE prop_agents ADD COLUMN blocked TINYINT(1) NOT NULL DEFAULT 0`)
}

// ── Session helpers ───────────────────────────────────────────────────────────

func getUserID(r *http.Request) string {
	c, err := r.Cookie(idCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// ── Permission check ──────────────────────────────────────────────────────────

// hasPermission returns true if the user (by ID) has at least the requested
// access level for the feature. "write" implies "read".
func hasPermission(userID, feature, access string) bool {
	if userID == "" {
		return false
	}
	var canRead, canWrite int
	db.QueryRow(
		`SELECT can_read, can_write FROM prop_permissions WHERE user_id=? AND feature=?`,
		userID, feature,
	).Scan(&canRead, &canWrite)
	if access == "write" {
		return canWrite == 1
	}
	return canRead == 1 || canWrite == 1
}

// loadUserPerms returns a flat map  "feature.read" / "feature.write" → true
// for use in templates.
func loadUserPerms(userID string) map[string]bool {
	out := map[string]bool{}
	rows, err := db.Query(
		`SELECT feature, can_read, can_write FROM prop_permissions WHERE user_id=?`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var feat string
		var r, w int
		rows.Scan(&feat, &r, &w)
		if r == 1 {
			out[feat+".read"] = true
		}
		if w == 1 {
			out[feat+".write"] = true
			out[feat+".read"] = true // write implies read
		}
	}
	return out
}

// hasAnyModulePermission returns true if the user has at least one granted
// feature in the named module (e.g. "Marketers Portal", "Van Booking").
func hasAnyModulePermission(uid, module string) bool {
	if uid == "" {
		return false
	}
	var keys []string
	for _, f := range featureRegistry {
		if f.Module == module {
			keys = append(keys, f.Key)
		}
	}
	if len(keys) == 0 {
		return false
	}
	ph := strings.Repeat("?,", len(keys))
	ph = ph[:len(ph)-1]
	args := make([]any, 0, 1+len(keys))
	args = append(args, uid)
	for _, k := range keys {
		args = append(args, k)
	}
	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM prop_permissions WHERE user_id=? AND feature IN ("+ph+") AND can_read=1", args...).Scan(&cnt)
	return cnt > 0
}

// hasAnyPrivateEstateAccess returns true if the user has at least one row in prop_restricted_access.
func hasAnyPrivateEstateAccess(userID string) bool {
	if userID == "" {
		return false
	}
	var cnt int
	db.QueryRow(`SELECT COUNT(*) FROM prop_restricted_access WHERE user_id=?`, userID).Scan(&cnt)
	return cnt > 0
}

// requirePerm middleware: system_admin passes freely; others need the feature
// enabled (read or write). "access" should be "read" or "write".
func requirePerm(feature, access string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := getRole(r)
		if role == roleSystemAdmin {
			next.ServeHTTP(w, r)
			return
		}
		if !hasPermission(getUserID(r), feature, access) {
			w.WriteHeader(http.StatusForbidden)
			render(w, "access_denied", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── renderAdmin ───────────────────────────────────────────────────────────────

// renderAdmin injects common sidebar/role data into admin page renders.
func renderAdmin(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	role := getRole(r)
	isSA := role == roleSystemAdmin
	uid := getUserID(r)
	data["IsSystemAdmin"] = isSA
	data["UserRole"] = role
	data["CanSeePrivateEstates"] = isSA || hasPermission(uid, "admin.private_estates", "read") || hasAnyPrivateEstateAccess(uid)
	data["CanSeePlotsOverview"] = isSA || hasPermission(uid, "admin.plots_overview", "read")
	data["CanSeePaymentPlans"] = isSA || hasPermission(uid, "admin.payment_plans", "read")
	render(w, name, data)
}

// renderAgent injects common sidebar permission flags into agent page renders.
func renderAgent(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	uid := getUserID(r)
	data["CanSeePrivateEstates"] = hasPermission(uid, "agent.private_estates", "read") || hasAnyPrivateEstateAccess(uid)
	render(w, name, data)
}

// ── Permission template types ────────────────────────────────────────────────
// Shared by the Settings → Modules page (settingsModulesHandler in
// settings.go), which superseded the old /admin/permissions and
// per-user-list pages — same underlying prop_permissions table, one unified
// editor covering every registered module (Marketers Portal, Van Booking,
// Staff Welfare, ...) instead of three separate scoped pages.

// moduleSection groups features by module for the permissions template.
type moduleSection struct {
	Name       string
	AdminFeats []featureWithState
	AgentFeats []featureWithState
}

type featureWithState struct {
	featureDef
	CanRead  bool
	CanWrite bool
}
