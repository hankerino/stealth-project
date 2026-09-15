package main

// HubSpot sync for founding-provider leads (/sell form). Uses a HubSpot
// Service Key (Bearer token, HUBSPOT_API_KEY) scoped to
// crm.objects.contacts.read/write. Every call is best-effort and
// fire-and-forget: a HubSpot outage or missing key must never block or
// delay someone submitting the application form.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type hubspotClient struct {
	apiKey string
	http   *http.Client
}

// loadHubSpot reads HUBSPOT_API_KEY. Returns nil (a no-op client) if unset,
// so dev/local environments work without HubSpot configured.
func loadHubSpot() *hubspotClient {
	key := strings.TrimSpace(os.Getenv("HUBSPOT_API_KEY"))
	if key == "" {
		return nil
	}
	return &hubspotClient{apiKey: key, http: &http.Client{Timeout: 10 * time.Second}}
}

// upsertContact creates or updates a HubSpot Contact keyed on email via the
// batch upsert endpoint (one object), so a duplicate application never
// creates a duplicate Contact.
func (h *hubspotClient) upsertContact(ctx context.Context, email string, props map[string]string) error {
	if h == nil {
		return nil
	}
	payload := map[string]any{
		"inputs": []map[string]any{{
			"idProperty": "email",
			"id":         email,
			"properties": props,
		}},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.hubapi.com/crm/v3/objects/contacts/batch/upsert", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("hubspot upsert %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// syncProviderApplication pushes a new founding-provider lead into HubSpot
// as a Contact, tagged hqube_founding_provider=true. Fire-and-forget: runs
// in its own goroutine with its own timeout and only logs on failure.
func (h *hubspotClient) syncProviderApplication(a providerApplication) {
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		props := map[string]string{
			"email":                   a.Email,
			"firstname":               a.Name,
			"lifecyclestage":          "lead",
			"hs_lead_status":          "NEW",
			"hqube_founding_provider": "true",
			"hqube_provider_status":   a.Status,
			"hqube_gpu_inventory":     a.GPUs,
		}
		if a.Company != nil {
			props["company"] = *a.Company
		}
		if a.Location != nil {
			props["hqube_location"] = *a.Location
		}
		if a.Notes != nil {
			props["hqube_application_notes"] = *a.Notes
		}
		if err := h.upsertContact(ctx, a.Email, props); err != nil {
			log.Printf("hubspot sync (new lead) %s: %v", a.Email, err)
		}
	}()
}

// syncProviderStatus mirrors an admin's status change (Portfolio > Provider
// leads) onto the matching HubSpot Contact, so hqube_provider_status and a
// reasonable hs_lead_status/lifecyclestage never drift from our own record.
func (h *hubspotClient) syncProviderStatus(email, status string) {
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		props := map[string]string{"hqube_provider_status": status}
		switch status {
		case "CONTACTED":
			props["hs_lead_status"] = "ATTEMPTED_TO_CONTACT"
		case "ONBOARDED":
			props["hs_lead_status"] = "CONNECTED"
			props["lifecyclestage"] = "opportunity"
		case "DECLINED":
			props["hs_lead_status"] = "UNQUALIFIED"
		}
		if err := h.upsertContact(ctx, email, props); err != nil {
			log.Printf("hubspot sync (status) %s: %v", email, err)
		}
	}()
}
