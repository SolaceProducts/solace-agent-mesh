// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

type capturedTemplateArgs struct {
	called      int
	accessToken string
	wabaID      string
	opts        whatsapp.ProbeOptions
}

func captureTemplateLookup(t *testing.T, res whatsapp.MessageTemplateListResult) *capturedTemplateArgs {
	t.Helper()
	prev := listWhatsAppMessageTemplates
	t.Cleanup(func() { listWhatsAppMessageTemplates = prev })
	args := &capturedTemplateArgs{}
	listWhatsAppMessageTemplates = func(_ context.Context, token, waba string, opts whatsapp.ProbeOptions) whatsapp.MessageTemplateListResult {
		args.called++
		args.accessToken, args.wabaID, args.opts = token, waba, opts
		return res
	}
	return args
}

func twoTemplateResult() whatsapp.MessageTemplateListResult {
	return whatsapp.MessageTemplateListResult{
		Templates: []whatsapp.MessageTemplate{
			{Name: "share_contact_request", Language: "en_US", Status: "APPROVED", Body: "Tap below.", Usable: true},
			{Name: "greetings", Language: "en", Status: "APPROVED", Body: "Hello.", Usable: false,
				Blockers: []whatsapp.TemplateBlocker{whatsapp.TemplateBlockerNoContactButton}},
		},
	}
}

func TestListWhatsAppMessageTemplates_ReturnsUsableAndBlocked(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	lookup := captureTemplateLookup(t, twoTemplateResult())

	resp, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            whatsappValues(),
	})
	if err != nil {
		t.Fatalf("ListWhatsAppMessageTemplates: %v", err)
	}
	if lookup.called != 1 {
		t.Fatalf("lookup called %d times, want 1", lookup.called)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("templates = %d, want both the usable and the blocked one", len(resp.Data))
	}
	if !resp.Data[0].Usable || resp.Data[0].Name != "share_contact_request" {
		t.Errorf("first row = %+v, want the usable template", resp.Data[0])
	}
	if resp.Data[1].Usable {
		t.Error("greetings came back usable")
	}
	if got := resp.Data[1].Blockers; len(got) != 1 || got[0] != string(whatsapp.TemplateBlockerNoContactButton) {
		t.Errorf("blockers = %v, want the no-button reason so the picker can say why", got)
	}
	if resp.Message != "" || resp.ErrorClass != "" {
		t.Errorf("a successful lookup carries no failure text, got %q / %q", resp.Message, resp.ErrorClass)
	}
}

func TestListWhatsAppMessageTemplates_TokenComesFromTheValues(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	lookup := captureTemplateLookup(t, twoTemplateResult())

	if _, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            whatsappValues(),
	}); err != nil {
		t.Fatalf("ListWhatsAppMessageTemplates: %v", err)
	}
	if lookup.accessToken != "real-stored-token" {
		t.Errorf("access token = %q, want the one from the values", lookup.accessToken)
	}
	if lookup.wabaID != "123456789" {
		t.Errorf("business account id = %q, want the requested one", lookup.wabaID)
	}
	if lookup.opts != (whatsapp.ProbeOptions{}) {
		t.Errorf("ProbeOptions = %+v, want the zero value", lookup.opts)
	}
}

func TestListWhatsAppMessageTemplates_UsesStoredSecretOnEdit(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	gatewayID := createWhatsAppGatewayForTest(t, svc, whatsappValues())
	lookup := captureTemplateLookup(t, twoTemplateResult())

	submitted := whatsappValues()
	delete(submitted, "access_token")

	if _, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		GatewayID:         gatewayID,
		Values:            submitted,
	}); err != nil {
		t.Fatalf("ListWhatsAppMessageTemplates: %v", err)
	}
	if lookup.accessToken != "real-stored-token" {
		t.Errorf("access token = %q, want the stored secret merged in behind the sentinel", lookup.accessToken)
	}
}

func TestListWhatsAppMessageTemplates_FailureCarriesTheHint(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	captureTemplateLookup(t, whatsapp.MessageTemplateListResult{
		ErrorClass:    whatsapp.ProbeClassTokenInvalid,
		Hint:          "Meta rejected the access token.",
		MetaErrorCode: 190,
	})

	resp, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            whatsappValues(),
	})
	if err != nil {
		t.Fatalf("expected the failure in the response, got an error: %v", err)
	}
	if resp.Message != "Meta rejected the access token." {
		t.Errorf("Message = %q, want the adapter's hint", resp.Message)
	}
	if resp.ErrorClass != string(whatsapp.ProbeClassTokenInvalid) {
		t.Errorf("ErrorClass = %q, want %q", resp.ErrorClass, whatsapp.ProbeClassTokenInvalid)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(b), `"data":[]`) {
		t.Errorf("failure response = %s, want an empty data array", b)
	}
}

func TestListWhatsAppMessageTemplates_RefusesPlaceholders(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	lookup := captureTemplateLookup(t, twoTemplateResult())

	values := whatsappValues()
	values["access_token"] = "<<__SAM_SECRET_WHATSAPP_TOKEN__>>"

	resp, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            values,
	})
	if err != nil {
		t.Fatalf("ListWhatsAppMessageTemplates: %v", err)
	}
	if lookup.called != 0 {
		t.Errorf("lookup called %d times, want 0 — a placeholder must not be sent as a credential", lookup.called)
	}
	if resp.ErrorClass != string(whatsapp.ProbeClassIncomplete) {
		t.Errorf("ErrorClass = %q, want %q", resp.ErrorClass, whatsapp.ProbeClassIncomplete)
	}
}

func TestListWhatsAppMessageTemplates_RefusedWhenTypeDisabled(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, false)
	lookup := captureTemplateLookup(t, twoTemplateResult())

	_, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            whatsappValues(),
	})
	if err == nil {
		t.Fatal("a disabled whatsapp type accepted a template lookup")
	}
	if lookup.called != 0 {
		t.Errorf("lookup called %d times, want 0", lookup.called)
	}
}

func TestListWhatsAppMessageTemplates_NeedsValuesOrGateway(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	captureTemplateLookup(t, twoTemplateResult())

	if _, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
	}); err == nil {
		t.Fatal("a lookup with neither values nor a gateway id was accepted")
	}
}

func TestListWhatsAppMessageTemplates_HasMore(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	res := twoTemplateResult()
	res.HasMore = true
	captureTemplateLookup(t, res)

	resp, err := svc.ListWhatsAppMessageTemplates(t.Context(), WhatsAppMessageTemplatesRequest{
		BusinessAccountID: "123456789",
		Values:            whatsappValues(),
	})
	if err != nil {
		t.Fatalf("ListWhatsAppMessageTemplates: %v", err)
	}
	if !resp.HasMore {
		t.Error("HasMore = false, want it relayed so the picker can say the list is partial")
	}
}
