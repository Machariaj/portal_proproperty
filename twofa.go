package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"marketers_portal/vanbooking"
)

const otpPendingCookie = "pp_2fa_uid"

// init2FATables creates the OTP and trusted-device tables if they don't exist.
func init2FATables() {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_otp_sessions (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		user_id    INT          NOT NULL,
		otp_code   VARCHAR(6)   NOT NULL,
		expires_at TIMESTAMP    NOT NULL,
		attempts   INT          NOT NULL DEFAULT 0,
		used       TINYINT(1)   NOT NULL DEFAULT 0,
		INDEX idx_otp_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("ERROR init2FATables prop_otp_sessions: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_trusted_devices (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		user_id    INT          NOT NULL,
		token      VARCHAR(64)  NOT NULL,
		expires_at TIMESTAMP    NOT NULL,
		created_at TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_token (token),
		INDEX idx_trust_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("ERROR init2FATables prop_trusted_devices: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_otp_log (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		email      VARCHAR(150) NOT NULL,
		phone      VARCHAR(20)  DEFAULT '',
		otp_code   VARCHAR(6)   NOT NULL,
		sent_at    TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_otp_log_email (email),
		INDEX idx_otp_log_sent (sent_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("ERROR init2FATables prop_otp_log: %v", err)
	}
}

// generateOTP returns a cryptographically random 6-digit code.
func generateOTP() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

// generateToken returns a cryptographically random 64-char hex trust token.
func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// maskEmail partially hides an email address, e.g. jo***@gmail.com
func maskEmail(email string) string {
	if email == "" {
		return ""
	}
	at := strings.LastIndex(email, "@")
	if at <= 2 {
		return email
	}
	return email[:2] + strings.Repeat("*", at-2) + email[at:]
}

// maskPhone shows only the last 4 digits, e.g. ******3737
func maskPhone(phone string) string {
	r := []rune(strings.TrimSpace(phone))
	if len(r) <= 4 {
		return phone
	}
	return strings.Repeat("*", len(r)-4) + string(r[len(r)-4:])
}

// sendOTP dispatches the code via email (always) and SMS (if phone present).
func sendOTP(email, phone, otp string) {
	log.Printf("2FA OTP | email=%s phone=%s code=%s", email, maskPhone(phone), otp)
	db.Exec(`INSERT INTO prop_otp_log (email, phone, otp_code) VALUES (?, ?, ?)`, email, phone, otp)
	body := fmt.Sprintf(
		"Your Pro-Property Hub verification code is: %s\n\nThis code expires in 10 minutes. Do not share it with anyone.",
		otp,
	)
	go sendPlainEmail([]string{email}, "ProProperty Login Code: "+otp, body)
	if phone != "" {
		go vanbooking.SendSMS(phone,
			fmt.Sprintf("ProProperty login code: %s. Expires in 10 min.", otp))
	}
}

// login2faHandler serves GET /login/verify (show form) and POST /login/verify (validate code).
func login2faHandler(w http.ResponseWriter, r *http.Request) {

	// ── GET: show OTP form ────────────────────────────────────────────────
	if r.Method == http.MethodGet {
		uidCookie, err := r.Cookie(otpPendingCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		var email, phone string
		db.QueryRow(
			`SELECT COALESCE(email,''), COALESCE(phone,'') FROM prop_agents WHERE id = ?`,
			uidCookie.Value,
		).Scan(&email, &phone)
		render(w, "login_2fa", map[string]any{
			"Title":       "Verify Your Identity",
			"MaskedEmail": maskEmail(email),
			"HasPhone":    phone != "",
			"MaskedPhone": maskPhone(phone),
		})
		return
	}

	// ── POST: validate OTP ───────────────────────────────────────────────
	uidCookie, err := r.Cookie(otpPendingCookie)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	userIDStr := uidCookie.Value

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	otp := strings.TrimSpace(r.FormValue("otp"))
	trustDevice := r.FormValue("trust_device") == "on"

	// re-render helper
	renderErr := func(msg string) {
		var email, phone string
		db.QueryRow(
			`SELECT COALESCE(email,''), COALESCE(phone,'') FROM prop_agents WHERE id = ?`,
			userIDStr,
		).Scan(&email, &phone)
		render(w, "login_2fa", map[string]any{
			"Title":       "Verify Your Identity",
			"Error":       msg,
			"MaskedEmail": maskEmail(email),
			"HasPhone":    phone != "",
			"MaskedPhone": maskPhone(phone),
		})
	}

	// look up valid OTP
	var otpID, attempts int
	var storedOTP string
	err = db.QueryRow(
		`SELECT id, otp_code, attempts FROM prop_otp_sessions
		 WHERE user_id = ? AND used = 0 AND expires_at > NOW()
		 ORDER BY id DESC LIMIT 1`,
		userIDStr,
	).Scan(&otpID, &storedOTP, &attempts)
	if err != nil {
		renderErr("Code has expired. Please log in again.")
		return
	}

	if attempts >= 5 {
		db.Exec(`UPDATE prop_otp_sessions SET used = 1 WHERE id = ?`, otpID)
		renderErr("Too many failed attempts. Please log in again.")
		return
	}

	if otp != storedOTP {
		db.Exec(`UPDATE prop_otp_sessions SET attempts = attempts + 1 WHERE id = ?`, otpID)
		remaining := 4 - attempts
		if remaining < 0 {
			remaining = 0
		}
		renderErr(fmt.Sprintf("Invalid code. %d attempt(s) remaining.", remaining))
		return
	}

	// ── OTP valid ─────────────────────────────────────────────────────────
	db.Exec(`UPDATE prop_otp_sessions SET used = 1 WHERE id = ?`, otpID)

	var name, role string
	db.QueryRow(`SELECT name, role FROM prop_agents WHERE id = ?`, userIDStr).Scan(&name, &role)

	// Trust this device for 30 days
	if trustDevice {
		token := generateToken()
		db.Exec(
			`INSERT INTO prop_trusted_devices (user_id, token, expires_at)
			 VALUES (?, ?, DATE_ADD(NOW(), INTERVAL 30 DAY))`,
			userIDStr, token,
		)
		http.SetCookie(w, &http.Cookie{
			Name:     "pp_trust_" + userIDStr,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(30 * 24 * time.Hour),
		})
	}

	// Clear pending cookie
	http.SetCookie(w, &http.Cookie{
		Name: otpPendingCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Unix(0, 0), MaxAge: -1,
	})

	setSession(w, role, name, userIDStr)
	logAccess("LOGIN_OK", name, role, clientIP(r), "2FA verified uid="+userIDStr)
	http.Redirect(w, r, "/hub", http.StatusFound)
}

// resend2faHandler handles POST /login/resend-2fa — regenerates and resends the OTP.
func resend2faHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/login/verify", http.StatusFound)
		return
	}
	uidCookie, err := r.Cookie(otpPendingCookie)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login/verify", http.StatusFound)
		return
	}

	method := r.FormValue("method") // "email" or "sms"

	var id int
	var name, email, phone string
	db.QueryRow(
		`SELECT id, name, COALESCE(email,''), COALESCE(phone,'') FROM prop_agents WHERE id = ?`,
		uidCookie.Value,
	).Scan(&id, &name, &email, &phone)

	otp := generateOTP()
	db.Exec(`DELETE FROM prop_otp_sessions WHERE user_id = ?`, id)
	db.Exec(
		`INSERT INTO prop_otp_sessions (user_id, otp_code, expires_at)
		 VALUES (?, ?, DATE_ADD(NOW(), INTERVAL 10 MINUTE))`,
		id, otp,
	)

	if method == "sms" && phone != "" {
		go vanbooking.SendSMS(phone, fmt.Sprintf("ProProperty login code: %s. Expires in 10 min.", otp))
	} else if email != "" {
		body := fmt.Sprintf("Your verification code is: %s\n\nExpires in 10 minutes.", otp)
		go sendPlainEmail([]string{email}, "ProProperty Login Code: "+otp, body)
	}

	logAccess("2FA_RESEND", name, "", clientIP(r), "method="+method)
	http.Redirect(w, r, "/login/verify", http.StatusFound)
}
