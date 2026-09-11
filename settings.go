package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Settings is a system_admin-only module for account administration: create
// users, reset passwords, change roles — and, over time, whatever other
// site-wide configuration doesn't belong inside the day-to-day
// admin/agent/accounts/legal areas.

func renderSettings(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	render(w, name, data)
}

func settingsRootHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/users", http.StatusFound)
}

type settingsUserRow struct {
	ID      int
	Name    string
	Email   string
	Phone   string
	Role    string
	Blocked bool
}

func settingsUsersHandler(w http.ResponseWriter, r *http.Request) {
	var formErr, formSuccess string

	if r.Method == http.MethodPost {
		r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(r.FormValue("email"))
		password := r.FormValue("password")
		phone := strings.TrimSpace(r.FormValue("phone"))
		role := r.FormValue("role")

		switch {
		case name == "":
			formErr = "Name is required"
		case email == "":
			formErr = "Email is required"
		case len(password) < 6:
			formErr = "Password must be at least 6 characters"
		case role != roleAdmin && role != roleAgent && role != roleAccounts && role != roleLegal:
			formErr = "Invalid role"
		default:
			var cnt int
			db.QueryRow(`SELECT COUNT(*) FROM prop_agents WHERE email=?`, email).Scan(&cnt)
			if cnt > 0 {
				formErr = "A user with that email already exists"
			} else {
				hash, herr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if herr != nil {
					formErr = "Could not hash password"
				} else {
					_, ierr := db.Exec(`INSERT INTO prop_agents (name, email, password, phone, role) VALUES (?,?,?,?,?)`,
						name, email, string(hash), phone, role)
					if ierr != nil {
						log.Printf("settings create user: %v", ierr)
						formErr = "Database error"
					} else {
						formSuccess = "User created successfully"
					}
				}
			}
		}
	}

	// Handle delete / quick block / quick unblock (simple GET actions from the list row)
	if r.Method == http.MethodGet {
		if del := r.URL.Query().Get("delete"); del != "" {
			db.Exec(`DELETE FROM prop_agents WHERE id=?`, del)
			http.Redirect(w, r, "/settings/users", http.StatusFound)
			return
		}
		if bid := r.URL.Query().Get("block"); bid != "" {
			db.Exec(`UPDATE prop_agents SET blocked=1 WHERE id=?`, bid)
			http.Redirect(w, r, fmt.Sprintf("/settings/users#user-%s", bid), http.StatusFound)
			return
		}
		if uid := r.URL.Query().Get("unblock"); uid != "" {
			db.Exec(`UPDATE prop_agents SET blocked=0 WHERE id=?`, uid)
			http.Redirect(w, r, fmt.Sprintf("/settings/users#user-%s", uid), http.StatusFound)
			return
		}
	}

	rows, _ := db.Query(`SELECT id, name, email, COALESCE(phone,''), role, COALESCE(blocked,0) FROM prop_agents ORDER BY name`)
	var users []settingsUserRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u settingsUserRow
			var blocked int
			if rows.Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Role, &blocked) == nil {
				u.Blocked = blocked == 1
				users = append(users, u)
			}
		}
	}

	renderSettings(w, r, "settings_users.html", map[string]any{
		"Title":       "Settings — Users",
		"Active":      "users",
		"Users":       users,
		"FormError":   formErr,
		"FormSuccess": formSuccess,
	})
}

func settingsEditUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimPrefix(r.URL.Path, "/settings/edit-user/")
	userID = strings.Trim(userID, "/")
	if userID == "" {
		http.Redirect(w, r, "/settings/users", http.StatusFound)
		return
	}

	var u settingsUserRow
	var blockedInt int
	err := db.QueryRow(`SELECT id, name, email, COALESCE(phone,''), role, COALESCE(blocked,0) FROM prop_agents WHERE id=?`, userID).
		Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Role, &blockedInt)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u.Blocked = blockedInt == 1

	var formErr, formSuccess string

	if r.Method == http.MethodPost {
		r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(r.FormValue("email"))
		phone := strings.TrimSpace(r.FormValue("phone"))
		role := r.FormValue("role")
		newPassword := r.FormValue("new_password")
		blocked := r.FormValue("blocked") == "1"
		blockedVal := 0
		if blocked {
			blockedVal = 1
		}

		switch {
		case name == "":
			formErr = "Name is required"
		case email == "":
			formErr = "Email is required"
		case role != roleAdmin && role != roleAgent && role != roleAccounts && role != roleLegal:
			formErr = "Invalid role"
		default:
			// Check email uniqueness (excluding this user)
			var cnt int
			db.QueryRow(`SELECT COUNT(*) FROM prop_agents WHERE email=? AND id!=?`, email, userID).Scan(&cnt)
			if cnt > 0 {
				formErr = "Another user with that email already exists"
			} else if newPassword != "" && len(newPassword) < 6 {
				formErr = "New password must be at least 6 characters"
			} else {
				if newPassword != "" {
					hash, herr := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
					if herr != nil {
						formErr = "Could not hash password"
					} else {
						db.Exec(`UPDATE prop_agents SET name=?, email=?, phone=?, role=?, password=?, blocked=? WHERE id=?`,
							name, email, phone, role, string(hash), blockedVal, userID)
					}
				} else {
					db.Exec(`UPDATE prop_agents SET name=?, email=?, phone=?, role=?, blocked=? WHERE id=?`,
						name, email, phone, role, blockedVal, userID)
				}
				if formErr == "" {
					formSuccess = "User updated successfully"
					if newPassword != "" {
						formSuccess = "User updated and password reset successfully"
					}
					u.Name = name
					u.Email = email
					u.Phone = phone
					u.Role = role
					u.Blocked = blocked
				}
			}
		}
	}

	renderSettings(w, r, "settings_edit_user.html", map[string]any{
		"Title":       "Settings — Edit User — " + u.Name,
		"Active":      "users",
		"User":        u,
		"FormError":   formErr,
		"FormSuccess": formSuccess,
	})
}

