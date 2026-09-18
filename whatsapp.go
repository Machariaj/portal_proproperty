package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// initWhatsAppLog creates the persistent log table if it doesn't exist.
func initWhatsAppLog() {
	db.Exec(`CREATE TABLE IF NOT EXISTS prop_whatsapp_log (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		sent_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		to_phone     VARCHAR(20)  NOT NULL,
		template     VARCHAR(100) NOT NULL,
		body_params  TEXT,
		http_status  INT          NOT NULL,
		api_response TEXT,
		error_msg    TEXT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
}

// sendWhatsApp sends a WhatsApp template message via Wapiverse API and logs
// every attempt (success or failure) to prop_whatsapp_log.
//
// bodyParams maps to the template's {{1}} {{2}} … placeholders in order:
//
//	[0] agent name   [1] estate name   [2] plot number
//	[3] days left    [4] expiry date
func sendWhatsApp(toPhone, templateName string, bodyParams []string) {
	if devMode {
		log.Printf("[devMode] skipping WhatsApp template %s to %s", templateName, toPhone)
		return
	}
	token := os.Getenv("WAPIVERSE_TOKEN")
	if token == "" {
		token = "459c32b29dd1566556b37e7b24743d54780943489cd455f6999e78cc85b81073"
	}
	phoneID := os.Getenv("WAPIVERSE_PHONE_ID")
	if phoneID == "" {
		phoneID = "943267062204795"
	}

	to := normalisePhone(toPhone)
	if to == "" {
		log.Printf("whatsapp: empty/invalid phone for template %s, skipping", templateName)
		return
	}

	paramsJSON, _ := json.Marshal(bodyParams)

	payload, err := json.Marshal(map[string]any{
		"to":         to,
		"phoneNoId":  phoneID,
		"type":       "template",
		"name":       templateName,
		"language":   "en",
		"bodyParams": bodyParams,
	})
	if err != nil {
		log.Printf("whatsapp: marshal error: %v", err)
		db.Exec(`INSERT INTO prop_whatsapp_log (to_phone,template,body_params,http_status,error_msg) VALUES (?,?,?,0,?)`,
			to, templateName, string(paramsJSON), err.Error())
		return
	}

	req, err := http.NewRequest("POST", "https://app.wapiverse.com/api/v2/whatsapp-business/messages", bytes.NewReader(payload))
	if err != nil {
		log.Printf("whatsapp: build request: %v", err)
		db.Exec(`INSERT INTO prop_whatsapp_log (to_phone,template,body_params,http_status,error_msg) VALUES (?,?,?,0,?)`,
			to, templateName, string(paramsJSON), err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("whatsapp: send to %s: %v", to, err)
		db.Exec(`INSERT INTO prop_whatsapp_log (to_phone,template,body_params,http_status,error_msg) VALUES (?,?,?,0,?)`,
			to, templateName, string(paramsJSON), err.Error())
		return
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	apiResp := buf.String()

	db.Exec(`INSERT INTO prop_whatsapp_log (to_phone,template,body_params,http_status,api_response) VALUES (?,?,?,?,?)`,
		to, templateName, string(paramsJSON), resp.StatusCode, apiResp)

	if resp.StatusCode >= 300 {
		log.Printf("whatsapp: Wapiverse returned HTTP %d for %s (template=%s)", resp.StatusCode, to, templateName)
	} else {
		log.Printf("whatsapp: sent template=%s to %s", templateName, to)
	}
}

// testWhatsAppHandler lists active bookings and lets an admin fire a real
// WhatsApp to the booking agent to confirm the scheduler integration works.
// Remove this handler once confirmed.
func testWhatsAppHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// If a booking ID was submitted, send the WA and show the result.
	if bookingID := r.URL.Query().Get("send"); bookingID != "" {
		var agentName, agentPhone, estateName, plotNumber, expiryDate string
		var daysRemaining int
		err := db.QueryRow(`
			SELECT COALESCE(b.agent_name,''), COALESCE(a.phone,''),
			       e.name, p.plot_number,
			       DATE_FORMAT(COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)),'%M %d, %Y'),
			       DATEDIFF(COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)), NOW())
			FROM prop_bookings b
			JOIN prop_plots   p ON p.id = b.plot_id
			JOIN prop_estates e ON e.id = b.estate_id
			LEFT JOIN prop_agents a ON a.name = b.agent_name
			WHERE b.id = ?`, bookingID).
			Scan(&agentName, &agentPhone, &estateName, &plotNumber, &expiryDate, &daysRemaining)
		if err != nil {
			fmt.Fprintf(w, `<p style="color:red">Booking not found: %v</p><a href="/admin/test-whatsapp">← Back</a>`, err)
			return
		}
		if agentPhone == "" {
			fmt.Fprintf(w, `<p style="color:red">Agent <b>%s</b> has no phone number on file.</p><a href="/admin/test-whatsapp">← Back</a>`, agentName)
			return
		}

		n := daysRemaining
		daysLeft := fmt.Sprintf("%d days", n)
		if n == 1 {
			daysLeft = "1 day"
		} else if n <= 0 {
			daysLeft = "Expired"
		}

		params := []string{agentName, estateName, plotNumber, daysLeft, expiryDate}
		status, apiResp := fireWhatsApp(normalisePhone(agentPhone), params)

		color := "#166534"
		label := "SUCCESS"
		if status >= 300 {
			color = "#991b1b"
			label = "FAILED"
		}
		fmt.Fprintf(w, `<h2 style="color:%s">%s — HTTP %d</h2>
