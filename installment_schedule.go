package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// installmentRow is one payment due date extracted from a sale agreement's
// payment schedule clause.
type installmentRow struct {
	Amount  float64
	DueDate time.Time
}

// parsePageRange parses an optional "5" or "5-6" page spec (as entered on
// the SA-signed upload form) into 1-based first/last page numbers. Returns
// (0, 0) for an empty or unparseable spec, meaning "no restriction — process
// the whole document" (the original, still-supported behavior).
func parsePageRange(spec string) (first, last int) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0
	}
	if a, b, ok := strings.Cut(spec, "-"); ok {
		f, err1 := strconv.Atoi(strings.TrimSpace(a))
		l, err2 := strconv.Atoi(strings.TrimSpace(b))
		if err1 != nil || err2 != nil || f < 1 || l < f {
			return 0, 0
		}
		return f, l
	}
	p, err := strconv.Atoi(spec)
	if err != nil || p < 1 {
		return 0, 0
	}
	return p, p
}

// extractPDFText shells out to pdftotext (poppler-utils) to get the raw text
// of a sale agreement PDF. -layout preserves line structure, which the
// installment-schedule regex below relies on. first/last (both 0 for "whole
// document") restrict extraction to a page range, when the uploader knows
// which page holds the payment schedule.
func extractPDFText(path string, first, last int) (string, error) {
	args := []string{"-layout"}
	if first > 0 {
		args = append(args, "-f", strconv.Itoa(first), "-l", strconv.Itoa(last))
	}
	args = append(args, path, "-")
	out, err := exec.Command("pdftotext", args...).Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

// minDirectTextChars is the threshold below which a PDF's direct text layer
// is too short to be a real page of a legal agreement — the signal that
// it's a scan/photo with no text layer, requiring OCR instead. A page-scoped
// extraction is naturally shorter than a whole document, so the threshold is
// scaled down proportionally when a page range is given.
const minDirectTextChars = 300

// preprocessForOCR runs a rendered page image through ImageMagick to improve
// OCR accuracy: deskewing a crooked scan/photo, normalizing contrast, and a
// light sharpen. Writes to a new file rather than overwriting the original,
// so a failed preprocessing pass can safely fall back to OCR-ing the raw
// render instead of losing the page entirely.
func preprocessForOCR(path string) (string, error) {
	out := strings.TrimSuffix(path, filepath.Ext(path)) + "-clean.png"
	cmd := exec.Command("convert", path,
		"-deskew", "40%",
		"-normalize",
		"-sharpen", "0x1",
		out,
	)
	if cout, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("convert: %w: %s", err, string(cout))
	}
	return out, nil
}

