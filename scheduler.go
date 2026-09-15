package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"marketers_portal/vanbooking"
)

var overdueAlertRecipients = []string{
	"systemadmin@proproperty.co.ke",
}

// initSchedulerTables creates the warning-tracking table used to prevent
// duplicate 12/13-day warning emails on each 30-minute tick.
func initSchedulerTables() {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_booking_warnings (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		booking_id INT NOT NULL,
		days_warned INT NOT NULL,
		warned_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_warn (booking_id, days_warned)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[scheduler] ERROR creating prop_booking_warnings: %v", err)
	}

	// Dedupes the buyer-facing day-10..14 SMS reminders (checkClientReminderSMS)
	// — a separate table from prop_booking_warnings above, which is an
	// internal staff email at day 12/13, not a buyer SMS; keeping them apart
	// avoids the two systems colliding on the same (booking, day) key.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_booking_sms_reminders (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		booking_id   INT NOT NULL,
		days_elapsed INT NOT NULL,
		sent_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_sms_reminder (booking_id, days_elapsed)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[scheduler] ERROR creating prop_booking_sms_reminders: %v", err)
	}
}

// startOverdueBookingChecker fires every 30 minutes to:
//  1. Send warning emails for plots booked exactly 12 or 13 days ago.
//  2. Send the buyer a daily SMS reminder from day 10 through day 14.
//  3. Auto-switch plots booked >14 days back to 'available' and notify.
func startOverdueBookingChecker() {
	go func() {
		// Run once immediately on startup, then every 30 minutes.
		checkWarningBookings()
		checkClientReminderSMS()
		checkOverdueBookings()

		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			checkWarningBookings()
			checkClientReminderSMS()
			checkOverdueBookings()
		}
	}()
}

// ── shared row types ──────────────────────────────────────────────────────────

type overdueRow struct {
	BookingID   int
	PlotID      int
	EstateID    int
	PlotNumber  string
	EstateName  string
	BuyerName   string
	BuyerPhone  string
	AgentName   string
	AgentPhone  string
	AgentEmail  string
	DateBooked  string
	ExpiryDate  string // date_booked + 14 days, formatted "Month DD, YYYY"
	DaysOver    int
	ZohoBooksID string
}

// ── 12/13-day warning ─────────────────────────────────────────────────────────

// checkWarningBookings finds plots booked exactly 12 or 13 days ago and sends
// a single warning email per (booking, day-count). Duplicate protection is
// handled by the UNIQUE KEY on prop_booking_warnings.
func checkWarningBookings() {
	rows, err := db.Query(`
		SELECT b.id, p.id, e.id, p.plot_number, e.name,
		       b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.agent_name,''),
		       COALESCE(a.phone,''), COALESCE(a.email,''),
		       DATE_FORMAT(b.date_booked,'%d %b %Y'),
		       DATE_FORMAT(DATE_ADD(b.date_booked, INTERVAL 14 DAY),'%M %d, %Y'),
		       DATEDIFF(NOW(), b.date_booked) AS days_over
		FROM prop_bookings b
		JOIN prop_plots   p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE p.status  = 'booked'
		  AND b.status  = 'active'
		  AND DATEDIFF(COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)), NOW()) IN (2, 1)
		ORDER BY b.date_booked ASC`)
	if err != nil {
		log.Printf("[scheduler] warning query error: %v", err)
		return
	}
	defer rows.Close()

	var toWarn []overdueRow
	for rows.Next() {
		var r overdueRow
		if err := rows.Scan(&r.BookingID, &r.PlotID, &r.EstateID, &r.PlotNumber, &r.EstateName,
			&r.BuyerName, &r.BuyerPhone, &r.AgentName, &r.AgentPhone, &r.AgentEmail,
			&r.DateBooked, &r.ExpiryDate, &r.DaysOver); err == nil {
			toWarn = append(toWarn, r)
		}
	}

	if len(toWarn) == 0 {
		return
	}

	// Filter out bookings already warned at this day count.
	var fresh []overdueRow
	for _, r := range toWarn {
		res, err := db.Exec(
			`INSERT IGNORE INTO prop_booking_warnings (booking_id, days_warned) VALUES (?, ?)`,
			r.BookingID, r.DaysOver,
		)
		if err != nil {
			log.Printf("[scheduler] warning insert error booking=%d days=%d: %v", r.BookingID, r.DaysOver, err)
			continue
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			fresh = append(fresh, r) // first time warning for this booking+day
		}
	}

	if len(fresh) == 0 {
		return
	}

	// Group by day count for cleaner email subjects.
	for _, day := range []int{12, 13} {
		var subset []overdueRow
		for _, r := range fresh {
			if r.DaysOver == day {
				subset = append(subset, r)
			}
		}
		if len(subset) == 0 {
			continue
		}
		sendWarningEmail(subset, day)
	}

	// Email + WhatsApp: notify each booking agent individually.
	for _, r := range fresh {
		n := 14 - r.DaysOver
		daysLeft := fmt.Sprintf("%d days", n)
		if n == 1 {
			daysLeft = "1 day"
		}

		// Individual email to the agent.
		if r.AgentEmail != "" {
			subject := fmt.Sprintf("⚠ Your booking for Plot %s expires in %s", r.PlotNumber, daysLeft)
			body := fmt.Sprintf(
				"Dear %s,\n\nYour booking for Plot %s at %s is expiring in %s (on %s).\n\n"+
					"Please ensure all required documents are uploaded to retain this booking. "+
					"If no action is taken, the plot will be automatically released back to available.\n\n"+
					"Log in to the portal to update the booking:\n"+
					"http://localhost:8080/agent/bookings\n\n"+
					"Client: %s\nBooked on: %s\n\nRegards,\nPro-Property Team",
				r.AgentName, r.PlotNumber, r.EstateName, daysLeft, r.ExpiryDate,
				r.BuyerName, r.DateBooked,
			)
			go func(email, subj, bod string) {
				if err := sendPlainEmail([]string{email}, subj, bod); err != nil {
					log.Printf("[scheduler] agent warning email error (%s): %v", email, err)
				}
			}(r.AgentEmail, subject, body)
		} else {
			log.Printf("[scheduler] no email for agent %q (booking %d), skipping agent email", r.AgentName, r.BookingID)
		}

		// WhatsApp to the agent.
		if r.AgentPhone != "" {
			go sendWhatsApp(r.AgentPhone, "bookingexpiry", []string{
				r.AgentName, r.EstateName, r.PlotNumber, daysLeft, r.ExpiryDate,
			})
		} else {
			log.Printf("[scheduler] no phone for agent %q (booking %d), skipping WA", r.AgentName, r.BookingID)
		}
	}
}

