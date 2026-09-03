package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// bookingRef is the buyer/estate/plot data needed to rebuild a Zoho Books
// contact_name in the "Buyer — Estate (Plot N)" format, plus the phone/email
// updateBooksContact needs so a fix never blanks out existing contact info.
type bookingRef struct {
	BuyerName  string
	BuyerPhone string
	BuyerEmail string
	EstateName string
	PlotNumber string
	Source     string // "booking" or "sale"
}

// findBookingRefByEstimate looks up the buyer/estate/plot behind a Zoho Books
// estimate ID, checking prop_bookings first, then the prop_sales mirror for
// legacy sold-only records.
func findBookingRefByEstimate(estimateID string) (bookingRef, bool) {
	var r bookingRef
	err := db.QueryRow(`
		SELECT COALESCE(b.buyer_name,''), COALESCE(b.buyer_phone,''), COALESCE(b.buyer_email,''),
		       e.name, p.plot_number
		FROM prop_bookings b
		JOIN prop_plots p ON p.id = b.plot_id
		JOIN prop_estates e ON e.id = p.estate_id
		WHERE b.zoho_books_id = ?
		ORDER BY b.id DESC LIMIT 1`, estimateID).
		Scan(&r.BuyerName, &r.BuyerPhone, &r.BuyerEmail, &r.EstateName, &r.PlotNumber)
	if err == nil {
		r.Source = "booking"
		return r, true
	}

	err = db.QueryRow(`
		SELECT COALESCE(s.buyer_name,''), COALESCE(s.buyer_phone,''), COALESCE(s.buyer_email,''),
		       e.name, p.plot_number
		FROM prop_sales s
		JOIN prop_plots p ON p.id = s.plot_id
		JOIN prop_estates e ON e.id = s.estate_id
		WHERE s.zoho_books_id = ?
		ORDER BY s.id DESC LIMIT 1`, estimateID).
		Scan(&r.BuyerName, &r.BuyerPhone, &r.BuyerEmail, &r.EstateName, &r.PlotNumber)
	if err == nil {
		r.Source = "sale"
		return r, true
	}

	return bookingRef{}, false
}

func runAuditBooksDisplayName(args []string) {
	fs := flag.NewFlagSet("audit-books-displayname", flag.ExitOnError)
	doFix := fs.Bool("fix", false, "actually update malformed contact names (default: list only)")
	fs.Parse(args)

	initDB()

	estimates, err := fetchAllBooksEstimates()
	if err != nil {
		log.Fatalf("[audit-displayname] fetch failed: %v", err)
	}
	log.Printf("[audit-displayname] total estimates in Zoho Books: %d", len(estimates))

	var candidates []booksEstimateSummary
	for _, e := range estimates {
		if !strings.Contains(e.CustomerName, " — ") {
			candidates = append(candidates, e)
		}
	}
	log.Printf("[audit-displayname] %d estimate(s) with a name missing the estate/plot suffix", len(candidates))

	fmt.Printf("\nestimate_id,customer_id,current_name,correct_name,source,action\n")
	fixable := 0
	for _, e := range candidates {
		ref, found := findBookingRefByEstimate(e.EstimateID)
		if !found {
			fmt.Printf("%s,%s,%q,,none,SKIP (no matching prop_bookings/prop_sales row for this estimate)\n",
				e.EstimateID, e.CustomerID, e.CustomerName)
			continue
		}

		correctName := fmt.Sprintf("%s — %s (Plot %s)", ref.BuyerName, ref.EstateName, ref.PlotNumber)
		if correctName == e.CustomerName {
			fmt.Printf("%s,%s,%q,%q,%s,SKIP (already correct)\n",
				e.EstimateID, e.CustomerID, e.CustomerName, correctName, ref.Source)
			continue
		}

		action := "NEEDS FIX"
		fixable++
		fmt.Printf("%s,%s,%q,%q,%s,%s\n",
			e.EstimateID, e.CustomerID, e.CustomerName, correctName, ref.Source, action)

		if *doFix {
			if err := updateBooksContact(e.EstimateID, ref.BuyerName, ref.BuyerPhone, ref.BuyerEmail, ref.EstateName, ref.PlotNumber); err != nil {
				log.Printf("[audit-displayname] fix estimate %s FAILED: %v", e.EstimateID, err)
				continue
			}
			log.Printf("[audit-displayname] fixed contact name for estimate %s -> %q", e.EstimateID, correctName)
		}
	}

	log.Printf("[audit-displayname] finished: %d candidate(s), %d fixable%s",
		len(candidates), fixable, map[bool]string{true: " (fixed)", false: " (not fixed — rerun with -fix)"}[*doFix])
	os.Exit(0)
}
