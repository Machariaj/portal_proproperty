package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// renderWelfareAdmin injects IsSystemAdmin into welfare permission page renders.
func renderWelfareAdmin(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	data["IsSystemAdmin"] = getRole(r) == roleSystemAdmin
	render(w, name, data)
}

// ── Welfare permissions list (system_admin only) ──────────────────────────────

func welfarePermissionsListHandler(w http.ResponseWriter, r *http.Request) {
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
	renderWelfareAdmin(w, r, "welfare_permissions_list.html", map[string]any{
		"Title":  "Welfare Permissions",
		"Active": "welfare-permissions",
		"Users":  users,
	})
}

// ── Welfare permissions edit (system_admin only) ──────────────────────────────

func welfarePermissionsHandler(w http.ResponseWriter, r *http.Request) {
	if getRole(r) != roleSystemAdmin {
		w.WriteHeader(http.StatusForbidden)
		render(w, "access_denied", nil)
		return
	}

	userID := strings.TrimPrefix(r.URL.Path, "/welfare/permissions/")
	userID = strings.Trim(userID, "/")
	if userID == "" {
		http.Redirect(w, r, "/welfare/permissions", http.StatusFound)
		return
	}

	// Only welfare features
	var welfareFeatures []featureDef
	for _, f := range featureRegistry {
		if f.Module == "Staff Welfare" {
			welfareFeatures = append(welfareFeatures, f)
		}
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		for _, f := range welfareFeatures {
			db.Exec(`DELETE FROM prop_permissions WHERE user_id=? AND feature=?`, userID, f.Key)
		}
		for _, f := range welfareFeatures {
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
					log.Printf("welfare perm save: %v", err)
				}
			}
		}
		http.Redirect(w, r, fmt.Sprintf("/welfare/permissions/%s?saved=1", userID), http.StatusFound)
		return
	}

	// Load target user
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

	// Build feature list with current state
	var features []featureWithState
	for _, f := range welfareFeatures {
		fs := featureWithState{featureDef: f}
		if p, ok := permsMap[f.Key]; ok {
			fs.CanRead = p.CanRead
			fs.CanWrite = p.CanWrite
		}
		features = append(features, fs)
	}

	renderWelfareAdmin(w, r, "welfare_permissions.html", map[string]any{
		"Title":     "Welfare Permissions — " + userName,
		"Active":    "welfare-permissions",
		"UserID":    userID,
		"UserName":  userName,
		"UserEmail": userEmail,
		"UserRole":  userRole,
		"Features":  features,
		"SaveOK":    r.URL.Query().Get("saved") == "1",
	})
}
