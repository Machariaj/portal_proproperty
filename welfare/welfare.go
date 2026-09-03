package welfare

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	db             *sql.DB
	renderFn       func(w http.ResponseWriter, name string, data any)
	getAgent       func(r *http.Request) string
	pathSeg        func(prefix, path string) string
	isAdminFn      func(r *http.Request) bool
	registerFeatFn func(key, label, group, module string)
	hasPermFn      func(userID, feature, access string) bool
	getUserIDFn    func(r *http.Request) string
)

func Init(
	d *sql.DB,
	render func(http.ResponseWriter, string, any),
	agentFn func(*http.Request) string,
	segFn func(string, string) string,
	regFeat func(key, label, group, module string),
	isSysAdmin func(r *http.Request) bool,
	hasPerm func(userID, feature, access string) bool,
	getUserID func(r *http.Request) string,
) {
	db = d
	renderFn = render
	getAgent = agentFn
	pathSeg = segFn
	registerFeatFn = regFeat
	isAdminFn = isSysAdmin
	hasPermFn = hasPerm
	getUserIDFn = getUserID

	regFeat("welfare.members", "Members", "admin", "Staff Welfare")
	regFeat("welfare.expenses", "Expenses & Withdrawals", "admin", "Staff Welfare")
	regFeat("welfare.claims", "Claims & Benefits", "admin", "Staff Welfare")
}

// renderWelfare wraps renderFn and injects IsSystemAdmin into every page.
func renderWelfare(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	data["IsSystemAdmin"] = isAdminFn(r)
	renderFn(w, name, data)
}