func sendWarningEmail(rows []overdueRow, day int) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Hello,\n\nThe following %d plot(s) have been in BOOKED status for %d days "+
			"and will be automatically released back to AVAILABLE in %d day(s) if not updated.\n\n"+
			"Please follow up urgently.\n\n",
		len(rows), day, 14-day,
	))
	sb.WriteString(fmt.Sprintf("%-12s  %-30s  %-25s  %-15s  %-20s  %s\n",
		"Plot", "Estate", "Client", "Phone", "Agent", "Booked On"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("%-12s  %-30s  %-25s  %-15s  %-20s  %s\n",
			r.PlotNumber, r.EstateName, r.BuyerName, r.BuyerPhone, r.AgentName, r.DateBooked))
	}
	sb.WriteString("\nPlease log in to the portal to update these bookings.\n")

	subject := fmt.Sprintf("⚠ %d Plot(s) Approaching Expiry — %d Days Booked", len(rows), day)
	if err := sendPlainEmail(overdueAlertRecipients, subject, sb.String()); err != nil {
		log.Printf("[scheduler] warning email error (day=%d): %v", day, err)
	} else {
		log.Printf("[scheduler] warning email sent for %d plot(s) at day %d", len(rows), day)
	}
}

// ── buyer/agent SMS reminders, day 10 through day 14 ──────────────────────────