// settingsModulesHandler is the "Modules" button on each user row: one
// unified permissions editor covering every registered module (Marketers
// Portal, Van Booking, Staff Welfare, ...) — see moduleSection/
// featureWithState in permissions.go, which this reuses. Pick a context
// ("As Admin" / "As Agent" — mirrors each feature's Group) then set each
// feature to No Access / Read Only / Full Access. Supersedes the old
// per-module permission pages, which all wrote the same prop_permissions
// table this does.
type moduleAccessRow struct {
	Name          string
	Key           string
	HasAdmin      bool
	HasAgent      bool
	SingleGroup   string // "admin" or "agent" when only one group exists; "" when both
	CurrentAccess string // "none", "admin", or "agent"
	AdminFeats    []featureWithState
	AgentFeats    []featureWithState
}

func moduleKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

func settingsModulesHandler(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimPrefix(r.URL.Path, "/settings/modules/")
	userID = strings.Trim(userID, "/")
	if userID == "" {
		http.Redirect(w, r, "/settings/users", http.StatusFound)
		return
	}

	var userName, userEmail, userRole string
	if err := db.QueryRow(`SELECT name, email, role FROM prop_agents WHERE id=?`, userID).
		Scan(&userName, &userEmail, &userRole); err != nil || userName == "" {
		http.NotFound(w, r)
		return
	}

	// Group features by module, preserving order.
	type modFeats struct {
		admin []featureDef
		agent []featureDef
	}
	modFeatMap := map[string]*modFeats{}
	var modOrder []string
	for _, f := range featureRegistry {
		if _, ok := modFeatMap[f.Module]; !ok {
			modFeatMap[f.Module] = &modFeats{}
			modOrder = append(modOrder, f.Module)
		}
		if f.Group == "admin" {
			modFeatMap[f.Module].admin = append(modFeatMap[f.Module].admin, f)
		} else {
			modFeatMap[f.Module].agent = append(modFeatMap[f.Module].agent, f)
		}
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		db.Exec(`DELETE FROM prop_permissions WHERE user_id=?`, userID)
		for _, name := range modOrder {
			feats := modFeatMap[name]
			access := r.FormValue(moduleKey(name) + "_access")
			var candidates []featureDef
			switch access {
			case "admin":
				candidates = feats.admin
			case "agent":
				candidates = feats.agent
			}
			for _, f := range candidates {
				if r.FormValue("feat_"+f.Key) != "1" {
					continue
				}
				if _, err := db.Exec(
					`INSERT INTO prop_permissions (user_id, feature, can_read, can_write) VALUES (?,?,1,1)
					 ON DUPLICATE KEY UPDATE can_read=1, can_write=1`,
					userID, f.Key,
				); err != nil {
					log.Printf("settings save module perm: %v", err)
				}
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/settings/modules/%s?saved=1", userID), http.StatusFound)
		return
	}

	// Load existing permissions into a set for lookup.
	grantedFeats := map[string]bool{}
	if rows, _ := db.Query(`SELECT feature FROM prop_permissions WHERE user_id=? AND can_read=1`, userID); rows != nil {
		defer rows.Close()
		for rows.Next() {
			var feat string
			rows.Scan(&feat)
			grantedFeats[feat] = true
		}
	}

	// Build module access rows, detecting current access level and per-feature state.
	var modules []moduleAccessRow
	for _, name := range modOrder {
		feats := modFeatMap[name]
		current := "none"
		for _, f := range feats.admin {
			if grantedFeats[f.Key] {
				current = "admin"
				break
			}
		}
		if current == "none" {
			for _, f := range feats.agent {
				if grantedFeats[f.Key] {
					current = "agent"
					break
				}
			}
		}
		sg := ""
		if len(feats.admin) > 0 && len(feats.agent) == 0 {
			sg = "admin"
		} else if len(feats.agent) > 0 && len(feats.admin) == 0 {
			sg = "agent"
		}
		buildFeats := func(defs []featureDef) []featureWithState {
			var out []featureWithState
			for _, f := range defs {
				granted := grantedFeats[f.Key]
				out = append(out, featureWithState{
					featureDef: f,
					CanRead:    granted,
					CanWrite:   granted,
				})
			}
			return out
		}
		modules = append(modules, moduleAccessRow{
			Name:          name,
			Key:           moduleKey(name),
			HasAdmin:      len(feats.admin) > 0,
			HasAgent:      len(feats.agent) > 0,
			SingleGroup:   sg,
			CurrentAccess: current,
			AdminFeats:    buildFeats(feats.admin),
			AgentFeats:    buildFeats(feats.agent),
		})
	}

	renderSettings(w, r, "settings_modules.html", map[string]any{
		"Title":     "Settings — Modules — " + userName,
		"Active":    "users",
		"UserID":    userID,
		"UserName":  userName,
		"UserEmail": userEmail,
		"UserRole":  userRole,
		"Modules":   modules,
		"SaveOK":    r.URL.Query().Get("saved") == "1",
	})
}