<p><b>Agent:</b> %s &nbsp;|&nbsp; <b>Phone:</b> %s</p>
<p><b>Plot:</b> %s — %s &nbsp;|&nbsp; <b>Days left:</b> %s &nbsp;|&nbsp; <b>Expires:</b> %s</p>
<p><b>Response:</b></p><pre style="background:#f1f5f9;padding:12px;border-radius:6px;">%s</pre>
<p><a href="/admin/test-whatsapp">← Back to list</a></p>`,
			color, label, status,
			agentName, normalisePhone(agentPhone),
			plotNumber, estateName, daysLeft, expiryDate,
			apiResp)
		return
	}

	// List active bookings ordered by age so nearest-expiry are at the top.
	rows, err := db.Query(`
		SELECT b.id, COALESCE(b.agent_name,'—'), COALESCE(a.phone,''),
		       p.plot_number, e.name,
		       DATEDIFF(NOW(), b.date_booked) AS days_booked,
		       DATE_FORMAT(COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)),'%M %d, %Y')
		FROM prop_bookings b
		JOIN prop_plots   p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE b.status NOT IN ('cancelled','expired')
		ORDER BY days_booked DESC
		LIMIT 30`)
	if err != nil {
		fmt.Fprintf(w, `<p style="color:red">DB error: %v</p>`, err)
		return
	}
	defer rows.Close()

	fmt.Fprint(w, `<h2>WhatsApp Test — Active Bookings</h2>
<p style="font-size:13px;color:#6b7280;">Click <b>Send WA</b> to fire the <i>bookingexpiry</i> template to that agent's phone right now.</p>
<table style="border-collapse:collapse;font-size:13px;width:100%;">
<tr style="background:#f1f5f9;">
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Agent</th>
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Phone</th>
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Plot</th>
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Estate</th>
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Days Booked</th>
<th style="padding:8px;text-align:left;border:1px solid #e2e8f0;">Expires</th>
<th style="padding:8px;border:1px solid #e2e8f0;"></th>
</tr>`)

	for rows.Next() {
		var id, daysBooked int
		var agentName, agentPhone, plotNumber, estateName, expiryDate string
		if err := rows.Scan(&id, &agentName, &agentPhone, &plotNumber, &estateName, &daysBooked, &expiryDate); err != nil {
			continue
		}
		rowColor := ""
		if daysBooked >= 13 {
			rowColor = "background:#fee2e2;"
		} else if daysBooked >= 12 {
			rowColor = "background:#fef9c3;"
		}
		phoneDisplay := agentPhone
		if phoneDisplay == "" {
			phoneDisplay = `<span style="color:#ef4444;">no phone</span>`
		}
		sendBtn := fmt.Sprintf(`<a href="?send=%d" style="background:#2563eb;color:#fff;padding:4px 10px;border-radius:4px;text-decoration:none;font-size:12px;">Send WA</a>`, id)
		if agentPhone == "" {
			sendBtn = `<span style="color:#94a3b8;font-size:12px;">no phone</span>`
		}
		fmt.Fprintf(w, `<tr style="%s">
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
<td style="padding:8px;border:1px solid #e2e8f0;font-weight:600;">%d</td>
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
<td style="padding:8px;border:1px solid #e2e8f0;">%s</td>
</tr>`, rowColor, agentName, phoneDisplay, plotNumber, estateName, daysBooked, expiryDate, sendBtn)
	}
	fmt.Fprint(w, `</table><p style="font-size:11px;color:#94a3b8;margin-top:8px;">🔴 Red = 13+ days &nbsp; 🟡 Yellow = 12 days</p>`)
}

// fireWhatsApp sends the bookingexpiry template and returns the HTTP status
// and response body. Used by the test handler for synchronous feedback.
func fireWhatsApp(to string, bodyParams []string) (int, string) {
	token := os.Getenv("WAPIVERSE_TOKEN")
	if token == "" {
		token = "459c32b29dd1566556b37e7b24743d54780943489cd455f6999e78cc85b81073"
	}
	phoneID := os.Getenv("WAPIVERSE_PHONE_ID")
	if phoneID == "" {
		phoneID = "943267062204795"
	}

	payload, _ := json.Marshal(map[string]any{
		"to":         to,
		"phoneNoId":  phoneID,
		"type":       "template",
		"name":       "bookingexpiry",
		"language":   "en",
		"bodyParams": bodyParams,
	})

	req, _ := http.NewRequest("POST", "https://app.wapiverse.com/api/v2/whatsapp-business/messages", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// normalisePhone converts common Kenyan phone formats to the international
// digits-only format expected by Wapiverse (e.g. "254712345678").
func normalisePhone(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "+")
	if strings.HasPrefix(p, "0") {
		p = "254" + p[1:]
	}
	if len(p) < 9 {
		return ""
	}
	return p
}
