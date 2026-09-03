package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// runInspectCRMFields is a one-off discovery tool: it asks Zoho CRM's own
// field-metadata API for a module's field list, so a lookup field's real API
// name (which Zoho auto-generates and often doesn't match its label) can be
// read directly from the source of truth instead of guessed at.
func runInspectCRMFields(args []string) {
	fs := flag.NewFlagSet("inspect-crm-fields", flag.ExitOnError)
	module := fs.String("module", "", "CRM module API name, e.g. Installment")
	fs.Parse(args)

	if *module == "" {
		fmt.Println("usage: inspect-crm-fields -module=<ModuleAPIName>")
		os.Exit(1)
	}

	token, err := getCRMToken()
	if err != nil {
		fmt.Printf("token error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("%ssettings/fields?module=%s", zohoCRMBase, *module)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("request error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fmt.Printf("HTTP %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var result struct {
		Fields []struct {
			APIName    string `json:"api_name"`
			FieldLabel string `json:"field_label"`
			DataType   string `json:"data_type"`
			Lookup     struct {
				Module struct {
					APIName string `json:"api_name"`
				} `json:"module"`
			} `json:"lookup"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("decode error: %v\nraw body: %s\n", err, string(body))
		os.Exit(1)
	}

	fmt.Printf("module: %s (%d fields)\n\n", *module, len(result.Fields))
	fmt.Printf("%-30s %-30s %-12s %s\n", "LABEL", "API_NAME", "TYPE", "LOOKS UP TO")
	for _, f := range result.Fields {
		lookupTarget := ""
		if f.DataType == "lookup" {
			lookupTarget = f.Lookup.Module.APIName
		}
		fmt.Printf("%-30s %-30s %-12s %s\n", f.FieldLabel, f.APIName, f.DataType, lookupTarget)
	}
	os.Exit(0)
}