// ocrPDFText rasterizes each page of a scanned PDF to a PNG (via pdftoppm)
// and runs Tesseract OCR on each page, concatenating the results. Used as a
// fallback when the PDF has no usable text layer — most sale agreements are
// printed, signed, then scanned/photographed, so this is the common path in
// practice, not an edge case. first/last (both 0 for "whole document")
// restrict rendering+OCR to a page range, which is both faster and more
// accurate than OCR-ing an entire multi-page agreement.
func ocrPDFText(path string, first, last int) (string, error) {
	tmpDir, err := os.MkdirTemp("", "sale-agreement-ocr-*")
	if err != nil {
		return "", fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")
	args := []string{"-r", "300", "-gray", "-png"}
	if first > 0 {
		args = append(args, "-f", strconv.Itoa(first), "-l", strconv.Itoa(last))
	}
	args = append(args, path, prefix)
	if out, err := exec.Command("pdftoppm", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftoppm: %w: %s", err, string(out))
	}

	pages, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return "", fmt.Errorf("glob rendered pages: %w", err)
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("pdftoppm produced no page images")
	}
	sort.Strings(pages)

	var sb strings.Builder
	for _, p := range pages {
		ocrTarget := p
		if cleaned, err := preprocessForOCR(p); err != nil {
			log.Printf("[ocr] preprocessing %s failed, OCR-ing original render instead: %v", p, err)
		} else {
			ocrTarget = cleaned
		}

		out, err := exec.Command("tesseract", ocrTarget, "stdout", "-l", "eng").Output()
		if err != nil {
			log.Printf("[ocr] tesseract failed on %s: %v", ocrTarget, err)
			continue
		}
		sb.Write(out)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// extractSaleAgreementText gets readable text from a sale agreement PDF,
// trying the direct text layer first (fast, exact) and falling back to OCR
// when that yields too little to be real content. pageRange is the optional
// "5" or "5-6" spec entered on the SA-signed upload form — when set, only
// that page (or range) is processed, which is faster and less likely to
// pick up an unrelated numbered clause elsewhere in the document. Returns
// the text and which method produced it, so callers can log which path was
// used.
func extractSaleAgreementText(path, pageRange string) (text string, method string, err error) {
	first, last := parsePageRange(pageRange)
	threshold := minDirectTextChars
	if first > 0 {
		threshold = minDirectTextChars * (last - first + 1) / 3 // a single page needs far less text than a whole agreement
	}

	direct, derr := extractPDFText(path, first, last)
	if derr == nil && len(direct) >= threshold {
		return direct, "text", nil
	}

	ocr, oerr := ocrPDFText(path, first, last)
	if oerr != nil {
		if derr != nil {
			return "", "", fmt.Errorf("text extraction failed (%v) and OCR failed (%v)", derr, oerr)
		}
		return direct, "text", fmt.Errorf("only %d chars of direct text and OCR fallback failed: %w", len(direct), oerr)
	}
	return ocr, "ocr", nil
}

// installmentLineRe matches the "Kshs X/- shall be paid on or before DATE"
// sentence used by both sale-agreement templates currently in use (M Kamuya
// Law Advocates and W Gichuhi & Company Advocates), tolerating the spacing
// variations seen between them (e.g. "67,500/-" vs "79,000/ -").
// The [a-zA-Z0-9]{0,4} after the day number (instead of a strict st/nd/rd/th
// match) absorbs OCR corruption of the ordinal suffix — observed in practice
// as "31st" -> "315t" (s misread as 5) and "30th" -> "30t" (h dropped). A
// strict suffix match caused the whole line, and the whitespace check right
// after it, to fail — silently dropping an otherwise perfectly readable row.
var installmentLineRe = regexp.MustCompile(
	`(?i)K(?:sh|es)s?\.?\s*([\d,]+)\s*/\s*-?\s*shall\s+be\s+paid\s+on\s+or\s+before\s+(\d{1,2})[a-zA-Z0-9]{0,4}\s+([A-Za-z]+)\s+(\d{4})`,
)

// paymentClauseHeadingRe matches the "Payment of the Purchase Price" section
// heading used by both known templates, tolerant of a dropped "the".
var paymentClauseHeadingRe = regexp.MustCompile(`(?i)payment\s+of\s+(?:the\s+)?purchase\s+price`)

// nextTopLevelHeadingRe matches the start of the next numbered top-level
// clause (e.g. "5. Completion" or "3. Approvals and Consents"), used to find
// where the Payment section ends. Templates differ in what that next clause
// is called, so this looks for the numbering pattern itself, not a specific
// title.
var nextTopLevelHeadingRe = regexp.MustCompile(`\n\s*\d{1,2}\.\s+[A-Z]`)

// maxPaymentSectionChars bounds the section when no next-heading match is
// found, so a mis-scan can't run away and swallow the rest of the document.
const maxPaymentSectionChars = 8000

// findPaymentClauseSection locates the "Payment of the Purchase Price"
// section within a sale agreement's full text and returns just that section
// — from its heading up to the next top-level numbered clause, or a bounded
// window if no such heading is found. This anchors extraction on the
// section by name rather than requiring a manually-specified PDF page
// number, which can vary between documents even of the same template.
func findPaymentClauseSection(text string) (string, bool) {
	loc := paymentClauseHeadingRe.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	start := loc[0]
	end := len(text)
	if m := nextTopLevelHeadingRe.FindStringIndex(text[loc[1]:]); m != nil {
		end = loc[1] + m[0]
	} else if start+maxPaymentSectionChars < len(text) {
		end = start + maxPaymentSectionChars
	}
	return text[start:end], true
}

// parseInstallmentSchedule extracts installment rows from sale agreement
// text. It's deliberately narrow — it only matches this one well-defined
// sentence pattern, so an agreement using different wording yields zero rows
// rather than a wrong guess. Callers should treat an empty result as "needs
// manual entry", not "no installments".
//
// Important: on OCR'd text, a garbled line simply fails to match and is
// silently dropped — the regex has no way to tell "this line had an
// installment that OCR mangled" from "this line was never an installment at
// all". A partial-but-plausible-looking result is a real failure mode, not
// just an edge case, so callers must not trust a non-empty result on its own
// — see verifyInstallmentTotal below, which is the actual completeness check.
func parseInstallmentSchedule(text string) []installmentRow {
	matches := installmentLineRe.FindAllStringSubmatch(text, -1)
	rows := make([]installmentRow, 0, len(matches))
	for _, m := range matches {
		amtStr := strings.ReplaceAll(m[1], ",", "")
		amt, err := strconv.ParseFloat(amtStr, 64)
		if err != nil {
			continue
		}
		dateStr := fmt.Sprintf("%s %s %s", m[2], m[3], m[4])
		due, err := time.Parse("2 January 2006", dateStr)
		if err != nil {
			continue
		}
		rows = append(rows, installmentRow{Amount: amt, DueDate: due})
	}
	return rows
}

// statedBalanceRe matches the sentence both known templates use to declare
// the total balance the installment schedule must sum to, e.g. "...the
// balance of Kenya Shillings Eight Hundred and Ten Thousand [Kes.
// 810,000/-] only ... shall be paid..." or "...balance of Kenya Shillings
// Nine Hundred and Ten Thousand Only (Kshs. 910,000/-) shall be paid as
// follows;". Used as an independent cross-check on the extracted rows, since
// a garbled OCR line silently drops a row with no other signal that it
// happened.
var statedBalanceRe = regexp.MustCompile(
	`(?i)balance\s+of[\s\S]{0,100}?Kenya\s+Shillings[\s\S]{0,120}?K(?:sh|es)s?\.?\s*([\d,]+)\s*/`,
)

// extractStatedBalance returns the balance figure the agreement itself
// declares, if that sentence can be found and read.
func extractStatedBalance(text string) (float64, bool) {
	m := statedBalanceRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	amt, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return amt, true
}

// verifyInstallmentTotal cross-checks parsed rows against the agreement's
// own stated balance. Only when both are found and match (within a 1 KES
// rounding tolerance) is the result trustworthy enough to push automatically
// — a mismatch means OCR dropped at least one row, and "balance not stated
// or unreadable" means there's no way to tell either way. Both failure cases
// must be treated the same: needs manual entry, never auto-pushed.
func verifyInstallmentTotal(text string, rows []installmentRow) (ok bool, stated float64, sum float64, statedFound bool) {
	for _, r := range rows {
		sum += r.Amount
	}
	stated, statedFound = extractStatedBalance(text)
	if !statedFound {
		return false, 0, sum, false
	}
	diff := stated - sum
	if diff < 0 {
		diff = -diff
	}
	return diff < 1.0, stated, sum, true
}

// runParseSaleAgreement is a standalone verification tool: point it at a
// sale_agreement file already on disk (either a full path, or a filename
// under uploadsDir) and it prints what would be extracted, without touching
// Zoho. Used to sanity-check the parser against real agreements before it's
// wired into the live booking flow.
func runParseSaleAgreement(args []string) {
	fs := flag.NewFlagSet("parse-sale-agreement", flag.ExitOnError)
	file := fs.String("file", "", "path to the sale_agreement PDF, or filename under UPLOADS_DIR")
	page := fs.String("page", "", "optional page or range to restrict extraction to, e.g. 5 or 5-6")
	dumpText := fs.Bool("dump-text", false, "print the full raw extracted/OCR'd text instead of just parsed rows — for diagnosing why a specific line failed to match")
	fs.Parse(args)

	if *file == "" {
		fmt.Println("usage: parse-sale-agreement -file=<path or filename> [-page=5 or -page=5-6]")
		os.Exit(1)
	}

	path := *file
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(uploadsDir, *file)
	}

	text, method, err := extractSaleAgreementText(path, *page)
	if err != nil {
		fmt.Printf("extractSaleAgreementText(%s) failed: %v\n", path, err)
		os.Exit(1)
	}

	if *dumpText {
		fmt.Printf("===== raw text (method: %s, %d chars) =====\n%s\n===== end raw text =====\n\n", method, len(text), text)
	}

	section := isolatePaymentSection(path, text)
	rows := parseInstallmentSchedule(section)
	fmt.Printf("file: %s\nextraction method: %s\nextracted %d chars of text, isolated section %d chars, %d installment row(s):\n", path, method, len(text), len(section), len(rows))
	var total float64
	for _, r := range rows {
		fmt.Printf("  %s  %.2f\n", r.DueDate.Format("2006-01-02"), r.Amount)
		total += r.Amount
	}
	fmt.Printf("total: %.2f\n", total)
	if len(rows) == 0 {
		fmt.Printf("NOTE: zero rows found%s. Needs manual entry / template review.\n", lowYieldHint(method, len(text)))
		os.Exit(0)
	}
	ok, stated, sum, statedFound := verifyInstallmentTotal(section, rows)
	if !statedFound {
		fmt.Println("VERIFY: could not find/read the agreement's stated balance — cannot confirm completeness, would NOT auto-push")
	} else if !ok {
		fmt.Printf("VERIFY: MISMATCH — extracted total %.2f does not match stated balance %.2f — likely a dropped OCR line, would NOT auto-push\n", sum, stated)
	} else {
		fmt.Printf("VERIFY: OK — extracted total matches stated balance %.2f, would push to CRM\n", stated)
	}
	os.Exit(0)
}

// createCRMInstallments bulk-creates rows in the Installment module. The
// module has three separate lookup fields to Deals (Lookup, Deals,
// Related_Deal), and neither Deals nor Related_Deal turned out to be the one
// driving the "Installments" related-list panel on a Deal's page — all three
// are set so a row shows up correctly regardless of which one Zoho's UI is
// actually keyed on. Zoho's module POST supports up to 100 records per call,
// well above any realistic installment count.
func createCRMInstallments(dealID string, rows []installmentRow) error {
	if len(rows) == 0 {
		return nil
	}
	token, err := getCRMToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	data := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		due := time.Date(r.DueDate.Year(), r.DueDate.Month(), r.DueDate.Day(), 0, 0, 0, 0, time.Local)
		data = append(data, map[string]any{
			"Due_Date":     due.Format(time.RFC3339),
			"Amount":       r.Amount,
			"Status":       "Pending",
			"Deals":        map[string]any{"id": dealID},
			"Related_Deal": map[string]any{"id": dealID},
			"Lookup":       map[string]any{"id": dealID},
		})
	}
	body, _ := json.Marshal(map[string]any{"data": data})

	req, _ := http.NewRequest("POST", zohoCRMBase+"Installment", bytes.NewReader(body))
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}

	var result struct {
		Data []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"data"`
	}
	json.Unmarshal(rb, &result)
	failed := 0
	for _, d := range result.Data {
		if d.Status != "success" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d row(s) rejected: %s", failed, len(rows), string(rb))
	}
	return nil
}

// lowYieldHint flags an OCR (or direct-text) result that's still too short
// to be a real multi-page legal agreement, suggesting a scan-quality/
// orientation problem worth checking manually rather than a wording gap.
func lowYieldHint(method string, chars int) string {
	if chars >= minDirectTextChars {
		return ""
	}
	if method == "ocr" {
		return ", OCR produced very little text — check scan quality/orientation"
	}
	return ", likely a scanned image with no text layer"
}

// isolatePaymentSection finds the "Payment of the Purchase Price" section
// within text and logs its full content, so a human can audit exactly what
// the installment parser worked from. Falls back to the full text (with a
// log line noting the heading wasn't found) rather than failing outright —
// some agreements may phrase the heading differently.
func isolatePaymentSection(label, text string) string {
	section, found := findPaymentClauseSection(text)
	if !found {
		log.Printf("[installments] %s: could not find a 'Payment of the Purchase Price' heading — using full extracted text (%d chars)", label, len(text))
		return text
	}
	log.Printf("[installments] %s: found Payment of the Purchase Price section (%d chars):\n%s", label, len(section), section)
	return section
}

// pushInstallmentsFromSaleAgreement extracts the payment schedule from the
// PDF file(s) recorded on a booking's sale_agreement field and pushes them
// into the Installment module under dealID. Non-PDF uploads are skipped
// since there's no text to extract — this is a best-effort convenience, not
// a guarantee, and callers must not treat its failure as blocking anything
// else.
func pushInstallmentsFromSaleAgreement(dealID, plotNumber, saleAgreementField, installmentPage string) {
	var combinedText strings.Builder
	var lastMethod string
	for _, f := range splitFiles(saleAgreementField) {
		if !strings.EqualFold(filepath.Ext(f), ".pdf") {
			log.Printf("[installments] plot %s: skipping non-PDF sale agreement file %s (can't extract text)", plotNumber, f)
			continue
		}
		text, method, err := extractSaleAgreementText(filepath.Join(uploadsDir, f), installmentPage)
		if err != nil {
			log.Printf("[installments] plot %s: extractSaleAgreementText(%s) failed: %v", plotNumber, f, err)
			continue
		}
		combinedText.WriteString(text)
		combinedText.WriteString("\n")
		lastMethod = method
	}
	fullText := combinedText.String()
	text := isolatePaymentSection("plot "+plotNumber, fullText)
	rows := parseInstallmentSchedule(text)

	if len(rows) == 0 {
		log.Printf("[installments] plot %s: no installment rows parsed from sale agreement (%d chars via %s%s) — needs manual entry in CRM",
			plotNumber, len(text), lastMethod, lowYieldHint(lastMethod, len(text)))
		return
	}

	ok, stated, sum, statedFound := verifyInstallmentTotal(text, rows)
	if !ok {
		if statedFound {
			log.Printf("[installments] plot %s: extracted %d row(s) totaling %.2f but agreement states balance %.2f — MISMATCH, likely a dropped OCR line, NOT pushing to CRM, needs manual entry",
				plotNumber, len(rows), sum, stated)
		} else {
			log.Printf("[installments] plot %s: extracted %d row(s) totaling %.2f but could not find/read the agreement's stated balance to verify completeness — NOT pushing to CRM, needs manual entry",
				plotNumber, len(rows), sum)
		}
		return
	}

	if err := createCRMInstallments(dealID, rows); err != nil {
		log.Printf("[installments] plot %s: push to CRM deal %s failed: %v", plotNumber, dealID, err)
		return
	}
	log.Printf("[installments] plot %s: pushed %d installment row(s) to CRM deal %s (verified against stated balance %.2f)", plotNumber, len(rows), dealID, stated)
}

// installmentLookupRef is the {id: "..."} shape Zoho returns for a
// populated lookup field, or absent/null when the field is empty.
type installmentLookupRef struct {
	ID string `json:"id"`
}

// installmentLinkRow is the subset of an Installment record needed to check
// which of its three Deal-lookup fields are actually populated.
type installmentLinkRow struct {
	ID          string                `json:"id"`
	Deals       *installmentLookupRef `json:"Deals"`
	RelatedDeal *installmentLookupRef `json:"Related_Deal"`
	Lookup      *installmentLookupRef `json:"Lookup"`
}

// runFixInstallmentLinkage is a one-off repair tool for installments created
// before createCRMInstallments was fixed to set all three Deal-lookup
// fields — Deals alone left them created but invisible in the "Installments"
// related-list panel, which turned out to be driven by a different field.
// Finds every Installment record with at least one of the three lookups set
// (so we know which deal it belongs to) but not all three, and fills in
// whichever are missing.
func runFixInstallmentLinkage(args []string) {
	fs := flag.NewFlagSet("fix-installment-linkage", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "list what would be fixed, without updating anything")
	fs.Parse(args)

	token, err := getCRMToken()
	if err != nil {
		log.Fatalf("[fix-installment-linkage] token: %v", err)
	}

	var toFix []installmentLinkRow
	page := 1
	for {
		url := fmt.Sprintf("%sInstallment?fields=Deals,Related_Deal,Lookup&per_page=200&page=%d", zohoCRMBase, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("[fix-installment-linkage] page %d: %v", page, err)
		}
		if resp.StatusCode == 204 {
			resp.Body.Close()
			break
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Fatalf("[fix-installment-linkage] page %d HTTP %d: %s", page, resp.StatusCode, string(body))
		}
		var result struct {
			Data []installmentLinkRow `json:"data"`
			Info struct {
				MoreRecords bool `json:"more_records"`
			} `json:"info"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatalf("[fix-installment-linkage] page %d decode: %v", page, err)
		}
		for _, r := range result.Data {
			complete := r.Deals != nil && r.RelatedDeal != nil && r.Lookup != nil
			hasAny := r.Deals != nil || r.RelatedDeal != nil || r.Lookup != nil
			if hasAny && !complete {
				toFix = append(toFix, r)
			}
		}
		log.Printf("[fix-installment-linkage] page %d: %d record(s) checked, %d need fixing so far", page, len(result.Data), len(toFix))
		if !result.Info.MoreRecords {
			break
		}
		page++
	}

	log.Printf("[fix-installment-linkage] found %d record(s) with at least one Deal-lookup field missing", len(toFix))

	if *dryRun {
		for _, r := range toFix {
			log.Printf("[fix-installment-linkage] DRY-RUN would complete linkage on installment %s", r.ID)
		}
		os.Exit(0)
	}

	fixed := 0
	for i := 0; i < len(toFix); i += 100 {
		end := i + 100
		if end > len(toFix) {
			end = len(toFix)
		}
		batch := toFix[i:end]
		data := make([]map[string]any, 0, len(batch))
		for _, r := range batch {
			dealID := ""
			switch {
			case r.Deals != nil:
				dealID = r.Deals.ID
			case r.RelatedDeal != nil:
				dealID = r.RelatedDeal.ID
			case r.Lookup != nil:
				dealID = r.Lookup.ID
			}
			data = append(data, map[string]any{
				"id":           r.ID,
				"Deals":        map[string]any{"id": dealID},
				"Related_Deal": map[string]any{"id": dealID},
				"Lookup":       map[string]any{"id": dealID},
			})
		}
		reqBody, _ := json.Marshal(map[string]any{"data": data})
		token, _ = getCRMToken()
		req, _ := http.NewRequest("PUT", zohoCRMBase+"Installment", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[fix-installment-linkage] batch %d-%d failed: %v", i, end, err)
			continue
		}
		rb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("[fix-installment-linkage] batch %d-%d HTTP %d: %s", i, end, resp.StatusCode, string(rb))
			continue
		}
		fixed += len(batch)
		log.Printf("[fix-installment-linkage] fixed batch %d-%d (%d records)", i, end, len(batch))
	}

	log.Printf("[fix-installment-linkage] finished: %d fixed", fixed)
	os.Exit(0)
}

