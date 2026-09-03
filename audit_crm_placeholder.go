package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// crmDealPlaceholder is the subset of a Zoho CRM Deal needed to find
// placeholder ("Joseph Maina" as both buyer and agent) records. Unlike Books
// contacts, Deal_Name and Agent_Name are both plain top-level fields here —
// no secondary lookup needed.
type crmDealPlaceholder struct {
	ID       string `json:"id"`
	DealName string `json:"Deal_Name"`
	Agent    string `json:"Agent_Name"`
	Estates  string `json:"Estates"`
	Plot     string `json:"Plot"`
}

func fetchAllCRMDealsWithAgent() ([]crmDealPlaceholder, error) {
	token, err := getCRMToken()
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	var all []crmDealPlaceholder
	page := 1
	for {
		url := fmt.Sprintf("%sDeals?fields=Deal_Name,Agent_Name,Estates,Plot&per_page=200&page=%d", zohoCRMBase, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		if resp.StatusCode == 204 {
			resp.Body.Close()
			break
		}
		var result struct {
			Data []crmDealPlaceholder `json:"data"`
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
		log.Printf("[audit-crm-placeholder] fetched page %d (%d deals, %d total so far)", page, len(result.Data), len(all))
		if !result.Info.MoreRecords {
			break
		}
		page++
	}
	return all, nil
}

func deleteCRMDeal(dealID string) error {
	token, err := getCRMToken()
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%sDeals/%s", zohoCRMBase, dealID)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func runAuditCRMPlaceholder(args []string) {
	fs := flag.NewFlagSet("audit-crm-placeholder", flag.ExitOnError)
	name := fs.String("name", "Joseph Maina", "name to match against both Deal_Name (buyer) and Agent_Name")
	doDelete := fs.Bool("delete", false, "actually delete confirmed-orphaned deals (default: list only)")
	fs.Parse(args)

	initDB()

	deals, err := fetchAllCRMDealsWithAgent()
	if err != nil {
		log.Fatalf("[audit-crm-placeholder] fetch failed: %v", err)
	}
	log.Printf("[audit-crm-placeholder] total deals in Zoho CRM: %d", len(deals))

	var candidates []crmDealPlaceholder
	for _, d := range deals {
		if strings.EqualFold(strings.TrimSpace(d.DealName), *name) && strings.EqualFold(strings.TrimSpace(d.Agent), *name) {
			candidates = append(candidates, d)
		}
	}
	log.Printf("[audit-crm-placeholder] %d deal(s) with buyer AND agent both %q", len(candidates), *name)

	// Load current DB references (both prop_bookings and prop_sales can hold a zoho_crm_id).
	referenced := map[string]bool{}
	rows, err := db.Query(`SELECT zoho_crm_id FROM prop_bookings WHERE zoho_crm_id IS NOT NULL AND zoho_crm_id <> ''`)
	if err != nil {
		log.Fatalf("[audit-crm-placeholder] db query: %v", err)
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			referenced[id] = true
		}
	}
	rows.Close()
	srows, err := db.Query(`SELECT zoho_crm_id FROM prop_sales WHERE zoho_crm_id IS NOT NULL AND zoho_crm_id <> ''`)
	if err == nil {
		for srows.Next() {
			var id string
			if srows.Scan(&id) == nil {
				referenced[id] = true
			}
		}
		srows.Close()
	}

	fmt.Printf("\ndeal_id,deal_name,agent_name,estate,plot,db_referenced,action\n")
	orphanedCount := 0
	for _, d := range candidates {
		isReferenced := referenced[d.ID]
		action := "ORPHANED — safe to delete"
		if isReferenced {
			action = "SKIP (still referenced by a current DB row)"
		} else {
			orphanedCount++
		}
		fmt.Printf("%s,%q,%q,%q,%q,%v,%s\n", d.ID, d.DealName, d.Agent, d.Estates, d.Plot, isReferenced, action)

		if !isReferenced && *doDelete {
			if err := deleteCRMDeal(d.ID); err != nil {
				log.Printf("[audit-crm-placeholder] delete deal %s FAILED: %v", d.ID, err)
				continue
			}
			log.Printf("[audit-crm-placeholder] deleted deal %s", d.ID)
		}
	}

	log.Printf("[audit-crm-placeholder] finished: %d candidate(s), %d orphaned+safe%s",
		len(candidates), orphanedCount, map[bool]string{true: " (deleted)", false: " (not deleted — rerun with -delete)"}[*doDelete])
	os.Exit(0)
}
