package vanbooking

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// atoi parses s as an integer, returning def on error or non-positive values.
func atoi(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
		return v
	}
	return def
}

// Package-level deps injected by Init.
var (
	db            *sql.DB
	renderFn      func(w http.ResponseWriter, name string, data any)
	getAgent      func(r *http.Request) string
	pathSeg       func(prefix, path string) string
	isSystemAdmin func(r *http.Request) bool
	devMode       bool
)

// Init wires the van-booking package to the main app's shared dependencies.
// registerFeature should be permissions.RegisterFeature; it is called here to
// add van-booking features to the global permission registry.
func Init(
	d *sql.DB,
	render func(http.ResponseWriter, string, any),
	agentFn func(*http.Request) string,
	segFn func(string, string) string,
	registerFeature func(key, label, group, module string),
	isSystemAdminFn func(*http.Request) bool,
	dev bool,
) {
	db = d
	renderFn = render
	getAgent = agentFn
	pathSeg = segFn
	isSystemAdmin = isSystemAdminFn
	devMode = dev

	// Register van-booking features so they appear in the permissions UI.
	registerFeature("admin.van_bookings", "Van Bookings", "admin", "Van Booking")
	registerFeature("admin.van_approve", "Approve / Reject Bookings", "admin", "Van Booking")
	registerFeature("admin.van_notifications", "Receive In-app Notifications", "admin", "Van Booking")
	registerFeature("admin.van_sms", "Receive SMS Notifications", "admin", "Van Booking")
	registerFeature("admin.van_manage", "Van & Driver Management", "admin", "Van Booking")
	registerFeature("agent.van_booking", "Van Booking", "agent", "Van Booking")
	registerFeature("agent.van_notifications", "Receive Notifications (In-app + SMS)", "agent", "Van Booking")
	registerFeature("agent.van_fleet", "Fleet Manager (Approve/Reject Bookings)", "agent", "Van Booking")

	// Start background goroutine that auto-completes elapsed sessions.
	go func() {
		autoCompleteSessions()
		for range time.Tick(5 * time.Minute) {
			autoCompleteSessions()
		}
	}()
}

