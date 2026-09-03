package vanbooking

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
)

// SendSMS is the exported wrapper so other packages can send SMS alerts.
func SendSMS(phone, message string) { sendSMS(phone, message) }

// sendSMS sends an SMS via Vidatech Bulk SMS API.
// Set the following environment variables before running:
//
//	VIDATECH_TOKEN  – Bearer token
//	VIDATECH_SENDER – Sender ID (default: "PROPROPERTY")
//
// If VIDATECH_TOKEN is not set, SMS is silently skipped.
func sendSMS(phone, message string) {
	if devMode {
		log.Printf("[devMode] skipping SMS to %s", phone)
		return
	}
	token := os.Getenv("VIDATECH_TOKEN")
	if token == "" {
		log.Printf("sms: VIDATECH_TOKEN not set, skipping SMS to %s", phone)
		return
	}
	sender := os.Getenv("VIDATECH_SENDER")
	if sender == "" {
		sender = "PROPROPERTY"
	}

	payload, err := json.Marshal([]map[string]any{
		{
			"sender":     sender,
			"message":    message,
			"phone":      phone,
			"correlator": 1,
		},
	})
	if err != nil {
		log.Printf("sms: marshal payload: %v", err)
		return
	}

	req, err := http.NewRequest("POST", "https://bulk.vidatech.co.ke/api/v1/send-sms", bytes.NewReader(payload))
	if err != nil {
		log.Printf("sms: build request: %v", err)
		return
	}
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("sms: send to %s: %v", phone, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("sms: Vidatech returned HTTP %d for %s", resp.StatusCode, phone)
	}
}

// notifyUser creates an in-app notification and sends an SMS if the user has
// the van_notifications read permission and a phone number on file.
func notifyUser(name, message string) {
	db.Exec(`INSERT INTO van_notifications (agent_name, message) VALUES (?,?)`, name, message)
	if canReceiveNotif(name) {
		var phone string
		db.QueryRow(`SELECT COALESCE(phone,'') FROM prop_agents WHERE name=?`, name).Scan(&phone)
		if phone != "" {
			go sendSMS(phone, message)
		}
	}
}

// notifyAdmins creates an in-app notification for every admin/system_admin
// who has admin.van_notifications read permission. SMS is sent separately via notifyAdminsSMS.
func notifyAdmins(message string) {
	rows, err := db.Query(`
		SELECT a.name
		FROM prop_agents a
		JOIN prop_permissions p ON p.user_id = a.id
		WHERE a.role IN ('admin','system_admin')
		  AND p.feature = 'admin.van_notifications'
		  AND p.can_read = 1`)
	if err != nil {
		log.Printf("notifyAdmins: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		db.Exec(`INSERT INTO van_notifications (agent_name, message) VALUES (?,?)`, name, message)
	}
	go notifyAdminsSMS(message)
}

// notifyAdminsSMS sends an SMS to every admin/system_admin who has admin.van_sms read permission.
func notifyAdminsSMS(message string) {
	rows, err := db.Query(`
		SELECT COALESCE(a.phone,'')
		FROM prop_agents a
		JOIN prop_permissions p ON p.user_id = a.id
		WHERE a.role IN ('admin','system_admin')
		  AND p.feature = 'admin.van_sms'
		  AND p.can_read = 1`)
	if err != nil {
		log.Printf("notifyAdminsSMS: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var phone string
		rows.Scan(&phone)
		if phone != "" {
			go sendSMS(phone, message)
		}
	}
}

// canReceiveNotif checks whether a user has agent.van_notifications or
// admin.van_notifications read permission.
func canReceiveNotif(name string) bool {
	var count int
	db.QueryRow(`
		SELECT COUNT(*) FROM prop_agents a
		JOIN prop_permissions p ON p.user_id = a.id
		WHERE a.name = ?
		  AND p.feature IN ('agent.van_notifications','admin.van_notifications')
		  AND p.can_read = 1`, name).Scan(&count)
	return count > 0
}

// notifyFleetManagersSMS sends an SMS to agent-role fleet managers who have
// agent.van_notifications read permission.
// Admin-role fleet managers are already handled by notifyAdminsSMS (via admin.van_sms).
func notifyFleetManagersSMS(message string) {
	rows, err := db.Query(`
		SELECT COALESCE(a.phone,'')
		FROM prop_agents a
		JOIN prop_permissions p ON p.user_id = a.id
		WHERE p.feature = 'agent.van_fleet'
		  AND p.can_write = 1
		  AND a.role NOT IN ('admin','system_admin')
		  AND EXISTS (
		    SELECT 1 FROM prop_permissions p2
		    WHERE p2.user_id = a.id
		      AND p2.feature = 'agent.van_notifications'
		      AND p2.can_read = 1
		  )`)
	if err != nil {
		log.Printf("notifyFleetManagersSMS: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var phone string
		rows.Scan(&phone)
		if phone != "" {
			go sendSMS(phone, message)
		}
	}
}

// canApprove checks whether a user has fleet manager permission.
// Accepts either admin.van_approve (admin-role users) or agent.van_fleet (agent-role users).
func canApprove(name string) bool {
	var count int
	db.QueryRow(`
		SELECT COUNT(*) FROM prop_agents a
		JOIN prop_permissions p ON p.user_id = a.id
		WHERE a.name = ?
		  AND p.feature IN ('admin.van_approve','agent.van_fleet')
		  AND p.can_write = 1`, name).Scan(&count)
	return count > 0 || isSysAdmin(name)
}

// isSysAdmin checks if a user is a system_admin by name.
func isSysAdmin(name string) bool {
	var role string
	db.QueryRow(`SELECT role FROM prop_agents WHERE name=?`, name).Scan(&role)
	return role == "system_admin"
}