// checkClientReminderSMS sends a daily SMS reminder from day 10 through day
// 14 (inclusive) after booking, to whichever audience Settings ->
// Notifications is set to (buyer, agent, or both), for as long as the
// booking is still 'active' — i.e. the deposit threshold and/or KYC docs
// are still incomplete (maybeAdvanceToAccountsReview moves it out of
// 'active', and out of this query, the moment both are met). Deduped per
// (booking, day) via prop_booking_sms_reminders — shared across both
// audiences, so a 30-minute tick never double-sends the same day's
// reminder even if the setting changes mid-day. This is separate from
// checkWarningBookings' day-12/13 internal staff email above — that alerts
// staff, this nudges the buyer/agent.
func checkClientReminderSMS() {
	notifyBuyer := notifyBuyerEnabled()
	notifyAgent := notifyAgentEnabled()
	if !notifyBuyer && !notifyAgent {
		return
	}
	rows, err := db.Query(`
		SELECT b.id, p.plot_number, e.name, COALESCE(b.buyer_phone,''),
		       COALESCE(b.agent_name,''), COALESCE(a.phone,''),
		       DATEDIFF(NOW(), b.date_booked) AS days_elapsed
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE p.status = 'booked'
		  AND b.status = 'active'
		  AND DATEDIFF(NOW(), b.date_booked) BETWEEN 10 AND 14`)
	if err != nil {
		log.Printf("[scheduler] client reminder query error: %v", err)
		return
	}
	defer rows.Close()

	type reminderRow struct {
		BookingID   int
		PlotNumber  string
		EstateName  string
		BuyerPhone  string
		AgentName   string
		AgentPhone  string
		DaysElapsed int
	}
	var toRemind []reminderRow
	for rows.Next() {
		var rr reminderRow
		if err := rows.Scan(&rr.BookingID, &rr.PlotNumber, &rr.EstateName, &rr.BuyerPhone,
			&rr.AgentName, &rr.AgentPhone, &rr.DaysElapsed); err == nil {
			toRemind = append(toRemind, rr)
		}
	}

	for _, rr := range toRemind {
		res, err := db.Exec(`INSERT IGNORE INTO prop_booking_sms_reminders (booking_id, days_elapsed) VALUES (?,?)`,
			rr.BookingID, rr.DaysElapsed)
		if err != nil {
			log.Printf("[scheduler] reminder dedupe error booking=%d day=%d: %v", rr.BookingID, rr.DaysElapsed, err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // already sent today's reminder for this booking
		}

		daysLeftMsg := fmt.Sprintf("%d day(s) left", 14-rr.DaysElapsed)
		if rr.DaysElapsed >= 14 {
			daysLeftMsg = "today is the last day"
		}

		if notifyBuyer {
			if rr.BuyerPhone == "" {
				log.Printf("[scheduler] no phone for buyer on booking %d, skipping reminder SMS", rr.BookingID)
			} else {
				msg := fmt.Sprintf(
					"Reminder: Plot %s at %s — payment/documents still pending. %s to pay the deposit threshold and submit your ID copy, KRA PIN and passport photo, or the plot will be released back to available. - Pro-Property",
					rr.PlotNumber, rr.EstateName, daysLeftMsg,
				)
				go vanbooking.SendSMS(rr.BuyerPhone, msg)
			}
		}
		if notifyAgent {
			if rr.AgentPhone == "" {
				log.Printf("[scheduler] no phone for agent %q on booking %d, skipping reminder SMS", rr.AgentName, rr.BookingID)
			} else {
				msg := fmt.Sprintf(
					"Reminder: Your client's booking for Plot %s at %s still needs payment/documents. %s before the plot is released back to available. - Pro-Property",
					rr.PlotNumber, rr.EstateName, daysLeftMsg,
				)
				go vanbooking.SendSMS(rr.AgentPhone, msg)
			}
		}
	}
}

// ── >14-day auto-release ──────────────────────────────────────────────────────

// checkOverdueBookings finds booked plots older than 14 days, switches them
// back to 'available', cancels the booking, and sends a notification email.
func checkOverdueBookings() {
	rows, err := db.Query(`
		SELECT b.id, p.id, e.id, p.plot_number, e.name,
		       b.buyer_name, COALESCE(b.buyer_phone,''), COALESCE(b.agent_name,''),
		       COALESCE(a.phone,''), COALESCE(a.email,''),
		       DATE_FORMAT(b.date_booked,'%d %b %Y'),
		       DATE_FORMAT(DATE_ADD(b.date_booked, INTERVAL 14 DAY),'%M %d, %Y'),
		       DATEDIFF(NOW(), b.date_booked) AS days_over,
		       COALESCE(b.zoho_books_id,'')
		FROM prop_bookings b
		JOIN prop_plots   p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		LEFT JOIN prop_agents a ON a.name = b.agent_name
		WHERE p.status = 'booked'
		  AND b.status = 'active'
		  AND COALESCE(b.booking_deadline, DATE_ADD(b.date_booked, INTERVAL 14 DAY)) < NOW()
		ORDER BY b.date_booked ASC`)
	if err != nil {
		log.Printf("[scheduler] overdue query error: %v", err)
		return
	}
	defer rows.Close()

	var overdue []overdueRow
	for rows.Next() {
		var r overdueRow
		if err := rows.Scan(&r.BookingID, &r.PlotID, &r.EstateID, &r.PlotNumber, &r.EstateName,
			&r.BuyerName, &r.BuyerPhone, &r.AgentName, &r.AgentPhone, &r.AgentEmail,
			&r.DateBooked, &r.ExpiryDate, &r.DaysOver, &r.ZohoBooksID); err == nil {
			overdue = append(overdue, r)
		}
	}

	if len(overdue) == 0 {
		return
	}

	// Auto-switch each plot back to available.
	var switched []overdueRow
	for _, r := range overdue {
		_, err1 := db.Exec(`UPDATE prop_plots SET status = 'available' WHERE id = ?`, r.PlotID)
		_, err2 := db.Exec(`UPDATE prop_bookings SET status = 'expired' WHERE id = ?`, r.BookingID)
		if err1 != nil || err2 != nil {
			log.Printf("[scheduler] auto-release error plot=%s booking=%d: plot_err=%v booking_err=%v",
				r.PlotNumber, r.BookingID, err1, err2)
			continue
		}
		logPlotStatus(r.PlotID, r.PlotNumber, r.EstateName, r.EstateID, "booked", "available", "scheduler", "auto-released after deadline")
		log.Printf("[scheduler] auto-released plot=%s (booking=%d, %d days booked)",
			r.PlotNumber, r.BookingID, r.DaysOver)
		if r.ZohoBooksID != "" {
			go cancelBooksEstimate(r.ZohoBooksID)
		}
		switched = append(switched, r)
	}

	if len(switched) == 0 {
		return
	}

	// Send notification email for switched plots.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Hello,\n\nThe following %d plot(s) have been automatically switched back to AVAILABLE "+
			"because they remained in BOOKED status for more than 14 days without being updated.\n\n",
		len(switched),
	))
	sb.WriteString(fmt.Sprintf("%-12s  %-30s  %-25s  %-15s  %-20s  %s\n",
		"Plot", "Estate", "Client", "Phone", "Agent", "Booked On (Days)"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	for _, r := range switched {
		sb.WriteString(fmt.Sprintf("%-12s  %-30s  %-25s  %-15s  %-20s  %s (%d days)\n",
			r.PlotNumber, r.EstateName, r.BuyerName, r.BuyerPhone, r.AgentName,
			r.DateBooked, r.DaysOver))
	}
	sb.WriteString("\nThese plots are now available for re-booking. Please follow up with the clients.\n")

	subject := fmt.Sprintf("🔄 %d Plot(s) Auto-Released Back to Available", len(switched))
	if err := sendPlainEmail(overdueAlertRecipients, subject, sb.String()); err != nil {
		log.Printf("[scheduler] auto-release email error: %v", err)
	} else {
		log.Printf("[scheduler] auto-release email sent for %d plot(s)", len(switched))
	}

	// Email + WhatsApp: notify each affected agent that their booking was released.
	for _, r := range switched {
		// Individual email to the agent.
		if r.AgentEmail != "" {
			subject := fmt.Sprintf("🔄 Your booking for Plot %s has been released", r.PlotNumber)
			body := fmt.Sprintf(
				"Dear %s,\n\nYour booking for Plot %s at %s has expired and has been automatically "+
					"released back to available.\n\n"+
					"The booking was open for %d days without being fully completed.\n\n"+
					"Client: %s\nBooked on: %s\n\n"+
					"If you wish to re-book this plot, please log in to the portal:\n"+
					"http://localhost:8080/agent/bookings\n\nRegards,\nPro-Property Team",
				r.AgentName, r.PlotNumber, r.EstateName, r.DaysOver,
				r.BuyerName, r.DateBooked,
			)
			go func(email, subj, bod string) {
				if err := sendPlainEmail([]string{email}, subj, bod); err != nil {
					log.Printf("[scheduler] agent release email error (%s): %v", email, err)
				}
			}(r.AgentEmail, subject, body)
		} else {
			log.Printf("[scheduler] no email for agent %q (booking %d), skipping agent email", r.AgentName, r.BookingID)
		}

		// WhatsApp to the agent.
		if r.AgentPhone != "" {
			go sendWhatsApp(r.AgentPhone, "bookingexpiry", []string{
				r.AgentName, r.EstateName, r.PlotNumber, "Expired", r.ExpiryDate,
			})
		} else {
			log.Printf("[scheduler] no phone for agent %q (booking %d), skipping WA", r.AgentName, r.BookingID)
		}

		// SMS to the buyer: plot released back to available, request refund details.
		if r.BuyerPhone != "" {
			msg := fmt.Sprintf(
				"Dear %s, your booking for Plot %s at %s has expired after 14 days and the plot has been "+
					"switched back to available for sale. Kindly share your bank account details so we can "+
					"process a refund of the amount you paid — a cheque will be written. Contact us for assistance. - Pro-Property",
				r.BuyerName, r.PlotNumber, r.EstateName,
			)
			go vanbooking.SendSMS(r.BuyerPhone, msg)
		} else {
			log.Printf("[scheduler] no phone for buyer %q (booking %d), skipping release SMS", r.BuyerName, r.BookingID)
		}
	}
}
