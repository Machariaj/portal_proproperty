package main

import (
	"fmt"
	"log"
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
	{"admin.create_user", "User Management", "admin", "Marketers Portal"},
	{"admin.permissions", "Manage Permissions", "admin", "Marketers Portal"},
	{"admin.private_estates", "Private Estates", "admin", "Marketers Portal"},
	{"admin.plots_overview", "Plots Overview", "admin", "Marketers Portal"},
	// Plot status transitions
	{"admin.booked_to_signed", "Booked → SA Signed", "admin", "Marketers Portal"},
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
	{"agent.sales", "My Sales", "agent", "Marketers Portal"},
	{"agent.private_estates", "Private Estates", "agent", "Marketers Portal"},
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

	// Ensure the role column accepts 'system_admin' (it may be an ENUM).
	db.Exec(`ALTER TABLE prop_agents MODIFY COLUMN role ENUM('admin','agent','system_admin') DEFAULT 'agent'`)

	// Ensure the system admin account has the correct role.
	db.Exec(`UPDATE prop_agents SET role='system_admin' WHERE email='systemadmin@proproperty.co.ke'`)
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
	data["CanManagePerms"] = isSA || hasPermission(uid, "admin.permissions", "read")
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

// ── Permissions list (system_admin only) ──────────────────────────────────────

func adminPermissionsListHandler(w http.ResponseWriter, r *http.Request) {
	role := getRole(r)
	if role != roleSystemAdmin && !hasPermission(getUserID(r), "admin.permissions", "read") {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	type userRow struct {
		ID    int
		Name  string
		Email string
		Role  string
	}
	rows, _ := db.Query(
		`SELECT id, name, email, role FROM prop_agents WHERE role != 'system_admin' ORDER BY role, name`)
	var users []userRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u userRow
			rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role)
			users = append(users, u)
		}
	}
	renderAdmin(w, r, "admin_permissions_list.html", map[string]any{
		"Title":  "Manage Permissions",
		"Active": "permissions",
		"Users":  users,
	})
}

// ── Permissions edit (system_admin only) ─────────────────────────────────────

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

func adminPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	role := getRole(r)
	if role != roleSystemAdmin && !hasPermission(getUserID(r), "admin.permissions", "read") {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Extract user ID from path: /admin/permissions/{id}
	userID := strings.TrimPrefix(r.URL.Path, "/admin/permissions/")
	userID = strings.Trim(userID, "/")
	if userID == "" {
		http.Redirect(w, r, "/admin/permissions", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		db.Exec(`DELETE FROM prop_permissions WHERE user_id=?`, userID)
		for _, f := range featureRegistry {
			canRead := 0
			canWrite := 0
			if r.FormValue(f.Key+"_read") == "1" {
				canRead = 1
			}
			if r.FormValue(f.Key+"_write") == "1" {
				canWrite = 1
				canRead = 1 // write implies read
			}
			if canRead == 1 || canWrite == 1 {
				_, err := db.Exec(
					`INSERT INTO prop_permissions (user_id, feature, can_read, can_write) VALUES (?,?,?,?)
					 ON DUPLICATE KEY UPDATE can_read=VALUES(can_read), can_write=VALUES(can_write)`,
					userID, f.Key, canRead, canWrite,
				)
				if err != nil {
					log.Printf("save perm: %v", err)
				}
			}
		}

		// Save private estate access: delete existing then re-insert checked ones.
		db.Exec(`DELETE FROM prop_restricted_access WHERE user_id=?`, userID)
		for _, v := range r.Form["private_estate"] {
			db.Exec(`INSERT IGNORE INTO prop_restricted_access (estate_id, user_id) VALUES (?,?)`, v, userID)
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/permissions/%s?saved=1", userID), http.StatusFound)
		return
	}

	// Load target user info
	var userName, userEmail, userRole string
	db.QueryRow(`SELECT name, email, role FROM prop_agents WHERE id=?`, userID).
		Scan(&userName, &userEmail, &userRole)

	if userName == "" {
		http.NotFound(w, r)
		return
	}

	// Load existing permissions into a map for template lookup
	type permState struct {
		CanRead  bool
		CanWrite bool
	}
	permsMap := map[string]permState{}
	rows, _ := db.Query(`SELECT feature, can_read, can_write FROM prop_permissions WHERE user_id=?`, userID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var feat string
			var cr, cw int
			rows.Scan(&feat, &cr, &cw)
			permsMap[feat] = permState{CanRead: cr == 1, CanWrite: cw == 1}
		}
	}

	// Build module sections — show all features for every user regardless of role.
	// Van Booking features are managed separately from /admin/van-permissions.
	moduleMap := map[string]*moduleSection{}
	var moduleOrder []string
	for _, f := range featureRegistry {
		if f.Module == "Van Booking" {
			continue
		}
		if _, ok := moduleMap[f.Module]; !ok {
			moduleMap[f.Module] = &moduleSection{Name: f.Module}
			moduleOrder = append(moduleOrder, f.Module)
		}
		fs := featureWithState{featureDef: f}
		if p, ok := permsMap[f.Key]; ok {
			fs.CanRead = p.CanRead
			fs.CanWrite = p.CanWrite
		}
		if f.Group == "admin" {
			moduleMap[f.Module].AdminFeats = append(moduleMap[f.Module].AdminFeats, fs)
		} else {
			moduleMap[f.Module].AgentFeats = append(moduleMap[f.Module].AgentFeats, fs)
		}
	}
	var modules []moduleSection
	for _, name := range moduleOrder {
		modules = append(modules, *moduleMap[name])
	}

	// Load private estates and which ones this user already has access to.
	type privateEstate struct {
		ID      int
		Name    string
		Granted bool
	}
	accessSet := map[string]bool{}
	if ar, _ := db.Query(`SELECT estate_id FROM prop_restricted_access WHERE user_id=?`, userID); ar != nil {
		defer ar.Close()
		for ar.Next() {
			var eid string
			ar.Scan(&eid)
			accessSet[eid] = true
		}
	}
	var privateEstates []privateEstate
	if er, _ := db.Query(`SELECT id, name FROM prop_estates WHERE COALESCE(is_restricted,0)=1 ORDER BY name`); er != nil {
		defer er.Close()
		for er.Next() {
			var pe privateEstate
			er.Scan(&pe.ID, &pe.Name)
			pe.Granted = accessSet[fmt.Sprintf("%d", pe.ID)]
			privateEstates = append(privateEstates, pe)
		}
	}

	renderAdmin(w, r, "admin_permissions.html", map[string]any{
		"Title":          "Permissions — " + userName,
		"Active":         "permissions",
		"UserID":         userID,
		"UserName":       userName,
		"UserEmail":      userEmail,
		"UserRole":       userRole,
		"Modules":        modules,
		"PrivateEstates": privateEstates,
		"SaveOK":         r.URL.Query().Get("saved") == "1",
	})
}
