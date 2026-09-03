package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

// crmDealSummary is the subset of a Zoho CRM Deal record needed to detect
// duplicates — deals that reference the same Books estimate, or the same
// estate+plot, more than once.
type crmDealSummary struct {
	ID             string `json:"id"`
	DealName       string `json:"Deal_Name"`
	BooksRefNumber string `json:"Books_Ref_Number"`
	Estates        string `json:"Estates"`
	Plot           string `json:"Plot"`
	CreatedTime    string `json:"Created_Time"`
}

// fetchAllCRMDeals pages through every Deal in Zoho CRM (v8 GET /Deals),
// 200 records per page, until Zoho reports no more records.
func fetchAllCRMDeals() ([]crmDealSummary, error) {
	token, err := getCRMToken()
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}

	var all []crmDealSummary
	page := 1
	for {
		url := fmt.Sprintf("%sDeals?fields=Deal_Name,Books_Ref_Number,Estates,Plot,Created_Time&per_page=200&page=%d&sort_by=Created_Time&sort_order=asc",
			zohoCRMBase, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		if resp.StatusCode == 204 {
			resp.Body.Close()
			break // no more records
		}
		var result struct {
			Data []crmDealSummary `json:"data"`
			Info struct {
				MoreRecords bool `json:"more_records"`
			} `json:"info"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("page %d decode: %w", page, err)
		}
		resp.Body.Close()

		all = append(all, result.Data...)
		log.Printf("[audit-crm] fetched page %d (%d deals, %d total so far)", page, len(result.Data), len(all))
		if !result.Info.MoreRecords {
			break
		}
		page++
	}
	return all, nil
}

func runAuditCRM(args []string) {
	fs := flag.NewFlagSet("audit-crm-duplicates", flag.ExitOnError)
	fs.Parse(args)

	deals, err := fetchAllCRMDeals()
	if err != nil {
		log.Fatalf("[audit-crm] fetch failed: %v", err)
	}
	log.Printf("[audit-crm] total deals in Zoho CRM: %d", len(deals))

	// ── group by Books_Ref_Number — the strongest duplicate signal, since
	// each booking/sale gets exactly one unique Books estimate. Two deals
	// sharing one is almost certainly the same underlying record created twice.
	byBooksRef := map[string][]crmDealSummary{}
	for _, d := range deals {
		ref := strings.TrimSpace(d.BooksRefNumber)
		if ref == "" {
			continue // deals with no ref can't be checked this way
		}
		byBooksRef[ref] = append(byBooksRef[ref], d)
	}

	// ── group by estate+plot as a secondary check (catches duplicates even
	// if Books_Ref_Number is blank or itself duplicated).
	byPlot := map[string][]crmDealSummary{}
	for _, d := range deals {
		key := normEstate(d.Estates) + "|" + normPlot(d.Plot)
		if strings.TrimSpace(key) == "|" {
			continue
		}
		byPlot[key] = append(byPlot[key], d)
	}

	dupBooksRefs := dupKeys(byBooksRef)
	dupPlots := dupKeys(byPlot)

	fmt.Printf("\n=== Duplicate Books_Ref_Number groups (%d) ===\n", len(dupBooksRefs))
	for _, k := range dupBooksRefs {
		fmt.Printf("\nBooks_Ref_Number=%s — %d deals:\n", k, len(byBooksRef[k]))
		for _, d := range byBooksRef[k] {
			fmt.Printf("  id=%s  name=%q  estate=%q  plot=%q  created=%s\n", d.ID, d.DealName, d.Estates, d.Plot, d.CreatedTime)
		}
	}

	fmt.Printf("\n=== Duplicate estate+plot groups (%d) ===\n", len(dupPlots))
	for _, k := range dupPlots {
		fmt.Printf("\n%s — %d deals:\n", k, len(byPlot[k]))
		for _, d := range byPlot[k] {
			fmt.Printf("  id=%s  name=%q  books_ref=%q  created=%s\n", d.ID, d.DealName, d.BooksRefNumber, d.CreatedTime)
		}
	}

	if len(dupBooksRefs) == 0 && len(dupPlots) == 0 {
		fmt.Println("\nNo duplicates found.")
	}
	os.Exit(0)
}

func dupKeys(m map[string][]crmDealSummary) []string {
	var out []string
	for k, v := range m {
		if len(v) > 1 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