func InitTables() {
	db.Exec(`CREATE TABLE IF NOT EXISTS welfare_members (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		member_code VARCHAR(20)  DEFAULT NULL,
		name        VARCHAR(255) NOT NULL,
		department  VARCHAR(100) DEFAULT NULL,
		phone       VARCHAR(30)  DEFAULT NULL,
		email       VARCHAR(255) DEFAULT NULL,
		joined_date DATE         DEFAULT NULL,
		status      ENUM('active','inactive','resigned') DEFAULT 'active',
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uq_member_code (member_code)
	)`)
	// Migrations for existing tables
	db.Exec(`ALTER TABLE welfare_members ADD COLUMN member_code VARCHAR(20) DEFAULT NULL`)
	db.Exec(`ALTER TABLE welfare_members ADD UNIQUE KEY uq_member_code (member_code)`)
	db.Exec(`ALTER TABLE welfare_members MODIFY COLUMN status ENUM('active','inactive','resigned') DEFAULT 'active'`)
	// Backfill member codes for any existing members that don't have one
	db.Exec(`UPDATE welfare_members SET member_code = CONCAT('pwf-', LPAD(id, 4, '0')) WHERE member_code IS NULL`)
	db.Exec(`CREATE TABLE IF NOT EXISTS welfare_contributions (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		member_id   INT NOT NULL,
		amount      DECIMAL(10,2) DEFAULT 500.00,
		month       VARCHAR(7) NOT NULL COMMENT 'YYYY-MM format',
		paid_date   DATE DEFAULT NULL,
		status      ENUM('paid','pending') DEFAULT 'paid',
		notes       VARCHAR(500) DEFAULT NULL,
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uq_member_month (member_id, month)
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS welfare_dependants (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		member_id    INT NOT NULL,
		name         VARCHAR(255) NOT NULL,
		relationship VARCHAR(100) NOT NULL,
		dob          DATE DEFAULT NULL,
		created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`ALTER TABLE welfare_dependants ADD COLUMN id_number VARCHAR(50) DEFAULT NULL`)
	db.Exec(`CREATE TABLE IF NOT EXISTS welfare_expenses (
		id               INT AUTO_INCREMENT PRIMARY KEY,
		amount           DECIMAL(10,2) NOT NULL,
		purpose          VARCHAR(500) NOT NULL,
		beneficiary_name VARCHAR(255) DEFAULT NULL,
		expense_date     DATE NOT NULL,
		recorded_by      VARCHAR(255) DEFAULT NULL,
		notes            TEXT DEFAULT NULL,
		created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS welfare_beneficiaries (
		id               INT AUTO_INCREMENT PRIMARY KEY,
		member_id        INT NOT NULL,
		category         VARCHAR(100) NOT NULL,
		description      TEXT DEFAULT NULL,
		amount_requested DECIMAL(10,2) DEFAULT NULL,
		amount_approved  DECIMAL(10,2) DEFAULT NULL,
		status           ENUM('pending','approved','completed') DEFAULT 'pending',
		requested_date   DATE NOT NULL,
		approved_date    DATE DEFAULT NULL,
		completed_date   DATE DEFAULT NULL,
		notes            TEXT DEFAULT NULL,
		created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
}

// welfareFeatureFor maps a request path to the welfare feature key it requires.
// Returns "" when the path does not map to a specific feature (e.g. the root redirect).
func welfareFeatureFor(p string) string {
	switch {
	case strings.HasPrefix(p, "/welfare/members"),
		strings.HasPrefix(p, "/welfare/api/"),
		strings.HasPrefix(p, "/welfare/export/"):
		return "welfare.members"
	case strings.HasPrefix(p, "/welfare/expenses"):
		return "welfare.expenses"
	case strings.HasPrefix(p, "/welfare/beneficiaries"):
		return "welfare.claims"
	}
	return ""
}

// Router handles all /welfare/* requests.
func Router(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	// Permission enforcement — system_admin always passes.
	if !isAdminFn(r) {
		uid := getUserIDFn(r)
		feat := welfareFeatureFor(p)
		if feat == "" {
			// Root redirect: allow if the user has any welfare permission.
			if !hasPermFn(uid, "welfare.members", "read") &&
				!hasPermFn(uid, "welfare.expenses", "read") &&
				!hasPermFn(uid, "welfare.claims", "read") {
				http.Error(w, "Access denied – no welfare permissions granted.", http.StatusForbidden)
				return
			}
		} else {
			access := "read"
			if r.Method == http.MethodPost {
				access = "write"
			}
			if !hasPermFn(uid, feat, access) {
				http.Error(w, "Access denied.", http.StatusForbidden)
				return
			}
		}
	}

	switch {
	case p == "/welfare" || p == "/welfare/":
		http.Redirect(w, r, "/welfare/members", http.StatusFound)

	case p == "/welfare/members":
		membersHandler(w, r)
	case p == "/welfare/members/add":
		addMemberHandler(w, r)
	case strings.HasPrefix(p, "/welfare/members/") && strings.HasSuffix(p, "/contributions/add"):
		addContributionHandler(w, r)
	case strings.HasPrefix(p, "/welfare/members/") && strings.HasSuffix(p, "/contributions/delete"):
		deleteContributionHandler(w, r)
	case strings.HasPrefix(p, "/welfare/members/") && strings.HasSuffix(p, "/dependants/add"):
		addDependantHandler(w, r)
	case strings.HasPrefix(p, "/welfare/members/") && strings.HasSuffix(p, "/dependants/delete"):
		deleteDependantHandler(w, r)
	case strings.HasPrefix(p, "/welfare/members/") && strings.HasSuffix(p, "/deactivate"):
		deactivateMemberHandler(w, r)

	case p == "/welfare/expenses":
		expensesHandler(w, r)
	case p == "/welfare/expenses/add":
		addExpenseHandler(w, r)
	case strings.HasPrefix(p, "/welfare/expenses/") && strings.HasSuffix(p, "/delete"):
		deleteExpenseHandler(w, r)

	case p == "/welfare/beneficiaries":
		beneficiariesHandler(w, r)
	case p == "/welfare/beneficiaries/add":
		addBeneficiaryHandler(w, r)
	case strings.HasPrefix(p, "/welfare/beneficiaries/") && strings.HasSuffix(p, "/approve"):
		approveBeneficiaryHandler(w, r)
	case strings.HasPrefix(p, "/welfare/beneficiaries/") && strings.HasSuffix(p, "/complete"):
		completeBeneficiaryHandler(w, r)
	case strings.HasPrefix(p, "/welfare/beneficiaries/") && strings.HasSuffix(p, "/delete"):
		deleteBeneficiaryHandler(w, r)

	case p == "/welfare/export/members":
		exportMembersHandler(w, r)

	case strings.HasPrefix(p, "/welfare/api/member/"):
		memberAPIHandler(w, r)

	default:
		http.NotFound(w, r)
	}
}

// ── Members ──────────────────────────────────────────────────────────────────

type memberRow struct {
	ID         int
	MemberCode string
	Name       string
	Department string
	Phone      string
	Email      string
	JoinedDate string
	Status     string
	TotalPaid  float64
	MonthsPaid int
	DepCount   int
	ThisMonth  string // "paid", "pending", or ""
}

// updateMemberStatuses auto-sets status based on contribution history.
// Members with any unpaid past month → inactive.
// Members who have cleared all dues → active (unless resigned).
func updateMemberStatuses() {
	// active → inactive when any past month's contribution is missing
	db.Exec(`
		UPDATE welfare_members
		SET status = 'inactive'
		WHERE status = 'active'
		  AND joined_date IS NOT NULL
		  AND DATE_FORMAT(joined_date,'%Y-%m') < DATE_FORMAT(NOW(),'%Y-%m')
		  AND (
			SELECT COUNT(DISTINCT month)
			FROM welfare_contributions
			WHERE member_id = welfare_members.id
			  AND status = 'paid'
			  AND month >= DATE_FORMAT(joined_date,'%Y-%m')
			  AND month <= DATE_FORMAT(DATE_SUB(NOW(), INTERVAL 1 MONTH),'%Y-%m')
		  ) < TIMESTAMPDIFF(MONTH,
			DATE_FORMAT(joined_date,'%Y-%m-01'),
			DATE_FORMAT(NOW(),'%Y-%m-01')
		  )`)
	// inactive → active when all past months are now paid
	db.Exec(`
		UPDATE welfare_members
		SET status = 'active'
		WHERE status = 'inactive'
		  AND joined_date IS NOT NULL
		  AND (
			DATE_FORMAT(joined_date,'%Y-%m') >= DATE_FORMAT(NOW(),'%Y-%m')
			OR (
				SELECT COUNT(DISTINCT month)
				FROM welfare_contributions
				WHERE member_id = welfare_members.id
				  AND status = 'paid'
				  AND month >= DATE_FORMAT(joined_date,'%Y-%m')
				  AND month <= DATE_FORMAT(DATE_SUB(NOW(), INTERVAL 1 MONTH),'%Y-%m')
			) >= TIMESTAMPDIFF(MONTH,
				DATE_FORMAT(joined_date,'%Y-%m-01'),
				DATE_FORMAT(NOW(),'%Y-%m-01')
			)
		  )`)
}

func membersHandler(w http.ResponseWriter, r *http.Request) {
	updateMemberStatuses()

	rows, err := db.Query(`
		SELECT
			m.id, COALESCE(m.member_code,''), m.name,
			COALESCE(m.department,''), COALESCE(m.phone,''), COALESCE(m.email,''),
			COALESCE(DATE_FORMAT(m.joined_date,'%d %b %Y'),''),
			m.status,
			COALESCE(SUM(CASE WHEN c.status='paid' THEN c.amount ELSE 0 END), 0),
			COUNT(DISTINCT CASE WHEN c.status='paid' THEN c.id END),
			COUNT(DISTINCT d.id),
			COALESCE((SELECT status FROM welfare_contributions
			           WHERE member_id=m.id AND month=DATE_FORMAT(NOW(),'%Y-%m') LIMIT 1), '')
		FROM welfare_members m
		LEFT JOIN welfare_contributions c ON c.member_id = m.id
		LEFT JOIN welfare_dependants d ON d.member_id = m.id
		GROUP BY m.id, m.member_code, m.name, m.department, m.phone, m.email, m.joined_date, m.status
		ORDER BY m.name`)
	if err != nil {
		log.Printf("welfare members: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var members []memberRow
	for rows.Next() {
		var m memberRow
		if err := rows.Scan(&m.ID, &m.MemberCode, &m.Name, &m.Department, &m.Phone, &m.Email,
			&m.JoinedDate, &m.Status, &m.TotalPaid, &m.MonthsPaid, &m.DepCount, &m.ThisMonth); err != nil {
			log.Printf("welfare members scan: %v", err)
			continue
		}
		members = append(members, m)
	}
	rows.Close() // close before running summary queries to free the connection

	// Fund summary
	var totalContributions, totalExpenses, totalBeneficiaryPayouts float64
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0.00) FROM welfare_contributions WHERE status='paid'`).Scan(&totalContributions); err != nil {
		log.Printf("welfare contributions sum: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0.00) FROM welfare_expenses`).Scan(&totalExpenses); err != nil {
		log.Printf("welfare expenses sum: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount_approved), 0.00) FROM welfare_beneficiaries WHERE status='completed'`).Scan(&totalBeneficiaryPayouts); err != nil {
		log.Printf("welfare beneficiary payouts sum: %v", err)
	}

	var activeCount int
	db.QueryRow(`SELECT COUNT(*) FROM welfare_members WHERE status='active'`).Scan(&activeCount)

	selected := r.URL.Query().Get("m")
	renderWelfare(w, r, "welfare_members.html", map[string]any{
		"Title":                   "Staff Welfare – Members",
		"Active":                  "members",
		"Members":                 members,
		"TotalContributions":      totalContributions,
		"TotalExpenses":           totalExpenses,
		"TotalBeneficiaryPayouts": totalBeneficiaryPayouts,
		"TotalDeductions":         totalExpenses + totalBeneficiaryPayouts,
		"Balance":                 totalContributions - totalExpenses - totalBeneficiaryPayouts,
		"ActiveMembers":           activeCount,
		"SelectedID":              selected,
		"Success":                 r.URL.Query().Get("ok"),
		"Error":                   r.URL.Query().Get("err"),
	})
}

func addMemberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	dept := strings.TrimSpace(r.FormValue("department"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	email := strings.TrimSpace(r.FormValue("email"))
	joined := r.FormValue("joined_date")
	if name == "" {
		http.Redirect(w, r, "/welfare/members?err=Name+is+required", http.StatusFound)
		return
	}
	var joinedVal interface{}
	if joined != "" {
		joinedVal = joined
	}
	res, err := db.Exec(`INSERT INTO welfare_members (name, department, phone, email, joined_date) VALUES (?,?,?,?,?)`,
		name, dept, phone, email, joinedVal)
	if err != nil {
		log.Printf("add welfare member: %v", err)
		http.Redirect(w, r, "/welfare/members?err=Database+error", http.StatusFound)
		return
	}
	newID, _ := res.LastInsertId()
	var maxSeq int
	db.QueryRow(`SELECT COALESCE(MAX(CAST(SUBSTRING_INDEX(member_code, '-', -1) AS UNSIGNED)), 0) FROM welfare_members WHERE member_code IS NOT NULL AND id != ?`, newID).Scan(&maxSeq)
	db.Exec(`UPDATE welfare_members SET member_code=? WHERE id=?`, fmt.Sprintf("pwf-%04d", maxSeq+1), newID)
	http.Redirect(w, r, "/welfare/members?ok=Member+added", http.StatusFound)
}

func memberIDFromPath(prefix, p string) string {
	s := strings.TrimPrefix(p, prefix)
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

func addContributionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	memberID := memberIDFromPath("/welfare/members/", r.URL.Path)
	r.ParseForm()
	month := strings.TrimSpace(r.FormValue("month"))
	amount := strings.TrimSpace(r.FormValue("amount"))
	paidDate := strings.TrimSpace(r.FormValue("paid_date"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if month == "" {
		month = time.Now().Format("2006-01")
	}
	if amount == "" {
		amount = "500"
	}
	var paidDateVal interface{}
	if paidDate != "" {
		paidDateVal = paidDate
	} else {
		paidDateVal = time.Now().Format("2006-01-02")
	}
	db.Exec(`INSERT INTO welfare_contributions (member_id, amount, month, paid_date, status, notes)
		VALUES (?,?,?,?,'paid',?)
		ON DUPLICATE KEY UPDATE amount=VALUES(amount), paid_date=VALUES(paid_date), status='paid', notes=VALUES(notes)`,
		memberID, amount, month, paidDateVal, notes)

	http.Redirect(w, r, fmt.Sprintf("/welfare/members?m=%s&ok=Contribution+recorded", memberID), http.StatusFound)
}

func deleteContributionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	memberID := memberIDFromPath("/welfare/members/", r.URL.Path)
	r.ParseForm()
	cid := r.FormValue("contribution_id")
	db.Exec(`DELETE FROM welfare_contributions WHERE id=? AND member_id=?`, cid, memberID)
	http.Redirect(w, r, fmt.Sprintf("/welfare/members?m=%s", memberID), http.StatusFound)
}

func addDependantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	memberID := memberIDFromPath("/welfare/members/", r.URL.Path)
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	rel := strings.TrimSpace(r.FormValue("relationship"))
	dob := strings.TrimSpace(r.FormValue("dob"))
	if name == "" || rel == "" {
		http.Redirect(w, r, fmt.Sprintf("/welfare/members?m=%s&err=Name+and+relationship+required", memberID), http.StatusFound)
		return
	}
	var dobVal interface{}
	if dob != "" {
		dobVal = dob
	}
	idNumber := r.FormValue("id_number")
	db.Exec(`INSERT INTO welfare_dependants (member_id, name, relationship, dob, id_number) VALUES (?,?,?,?,?)`,
		memberID, name, rel, dobVal, idNumber)
	http.Redirect(w, r, fmt.Sprintf("/welfare/members?m=%s&ok=Dependant+added", memberID), http.StatusFound)
}

func deleteDependantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	memberID := memberIDFromPath("/welfare/members/", r.URL.Path)
	r.ParseForm()
	did := r.FormValue("dependant_id")
	db.Exec(`DELETE FROM welfare_dependants WHERE id=? AND member_id=?`, did, memberID)
	http.Redirect(w, r, fmt.Sprintf("/welfare/members?m=%s", memberID), http.StatusFound)
}

func deactivateMemberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/members", http.StatusFound)
		return
	}
	memberID := memberIDFromPath("/welfare/members/", r.URL.Path)
	r.ParseForm()
	var newStatus string
	switch r.FormValue("action") {
	case "activate":
		newStatus = "active"
	case "resign":
		newStatus = "resigned"
	default:
		newStatus = "inactive"
	}
	db.Exec(`UPDATE welfare_members SET status=? WHERE id=?`, newStatus, memberID)
	http.Redirect(w, r, "/welfare/members", http.StatusFound)
}

// ── Export ───────────────────────────────────────────────────────────────────

func exportMembersHandler(w http.ResponseWriter, r *http.Request) {
	// Members with contribution totals
	rows, err := db.Query(`
		SELECT m.id, m.name,
			COALESCE(m.department,''), COALESCE(m.phone,''), COALESCE(m.email,''),
			COALESCE(DATE_FORMAT(m.joined_date,'%Y-%m-%d'),''), m.status,
			COALESCE(SUM(CASE WHEN c.status='paid' THEN c.amount ELSE 0 END), 0),
			COUNT(DISTINCT CASE WHEN c.status='paid' THEN c.id END)
		FROM welfare_members m
		LEFT JOIN welfare_contributions c ON c.member_id = m.id
		GROUP BY m.id, m.name, m.department, m.phone, m.email, m.joined_date, m.status
		ORDER BY m.name`)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type memberExport struct {
		ID         int
		Name       string
		Department string
		Phone      string
		Email      string
		JoinedDate string
		Status     string
		TotalPaid  float64
		MonthsPaid int
	}
	var members []memberExport
	for rows.Next() {
		var m memberExport
		if err := rows.Scan(&m.ID, &m.Name, &m.Department, &m.Phone, &m.Email,
			&m.JoinedDate, &m.Status, &m.TotalPaid, &m.MonthsPaid); err != nil {
			log.Printf("export members scan: %v", err)
			continue
		}
		members = append(members, m)
	}

	// Dependants per member
	depRows, err := db.Query(`
		SELECT member_id, name, relationship, COALESCE(DATE_FORMAT(dob,'%Y-%m-%d'),'')
		FROM welfare_dependants ORDER BY member_id, name`)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer depRows.Close()

	type depExport struct {
		Name         string
		Relationship string
		DOB          string
	}
	deps := map[int][]depExport{}
	for depRows.Next() {
		var mid int
		var d depExport
		if depRows.Scan(&mid, &d.Name, &d.Relationship, &d.DOB) == nil {
			deps[mid] = append(deps[mid], d)
		}
	}

	fname := fmt.Sprintf("welfare_members_%s.csv", time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fname))

	// UTF-8 BOM so Excel auto-detects encoding
	w.Write([]byte("\xef\xbb\xbf"))

	cw := csv.NewWriter(w)
	cw.Write([]string{
		"Member Name", "Department", "Phone", "Email",
		"Date Joined", "Status", "Total Paid (KES)", "Months Paid",
		"Dependant Name", "Relationship", "Date of Birth",
	})

	for _, m := range members {
		base := []string{
			m.Name, m.Department, m.Phone, m.Email,
			m.JoinedDate, m.Status,
			fmt.Sprintf("%.0f", m.TotalPaid),
			fmt.Sprintf("%d", m.MonthsPaid),
		}
		memberDeps := deps[m.ID]
		if len(memberDeps) == 0 {
			cw.Write(append(base, "", "", ""))
		} else {
			for _, d := range memberDeps {
				cw.Write(append(base, d.Name, d.Relationship, d.DOB))
			}
		}
	}
	cw.Flush()
}

// ── Member JSON API ──────────────────────────────────────────────────────────

type memberDetail struct {
	ID            int            `json:"id"`
	MemberCode    string         `json:"member_code"`
	Name          string         `json:"name"`
	Department    string         `json:"department"`
	Phone         string         `json:"phone"`
	Email         string         `json:"email"`
	JoinedDate    string         `json:"joined_date"`
	Status        string         `json:"status"`
	Contributions []contribution `json:"contributions"`
	Dependants    []dependant    `json:"dependants"`
	TotalPaid     float64        `json:"total_paid"`
	MonthsPaid    int            `json:"months_paid"`
}

type contribution struct {
	ID       int     `json:"id"`
	Month    string  `json:"month"`
	Amount   float64 `json:"amount"`
	PaidDate string  `json:"paid_date"`
	Status   string  `json:"status"`
	Notes    string  `json:"notes"`
}

type dependant struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	DOB          string `json:"dob"`
	IDNumber     string `json:"id_number"`
}

func memberAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/welfare/api/member/")
	id = strings.Trim(id, "/")

	var m memberDetail
	err := db.QueryRow(`SELECT id, COALESCE(member_code,''), name, COALESCE(department,''), COALESCE(phone,''), COALESCE(email,''),
		COALESCE(DATE_FORMAT(joined_date,'%d %b %Y'),''), status
		FROM welfare_members WHERE id=? ORDER BY id, member_code`, id).
		Scan(&m.ID, &m.MemberCode, &m.Name, &m.Department, &m.Phone, &m.Email, &m.JoinedDate, &m.Status)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	// Contributions
	crows, _ := db.Query(`SELECT id, month, amount, COALESCE(DATE_FORMAT(paid_date,'%d %b %Y'),''), status, COALESCE(notes,'')
		FROM welfare_contributions WHERE member_id=? ORDER BY month DESC`, id)
	if crows != nil {
		defer crows.Close()
		for crows.Next() {
			var c contribution
			if crows.Scan(&c.ID, &c.Month, &c.Amount, &c.PaidDate, &c.Status, &c.Notes) == nil {
				m.Contributions = append(m.Contributions, c)
				if c.Status == "paid" {
					m.TotalPaid += c.Amount
					m.MonthsPaid++
				}
			}
		}
	}

	// Dependants
	drows, _ := db.Query(`SELECT id, name, relationship, COALESCE(DATE_FORMAT(dob,'%d %b %Y'),''), COALESCE(id_number,'')
		FROM welfare_dependants WHERE member_id=? ORDER BY name`, id)
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var d dependant
			if drows.Scan(&d.ID, &d.Name, &d.Relationship, &d.DOB, &d.IDNumber) == nil {
				m.Dependants = append(m.Dependants, d)
			}
		}
	}

	if m.Contributions == nil {
		m.Contributions = []contribution{}
	}
	if m.Dependants == nil {
		m.Dependants = []dependant{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

// ── Expenses ─────────────────────────────────────────────────────────────────

type expenseRow struct {
	ID              int
	Amount          float64
	Purpose         string
	BeneficiaryName string
	ExpenseDate     string
	RecordedBy      string
	Notes           string
}

func expensesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT id, amount, purpose, COALESCE(beneficiary_name,''),
			DATE_FORMAT(expense_date,'%d %b %Y'),
			COALESCE(recorded_by,''), COALESCE(notes,'')
		FROM welfare_expenses
		ORDER BY expense_date DESC, id DESC`)
	if err != nil {
		log.Printf("welfare expenses: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var expenses []expenseRow
	for rows.Next() {
		var e expenseRow
		if err := rows.Scan(&e.ID, &e.Amount, &e.Purpose, &e.BeneficiaryName, &e.ExpenseDate, &e.RecordedBy, &e.Notes); err != nil {
			log.Printf("welfare expenses scan: %v", err)
			continue
		}
		expenses = append(expenses, e)
	}
	rows.Close() // close before running summary queries to free the connection

	var totalContributions, totalExpenses, totalBeneficiaryPayouts float64
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0.00) FROM welfare_contributions WHERE status='paid'`).Scan(&totalContributions); err != nil {
		log.Printf("welfare contributions sum: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0.00) FROM welfare_expenses`).Scan(&totalExpenses); err != nil {
		log.Printf("welfare expenses sum: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount_approved), 0.00) FROM welfare_beneficiaries WHERE status='completed'`).Scan(&totalBeneficiaryPayouts); err != nil {
		log.Printf("welfare beneficiary payouts sum: %v", err)
	}

	renderWelfare(w, r, "welfare_expenses.html", map[string]any{
		"Title":                   "Staff Welfare – Expenses",
		"Active":                  "expenses",
		"Expenses":                expenses,
		"TotalExpenses":           totalExpenses,
		"TotalBeneficiaryPayouts": totalBeneficiaryPayouts,
		"TotalContributions":      totalContributions,
		"Balance":                 totalContributions - totalExpenses - totalBeneficiaryPayouts,
		"Today":                   time.Now().Format("2006-01-02"),
		"Success":                 r.URL.Query().Get("ok"),
		"Error":                   r.URL.Query().Get("err"),
	})
}

func addExpenseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/expenses", http.StatusFound)
		return
	}
	r.ParseForm()
	amount := strings.TrimSpace(r.FormValue("amount"))
	purpose := strings.TrimSpace(r.FormValue("purpose"))
	beneficiary := strings.TrimSpace(r.FormValue("beneficiary_name"))
	expDate := strings.TrimSpace(r.FormValue("expense_date"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	recordedBy := strings.TrimSpace(r.FormValue("recorded_by"))

	if amount == "" || purpose == "" || expDate == "" {
		http.Redirect(w, r, "/welfare/expenses?err=Amount+purpose+and+date+are+required", http.StatusFound)
		return
	}
	if _, err := db.Exec(`INSERT INTO welfare_expenses (amount, purpose, beneficiary_name, expense_date, recorded_by, notes) VALUES (?,?,?,?,?,?)`,
		amount, purpose, beneficiary, expDate, recordedBy, notes); err != nil {
		log.Printf("add expense: %v", err)
		http.Redirect(w, r, "/welfare/expenses?err=Database+error", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/welfare/expenses?ok=Expense+recorded", http.StatusFound)
}

func deleteExpenseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/expenses", http.StatusFound)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/welfare/expenses/")
	id = strings.TrimSuffix(id, "/delete")
	db.Exec(`DELETE FROM welfare_expenses WHERE id=?`, id)
	http.Redirect(w, r, "/welfare/expenses", http.StatusFound)
}

// ── Beneficiaries ─────────────────────────────────────────────────────────────

type beneficiaryRow struct {
	ID              int
	MemberName      string
	MemberID        int
	Category        string
	Description     string
	AmountRequested float64
	AmountApproved  float64
	Status          string
	RequestedDate   string
	ApprovedDate    string
	CompletedDate   string
	Notes           string
}

func beneficiariesHandler(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	rows, err := db.Query(`
		SELECT b.id, m.name, m.id, b.category, COALESCE(b.description,''),
			COALESCE(b.amount_requested,0), COALESCE(b.amount_approved,0),
			b.status,
			DATE_FORMAT(b.requested_date,'%d %b %Y'),
			COALESCE(DATE_FORMAT(b.approved_date,'%d %b %Y'),''),
			COALESCE(DATE_FORMAT(b.completed_date,'%d %b %Y'),''),
			COALESCE(b.notes,'')
		FROM welfare_beneficiaries b
		JOIN welfare_members m ON m.id = b.member_id
		WHERE b.status = ?
		ORDER BY b.created_at DESC`, status)
	if err != nil {
		log.Printf("welfare beneficiaries: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var beneficiaries []beneficiaryRow
	for rows.Next() {
		var b beneficiaryRow
		if err := rows.Scan(&b.ID, &b.MemberName, &b.MemberID, &b.Category, &b.Description,
			&b.AmountRequested, &b.AmountApproved, &b.Status,
			&b.RequestedDate, &b.ApprovedDate, &b.CompletedDate, &b.Notes); err != nil {
			continue
		}
		beneficiaries = append(beneficiaries, b)
	}

	// Status counts for tabs
	counts := map[string]int{"pending": 0, "approved": 0, "completed": 0}
	crows, _ := db.Query(`SELECT status, COUNT(*) FROM welfare_beneficiaries GROUP BY status`)
	if crows != nil {
		defer crows.Close()
		for crows.Next() {
			var s string
			var c int
			if crows.Scan(&s, &c) == nil {
				counts[s] = c
			}
		}
	}

	// Load members for the add form dropdown
	mrows, _ := db.Query(`SELECT id, name FROM welfare_members WHERE status='active' ORDER BY name`)
	type memberOpt struct {
		ID   int
		Name string
	}
	var memberOpts []memberOpt
	if mrows != nil {
		defer mrows.Close()
		for mrows.Next() {
			var mo memberOpt
			if mrows.Scan(&mo.ID, &mo.Name) == nil {
				memberOpts = append(memberOpts, mo)
			}
		}
	}

	renderWelfare(w, r, "welfare_beneficiaries.html", map[string]any{
		"Title":         "Staff Welfare – Beneficiaries",
		"Active":        "beneficiaries",
		"Status":        status,
		"Beneficiaries": beneficiaries,
		"Counts":        counts,
		"Members":       memberOpts,
		"Today":         time.Now().Format("2006-01-02"),
		"Success":       r.URL.Query().Get("ok"),
		"Error":         r.URL.Query().Get("err"),
	})
}

func addBeneficiaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/beneficiaries", http.StatusFound)
		return
	}
	r.ParseForm()
	memberID := r.FormValue("member_id")
	category := strings.TrimSpace(r.FormValue("category"))
	description := strings.TrimSpace(r.FormValue("description"))
	amtReq := strings.TrimSpace(r.FormValue("amount_requested"))
	reqDate := strings.TrimSpace(r.FormValue("requested_date"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if memberID == "" || category == "" || reqDate == "" {
		http.Redirect(w, r, "/welfare/beneficiaries?err=Member+category+and+date+are+required", http.StatusFound)
		return
	}
	var amtVal interface{}
	if amtReq != "" {
		amtVal = amtReq
	}
	if _, err := db.Exec(`INSERT INTO welfare_beneficiaries (member_id, category, description, amount_requested, requested_date, notes) VALUES (?,?,?,?,?,?)`,
		memberID, category, description, amtVal, reqDate, notes); err != nil {
		log.Printf("add beneficiary: %v", err)
		http.Redirect(w, r, "/welfare/beneficiaries?err=Database+error", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/welfare/beneficiaries?status=pending&ok=Case+added", http.StatusFound)
}

func approveBeneficiaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/beneficiaries", http.StatusFound)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/welfare/beneficiaries/")
	id = strings.TrimSuffix(id, "/approve")
	r.ParseForm()
	amtApproved := strings.TrimSpace(r.FormValue("amount_approved"))
	var amtVal interface{}
	if amtApproved != "" {
		amtVal = amtApproved
	}
	db.Exec(`UPDATE welfare_beneficiaries SET status='approved', amount_approved=?, approved_date=CURDATE() WHERE id=? AND status='pending'`,
		amtVal, id)
	http.Redirect(w, r, "/welfare/beneficiaries?status=approved&ok=Case+approved", http.StatusFound)
}

func completeBeneficiaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/beneficiaries", http.StatusFound)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/welfare/beneficiaries/")
	id = strings.TrimSuffix(id, "/complete")
	db.Exec(`UPDATE welfare_beneficiaries SET status='completed', completed_date=CURDATE() WHERE id=? AND status='approved'`, id)
	http.Redirect(w, r, "/welfare/beneficiaries?status=completed&ok=Case+completed", http.StatusFound)
}

func deleteBeneficiaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/welfare/beneficiaries", http.StatusFound)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/welfare/beneficiaries/")
	id = strings.TrimSuffix(id, "/delete")
	db.Exec(`DELETE FROM welfare_beneficiaries WHERE id=?`, id)
	http.Redirect(w, r, "/welfare/beneficiaries", http.StatusFound)
}