// countExistingCRMInstallments returns how many Installment records already
// exist for dealID, so a backfill run never creates duplicates for a deal
// that already has a payment schedule (from live traffic or a prior run).
func countExistingCRMInstallments(dealID string) (int, error) {
	token, err := getCRMToken()
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%sInstallment/search?criteria=(((Deals:equals:%s)or(Related_Deal:equals:%s))or(Lookup:equals:%s))", zohoCRMBase, dealID, dealID, dealID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return 0, nil
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	return len(result.Data), nil
}

// runBackfillInstallments scans existing sa_signed/completed bookings that
// have both a CRM deal and an uploaded sale agreement, and pushes installment
// rows for any deal that doesn't already have some — for bookings that went
// through SA-signed before this feature existed.
func runBackfillInstallments(args []string) {
	fs := flag.NewFlagSet("backfill-installments", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "list what would be pushed, without pushing anything")
	limit := fs.Int("limit", 0, "only process the first N matching bookings (0 = no limit)")
	bookingID := fs.Int("booking-id", 0, "only process this one specific prop_bookings.id (0 = no filter)")
	pageOverride := fs.String("page", "", "override every booking's page range for this run, e.g. 3-6 (historical bookings have no stored page)")
	fs.Parse(args)

	initDB()

	query := `
		SELECT id, COALESCE(zoho_crm_id,''), COALESCE(sale_agreement,''), COALESCE(installment_page,'')
		FROM prop_bookings
		WHERE status IN ('sa_signed','completed')
		  AND zoho_crm_id IS NOT NULL AND zoho_crm_id <> ''
		  AND sale_agreement IS NOT NULL AND sale_agreement <> ''`
	var queryArgs []any
	if *bookingID > 0 {
		query += ` AND id = ?`
		queryArgs = append(queryArgs, *bookingID)
	}
	rows, err := db.Query(query, queryArgs...)
	if err != nil {
		log.Fatalf("[backfill-installments] query: %v", err)
	}
	type bookingRow struct {
		ID              int
		CRMID           string
		SaleAgreement   string
		InstallmentPage string
	}
	var list []bookingRow
	for rows.Next() {
		var r bookingRow
		if rows.Scan(&r.ID, &r.CRMID, &r.SaleAgreement, &r.InstallmentPage) == nil {
			list = append(list, r)
		}
	}
	rows.Close()

	if *limit > 0 && len(list) > *limit {
		list = list[:*limit]
	}

	log.Printf("[backfill-installments] checking %d booking(s) with a CRM deal and sale agreement", len(list))

	checked, pushed, alreadyHas, noRows, unverified, failed := 0, 0, 0, 0, 0, 0
	for _, r := range list {
		checked++

		existing, err := countExistingCRMInstallments(r.CRMID)
		if err != nil {
			failed++
			log.Printf("[backfill-installments] booking %d: deal %s check failed: %v", r.ID, r.CRMID, err)
			continue
		}
		if existing > 0 {
			alreadyHas++
			log.Printf("[backfill-installments] booking %d: deal %s already has %d installment row(s) — skipping", r.ID, r.CRMID, existing)
			continue
		}

		pageRange := r.InstallmentPage
		if *pageOverride != "" {
			pageRange = *pageOverride
		}

		var combinedText strings.Builder
		var lastMethod string
		for _, f := range splitFiles(r.SaleAgreement) {
			if !strings.EqualFold(filepath.Ext(f), ".pdf") {
				log.Printf("[backfill-installments] booking %d: skipping non-PDF sale agreement file %s", r.ID, f)
				continue
			}
			text, method, err := extractSaleAgreementText(filepath.Join(uploadsDir, f), pageRange)
			if err != nil {
				log.Printf("[backfill-installments] booking %d: extractSaleAgreementText(%s) failed: %v", r.ID, f, err)
				continue
			}
			combinedText.WriteString(text)
			combinedText.WriteString("\n")
			lastMethod = method
		}
		fullText := combinedText.String()
		text := isolatePaymentSection(fmt.Sprintf("booking %d", r.ID), fullText)
		parsed := parseInstallmentSchedule(text)

		if len(parsed) == 0 {
			noRows++
			log.Printf("[backfill-installments] booking %d: no installment rows parsed from %q (%d chars via %s%s) — needs manual entry",
				r.ID, r.SaleAgreement, len(text), lastMethod, lowYieldHint(lastMethod, len(text)))
			continue
		}

		ok, stated, sum, statedFound := verifyInstallmentTotal(text, parsed)
		if !ok {
			unverified++
			if statedFound {
				log.Printf("[backfill-installments] booking %d: extracted %d row(s) totaling %.2f but agreement states balance %.2f — MISMATCH, likely a dropped OCR line, needs manual entry",
					r.ID, len(parsed), sum, stated)
			} else {
				log.Printf("[backfill-installments] booking %d: extracted %d row(s) totaling %.2f but could not find/read the stated balance to verify — needs manual entry",
					r.ID, len(parsed), sum)
			}
			continue
		}

		if *dryRun {
			log.Printf("[backfill-installments] DRY-RUN booking %d -> deal %s: would push %d row(s), verified against stated balance %.2f", r.ID, r.CRMID, len(parsed), stated)
			for _, ir := range parsed {
				log.Printf("    %s  %.2f", ir.DueDate.Format("2006-01-02"), ir.Amount)
			}
			continue
		}

		if err := createCRMInstallments(r.CRMID, parsed); err != nil {
			failed++
			log.Printf("[backfill-installments] booking %d: push to deal %s failed: %v", r.ID, r.CRMID, err)
			continue
		}
		pushed++
		log.Printf("[backfill-installments] booking %d: pushed %d row(s) to deal %s (verified against stated balance %.2f)", r.ID, len(parsed), r.CRMID, stated)
	}

	log.Printf("[backfill-installments] finished: %d checked, %d pushed, %d already had installments, %d had zero parseable rows, %d failed verification (mismatch/unreadable balance), %d failed",
		checked, pushed, alreadyHas, noRows, unverified, failed)
	os.Exit(0)
}
