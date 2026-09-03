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

// booksEstimateSummary is the subset of a Zoho Books estimate needed to find
// placeholder ("Joseph Maina") records — customer_name is the denormalized
// contact name, e.g. "Joseph Maina — NACHU VIEWS ESTATE PHASE 2 (Plot 16)".
type booksEstimateSummary struct {
	EstimateID   string `json:"estimate_id"`
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
}

// fetchAllBooksEstimates pages through every estimate in Zoho Books.
func fetchAllBooksEstimates() ([]booksEstimateSummary, error) {
	token, err := getBooksToken()
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	var all []booksEstimateSummary
	page := 1
	for {
		url := fmt.Sprintf("%sestimates?organization_id=%s&per_page=200&page=%d", zohoBooksBase, zohoBooksOrgID, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		var result struct {
			Estimates   []booksEstimateSummary `json:"estimates"`
			PageContext struct {
				HasMorePage bool `json:"has_more_page"`
			} `json:"page_context"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("page %d decode: %w", page, err)
		}
		resp.Body.Close()
		all = append(all, result.Estimates...)
		log.Printf("[audit-books] fetched page %d (%d estimates, %d total so far)", page, len(result.Estimates), len(all))
		if !result.PageContext.HasMorePage {
			break
		}
		page++
	}
	return all, nil
}

// contactPerson mirrors the shape used when creating a contact in
// syncBooksCustomer — the secondary contact_person holds the agent's name.
type contactPerson struct {
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Salutation string `json:"salutation"`
}

// getBooksContactPersons fetches a contact's full detail to inspect its
// contact_persons list (not present on the estimates list endpoint).
func getBooksContactPersons(contactID string) ([]contactPerson, error) {
	token, err := getBooksToken()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%scontacts/%s?organization_id=%s", zohoBooksBase, contactID, zohoBooksOrgID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Contact struct {
			ContactPersons []contactPerson `json:"contact_persons"`
		} `json:"contact"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Contact.ContactPersons, nil
}

func deleteBooksEstimate(estimateID string) error {
	token, err := getBooksToken()
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%sestimates/%s?organization_id=%s", zohoBooksBase, estimateID, zohoBooksOrgID)
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

func deleteBooksContact(contactID string) error {
	token, err := getBooksToken()
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%scontacts/%s?organization_id=%s", zohoBooksBase, contactID, zohoBooksOrgID)
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

func runAuditBooksPlaceholder(args []string) {
	fs := flag.NewFlagSet("audit-books-placeholder", flag.ExitOnError)
	name := fs.String("name", "Joseph Maina", "buyer name to match (as the prefix of the Books contact name)")
	doDelete := fs.Bool("delete", false, "actually delete confirmed-orphaned estimates + contacts (default: list only)")
	fs.Parse(args)

	initDB()

	estimates, err := fetchAllBooksEstimates()
	if err != nil {
		log.Fatalf("[audit-books] fetch failed: %v", err)
	}
	log.Printf("[audit-books] total estimates in Zoho Books: %d", len(estimates))

	prefix := *name + " —"
	var candidates []booksEstimateSummary
	for _, e := range estimates {
		if strings.HasPrefix(e.CustomerName, prefix) {
			candidates = append(candidates, e)
		}
	}
	log.Printf("[audit-books] %d estimate(s) with buyer name %q", len(candidates), *name)

	// Load current DB references once.
	referenced := map[string]bool{}
	rows, err := db.Query(`SELECT zoho_books_id FROM prop_bookings WHERE zoho_books_id IS NOT NULL AND zoho_books_id <> ''`)
	if err != nil {
		log.Fatalf("[audit-books] db query: %v", err)
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			referenced[id] = true
		}
	}
	rows.Close()
	srows, err := db.Query(`SELECT zoho_books_id FROM prop_sales WHERE zoho_books_id IS NOT NULL AND zoho_books_id <> ''`)
	if err == nil {
		for srows.Next() {
			var id string
			if srows.Scan(&id) == nil {
				referenced[id] = true
			}
		}
		srows.Close()
	}

	fmt.Printf("\nestimate_id,customer_id,customer_name,agent_match,db_referenced,action\n")
	orphanedCount := 0
	for _, e := range candidates {
		agentMatch := false
		persons, err := getBooksContactPersons(e.CustomerID)
		if err != nil {
			log.Printf("[audit-books] %s: fetch contact persons failed: %v", e.EstimateID, err)
		} else {
			for _, p := range persons {
				full := strings.TrimSpace(p.FirstName + " " + p.LastName)
				if strings.EqualFold(full, *name) {
					agentMatch = true
					break
				}
			}
		}

		isReferenced := referenced[e.EstimateID]
		action := "SKIP (agent name doesn't match)"
		if agentMatch {
			if isReferenced {
				action = "SKIP (still referenced by a current DB row)"
			} else {
				action = "ORPHANED — safe to delete"
				orphanedCount++
			}
		}
		fmt.Printf("%s,%s,%q,%v,%v,%s\n", e.EstimateID, e.CustomerID, e.CustomerName, agentMatch, isReferenced, action)

		if agentMatch && !isReferenced && *doDelete {
			if err := deleteBooksEstimate(e.EstimateID); err != nil {
				log.Printf("[audit-books] delete estimate %s FAILED: %v", e.EstimateID, err)
				continue
			}
			log.Printf("[audit-books] deleted estimate %s", e.EstimateID)
			if err := deleteBooksContact(e.CustomerID); err != nil {
				log.Printf("[audit-books] delete contact %s failed (non-fatal, may have other transactions): %v", e.CustomerID, err)
			} else {
				log.Printf("[audit-books] deleted contact %s", e.CustomerID)
			}
		}
	}

	log.Printf("[audit-books] finished: %d candidate(s), %d orphaned+safe%s",
		len(candidates), orphanedCount, map[bool]string{true: " (deleted)", false: " (not deleted — rerun with -delete)"}[*doDelete])
	os.Exit(0)
}
