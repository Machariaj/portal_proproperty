package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// renderVanAdmin injects IsSystemAdmin into van-booking page renders.
func renderVanAdmin(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	data["IsSystemAdmin"] = getRole(r) == roleSystemAdmin
	render(w, name, data)
}

// ── Van Booking permissions list (system_admin only) ─────────────────────────

func vanPermissionsListHandler(w http.ResponseWriter, r *http.Request) {
	if getRole(r) != roleSystemAdmin {
		w.WriteHeader(http.StatusForbidden)
		render(w, "access_denied", nil)
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
	renderVanAdmin(w, r, "van_admin_permissions_list.html", map[string]any{
		"Title":  "Van Booking Permissions",
		"Active": "van-permissions",
		"Users":  users,
	})
}

// ── Van Booking permissions edit (system_admin only) ─────────────────────────

func vanPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	if getRole(r) != roleSystemAdmin {
		w.WriteHeader(http.StatusForbidden)
		render(w, "access_denied", nil)
		return
	}

	userID := strings.TrimPrefix(r.URL.Path, "/admin/van-permissions/")
	userID = strings.Trim(userID, "/")
	if userID == "" {
		http.Redirect(w, r, "/admin/van-permissions", http.StatusFound)
		return
	}

	// Only Van Booking features
	var vanFeatures []featureDef
	for _, f := range featureRegistry {
		if f.Module == "Van Booking" {
			vanFeatures = append(vanFeatures, f)
		}
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		// Delete only van booking feature permissions, not all permissions
		for _, f := range vanFeatures {
			db.Exec(`DELETE FROM prop_permissions WHERE user_id=? AND feature=?`, userID, f.Key)
		}
		for _, f := range vanFeatures {
			canRead := 0
			canWrite := 0
			if r.FormValue(f.Key+"_read") == "1" {
				canRead = 1
			}
			if r.FormValue(f.Key+"_write") == "1" {
				canWrite = 1
				canRead = 1
			}
			if canRead == 1 || canWrite == 1 {
				_, err := db.Exec(
					`INSERT INTO prop_permissions (user_id, feature, can_read, can_write) VALUES (?,?,?,?)
					 ON DUPLICATE KEY UPDATE can_read=VALUES(can_read), can_write=VALUES(can_write)`,
					userID, f.Key, canRead, canWrite,
				)
				if err != nil {
					log.Printf("van save perm: %v", err)
				}
			}
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/van-permissions/%s?saved=1", userID), http.StatusFound)
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

	// Load existing permissions
	type permState struct{ CanRead, CanWrite bool }
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

	// Build sections — show all van booking features for every user regardless of role
	ms := moduleSection{Name: "Van Booking"}
	for _, f := range vanFeatures {
		fs := featureWithState{featureDef: f}
		if p, ok := permsMap[f.Key]; ok {
			fs.CanRead = p.CanRead
			fs.CanWrite = p.CanWrite
		}
		if f.Group == "admin" {
			ms.AdminFeats = append(ms.AdminFeats, fs)
		} else {
			ms.AgentFeats = append(ms.AgentFeats, fs)
		}
	}

	renderVanAdmin(w, r, "van_admin_permissions.html", map[string]any{
		"Title":     "Van Permissions — " + userName,
		"Active":    "van-permissions",
		"UserID":    userID,
		"UserName":  userName,
		"UserEmail": userEmail,
		"UserRole":  userRole,
		"Section":   ms,
		"SaveOK":    r.URL.Query().Get("saved") == "1",
	})
}
