package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	accessLogger  *log.Logger
	bookingLogger *log.Logger
)

func initLoggers() {
	// Resolve log directory: LOG_DIR env var, else logs/ next to the executable.
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		exe, err := os.Executable()
		if err == nil {
			logDir = filepath.Join(filepath.Dir(exe), "logs")
		} else {
			logDir = "logs"
		}
	}

	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("logger: could not create logs dir %s: %v", logDir, err)
		return
	}

	af, err := os.OpenFile(filepath.Join(logDir, "access.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("logger: could not open access.log: %v", err)
	} else {
		accessLogger = log.New(af, "", 0)
	}

	bf, err := os.OpenFile(filepath.Join(logDir, "bookings.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("logger: could not open bookings.log: %v", err)
	} else {
		bookingLogger = log.New(bf, "", 0)
	}

	// Route all log.* output to both stdout and app.log so `cat logs/app.log`
	// shows everything: errors, status changes, Zoho calls, scheduler events.
	appf, err := os.OpenFile(filepath.Join(logDir, "app.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("logger: could not open app.log: %v", err)
	} else {
		log.SetOutput(io.MultiWriter(os.Stdout, appf))
		log.SetFlags(log.LstdFlags)
	}

	log.Printf("logger: writing logs to %s", logDir)
}

func ts() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// logAccess writes one line to access.log.
// event: LOGIN_OK, LOGIN_FAIL, LOGOUT, PAGE
func logAccess(event, name, role, ip, detail string) {
	if accessLogger == nil {
		return
	}
	accessLogger.Printf("%s | %-12s | %-30s | %-14s | %-16s | %s",
		ts(), event, name+" ("+role+")", ip, role, detail)
}

// logBooking writes one line to bookings.log.
// event: PLOT_BOOKED, DOCS_UPLOADED, EMAIL_SENT
func logBooking(event, agentName, clientName, plotInfo, detail string) {
	if bookingLogger == nil {
		return
	}
	bookingLogger.Printf("%s | %-14s | Agent: %-25s | Client: %-25s | %s | %s",
		ts(), event, agentName, clientName, plotInfo, detail)
}

// skipNavLog returns true for paths that should not be written to the access log
// (static assets, uploads, favicon).
func skipNavLog(path string) bool {
	for _, pfx := range []string{"/static/", "/uploads/", "/favicon"} {
		if strings.HasPrefix(path, pfx) {
			return true
		}
	}
	return false
}

// loggingMiddleware wraps authMiddleware to record every page visit.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !skipNavLog(r.URL.Path) {
			name := getAgentName(r)
			role := getRole(r)
			if name == "" {
				name = "anonymous"
			}
			if role == "" {
				role = "—"
			}
			detail := fmt.Sprintf("%s %s", r.Method, r.URL.RequestURI())
			logAccess("PAGE", name, role, clientIP(r), detail)
		}
		next.ServeHTTP(w, r)
	})
}