// autoCompleteSessions marks any non-done session as 'done' once its trip time
// has elapsed. Rules:
//   - Sessions with a trip time: done when trip_time_end (or trip_time + 2 h)
//     is in the past on today's date, or when the trip date is any past date.
//   - Sessions with no trip time: done when the trip date is a past date.
func autoCompleteSessions() {
	res, err := db.Exec(`
		UPDATE van_sessions
		SET trip_status = 'done'
		WHERE trip_status != 'done'
		AND (
			trip_date < CURDATE()
			OR (
				trip_date = CURDATE()
				AND trip_time IS NOT NULL
				AND COALESCE(trip_time_end, ADDTIME(trip_time, '02:00:00')) <= CURTIME()
			)
		)`)
	if err != nil {
		log.Printf("autoCompleteSessions: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("autoCompleteSessions: marked %d session(s) as done", n)
	}
}

// InitTables creates the van booking tables and seeds default vans.
func InitTables() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS van_vans (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			name        VARCHAR(100) NOT NULL,
			plate_number VARCHAR(20) NOT NULL,
			capacity    INT DEFAULT 0,
			status      ENUM('available','maintenance') DEFAULT 'available',
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS van_destinations (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			name       VARCHAR(150) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS van_bookings (
			id             INT AUTO_INCREMENT PRIMARY KEY,
			van_id         INT DEFAULT NULL,
			agent_name     VARCHAR(100) NOT NULL,
			destination_id INT DEFAULT NULL,
			trip_date      DATE DEFAULT NULL,
			purpose        TEXT,
			status         ENUM('pending','approved','rejected') DEFAULT 'pending',
			admin_notes    TEXT,
			created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS van_notifications (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			agent_name VARCHAR(100),
			message    TEXT NOT NULL,
			is_read    TINYINT(1) DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS van_sessions (
			id             INT AUTO_INCREMENT PRIMARY KEY,
			van_id         INT NOT NULL,
			destination_id INT NOT NULL,
			trip_date      DATE NOT NULL,
			trip_time      TIME DEFAULT NULL,
			purpose        TEXT,
			created_by     VARCHAR(100),
			created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			log.Printf("vanbooking InitTables: %v", err)
		}
	}
	db.Exec(`ALTER TABLE van_bookings ADD COLUMN session_id   INT DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_bookings ADD COLUMN seats        INT NOT NULL DEFAULT 1`)
	db.Exec(`ALTER TABLE van_bookings ADD COLUMN num_clients  INT NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE van_bookings ADD COLUMN lead_source  VARCHAR(50) DEFAULT NULL`)
	// Allow NULL for session-based bookings that don't have a direct van/destination/date
	db.Exec(`ALTER TABLE van_bookings MODIFY COLUMN van_id         INT DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_bookings MODIFY COLUMN destination_id INT DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_bookings MODIFY COLUMN trip_date      DATE DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_bookings MODIFY COLUMN status ENUM('pending','approved','rejected','cancelled') DEFAULT 'pending'`)
	db.Exec(`ALTER TABLE van_sessions ADD COLUMN trip_time     TIME DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_sessions ADD COLUMN trip_time_end TIME DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_sessions ADD COLUMN estates       VARCHAR(500) DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_sessions ADD COLUMN driver_id INT DEFAULT NULL`)
	db.Exec(`CREATE TABLE IF NOT EXISTS van_drivers (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		name       VARCHAR(100) NOT NULL,
		phone      VARCHAR(20)  NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`ALTER TABLE van_drivers ADD COLUMN agent_name VARCHAR(100) DEFAULT NULL`)
	db.Exec(`ALTER TABLE van_sessions ADD COLUMN trip_status ENUM('waiting','approved','done') DEFAULT 'waiting'`)
	db.Exec(`ALTER TABLE van_sessions MODIFY COLUMN trip_status ENUM('waiting','approved','done') DEFAULT 'waiting'`)
	db.Exec(`CREATE TABLE IF NOT EXISTS van_maintenance (
		id         INT AUTO_INCREMENT PRIMARY KEY,
		van_id     INT NOT NULL,
		start_date DATE NOT NULL,
		end_date   DATE NOT NULL,
		start_time TIME DEFAULT NULL,
		end_time   TIME DEFAULT NULL,
		reason     VARCHAR(200) DEFAULT NULL,
		type       VARCHAR(50)  DEFAULT 'maintenance',
		created_by VARCHAR(100) DEFAULT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`)
	db.Exec(`ALTER TABLE van_maintenance ADD COLUMN type VARCHAR(50) DEFAULT 'maintenance'`)

	// Seed default vans if none exist.
	var vanCount int
	db.QueryRow(`SELECT COUNT(*) FROM van_vans`).Scan(&vanCount)
	if vanCount == 0 {
		db.Exec(`INSERT INTO van_vans (name, plate_number, capacity) VALUES (?,?,?)`, "Van 1", "KDQ 048P", 14)
		db.Exec(`INSERT INTO van_vans (name, plate_number, capacity) VALUES (?,?,?)`, "Van 2", "KDW 305X", 14)
		log.Println("vanbooking: seeded default vans")
	}

	// Seed default destinations if none exist.
	var destCount int
	db.QueryRow(`SELECT COUNT(*) FROM van_destinations`).Scan(&destCount)
	if destCount == 0 {
		db.Exec(`INSERT INTO van_destinations (name) VALUES (?)`, "Nachu - Mikuyuini")
		db.Exec(`INSERT INTO van_destinations (name) VALUES (?)`, "Other Plots")
		db.Exec(`INSERT INTO van_destinations (name) VALUES (?)`, "Operations")
		log.Println("vanbooking: seeded default destinations")
	}
	// Ensure Operations destination exists even on older installs.
	var opsCount int
	db.QueryRow(`SELECT COUNT(*) FROM van_destinations WHERE name='Operations'`).Scan(&opsCount)
	if opsCount == 0 {
		db.Exec(`INSERT INTO van_destinations (name) VALUES ('Operations')`)
		log.Println("vanbooking: seeded Operations destination")
	}
	// Ensure Digital Team destination exists.
	var digitalCount int
	db.QueryRow(`SELECT COUNT(*) FROM van_destinations WHERE name='Digital Team'`).Scan(&digitalCount)
	if digitalCount == 0 {
		db.Exec(`INSERT INTO van_destinations (name) VALUES ('Digital Team')`)
		log.Println("vanbooking: seeded Digital Team destination")
	}
	// Ensure Rose Haven Team destination exists.
	var roseHavenCount int
	db.QueryRow(`SELECT COUNT(*) FROM van_destinations WHERE name='Rose Haven Team'`).Scan(&roseHavenCount)
	if roseHavenCount == 0 {
		db.Exec(`INSERT INTO van_destinations (name) VALUES ('Rose Haven Team')`)
		log.Println("vanbooking: seeded Rose Haven Team destination")
	}
}

// ── shared types ──────────────────────────────────────────────────────────────

// VanRow holds a single van record.
type VanRow struct {
	ID          int
	Name        string
	PlateNumber string
	Capacity    int
	Status      string
}

// VanDestRow holds a destination record.
type VanDestRow struct {
	ID   int
	Name string
}

// VanDriverRow holds a driver record.
type VanDriverRow struct {
	ID        int
	Name      string
	Phone     string
	AgentName string
}

// VanMaintenanceRow holds a maintenance/unavailability window for a van.
type VanMaintenanceRow struct {
	ID        int
	VanID     int
	VanName   string
	StartDate string // "YYYY-MM-DD"
	EndDate   string
	StartTime string // "HH:MM" or "" for full day
	EndTime   string // "HH:MM" or ""
	Type      string // "maintenance", "unavailable", "reserved", "other"
	Reason    string
	CreatedBy string
	CreatedAt string
}

// typeLabel returns a human-readable label for a maintenance type.
func (m VanMaintenanceRow) TypeLabel() string {
	switch m.Type {
	case "unavailable":
		return "Unavailable"
	case "reserved":
		return "Reserved"
	case "other":
		return "Unavailable"
	default:
		return "Maintenance"
	}
}

// VanBookingRow holds a booking record joined with van and destination info.
type VanBookingRow struct {
	ID          int
	VanName     string
	PlateNumber string
	AgentName   string
	Destination string
	TripDate    string
	TripTime    string
	Purpose     string
	Status      string
	AdminNotes  string
	CreatedAt   string
	Seats       int
	NumClients  int
	LeadSource  string
	DriverName  string
}

// VanSessionRow represents one session (van trip on a date).
type VanSessionRow struct {
	ID          int
	VanID       int
	VanName     string
	PlateNumber string
	Destination string
	TripDate    string
	TripTime    string   // e.g. "08:30"
	Purpose     string
	Estates     string   // comma-separated estate names
	SeatsTaken  int
	MaxSeats    int
	Remaining   int      // MaxSeats - SeatsTaken
	Agents      []string // agents with approved/pending bookings
	TripStatus  string   // "waiting" or "done"
}

func loadVans() []VanRow {
	rows, err := db.Query(`SELECT id, name, plate_number, capacity, status FROM van_vans ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []VanRow
	for rows.Next() {
		var v VanRow
		rows.Scan(&v.ID, &v.Name, &v.PlateNumber, &v.Capacity, &v.Status)
		out = append(out, v)
	}
	return out
}

func loadDests() []VanDestRow {
	rows, err := db.Query(`SELECT id, name FROM van_destinations ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []VanDestRow
	for rows.Next() {
		var d VanDestRow
		rows.Scan(&d.ID, &d.Name)
		out = append(out, d)
	}
	return out
}

// maintenanceConflict checks whether a van has a scheduled unavailability window
// that overlaps the given trip date and optional time range.
// Returns the maintenance ID (>0) and a human-readable conflict message, otherwise 0/"".
func maintenanceConflict(vanID, tripDate, tripTime, tripTimeEnd string) (int, string) {
	var mid int
	var reason, mType string
	if tripTime == "" {
		db.QueryRow(`
			SELECT id, COALESCE(reason,''), COALESCE(type,'maintenance') FROM van_maintenance
			WHERE van_id=? AND start_date<=? AND end_date>=?
			LIMIT 1`, vanID, tripDate, tripDate).Scan(&mid, &reason, &mType)
	} else {
		var endArg interface{}
		if tripTimeEnd != "" {
			endArg = tripTimeEnd
		}
		db.QueryRow(`
			SELECT id, COALESCE(reason,''), COALESCE(type,'maintenance') FROM van_maintenance
			WHERE van_id=? AND start_date<=? AND end_date>=?
			AND (
				start_time IS NULL
				OR (start_time < COALESCE(?, ADDTIME(?, '02:00:00'))
				    AND end_time > ?)
			) LIMIT 1`, vanID, tripDate, tripDate, endArg, tripTime, tripTime).Scan(&mid, &reason, &mType)
	}
	if mid == 0 {
		return 0, ""
	}
	var label string
	switch mType {
	case "unavailable":
		label = "unavailable"
	case "reserved":
		label = "reserved for another purpose"
	default:
		label = "under scheduled maintenance"
	}
	msg := "This van is " + label + " at that time"
	if reason != "" {
		msg += " (" + reason + ")"
	}
	return mid, msg
}

// UnreadNotifs returns the count of unread notifications for an agent.
func UnreadNotifs(agentName string) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM van_notifications WHERE (agent_name=? OR agent_name IS NULL) AND is_read=0`, agentName).Scan(&n)
	return n
}

// ── Agent: booking form ───────────────────────────────────────────────────────

// AgentVanBookingHandler handles the new session-based booking flow.
// GET /agent/van-booking              → date picker
// GET /agent/van-booking?date=...     → date picker + sessions for that date
// POST action=join&session_id=X       → join existing session
// POST action=new                     → create session + seat
// AdminVanBookHandler lets an admin book a van (same logic, admin sidebar template).
func AdminVanBookHandler(w http.ResponseWriter, r *http.Request) {
	vanBookingHandler(w, r, "admin_van_book.html", "admin_van_my_bookings.html", true)
}

// AgentVanBookingHandler handles the agent-facing booking form.
func AgentVanBookingHandler(w http.ResponseWriter, r *http.Request) {
	vanBookingHandler(w, r, "agent_van_booking.html", "agent_van_bookings.html", false)
}

func vanBookingHandler(w http.ResponseWriter, r *http.Request, bookTmpl, myBookingsTmpl string, isAdmin bool) {
	agentName := getAgent(r)
	dests := loadDests()
	vans := loadVans()

	type estateOpt struct{ ID int; Name string }
	var estateOpts []estateOpt
	if erows, err := db.Query(`SELECT id, name FROM prop_estates ORDER BY name`); err == nil {
		defer erows.Close()
		for erows.Next() {
			var e estateOpt
			erows.Scan(&e.ID, &e.Name)
			estateOpts = append(estateOpts, e)
		}
	}

	data := map[string]any{
		"Active":          "van-new",
		"AgentName":       agentName,
		"Destinations":    dests,
		"Vans":            vans,
		"Estates":         estateOpts,
		"UnreadCount":     UnreadNotifs(agentName),
		"IsFleetManager":  canApprove(agentName),
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		action := r.FormValue("action")
		tripDate := r.FormValue("trip_date")

		successURL := "/agent/van-bookings?success=1"
		if isAdmin {
			successURL = "/admin/van-my-bookings?success=1"
		}

		if action == "join" {
			sessionID := r.FormValue("session_id")
			purpose := strings.TrimSpace(r.FormValue("purpose"))
			leadSource := strings.TrimSpace(r.FormValue("lead_source"))
			seats := atoi(r.FormValue("seats"), 1)
			numClients := atoi(r.FormValue("num_clients"), 0)
			if sessionID == "" || seats < 1 {
				data["FormError"] = "Invalid session or seat count."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			// Look up van + time for this session to check maintenance.
			var sessVanID, sessTripDate, sessTripTime, sessTripTimeEnd string
			db.QueryRow(`SELECT van_id, DATE_FORMAT(trip_date,'%Y-%m-%d'), COALESCE(TIME_FORMAT(trip_time,'%H:%i'),''), COALESCE(TIME_FORMAT(trip_time_end,'%H:%i'),'') FROM van_sessions WHERE id=?`, sessionID).
				Scan(&sessVanID, &sessTripDate, &sessTripTime, &sessTripTimeEnd)
			if mid, conflictMsg := maintenanceConflict(sessVanID, sessTripDate, sessTripTime, sessTripTimeEnd); mid > 0 {
				data["FormError"] = conflictMsg + ". Bookings cannot be made during this period."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			var taken int
			db.QueryRow(`SELECT COALESCE(SUM(seats),0) FROM van_bookings WHERE session_id=? AND status NOT IN ('rejected','cancelled')`, sessionID).Scan(&taken)
			remaining := 11 - taken
			if seats > remaining {
				data["FormError"] = fmt.Sprintf("Only %d seat(s) remaining in this session.", remaining)
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}

			_, err := db.Exec(`INSERT INTO van_bookings (session_id, agent_name, purpose, seats, num_clients, lead_source) VALUES (?,?,?,?,?,?)`,
				sessionID, agentName, purpose, seats, numClients, leadSource)
			if err != nil {
				log.Printf("van join session: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
			// Get session info for notification message
			var destName, tripDateStr string
			db.QueryRow(`SELECT d.name, DATE_FORMAT(s.trip_date,'%d %b %Y') FROM van_sessions s JOIN van_destinations d ON d.id=s.destination_id WHERE s.id=?`, sessionID).Scan(&destName, &tripDateStr)
			msg := fmt.Sprintf("New van booking: %s booked %d seat(s) to %s on %s.", agentName, seats, destName, tripDateStr)
			go notifyAdmins(msg)
			go notifyFleetManagersSMS(msg)
			http.Redirect(w, r, successURL, http.StatusFound)
			return
		}

		if action == "new" {
			vanID := r.FormValue("van_id")
			destID := r.FormValue("destination_id")
			tripTime := strings.TrimSpace(r.FormValue("trip_time"))
			tripTimeEnd := strings.TrimSpace(r.FormValue("trip_time_end"))
			// "custom" is the sentinel value from the select; the actual times come from trip_time_custom / trip_time_end_custom
			if tripTime == "custom" {
				tripTime = strings.TrimSpace(r.FormValue("trip_time_custom"))
				tripTimeEnd = strings.TrimSpace(r.FormValue("trip_time_end_custom"))
			}
			purpose := strings.TrimSpace(r.FormValue("purpose"))
			leadSource := strings.TrimSpace(r.FormValue("lead_source"))
			estates := strings.Join(r.Form["estates"], ", ")
			seats := atoi(r.FormValue("seats"), 1)
			numClients := atoi(r.FormValue("num_clients"), 0)
			if vanID == "" || destID == "" || tripDate == "" {
				data["FormError"] = "Please fill in all required fields."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			var vanStatus string
			db.QueryRow(`SELECT status FROM van_vans WHERE id=?`, vanID).Scan(&vanStatus)
			if vanStatus == "maintenance" {
				data["FormError"] = "That van is currently under maintenance."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			// Check scheduled unavailability window.
			if mid, conflictMsg := maintenanceConflict(vanID, tripDate, tripTime, tripTimeEnd); mid > 0 {
				data["FormError"] = conflictMsg + ". Choose a different van or time slot."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			// Check for time conflict: same van, same day, overlapping time slot.
			if tripTime != "" {
				var endArg interface{}
				if tripTimeEnd != "" {
					endArg = tripTimeEnd
				}
				var conflictID int
				var conflictDest, conflictTime string
				db.QueryRow(`
					SELECT s.id, COALESCE(d.name, ''), TIME_FORMAT(s.trip_time,'%H:%i')
					FROM van_sessions s
					LEFT JOIN van_destinations d ON d.id = s.destination_id
					WHERE s.van_id = ? AND s.trip_date = ?
					AND s.trip_time IS NOT NULL
					AND s.trip_time < COALESCE(?, ADDTIME(?, '02:00:00'))
					AND COALESCE(s.trip_time_end, ADDTIME(s.trip_time, '02:00:00')) > ?
					LIMIT 1`,
					vanID, tripDate, endArg, tripTime, tripTime).Scan(&conflictID, &conflictDest, &conflictTime)
				if conflictID > 0 {
					msg := "This van already has a session at that time"
					if conflictDest != "" && conflictTime != "" {
						msg += fmt.Sprintf(" (%s at %s)", conflictDest, conflictTime)
					} else if conflictTime != "" {
						msg += fmt.Sprintf(" (%s)", conflictTime)
					}
					msg += ". Join that existing trip above if seats are available, or choose a different time slot."
					data["FormError"] = msg
					data["SelectedDate"] = tripDate
					loadSessionsForDate(tripDate, data)
					renderFn(w, bookTmpl, data)
					return
				}
			}
			if seats > 11 {
				data["FormError"] = "Cannot book more than 11 seats."
				data["SelectedDate"] = tripDate
				loadSessionsForDate(tripDate, data)
				renderFn(w, bookTmpl, data)
				return
			}
			var tripTimeArg, tripTimeEndArg interface{}
			if tripTime != "" {
				tripTimeArg = tripTime
			}
			if tripTimeEnd != "" {
				tripTimeEndArg = tripTimeEnd
			}
			res, err := db.Exec(`INSERT INTO van_sessions (van_id, destination_id, trip_date, trip_time, trip_time_end, purpose, created_by, estates) VALUES (?,?,?,?,?,?,?,?)`,
				vanID, destID, tripDate, tripTimeArg, tripTimeEndArg, purpose, agentName, estates)
			if err != nil {
				log.Printf("van create session: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
			sid, _ := res.LastInsertId()
			_, err = db.Exec(`INSERT INTO van_bookings (session_id, agent_name, purpose, seats, num_clients, lead_source) VALUES (?,?,?,?,?,?)`,
				sid, agentName, purpose, seats, numClients, leadSource)
			if err != nil {
				log.Printf("van booking insert: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
			var destName string
			db.QueryRow(`SELECT name FROM van_destinations WHERE id=?`, destID).Scan(&destName)
			msg2 := fmt.Sprintf("New van session created: %s booked %d seat(s) to %s on %s.", agentName, seats, destName, tripDate)
			go notifyAdmins(msg2)
			go notifyFleetManagersSMS(msg2)
			http.Redirect(w, r, successURL, http.StatusFound)
			return
		}
	}

	// GET — load sessions if date provided
	if date := r.URL.Query().Get("date"); date != "" {
		data["SelectedDate"] = date
		loadSessionsForDate(date, data)
	}
	data["SelectedTime"] = r.URL.Query().Get("time")
	data["Today"] = time.Now().Format("2006-01-02")
	renderFn(w, bookTmpl, data)
}

// loadSessionsForDate populates data["VanSessions"] with sessions grouped by van for a given date.
func loadSessionsForDate(date string, data map[string]any) {
	rows, err := db.Query(`
		SELECT s.id, s.van_id, v.name, v.plate_number, d.name, COALESCE(s.purpose,''),
		       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),''),
		       COALESCE(SUM(b.seats),0), COALESCE(s.estates,'')
		FROM van_sessions s
		JOIN van_vans v ON v.id = s.van_id
		JOIN van_destinations d ON d.id = s.destination_id
		LEFT JOIN van_bookings b ON b.session_id = s.id AND b.status NOT IN ('rejected','cancelled')
		WHERE s.trip_date = ?
		GROUP BY s.id
		ORDER BY s.van_id, s.trip_time, s.id`, date)
	if err != nil {
		log.Printf("loadSessionsForDate: %v", err)
		return
	}
	defer rows.Close()

	sessionMap := map[int]*VanSessionRow{} // van_id → latest info (we use slice for ordering)
	var vanOrder []int
	var sessions []*VanSessionRow
	vanSeen := map[int]bool{}

	for rows.Next() {
		var s VanSessionRow
		rows.Scan(&s.ID, &s.VanID, &s.VanName, &s.PlateNumber, &s.Destination, &s.Purpose, &s.TripTime, &s.SeatsTaken, &s.Estates)
		s.MaxSeats = 11
		s.Remaining = s.MaxSeats - s.SeatsTaken
		if s.Remaining < 0 {
			s.Remaining = 0
		}

		// Load agent names + seats for this session
		arows, _ := db.Query(`SELECT agent_name, seats FROM van_bookings WHERE session_id=? AND status NOT IN ('rejected','cancelled') ORDER BY created_at`, s.ID)
		if arows != nil {
			for arows.Next() {
				var name string
				var seats int
				arows.Scan(&name, &seats)
				if seats > 1 {
					s.Agents = append(s.Agents, fmt.Sprintf("%s (%d seats)", name, seats))
				} else {
					s.Agents = append(s.Agents, name)
				}
			}
			arows.Close()
		}

		sessions = append(sessions, &s)
		if !vanSeen[s.VanID] {
			vanSeen[s.VanID] = true
			vanOrder = append(vanOrder, s.VanID)
		}
		sessionMap[s.ID] = &s // not used by key but kept for reference
	}
	_ = sessionMap

	// Group by van
	type vanGroup struct {
		VanID       int
		VanName     string
		PlateNumber string
		Sessions    []*VanSessionRow
		Maintenance []VanMaintenanceRow
	}
	groupMap := map[int]*vanGroup{}
	var groups []*vanGroup

	for _, s := range sessions {
		if _, ok := groupMap[s.VanID]; !ok {
			g := &vanGroup{VanID: s.VanID, VanName: s.VanName, PlateNumber: s.PlateNumber}
			groupMap[s.VanID] = g
			groups = append(groups, g)
		}
		groupMap[s.VanID].Sessions = append(groupMap[s.VanID].Sessions, s)
	}

	// Add vans with no sessions
	allVans, _ := db.Query(`SELECT id, name, plate_number FROM van_vans WHERE status != 'maintenance'`)
	if allVans != nil {
		defer allVans.Close()
		for allVans.Next() {
			var vid int
			var vname, plate string
			allVans.Scan(&vid, &vname, &plate)
			if _, ok := groupMap[vid]; !ok {
				groups = append(groups, &vanGroup{VanID: vid, VanName: vname, PlateNumber: plate})
			}
		}
	}
	_ = vanOrder

	// Build a map of all groups by van_id for maintenance attachment.
	allGroupMap := map[int]*vanGroup{}
	for _, g := range groups {
		allGroupMap[g.VanID] = g
	}

	// Load maintenance windows for all vans on this date and attach.
	if mrows, err := db.Query(`
		SELECT m.van_id, m.id,
		       COALESCE(TIME_FORMAT(m.start_time,'%H:%i'),''),
		       COALESCE(TIME_FORMAT(m.end_time,'%H:%i'),''),
		       COALESCE(m.type,'maintenance'),
		       COALESCE(m.reason,'')
		FROM van_maintenance m
		WHERE m.start_date <= ? AND m.end_date >= ?
		ORDER BY m.start_time`, date, date); err == nil {
		defer mrows.Close()
		for mrows.Next() {
			var vid int
			var m VanMaintenanceRow
			mrows.Scan(&vid, &m.ID, &m.StartTime, &m.EndTime, &m.Type, &m.Reason)
			m.VanID = vid
			if g, ok := allGroupMap[vid]; ok {
				g.Maintenance = append(g.Maintenance, m)
			}
		}
	}

	data["VanGroups"] = groups
}

// ── Agent/Admin: my bookings list ────────────────────────────────────────────

func AdminVanMyBookingsHandler(w http.ResponseWriter, r *http.Request) {
	myBookingsHandler(w, r, "admin_van_my_bookings.html", "van-my-bookings")
}

func AgentVanBookingsHandler(w http.ResponseWriter, r *http.Request) {
	myBookingsHandler(w, r, "agent_van_bookings.html", "van-bookings")
}

func myBookingsHandler(w http.ResponseWriter, r *http.Request, tmpl, active string) {
	agentName := getAgent(r)

	rows, err := db.Query(`
		SELECT b.id,
		       COALESCE(v2.name, v.name, ''),
		       COALESCE(v2.plate_number, v.plate_number, ''),
		       COALESCE(d2.name, d.name, ''),
		       COALESCE(DATE_FORMAT(s.trip_date,'%d %b %Y'), DATE_FORMAT(b.trip_date,'%d %b %Y'), ''),
		       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),''),
		       COALESCE(s.purpose, b.purpose, ''),
		       b.status,
		       COALESCE(b.admin_notes,''),
		       DATE_FORMAT(b.created_at,'%d %b %Y'),
		       COALESCE(dr.name,'')
		FROM van_bookings b
		LEFT JOIN van_sessions s ON s.id = b.session_id
		LEFT JOIN van_vans v2 ON v2.id = s.van_id
		LEFT JOIN van_destinations d2 ON d2.id = s.destination_id
		LEFT JOIN van_vans v ON v.id = b.van_id
		LEFT JOIN van_destinations d ON d.id = b.destination_id
		LEFT JOIN van_drivers dr ON dr.id = s.driver_id
		WHERE b.agent_name = ?
		ORDER BY b.created_at DESC`, agentName)
	if err != nil {
		log.Printf("myBookingsHandler: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var bookings []VanBookingRow
	for rows.Next() {
		var b VanBookingRow
		rows.Scan(&b.ID, &b.VanName, &b.PlateNumber, &b.Destination,
			&b.TripDate, &b.TripTime, &b.Purpose, &b.Status, &b.AdminNotes, &b.CreatedAt, &b.DriverName)
		bookings = append(bookings, b)
	}

	renderFn(w, tmpl, map[string]any{
		"Active":         active,
		"AgentName":      agentName,
		"Bookings":       bookings,
		"Success":        r.URL.Query().Get("success") == "1",
		"UnreadCount":    UnreadNotifs(agentName),
		"IsSystemAdmin":  isSystemAdmin(r),
		"IsFleetManager": canApprove(agentName),
	})
}

// AgentVanCancelHandler cancels a van booking made by the logged-in agent.
// POST /agent/van-booking/{id}/cancel
func AgentVanCancelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agentName := getAgent(r)
	bookingID := pathSeg("/agent/van-booking/", r.URL.Path)

	var status, owner string
	db.QueryRow(`SELECT status, agent_name FROM van_bookings WHERE id=?`, bookingID).Scan(&status, &owner)
	if owner != agentName {
		http.Error(w, "Not your booking.", http.StatusForbidden)
		return
	}
	if status == "cancelled" || status == "rejected" {
		http.Redirect(w, r, "/agent/van-bookings", http.StatusFound)
		return
	}

	db.Exec(`UPDATE van_bookings SET status='cancelled', admin_notes='Cancelled by agent' WHERE id=?`, bookingID)

	var destName, tripDate string
	db.QueryRow(`
		SELECT COALESCE(d2.name, d.name,''), COALESCE(DATE_FORMAT(s.trip_date,'%d %b %Y'), DATE_FORMAT(b.trip_date,'%d %b %Y'),'')
		FROM van_bookings b
		LEFT JOIN van_sessions s ON s.id = b.session_id
		LEFT JOIN van_destinations d2 ON d2.id = s.destination_id
		LEFT JOIN van_destinations d ON d.id = b.destination_id
		WHERE b.id=?`, bookingID).Scan(&destName, &tripDate)
	go notifyAdmins(fmt.Sprintf("%s cancelled their van booking to %s on %s.", agentName, destName, tripDate))

	http.Redirect(w, r, "/agent/van-bookings", http.StatusFound)
}

// ── Agent: notifications ──────────────────────────────────────────────────────

func AgentVanNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgent(r)

	type notifRow struct {
		ID        int
		Message   string
		IsRead    bool
		CreatedAt string
	}

	rows, err := db.Query(`
		SELECT id, message, is_read, DATE_FORMAT(created_at,'%d %b %Y %H:%i')
		FROM van_notifications
		WHERE agent_name=? OR agent_name IS NULL
		ORDER BY created_at DESC`, agentName)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var notifs []notifRow
	for rows.Next() {
		var n notifRow
		var isRead int
		rows.Scan(&n.ID, &n.Message, &isRead, &n.CreatedAt)
		n.IsRead = isRead == 1
		notifs = append(notifs, n)
	}

	db.Exec(`UPDATE van_notifications SET is_read=1 WHERE agent_name=? OR agent_name IS NULL`, agentName)

	renderFn(w, "agent_van_notifications.html", map[string]any{
		"Active":         "van-notif",
		"AgentName":      agentName,
		"Notifications":  notifs,
		"IsFleetManager": canApprove(agentName),
	})
}

// AdminVanNotificationsHandler shows notifications for an admin user.
func AdminVanNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	adminName := getAgent(r)

	type notifRow struct {
		ID        int
		Message   string
		IsRead    bool
		CreatedAt string
	}

	rows, err := db.Query(`
		SELECT id, message, is_read, DATE_FORMAT(created_at,'%d %b %Y %H:%i')
		FROM van_notifications
		WHERE agent_name=?
		ORDER BY created_at DESC`, adminName)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var notifs []notifRow
	for rows.Next() {
		var n notifRow
		var isRead int
		rows.Scan(&n.ID, &n.Message, &isRead, &n.CreatedAt)
		n.IsRead = isRead == 1
		notifs = append(notifs, n)
	}

	db.Exec(`UPDATE van_notifications SET is_read=1 WHERE agent_name=?`, adminName)

	renderFn(w, "admin_van_notifications.html", map[string]any{
		"Active":        "van-notif",
		"AgentName":     adminName,
		"Notifications": notifs,
		"IsSystemAdmin": isSystemAdmin(r),
		"UnreadCount":   0, // just marked all as read
	})
}

// ── Admin: all bookings + approve/reject ──────────────────────────────────────

// AdminVanBookingsHandler shows pending/waiting trip sessions.
func AdminVanBookingsHandler(w http.ResponseWriter, r *http.Request) {
	adminVanBookingsPage(w, r, "waiting")
}

// AdminVanBookingsApprovedHandler shows approved trip sessions.
func AdminVanBookingsApprovedHandler(w http.ResponseWriter, r *http.Request) {
	adminVanBookingsPage(w, r, "approved")
}

// AdminVanBookingsDoneHandler shows completed trip sessions.
func AdminVanBookingsDoneHandler(w http.ResponseWriter, r *http.Request) {
	adminVanBookingsPage(w, r, "done")
}

func adminVanBookingsPage(w http.ResponseWriter, r *http.Request, statusFilter string) {
	autoCompleteSessions() // ensure elapsed trips are moved to done before rendering
	dateFilter := r.URL.Query().Get("date")

	// Build WHERE / HAVING clauses based on the tab:
	//   "waiting"  → session is not done AND has at least one pending booking
	//   "approved" → session is not done AND has NO pending bookings
	//   "done"     → session trip_status is 'done'
	dateClause := ""
	var args []any
	if dateFilter != "" {
		dateClause = " AND s.trip_date = ?"
		args = append(args, dateFilter)
	}

	var whereClause, havingClause string
	switch statusFilter {
	case "done":
		whereClause = "WHERE COALESCE(s.trip_status,'waiting') = 'done'" + dateClause
	case "approved":
		whereClause = "WHERE COALESCE(s.trip_status,'waiting') != 'done'" + dateClause
		havingClause = "HAVING SUM(CASE WHEN b.status = 'pending' THEN 1 ELSE 0 END) = 0"
	default: // "waiting" / pending approval
		whereClause = "WHERE COALESCE(s.trip_status,'waiting') != 'done'" + dateClause
		havingClause = "HAVING SUM(CASE WHEN b.status = 'pending' THEN 1 ELSE 0 END) > 0"
	}

	query := `
		SELECT s.id, v.name, v.plate_number, d.name,
		       DATE_FORMAT(s.trip_date,'%d %b %Y'), COALESCE(s.purpose,''),
		       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),''),
		       s.created_by,
		       COALESCE(SUM(b.seats),0) as seats_taken,
		       COALESCE(dr.name,''), COALESCE(s.driver_id,0),
		       COALESCE(s.trip_status,'waiting')
		FROM van_sessions s
		JOIN van_vans v ON v.id = s.van_id
		JOIN van_destinations d ON d.id = s.destination_id
		LEFT JOIN van_bookings b ON b.session_id = s.id AND b.status NOT IN ('rejected','cancelled')
		LEFT JOIN van_drivers dr ON dr.id = s.driver_id
		` + whereClause + `
		GROUP BY s.id
		` + havingClause + `
		ORDER BY s.trip_date DESC, s.trip_time, s.van_id`

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("adminVanBookings: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type SessionWithBookings struct {
		VanSessionRow
		CreatedBy  string
		DriverName string
		DriverID   int
		Bookings   []VanBookingRow
	}
	var sessions []SessionWithBookings
	for rows.Next() {
		var s SessionWithBookings
		rows.Scan(&s.ID, &s.VanName, &s.PlateNumber, &s.Destination,
			&s.TripDate, &s.Purpose, &s.TripTime, &s.CreatedBy, &s.SeatsTaken,
			&s.DriverName, &s.DriverID, &s.TripStatus)
		s.MaxSeats = 11

		// Load individual bookings for this session
		brows, _ := db.Query(`
			SELECT b.id, b.agent_name, COALESCE(b.purpose,''), b.status,
			       COALESCE(b.admin_notes,''), DATE_FORMAT(b.created_at,'%d %b %Y %H:%i'),
			       COALESCE(b.seats,1), COALESCE(b.num_clients,0), COALESCE(b.lead_source,'')
			FROM van_bookings b
			WHERE b.session_id = ?
			ORDER BY b.created_at`, s.ID)
		if brows != nil {
			for brows.Next() {
				var b VanBookingRow
				b.VanName = s.VanName
				b.PlateNumber = s.PlateNumber
				b.Destination = s.Destination
				b.TripDate = s.TripDate
				rows2err := brows.Scan(&b.ID, &b.AgentName, &b.Purpose, &b.Status, &b.AdminNotes, &b.CreatedAt, &b.Seats, &b.NumClients, &b.LeadSource)
				if rows2err == nil {
					s.Bookings = append(s.Bookings, b)
				}
			}
			brows.Close()
		}
		sessions = append(sessions, s)
	}

	// Legacy bookings (no session_id) only shown on pending page
	var legacyBookings []VanBookingRow
	if statusFilter == "waiting" {
		legacyRows, _ := db.Query(`
			SELECT b.id, v.name, v.plate_number, d.name,
			       DATE_FORMAT(b.trip_date,'%d %b %Y'), COALESCE(b.purpose,''),
			       b.agent_name, b.status, COALESCE(b.admin_notes,''),
			       DATE_FORMAT(b.created_at,'%d %b %Y %H:%i')
			FROM van_bookings b
			JOIN van_vans v ON v.id = b.van_id
			JOIN van_destinations d ON d.id = b.destination_id
			WHERE b.session_id IS NULL
			ORDER BY b.trip_date DESC`)
		if legacyRows != nil {
			defer legacyRows.Close()
			for legacyRows.Next() {
				var b VanBookingRow
				legacyRows.Scan(&b.ID, &b.VanName, &b.PlateNumber, &b.Destination,
					&b.TripDate, &b.Purpose, &b.AgentName, &b.Status, &b.AdminNotes, &b.CreatedAt)
				legacyBookings = append(legacyBookings, b)
			}
		}
	}

	type agentName struct{ Name string }
	arows, _ := db.Query(`SELECT DISTINCT name FROM prop_agents WHERE role='agent' ORDER BY name`)
	var agents []agentName
	if arows != nil {
		defer arows.Close()
		for arows.Next() {
			var a agentName
			arows.Scan(&a.Name)
			agents = append(agents, a)
		}
	}

	var drivers []VanDriverRow
	drRows, _ := db.Query(`SELECT id, name, phone, COALESCE(agent_name,'') FROM van_drivers ORDER BY name`)
	if drRows != nil {
		defer drRows.Close()
		for drRows.Next() {
			var dr VanDriverRow
			drRows.Scan(&dr.ID, &dr.Name, &dr.Phone, &dr.AgentName)
			drivers = append(drivers, dr)
		}
	}

	tmpl := "admin_van_bookings.html"
	active := "van-bookings"
	switch statusFilter {
	case "approved":
		tmpl = "admin_van_bookings_approved.html"
		active = "van-bookings-approved"
	case "done":
		tmpl = "admin_van_bookings_done.html"
		active = "van-bookings-done"
	}

	adminName := getAgent(r)
	renderFn(w, tmpl, map[string]any{
		"Active":         active,
		"Sessions":       sessions,
		"LegacyBookings": legacyBookings,
		"Agents":         agents,
		"Drivers":        drivers,
		"DateFilter":     dateFilter,
		"Success":        r.URL.Query().Get("success") == "1",
		"Error":          r.URL.Query().Get("error"),
		"IsSystemAdmin":  isSystemAdmin(r),
		"CanApprove":     canApprove(adminName),
		"UnreadCount":    UnreadNotifs(adminName),
	})
}

// AdminVanBookingActionHandler handles /admin/van-booking/{id}/approve|reject|cancel.
func AdminVanBookingActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bookingID := pathSeg("/admin/van-booking/", r.URL.Path)
	suffix := strings.TrimPrefix(r.URL.Path, "/admin/van-booking/"+bookingID)

	if suffix == "/cancel" {
		adminName := getAgent(r)
		var status, owner string
		db.QueryRow(`SELECT status, agent_name FROM van_bookings WHERE id=?`, bookingID).Scan(&status, &owner)
		if owner != adminName {
			http.Error(w, "Not your booking.", http.StatusForbidden)
			return
		}
		if status == "cancelled" || status == "rejected" {
			http.Redirect(w, r, "/admin/van-my-bookings", http.StatusFound)
			return
		}
		db.Exec(`UPDATE van_bookings SET status='cancelled', admin_notes='Cancelled by admin' WHERE id=?`, bookingID)
		var destName, tripDate string
		db.QueryRow(`
			SELECT COALESCE(d2.name, d.name,''), COALESCE(DATE_FORMAT(s.trip_date,'%d %b %Y'), DATE_FORMAT(b.trip_date,'%d %b %Y'),'')
			FROM van_bookings b
			LEFT JOIN van_sessions s ON s.id = b.session_id
			LEFT JOIN van_destinations d2 ON d2.id = s.destination_id
			LEFT JOIN van_destinations d ON d.id = b.destination_id
			WHERE b.id=?`, bookingID).Scan(&destName, &tripDate)
		go notifyAdmins(fmt.Sprintf("%s cancelled their van booking to %s on %s.", adminName, destName, tripDate))
		http.Redirect(w, r, "/admin/van-my-bookings", http.StatusFound)
		return
	}

	if !canApprove(getAgent(r)) {
		http.Error(w, "You do not have permission to approve or reject bookings.", http.StatusForbidden)
		return
	}

	r.ParseForm()
	notes := strings.TrimSpace(r.FormValue("admin_notes"))

	var newStatus string
	switch suffix {
	case "/approve":
		newStatus = "approved"
	case "/reject":
		newStatus = "rejected"
	default:
		http.NotFound(w, r)
		return
	}

	var agentN, vanName, destination, tripDate, tripTime string
	db.QueryRow(`
		SELECT b.agent_name,
		       COALESCE(v2.name, v.name, ''),
		       COALESCE(d2.name, d.name, ''),
		       COALESCE(DATE_FORMAT(s.trip_date,'%d %b %Y'), DATE_FORMAT(b.trip_date,'%d %b %Y'), ''),
		       COALESCE(TIME_FORMAT(s.trip_time,'%H:%i'), '')
		FROM van_bookings b
		LEFT JOIN van_sessions s    ON s.id = b.session_id
		LEFT JOIN van_vans v2       ON v2.id = s.van_id
		LEFT JOIN van_destinations d2 ON d2.id = s.destination_id
		LEFT JOIN van_vans v        ON v.id = b.van_id
		LEFT JOIN van_destinations d ON d.id = b.destination_id
		WHERE b.id=?`, bookingID).Scan(&agentN, &vanName, &destination, &tripDate, &tripTime)

	db.Exec(`UPDATE van_bookings SET status=?, admin_notes=? WHERE id=?`, newStatus, notes, bookingID)

	dateTime := tripDate
	if tripTime != "" {
		dateTime += " at " + tripTime
	}
	var msg string
	if newStatus == "approved" {
		msg = fmt.Sprintf("Your van booking (%s to %s on %s) has been APPROVED.", vanName, destination, dateTime)
	} else {
		msg = fmt.Sprintf("Your van booking (%s to %s on %s) has been REJECTED.", vanName, destination, dateTime)
	}
	if notes != "" {
		msg += " Note: " + notes
	}
	if agentN != "" {
		go notifyUser(agentN, msg)
	}

	http.Redirect(w, r, "/admin/van-bookings?success="+newStatus, http.StatusFound)
}

// AdminVansHandler manages the van and destination list.
func AdminVansHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		switch r.FormValue("action") {
		case "add_van":
			name := strings.TrimSpace(r.FormValue("name"))
			plate := strings.TrimSpace(r.FormValue("plate_number"))
			cap := r.FormValue("capacity")
			if name != "" && plate != "" {
				db.Exec(`INSERT INTO van_vans (name, plate_number, capacity) VALUES (?,?,?)`, name, plate, cap)
			}
		case "toggle_van":
			db.Exec(`UPDATE van_vans SET status = CASE WHEN status='available' THEN 'maintenance' ELSE 'available' END WHERE id=?`, r.FormValue("van_id"))
		case "delete_van":
			db.Exec(`DELETE FROM van_vans WHERE id=?`, r.FormValue("van_id"))
		case "add_dest":
			name := strings.TrimSpace(r.FormValue("dest_name"))
			if name != "" {
				db.Exec(`INSERT INTO van_destinations (name) VALUES (?)`, name)
			}
		case "del_dest":
			db.Exec(`DELETE FROM van_destinations WHERE id=?`, r.FormValue("dest_id"))
		case "add_driver":
			agentName := strings.TrimSpace(r.FormValue("driver_agent"))
			phone := strings.TrimSpace(r.FormValue("driver_phone"))
			name := strings.TrimSpace(r.FormValue("driver_name"))
			// If agent selected from dropdown, use their name and phone
			if agentName != "" {
				var agentPhone string
				db.QueryRow(`SELECT COALESCE(phone,'') FROM prop_agents WHERE name=?`, agentName).Scan(&agentPhone)
				if agentPhone != "" {
					phone = agentPhone
				}
				if name == "" {
					name = agentName
				}
			}
			if name != "" && phone != "" {
				db.Exec(`INSERT INTO van_drivers (name, phone, agent_name) VALUES (?,?,?)`, name, phone, agentName)
			}
		case "del_driver":
			db.Exec(`DELETE FROM van_drivers WHERE id=?`, r.FormValue("driver_id"))
		}
		http.Redirect(w, r, "/admin/vans", http.StatusFound)
		return
	}

	var drivers []VanDriverRow
	driverRows, _ := db.Query(`SELECT id, name, phone, COALESCE(agent_name,'') FROM van_drivers ORDER BY name`)
	if driverRows != nil {
		defer driverRows.Close()
		for driverRows.Next() {
			var dr VanDriverRow
			driverRows.Scan(&dr.ID, &dr.Name, &dr.Phone, &dr.AgentName)
			drivers = append(drivers, dr)
		}
	}

	// Load all agents for the add-driver dropdown
	type agentOpt struct{ Name string; Phone string }
	var agentOpts []agentOpt
	aRows, _ := db.Query(`SELECT name, COALESCE(phone,'') FROM prop_agents ORDER BY name`)
	if aRows != nil {
		defer aRows.Close()
		for aRows.Next() {
			var a agentOpt
			aRows.Scan(&a.Name, &a.Phone)
			agentOpts = append(agentOpts, a)
		}
	}

	renderFn(w, "admin_vans.html", map[string]any{
		"Active":        "van-manage",
		"Vans":          loadVans(),
		"Destinations":  loadDests(),
		"Drivers":       drivers,
		"Agents":        agentOpts,
		"IsSystemAdmin": isSystemAdmin(r),
		"UnreadCount":   UnreadNotifs(getAgent(r)),
	})
}

// AdminVanMaintenanceHandler manages scheduled maintenance windows.
// GET  /admin/van-maintenance          → list + add form
// POST action=add                      → insert window
// POST action=delete                   → remove window
func AdminVanMaintenanceHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgent(r)

	if r.Method == http.MethodPost {
		r.ParseForm()
		switch r.FormValue("action") {
		case "add":
			vanID := r.FormValue("van_id")
			startDate := r.FormValue("start_date")
			endDate := strings.TrimSpace(r.FormValue("end_date"))
			if endDate == "" {
				endDate = startDate
			}
			startTime := strings.TrimSpace(r.FormValue("start_time"))
			endTime := strings.TrimSpace(r.FormValue("end_time"))
			reason := strings.TrimSpace(r.FormValue("reason"))
			mType := r.FormValue("type")
			if mType == "" {
				mType = "maintenance"
			}
			var stArg, etArg interface{}
			if startTime != "" {
				stArg = startTime
			}
			if endTime != "" {
				etArg = endTime
			}
			if vanID != "" && startDate != "" {
				db.Exec(`INSERT INTO van_maintenance (van_id, start_date, end_date, start_time, end_time, reason, type, created_by) VALUES (?,?,?,?,?,?,?,?)`,
					vanID, startDate, endDate, stArg, etArg, reason, mType, agentName)
			}
		case "delete":
			db.Exec(`DELETE FROM van_maintenance WHERE id=?`, r.FormValue("maint_id"))
		}
		http.Redirect(w, r, "/admin/van-maintenance", http.StatusFound)
		return
	}

	mrows, _ := db.Query(`
		SELECT m.id, m.van_id, v.name,
		       DATE_FORMAT(m.start_date,'%Y-%m-%d'), DATE_FORMAT(m.end_date,'%Y-%m-%d'),
		       COALESCE(TIME_FORMAT(m.start_time,'%H:%i'),''), COALESCE(TIME_FORMAT(m.end_time,'%H:%i'),''),
		       COALESCE(m.type,'maintenance'),
		       COALESCE(m.reason,''), COALESCE(m.created_by,''),
		       DATE_FORMAT(m.created_at,'%d %b %Y')
		FROM van_maintenance m
		JOIN van_vans v ON v.id = m.van_id
		ORDER BY m.start_date DESC, v.name`)
	var windows []VanMaintenanceRow
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var m VanMaintenanceRow
			mrows.Scan(&m.ID, &m.VanID, &m.VanName, &m.StartDate, &m.EndDate,
				&m.StartTime, &m.EndTime, &m.Type, &m.Reason, &m.CreatedBy, &m.CreatedAt)
			windows = append(windows, m)
		}
	}

	renderFn(w, "admin_van_maintenance.html", map[string]any{
		"Active":        "van-maintenance",
		"Vans":          loadVans(),
		"Windows":       windows,
		"IsSystemAdmin": isSystemAdmin(r),
		"UnreadCount":   UnreadNotifs(agentName),
	})
}

// AdminVanAvailabilityHandler shows each van's sessions and maintenance windows
// for a given date so the fleet manager can identify free slots.
// GET /admin/van-availability?date=YYYY-MM-DD
func AdminVanAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgent(r)
	date := r.URL.Query().Get("date")

	data := map[string]any{
		"Active":        "van-availability",
		"IsSystemAdmin": isSystemAdmin(r),
		"UnreadCount":   UnreadNotifs(agentName),
		"Today":         time.Now().Format("2006-01-02"),
	}

	if date != "" {
		data["SelectedDate"] = date

		type AvailSession struct {
			ID          int
			Destination string
			StartTime   string
			EndTime     string
			SeatsTaken  int
			TripStatus  string
		}
		type AvailRow struct {
			VanID        int
			VanName      string
			PlateNumber  string
			GlobalStatus string
			Sessions     []AvailSession
			Maintenance  []VanMaintenanceRow
		}

		vans := loadVans()
		avail := make([]AvailRow, 0, len(vans))

		for _, van := range vans {
			row := AvailRow{
				VanID: van.ID, VanName: van.Name,
				PlateNumber: van.PlateNumber, GlobalStatus: van.Status,
			}

			// Sessions for this van on this date.
			srows, _ := db.Query(`
				SELECT s.id, d.name,
				       COALESCE(TIME_FORMAT(s.trip_time,'%H:%i'),''),
				       COALESCE(TIME_FORMAT(s.trip_time_end,'%H:%i'),''),
				       COALESCE(SUM(b.seats),0), s.trip_status
				FROM van_sessions s
				JOIN van_destinations d ON d.id = s.destination_id
				LEFT JOIN van_bookings b ON b.session_id = s.id AND b.status NOT IN ('rejected','cancelled')
				WHERE s.van_id=? AND s.trip_date=?
				GROUP BY s.id ORDER BY s.trip_time`, van.ID, date)
			if srows != nil {
				for srows.Next() {
					var s AvailSession
					srows.Scan(&s.ID, &s.Destination, &s.StartTime, &s.EndTime, &s.SeatsTaken, &s.TripStatus)
					row.Sessions = append(row.Sessions, s)
				}
				srows.Close()
			}

			// Maintenance windows for this van on this date.
			mrows, _ := db.Query(`
				SELECT id, COALESCE(TIME_FORMAT(start_time,'%H:%i'),''),
				           COALESCE(TIME_FORMAT(end_time,'%H:%i'),''),
				           COALESCE(type,'maintenance'),
				           COALESCE(reason,'')
				FROM van_maintenance
				WHERE van_id=? AND start_date<=? AND end_date>=?
				ORDER BY start_time`, van.ID, date, date)
			if mrows != nil {
				for mrows.Next() {
					var m VanMaintenanceRow
					m.VanID = van.ID
					mrows.Scan(&m.ID, &m.StartTime, &m.EndTime, &m.Type, &m.Reason)
					row.Maintenance = append(row.Maintenance, m)
				}
				mrows.Close()
			}

			avail = append(avail, row)
		}
		data["Availability"] = avail
	}

	renderFn(w, "admin_van_availability.html", data)
}

// AdminVanAssignDriverHandler assigns or removes a driver from a session, or
// sets the trip_status of a session.
// POST /admin/van-session/{id}/assign-driver
// POST /admin/van-session/{id}/set-status
func AdminVanAssignDriverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	// Extract session ID and action from URL: /admin/van-session/{id}/{action}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var sessionID, action string
	for i, p := range parts {
		if p == "van-session" && i+1 < len(parts) {
			sessionID = parts[i+1]
			if i+2 < len(parts) {
				action = parts[i+2]
			}
			break
		}
	}
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()

	if action == "set-status" {
		newStatus := r.FormValue("trip_status")
		if newStatus != "waiting" && newStatus != "approved" && newStatus != "done" {
			http.NotFound(w, r)
			return
		}
		db.Exec(`UPDATE van_sessions SET trip_status=? WHERE id=?`, newStatus, sessionID)
		switch newStatus {
		case "approved":
			http.Redirect(w, r, "/admin/van-bookings?success=1", http.StatusFound)
		case "done":
			http.Redirect(w, r, "/admin/van-bookings/approved?success=1", http.StatusFound)
		default:
			http.Redirect(w, r, "/admin/van-bookings/approved?success=1", http.StatusFound)
		}
		return
	}

	if action == "delete" {
		// Refuse only if there are approved bookings — those represent confirmed seats.
		// Pending bookings are not yet confirmed and are cleared along with the session.
		var approvedCount int
		db.QueryRow(`SELECT COUNT(*) FROM van_bookings WHERE session_id=? AND status='approved'`, sessionID).Scan(&approvedCount)
		redirectTo := r.FormValue("redirect")
		if redirectTo == "" {
			redirectTo = "/admin/van-bookings"
		}
		if approvedCount > 0 {
			http.Redirect(w, r, redirectTo+"?error=session_has_bookings", http.StatusFound)
			return
		}
		// Delete bookings then session.
		db.Exec(`DELETE FROM van_bookings WHERE session_id=?`, sessionID)
		db.Exec(`DELETE FROM van_sessions WHERE id=?`, sessionID)
		http.Redirect(w, r, redirectTo+"?success=1", http.StatusFound)
		return
	}

	// Default: assign-driver
	driverIDStr := r.FormValue("driver_id")
	if driverIDStr == "" || driverIDStr == "0" {
		db.Exec(`UPDATE van_sessions SET driver_id=NULL WHERE id=?`, sessionID)
	} else {
		db.Exec(`UPDATE van_sessions SET driver_id=? WHERE id=?`, driverIDStr, sessionID)

		// Fetch session details and driver phone, then send SMS
		var vanPlate, destName, tripDate, tripTime string
		db.QueryRow(`
			SELECT v.plate_number, d.name,
			       DATE_FORMAT(s.trip_date,'%d %b %Y'),
			       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),'')
			FROM van_sessions s
			JOIN van_vans v ON v.id = s.van_id
			JOIN van_destinations d ON d.id = s.destination_id
			WHERE s.id=?`, sessionID).
			Scan(&vanPlate, &destName, &tripDate, &tripTime)

		var driverPhone string
		db.QueryRow(`SELECT phone FROM van_drivers WHERE id=?`, driverIDStr).Scan(&driverPhone)
		if driverPhone != "" {
			timeStr := ""
			if tripTime != "" {
				timeStr = " at " + tripTime
			}
			msg := fmt.Sprintf("DRIVER ASSIGNMENT: You have been assigned to drive %s to %s on %s%s. - Pro-Property",
				vanPlate, destName, tripDate, timeStr)
			go sendSMS(driverPhone, msg)
		}
	}
	http.Redirect(w, r, "/admin/van-bookings?success=1", http.StatusFound)
}

// DriverSessionsHandler shows sessions assigned to the logged-in driver.
// GET /agent/van-driver-sessions          → pending/waiting trips
// GET /agent/van-driver-sessions?tab=done → completed trips
func DriverSessionsHandler(w http.ResponseWriter, r *http.Request) {
	agentName := getAgent(r)

	// Find the driver record linked to this agent
	var driverID int
	var driverName string
	db.QueryRow(`SELECT id, name FROM van_drivers WHERE agent_name=?`, agentName).Scan(&driverID, &driverName)

	tab := r.URL.Query().Get("tab")
	if tab != "done" {
		tab = "pending"
	}

	type DriverSession struct {
		ID          int
		PlateNumber string
		VanName     string
		Destination string
		TripDate    string
		TripTime    string
		Estates     string
		Purpose     string
		SeatsTaken  int
		TripStatus  string
		Agents      []string
	}

	var sessions []DriverSession
	if driverID > 0 {
		// Pending tab shows both waiting and approved (both are upcoming trips).
		// Done tab shows only completed trips.
		var statusQuery string
		var queryArgs []any
		if tab == "done" {
			statusQuery = `WHERE s.driver_id = ? AND COALESCE(s.trip_status,'waiting') = 'done'`
			queryArgs = []any{driverID}
		} else {
			statusQuery = `WHERE s.driver_id = ? AND COALESCE(s.trip_status,'waiting') IN ('waiting','approved')`
			queryArgs = []any{driverID}
		}
		rows, err := db.Query(`
			SELECT s.id, v.plate_number, v.name, d.name,
			       DATE_FORMAT(s.trip_date,'%d %b %Y'),
			       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),''),
			       COALESCE(s.estates,''), COALESCE(s.purpose,''),
			       COALESCE(SUM(b.seats),0),
			       COALESCE(s.trip_status,'waiting')
			FROM van_sessions s
			JOIN van_vans v ON v.id = s.van_id
			JOIN van_destinations d ON d.id = s.destination_id
			LEFT JOIN van_bookings b ON b.session_id = s.id AND b.status NOT IN ('rejected','cancelled')
			`+statusQuery+`
			GROUP BY s.id
			ORDER BY s.trip_date DESC, s.trip_time`, queryArgs...)
		if err != nil {
			log.Printf("DriverSessionsHandler: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var s DriverSession
			rows.Scan(&s.ID, &s.PlateNumber, &s.VanName, &s.Destination,
				&s.TripDate, &s.TripTime, &s.Estates, &s.Purpose, &s.SeatsTaken, &s.TripStatus)

			// Load agent names for this session
			arows, _ := db.Query(`SELECT agent_name, seats FROM van_bookings WHERE session_id=? AND status!='rejected' ORDER BY created_at`, s.ID)
			if arows != nil {
				for arows.Next() {
					var name string
					var seats int
					arows.Scan(&name, &seats)
					if seats > 1 {
						s.Agents = append(s.Agents, fmt.Sprintf("%s (%d seats)", name, seats))
					} else {
						s.Agents = append(s.Agents, name)
					}
				}
				arows.Close()
			}
			sessions = append(sessions, s)
		}
	}

	renderFn(w, "driver_sessions.html", map[string]any{
		"Active":         "van-driver",
		"AgentName":      agentName,
		"DriverName":     driverName,
		"IsDriver":       driverID > 0,
		"Sessions":       sessions,
		"Tab":            tab,
		"UnreadCount":    UnreadNotifs(agentName),
		"IsFleetManager": canApprove(agentName),
	})
}

// DriverMarkDoneHandler lets a driver mark one of their assigned sessions as done.
// POST /agent/van-session/{id}/mark-done
func DriverMarkDoneHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agentName := getAgent(r)

	// Confirm this agent is a registered driver
	var driverID int
	db.QueryRow(`SELECT id FROM van_drivers WHERE agent_name=?`, agentName).Scan(&driverID)
	if driverID == 0 {
		http.Error(w, "Not a registered driver.", http.StatusForbidden)
		return
	}

	// Extract session ID from URL: /agent/van-session/{id}/mark-done
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var sessionID string
	for i, p := range parts {
		if p == "van-session" && i+1 < len(parts) {
			sessionID = parts[i+1]
			break
		}
	}
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	// Confirm the session belongs to this driver
	var ownerDriverID int
	db.QueryRow(`SELECT driver_id FROM van_sessions WHERE id=?`, sessionID).Scan(&ownerDriverID)
	if ownerDriverID != driverID {
		http.Error(w, "This trip is not assigned to you.", http.StatusForbidden)
		return
	}

	db.Exec(`UPDATE van_sessions SET trip_status='done' WHERE id=?`, sessionID)
	http.Redirect(w, r, "/agent/van-driver-sessions?tab=done", http.StatusFound)
}

// AdminVanNotifyHandler sends a notification to an agent (or all agents).
func AdminVanNotifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	agentN := r.FormValue("agent_name")
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		http.Redirect(w, r, "/admin/van-bookings", http.StatusFound)
		return
	}
	if agentN != "" {
		go notifyUser(agentN, message)
	} else {
		// Broadcast to all agents
		arows, _ := db.Query(`SELECT name FROM prop_agents WHERE role='agent'`)
		if arows != nil {
			defer arows.Close()
			for arows.Next() {
				var n string
				arows.Scan(&n)
				go notifyUser(n, message)
			}
		}
	}
	http.Redirect(w, r, "/admin/van-bookings?success=notified", http.StatusFound)
}

// AdminVanExportDetailedHandler streams a detailed CSV of all bookings for a given date.
func AdminVanExportDetailedHandler(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var args []any
	where := "1=1"
	if date != "" {
		where = "COALESCE(s.trip_date, b.trip_date) = ?"
		args = append(args, date)
	}
	rows, err := db.Query(`
		SELECT b.id,
		       COALESCE(vv.plate_number, '') AS plate,
		       COALESCE(vv.name, '') AS van_name,
		       b.agent_name,
		       COALESCE(s.destination, d.name, '') AS destination,
		       COALESCE(DATE_FORMAT(s.trip_date,'%Y-%m-%d'), DATE_FORMAT(b.trip_date,'%Y-%m-%d'), '') AS trip_date,
		       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')), '') AS trip_time,
		       COALESCE(b.seats, 1) AS seats,
		       COALESCE(b.num_clients, 0) AS num_clients,
		       COALESCE(b.lead_source, '') AS lead_source,
		       b.status,
		       DATE_FORMAT(b.created_at,'%Y-%m-%d %H:%i') AS booked_at
		FROM van_bookings b
		LEFT JOIN van_sessions s ON s.id = b.session_id
		LEFT JOIN van_vans vv ON vv.id = COALESCE(s.van_id, b.van_id)
		LEFT JOIN van_destinations d ON d.id = b.destination_id
		WHERE `+where+`
		ORDER BY trip_date DESC, b.created_at DESC`, args...)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	fname := "van_bookings_detailed"
	if date != "" {
		fname += "_" + date
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, fname))
	cw := csv.NewWriter(w)
	cw.Write([]string{"ID", "Plate", "Van", "Agent", "Destination", "Trip Date", "Trip Time", "Seats", "Clients", "Lead Source", "Status", "Booked At"})
	for rows.Next() {
		var id, seats, numClients int
		var plate, vanName, agent, dest, tripDate, tripTime, leadSource, status, bookedAt string
		rows.Scan(&id, &plate, &vanName, &agent, &dest, &tripDate, &tripTime, &seats, &numClients, &leadSource, &status, &bookedAt)
		cw.Write([]string{
			fmt.Sprintf("%d", id), plate, vanName, agent, dest, tripDate, tripTime,
			fmt.Sprintf("%d", seats), fmt.Sprintf("%d", numClients), leadSource, status, bookedAt,
		})
	}
	cw.Flush()
}

// AdminVanExportSummaryHandler streams a summary CSV grouped by agent for a given date.
func AdminVanExportSummaryHandler(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var args []any
	where := "1=1"
	if date != "" {
		where = "COALESCE(s.trip_date, b.trip_date) = ?"
		args = append(args, date)
	}
	rows, err := db.Query(`
		SELECT b.agent_name,
		       SUM(COALESCE(b.num_clients, 0)) AS total_clients,
		       COALESCE(b.lead_source, '') AS lead_source,
		       COALESCE(DATE_FORMAT(s.trip_date,'%Y-%m-%d'), DATE_FORMAT(b.trip_date,'%Y-%m-%d'), '') AS trip_date
		FROM van_bookings b
		LEFT JOIN van_sessions s ON s.id = b.session_id
		WHERE `+where+`
		GROUP BY b.agent_name, b.lead_source, trip_date
		ORDER BY trip_date DESC, b.agent_name`, args...)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	fname := "van_bookings_summary"
	if date != "" {
		fname += "_" + date
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, fname))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Agent", "Total Clients", "Lead Source", "Date"})
	for rows.Next() {
		var agent, leadSource, tripDate string
		var totalClients int
		rows.Scan(&agent, &totalClients, &leadSource, &tripDate)
		cw.Write([]string{agent, fmt.Sprintf("%d", totalClients), leadSource, tripDate})
	}
	cw.Flush()
}

// AdminVanExportHandler kept for backward compatibility — redirects to detailed export.
func AdminVanExportHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/van-bookings/export/detailed", http.StatusFound)
}

// ── Fleet manager: approval view ──────────────────────────────────────────────

// FleetApprovalHandler shows all sessions with their bookings so that a fleet
// manager (any user with admin.van_approve write permission) can approve or
// reject individual bookings.
func FleetApprovalHandler(w http.ResponseWriter, r *http.Request) {
	name := getAgent(r)
	if !canApprove(name) {
		http.Error(w, "You do not have fleet manager permission.", http.StatusForbidden)
		return
	}

	rows, err := db.Query(`
		SELECT s.id, v.name, v.plate_number, d.name,
		       DATE_FORMAT(s.trip_date,'%d %b %Y'),
		       COALESCE(CONCAT(TIME_FORMAT(s.trip_time,'%H:%i'),' - ',TIME_FORMAT(COALESCE(s.trip_time_end,ADDTIME(s.trip_time,'02:00:00')),'%H:%i')),''),
		       COALESCE(s.purpose,''),
		       COALESCE(SUM(b.seats),0)
		FROM van_sessions s
		JOIN van_vans v ON v.id = s.van_id
		JOIN van_destinations d ON d.id = s.destination_id
		LEFT JOIN van_bookings b ON b.session_id = s.id AND b.status NOT IN ('rejected','cancelled')
		GROUP BY s.id
		HAVING COUNT(b.id) > 0
		ORDER BY s.trip_date DESC, s.trip_time, s.van_id`)
	if err != nil {
		log.Printf("FleetApprovalHandler: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type FleetSession struct {
		VanSessionRow
		Bookings []VanBookingRow
	}
	var sessions []FleetSession
	for rows.Next() {
		var s FleetSession
		rows.Scan(&s.ID, &s.VanName, &s.PlateNumber, &s.Destination,
			&s.TripDate, &s.TripTime, &s.Purpose, &s.SeatsTaken)
		s.MaxSeats = 11

		brows, _ := db.Query(`
			SELECT b.id, b.agent_name, COALESCE(b.purpose,''), b.status,
			       COALESCE(b.admin_notes,''), DATE_FORMAT(b.created_at,'%d %b %Y %H:%i'),
			       COALESCE(b.seats,1), COALESCE(b.num_clients,0), COALESCE(b.lead_source,'')
			FROM van_bookings b
			WHERE b.session_id = ? AND b.status NOT IN ('cancelled')
			ORDER BY b.created_at`, s.ID)
		if brows != nil {
			for brows.Next() {
				var b VanBookingRow
				b.VanName = s.VanName
				b.PlateNumber = s.PlateNumber
				b.Destination = s.Destination
				b.TripDate = s.TripDate
				brows.Scan(&b.ID, &b.AgentName, &b.Purpose, &b.Status,
					&b.AdminNotes, &b.CreatedAt, &b.Seats, &b.NumClients, &b.LeadSource)
				s.Bookings = append(s.Bookings, b)
			}
			brows.Close()
		}
		sessions = append(sessions, s)
	}

	renderFn(w, "fleet_approval.html", map[string]any{
		"Active":          "van-fleet",
		"AgentName":       name,
		"Sessions":        sessions,
		"Success":         r.URL.Query().Get("success"),
		"IsFleetManager":  true,
		"UnreadCount":     UnreadNotifs(name),
	})
}

// FleetBookingActionHandler handles approve/reject from the fleet manager view.
// POST /agent/van-fleet-booking/{id}/approve|reject
func FleetBookingActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	name := getAgent(r)
	if !canApprove(name) {
		http.Error(w, "You do not have fleet manager permission.", http.StatusForbidden)
		return
	}

	// parse /agent/van-fleet-booking/{id}/approve  or  .../reject
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var bookingID, suffix string
	for i, p := range parts {
		if p == "van-fleet-booking" && i+1 < len(parts) {
			bookingID = parts[i+1]
			if i+2 < len(parts) {
				suffix = parts[i+2]
			}
			break
		}
	}
	if bookingID == "" {
		http.NotFound(w, r)
		return
	}

	r.ParseForm()
	notes := strings.TrimSpace(r.FormValue("admin_notes"))

	var newStatus string
	switch suffix {
	case "approve":
		newStatus = "approved"
	case "reject":
		newStatus = "rejected"
	default:
		http.NotFound(w, r)
		return
	}

	var agentN, vanName, destination, tripDate, tripTime string
	db.QueryRow(`
		SELECT b.agent_name,
		       COALESCE(v2.name, v.name, ''),
		       COALESCE(d2.name, d.name, ''),
		       COALESCE(DATE_FORMAT(s.trip_date,'%d %b %Y'), DATE_FORMAT(b.trip_date,'%d %b %Y'), ''),
		       COALESCE(TIME_FORMAT(s.trip_time,'%H:%i'), '')
		FROM van_bookings b
		LEFT JOIN van_sessions s    ON s.id = b.session_id
		LEFT JOIN van_vans v2       ON v2.id = s.van_id
		LEFT JOIN van_destinations d2 ON d2.id = s.destination_id
		LEFT JOIN van_vans v        ON v.id = b.van_id
		LEFT JOIN van_destinations d ON d.id = b.destination_id
		WHERE b.id=?`, bookingID).Scan(&agentN, &vanName, &destination, &tripDate, &tripTime)

	db.Exec(`UPDATE van_bookings SET status=?, admin_notes=? WHERE id=?`, newStatus, notes, bookingID)

	dateTime := tripDate
	if tripTime != "" {
		dateTime += " at " + tripTime
	}
	var msg string
	if newStatus == "approved" {
		msg = fmt.Sprintf("Your van booking (%s to %s on %s) has been APPROVED.", vanName, destination, dateTime)
	} else {
		msg = fmt.Sprintf("Your van booking (%s to %s on %s) has been REJECTED.", vanName, destination, dateTime)
	}
	if notes != "" {
		msg += " Note: " + notes
	}
	if agentN != "" {
		go notifyUser(agentN, msg)
	}

	http.Redirect(w, r, "/agent/van-fleet-approval?success="+newStatus, http.StatusFound)
}
