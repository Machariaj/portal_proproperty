package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// crmAttachment is the subset of a Zoho CRM Attachment record needed to
// check whether a given file has already been uploaded to a deal.
type crmAttachment struct {
	FileName string `json:"File_Name"`
}

// getCRMDealAttachments lists the filenames already attached to a deal, so
// backfilling doesn't re-upload (and duplicate) files that are already there.
func getCRMDealAttachments(dealID string) ([]string, error) {
	token, err := getCRMToken()
	if err != nil {
		return nil, err
	}
	// Zoho CRM v8 requires a "fields" param on this related-list endpoint,
	// even though File_Name is already always returned.
	url := fmt.Sprintf("%sDeals/%s/Attachments?fields=File_Name", zohoCRMBase, dealID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return nil, nil // no attachments yet
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		// Covers a deleted/nonexistent deal (INVALID_URL_PATTERN, RECORD_NOT_FOUND,
		// etc.) — surfaced as an error so the caller skips it instead of treating
		// it as "0 attachments" and trying to upload to a dead deal ID.
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Data []crmAttachment `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	names := make([]string, len(result.Data))
	for i, a := range result.Data {
		names[i] = a.FileName
	}
	return names, nil
}

// backfillRow is the subset of prop_bookings/prop_sales needed to check and
// upload missing attachments for an already-existing CRM deal.
type backfillRow struct {
	Label         string // for logging, e.g. "booking 1151" or "sale 479"
	ZohoCRMID     string
	DepositRef    string
	IDPhoto       string
	KRA           string
	PassportPhoto string
	SaleAgreement string
}

func runBackfillAttachments(args []string) {
	fs := flag.NewFlagSet("backfill-crm-attachments", flag.ExitOnError)
	statuses := fs.String("statuses", "sa_signed,completed", "comma-separated prop_bookings statuses to check")
	dryRun := fs.Bool("dry-run", false, "list what would be uploaded, without uploading anything")
	fs.Parse(args)

	initDB()

	var rows []backfillRow

	// prop_bookings
	statusList := strings.Split(*statuses, ",")
	placeholders := make([]string, len(statusList))
	args2 := make([]any, len(statusList))
	for i, s := range statusList {
		placeholders[i] = "?"
		args2[i] = strings.TrimSpace(s)
	}
	bq := fmt.Sprintf(`
		SELECT id, COALESCE(zoho_crm_id,''), COALESCE(deposit_ref,''), COALESCE(id_photo,''),
		       COALESCE(kra,''), COALESCE(passport_photo,''), COALESCE(sale_agreement,'')
		FROM prop_bookings
		WHERE status IN (%s) AND zoho_crm_id IS NOT NULL AND zoho_crm_id <> ''`, strings.Join(placeholders, ","))
	brows, err := db.Query(bq, args2...)
	if err != nil {
		log.Fatalf("[backfill] query prop_bookings: %v", err)
	}
	for brows.Next() {
		var id int
		var r backfillRow
		if err := brows.Scan(&id, &r.ZohoCRMID, &r.DepositRef, &r.IDPhoto, &r.KRA, &r.PassportPhoto, &r.SaleAgreement); err == nil {
			r.Label = fmt.Sprintf("booking %d", id)
			rows = append(rows, r)
		}
	}
	brows.Close()

	// prop_sales (legacy sales-only records that got a CRM deal via -sales-only-zoho)
	srows, err := db.Query(`
		SELECT id, COALESCE(zoho_crm_id,''), COALESCE(deposit_doc,''), COALESCE(id_doc,''),
		       COALESCE(kra_doc,''), COALESCE(passport_photo,'')
		FROM prop_sales
		WHERE zoho_crm_id IS NOT NULL AND zoho_crm_id <> ''`)
	if err != nil {
		log.Fatalf("[backfill] query prop_sales: %v", err)
	}
	for srows.Next() {
		var id int
		var r backfillRow
		if err := srows.Scan(&id, &r.ZohoCRMID, &r.DepositRef, &r.IDPhoto, &r.KRA, &r.PassportPhoto); err == nil {
			r.Label = fmt.Sprintf("sale %d", id)
			rows = append(rows, r)
		}
	}
	srows.Close()

	log.Printf("[backfill] checking %d record(s) with an existing CRM deal", len(rows))

	checked, uploaded, failed, dealMissing, missingFile := 0, 0, 0, 0, 0
	for _, r := range rows {
		local := splitFiles(r.DepositRef, r.IDPhoto, r.KRA, r.PassportPhoto, r.SaleAgreement)
		if len(local) == 0 {
			continue
		}
		checked++

		existing, err := getCRMDealAttachments(r.ZohoCRMID)
		if err != nil {
			dealMissing++
			log.Printf("[backfill] %s: deal %s unreachable (likely deleted) — skipping: %v", r.Label, r.ZohoCRMID, err)
			continue
		}
		existingSet := map[string]bool{}
		for _, f := range existing {
			existingSet[f] = true
		}

		for _, f := range local {
			if existingSet[f] {
				continue // already attached
			}
			if *dryRun {
				fullPath := filepath.Join(uploadsDir, f)
				if info, err := os.Stat(fullPath); err != nil || info.Size() == 0 {
					missingFile++
					log.Printf("[backfill] DRY-RUN %s -> deal %s: %s NOT FOUND on disk (%s)", r.Label, r.ZohoCRMID, f, fullPath)
				} else {
					log.Printf("[backfill] DRY-RUN would upload %s -> deal %s: %s (found, %d bytes)", r.Label, r.ZohoCRMID, f, info.Size())
				}
				continue
			}
			if err := uploadCRMAttachment(r.ZohoCRMID, f); err != nil {
				failed++
				log.Printf("[backfill] %s: upload %q failed: %v", r.Label, f, err)
				continue
			}
			uploaded++
			log.Printf("[backfill] %s: uploaded %q to deal %s", r.Label, f, r.ZohoCRMID)
		}
	}

	log.Printf("[backfill] finished: %d record(s) checked, %d file(s) uploaded, %d failed, %d skipped (deal not found), %d missing on disk",
		checked, uploaded, failed, dealMissing, missingFile)
	os.Exit(0)
}
