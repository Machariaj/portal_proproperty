package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type instBookingInfo struct {
	BookingID  int
	BuyerName  string
	PlotNumber string
	EstateName string
	ZohoCRMID  string
}

// instRowDisplay is an installmentRow with DueDate pre-formatted for the
// HTML date input (YYYY-MM-DD) so the template needs no custom function.
type instRowDisplay struct {
	Number      int
	Amount      float64
	DueDateStr  string
	Description string
}

func toDisplay(rows []installmentRow) []instRowDisplay {
	out := make([]instRowDisplay, len(rows))
	for i, r := range rows {
		num := r.Number
		if num == 0 {
			num = i + 1
		}
		out[i] = instRowDisplay{
			Number:      num,
			Amount:      r.Amount,
			DueDateStr:  r.DueDate.Format("2006-01-02"),
			Description: r.Description,
		}
	}
	return out
}

// adminInstallmentsHandler serves GET /admin/installments/{bookingID} (upload
// form) and POST /admin/installments/{bookingID} (extract or push).
func adminInstallmentsHandler(w http.ResponseWriter, r *http.Request) {
	bidStr := pathSegment("/admin/installments/", r.URL.Path)
	bid, err := strconv.Atoi(bidStr)
	if err != nil || bid <= 0 {
		http.NotFound(w, r)
		return
	}

	var info instBookingInfo
	if err := db.QueryRow(`
		SELECT b.id, b.buyer_name, p.plot_number, e.name, COALESCE(b.zoho_crm_id,'')
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = b.estate_id
		WHERE b.id = ? AND b.status IN ('sa_signed','completed')`, bid).
		Scan(&info.BookingID, &info.BuyerName, &info.PlotNumber, &info.EstateName, &info.ZohoCRMID); err != nil {
		http.Error(w, "Booking not found or not SA signed", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodGet {
		renderAdmin(w, r, "admin_installments.html", map[string]any{
			"Title":   "Installment Schedule",
			"Active":  "signed-plots",
			"Booking": info,
			"Error":   r.URL.Query().Get("err"),
			"Success": r.URL.Query().Get("ok"),
		})
		return
	}

	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	switch r.FormValue("action") {
	case "extract":
		adminInstallmentsExtract(w, r, info)
	case "push":
		adminInstallmentsPush(w, r, info)
	default:
		http.NotFound(w, r)
	}
}

func adminInstallmentsExtract(w http.ResponseWriter, r *http.Request, info instBookingInfo) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "form parse error", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("schedule")
	if err != nil {
		renderInstPage(w, r, info, nil, "No file uploaded — please choose an image or PDF.")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}

	tmp, err := os.CreateTemp("", "inst-upload-*"+ext)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		http.Error(w, "upload error", http.StatusInternalServerError)
		return
	}
	tmp.Close()

	rows, err := claudeExtractInstallments(tmpPath, ext)
	if err != nil {
		log.Printf("[installments-extract] booking %d: %v", info.BookingID, err)
		renderInstPage(w, r, info, nil, "Extraction failed: "+err.Error())
		return
	}
	if len(rows) == 0 {
		renderInstPage(w, r, info, nil, "No installments found in the image. Check the image is clear and try again.")
		return
	}
	log.Printf("[installments-extract] booking %d: extracted %d row(s) via Claude Vision", info.BookingID, len(rows))
	renderInstPage(w, r, info, toDisplay(rows), "")
}

func adminInstallmentsPush(w http.ResponseWriter, r *http.Request, info instBookingInfo) {
	if info.ZohoCRMID == "" {
		http.Redirect(w, r, fmt.Sprintf("/admin/installments/%d?err=No+Zoho+CRM+deal+found.+SA+Signed+step+may+not+have+run+yet.", info.BookingID), http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse error", 400)
		return
	}
	count, _ := strconv.Atoi(r.FormValue("count"))
	var rows []installmentRow
	for i := 0; i < count; i++ {
		amtStr := strings.ReplaceAll(r.FormValue(fmt.Sprintf("amount_%d", i)), ",", "")
		amt, err := strconv.ParseFloat(amtStr, 64)
		if err != nil || amt <= 0 {
			continue
		}
		due, err := time.Parse("2006-01-02", r.FormValue(fmt.Sprintf("due_date_%d", i)))
		if err != nil {
			continue
		}
		num, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("number_%d", i)))
		if num == 0 {
			num = i + 1
		}
		rows = append(rows, installmentRow{
			Number:      num,
			Amount:      amt,
			DueDate:     due,
			Description: r.FormValue(fmt.Sprintf("description_%d", i)),
		})
	}
	if len(rows) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/admin/installments/%d?err=No+valid+installments+to+push", info.BookingID), http.StatusFound)
		return
	}
	if err := createCRMInstallments(info.ZohoCRMID, rows); err != nil {
		log.Printf("[installments-push] booking %d CRM %s: %v", info.BookingID, info.ZohoCRMID, err)
		http.Redirect(w, r, fmt.Sprintf("/admin/installments/%d?err=Zoho+push+failed:+%s", info.BookingID, err.Error()), http.StatusFound)
		return
	}
	log.Printf("[installments-push] booking %d: pushed %d installment(s) to CRM deal %s", info.BookingID, len(rows), info.ZohoCRMID)
	http.Redirect(w, r, fmt.Sprintf("/admin/installments/%d?ok=1", info.BookingID), http.StatusFound)
}

func renderInstPage(w http.ResponseWriter, r *http.Request, info instBookingInfo, rows []instRowDisplay, errMsg string) {
	renderAdmin(w, r, "admin_installments.html", map[string]any{
		"Title":    "Installment Schedule",
		"Active":   "signed-plots",
		"Booking":  info,
		"Rows":     rows,
		"RowCount": len(rows),
		"Error":    errMsg,
	})
}
