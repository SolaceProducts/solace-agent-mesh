// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func contactShare(origin, vcard string, phones ...sharedPhone) *inboundMessage {
	return &inboundMessage{
		ID:         "wamid.SHARE",
		Type:       "contacts",
		FromUserID: "US.13491208655302741918",
		Contacts:   []sharedContact{{Origin: origin, VCard: vcard, Phones: phones}},
	}
}

func TestSharedTheirOwnNumber(t *testing.T) {
	t.Parallel()

	t.Run("a button tap is their own number", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "", sharedPhone{Phone: "+1 555 123 4567", WaID: "15551234567"})
		if !sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = false, want true for a button tap")
		}
	})

	t.Run("a hand-shared card is not", func(t *testing.T) {
		msg := contactShare(contactOriginOther, "BEGIN:VCARD\nEND:VCARD", sharedPhone{WaID: "15559999999"})
		if sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = true for an origin: other card")
		}
	})

	t.Run("a button tap carrying a vcard is still a button tap", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "QkVHSU46VkNBUkQKVkVSU0lPTjozLjAKRU5EOlZDQVJE",
			sharedPhone{Phone: "+1 (613) 555-0138", Type: "CELL", WaID: "16135550138"})
		if !sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = false; a real button tap carries a vCard and is still a button tap")
		}
	})

	t.Run("a hand-shared card without a vcard is still not theirs", func(t *testing.T) {
		msg := contactShare(contactOriginOther, "", sharedPhone{WaID: "15559999999"})
		if sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = true; origin alone decides")
		}
	})

	t.Run("two different numbers are not their own number", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "",
			sharedPhone{WaID: "15551234567"}, sharedPhone{WaID: "15557654321"})
		if sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = true for a share naming two different numbers")
		}
	})

	t.Run("the same number twice is not ambiguous", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "",
			sharedPhone{WaID: "15551234567", Type: "MOBILE"}, sharedPhone{WaID: "15551234567", Type: "WORK"})
		if !sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = false for one number listed twice")
		}
	})

	t.Run("a display-formatted phone alone still counts", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "", sharedPhone{Phone: "+1 (555) 123-4567"})
		if !sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = false for a share carrying only a display number")
		}
	})

	t.Run("a contacts block on another message type is ignored", func(t *testing.T) {
		msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15551234567"})
		msg.Type = "text"
		if sharedTheirOwnNumber(msg) {
			t.Error("sharedTheirOwnNumber = true for a non-contacts message")
		}
	})

	t.Run("nil and empty are safe", func(t *testing.T) {
		if sharedTheirOwnNumber(nil) {
			t.Error("sharedTheirOwnNumber(nil) = true")
		}
		if sharedTheirOwnNumber(&inboundMessage{Type: "contacts"}) {
			t.Error("sharedTheirOwnNumber(no contacts) = true")
		}
	})
}

func TestSenderPhonePrefersTheProvider(t *testing.T) {
	t.Parallel()

	msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15559999999"})
	msg.From = "15551234567"
	value := &webhookChangeValue{
		Contacts: []webhookContact{{WaID: "15551234567", UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	got, exact := senderPhone(value, msg)
	if got != "15551234567" {
		t.Errorf("senderPhone = %q, want the provider's wa_id — a share must never override it", got)
	}
	if !exact {
		t.Error("senderPhone exact = false, want true for a contact matched on wa_id")
	}
}

func TestSenderPhoneNeverReadsTheShareBody(t *testing.T) {
	t.Parallel()

	msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15551234567"})
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918", Profile: struct {
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Name: "Ada", Username: "ada"}}},
		Messages: []inboundMessage{*msg},
	}
	got, exact := senderPhone(value, msg)
	if got != "" {
		t.Errorf("senderPhone = %q, want empty — there is no contacts[].wa_id and the share body is not a fallback", got)
	}
	if exact {
		t.Error("senderPhone exact = true with no value")
	}
}

func TestResolveIngressIdentityIgnoresTheShareBody(t *testing.T) {
	t.Parallel()

	a := &Adapter{cfg: &WhatsAppConfig{IdentityLookupKey: IdentityLookupPhone}}
	msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15551234567"})
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	id := a.resolveIngressIdentity(value, msg)
	if id.LookupValue != "" {
		t.Errorf("LookupValue = %q, want empty — the share body must not reach the claim match", id.LookupValue)
	}
	if id.ContinuityToken != "US.13491208655302741918" {
		t.Errorf("ContinuityToken = %q, want the BSUID", id.ContinuityToken)
	}
}

func TestResolveIngressIdentityIgnoresAShareUnderAUsernameLookup(t *testing.T) {
	t.Parallel()

	a := &Adapter{cfg: &WhatsAppConfig{IdentityLookupKey: IdentityLookupWAUsername}}
	msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15551234567"})
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	if id := a.resolveIngressIdentity(value, msg); id.LookupValue != "" {
		t.Errorf("LookupValue = %q, want \"\" — a phone cannot answer a username lookup", id.LookupValue)
	}
}

func TestClientSendTemplate(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = nil
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.TMPL"}]}`))
	}))
	defer srv.Close()

	c := NewClient(&WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111",
		GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Run("a BSUID uses recipient", func(t *testing.T) {
		id, err := c.SendTemplate(t.Context(), "US.13491208655302741918", "request_contact_info_1", "en_US")
		if err != nil {
			t.Fatalf("SendTemplate: %v", err)
		}
		if id != "wamid.TMPL" {
			t.Errorf("wamid = %q, want wamid.TMPL", id)
		}
		if got, _ := gotBody["recipient"].(string); got != "US.13491208655302741918" {
			t.Errorf("recipient = %q, want the BSUID", got)
		}
		if _, present := gotBody["to"]; present {
			t.Error(`body carries "to" as well as "recipient"; Meta accepts exactly one`)
		}
		tmpl, _ := gotBody["template"].(map[string]any)
		if name, _ := tmpl["name"].(string); name != "request_contact_info_1" {
			t.Errorf("template.name = %q", name)
		}
		lang, _ := tmpl["language"].(map[string]any)
		if code, _ := lang["code"].(string); code != "en_US" {
			t.Errorf("template.language.code = %q, want en_US", code)
		}
		if _, present := tmpl["components"]; present {
			t.Error("template carries components; an unparameterized template must send none")
		}
	})

	t.Run("a phone number uses to", func(t *testing.T) {
		if _, err := c.SendTemplate(t.Context(), "15551234567", "request_contact_info_1", "en_US"); err != nil {
			t.Fatalf("SendTemplate: %v", err)
		}
		if got, _ := gotBody["to"].(string); got != "15551234567" {
			t.Errorf("to = %q, want the phone number", got)
		}
		if _, present := gotBody["recipient"]; present {
			t.Error(`body carries "recipient" for a phone number`)
		}
	})
}

func TestIsBSUID(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":                        false,
		"15551234567":             false,
		"US.13491208655302741918": true,
		"BR.99887766554433221100": true,
	}
	for in, want := range cases {
		if got := isBSUID(in); got != want {
			t.Errorf("isBSUID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSendPromptShapeTemplate(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.TMPL"}]}`))
	}))
	defer srv.Close()

	a := &Adapter{client: NewClient(&WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111",
		GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))}

	wamid, err := a.sendPromptShape(t.Context(), "US.1349", "", &promptPayload{
		Mode:     promptModeTemplate,
		Template: &templateRef{Name: "request_contact_info_1", Language: "en_US"},
	})
	if err != nil || wamid != "wamid.TMPL" {
		t.Fatalf("sendPromptShape = %q, %v", wamid, err)
	}
	if gotBody["type"] != "template" {
		t.Errorf("type = %v, want template", gotBody["type"])
	}

	_, err = a.sendPromptShape(t.Context(), "US.1349", "", &promptPayload{Mode: promptModeTemplate})
	if err == nil || !strings.Contains(err.Error(), "names no template") {
		t.Errorf("a template row naming no template returned %v, want a terminal error", err)
	}
}

func TestSendableOutsideWindow(t *testing.T) {
	t.Parallel()

	if !sendableOutsideWindow(&outboundPayload{Prompt: &promptPayload{Mode: promptModeTemplate}}) {
		t.Error("a template is not sendable outside the window; it is the only shape that is")
	}
	for _, mode := range []string{promptModeText, promptModeButtons, promptModeList, promptModeCTAURL} {
		if sendableOutsideWindow(&outboundPayload{Prompt: &promptPayload{Mode: mode}}) {
			t.Errorf("mode %q reported sendable outside the window", mode)
		}
	}
	if sendableOutsideWindow(&outboundPayload{Text: "hello"}) {
		t.Error("a plain text reply reported sendable outside the window")
	}
}

func TestPromptAuditType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		promptModeTemplate: "template",
		promptModeText:     "text",
		promptModeCTAURL:   "interactive",
		promptModeButtons:  "interactive",
		"":                 "interactive",
	}
	for mode, want := range cases {
		if got := promptAuditType(mode); got != want {
			t.Errorf("promptAuditType(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestParseContactRequestConfig(t *testing.T) {
	t.Parallel()

	t.Run("a template defaults its language", func(t *testing.T) {
		values := identityCfgBase()
		values["contact_request_template"] = " request_contact_info_1 "
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.ContactRequestTemplate != "request_contact_info_1" {
			t.Errorf("ContactRequestTemplate = %q", cfg.ContactRequestTemplate)
		}
		if cfg.ContactRequestTemplateLanguage != defaultTemplateLanguage {
			t.Errorf("ContactRequestTemplateLanguage = %q, want %q", cfg.ContactRequestTemplateLanguage, defaultTemplateLanguage)
		}
	})

	t.Run("an explicit language is kept", func(t *testing.T) {
		values := identityCfgBase()
		values["contact_request_template"] = "request_contact_info_1"
		values["contact_request_template_language"] = "pt_BR"
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.ContactRequestTemplateLanguage != "pt_BR" {
			t.Errorf("ContactRequestTemplateLanguage = %q, want pt_BR", cfg.ContactRequestTemplateLanguage)
		}
	})

	t.Run("neither key leaves the offer off", func(t *testing.T) {
		cfg, err := ParseWhatsAppConfig(identityCfgBase())
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.ContactRequestTemplate != "" || cfg.ContactRequestTemplateLanguage != "" {
			t.Errorf("unconfigured entrypoint has template %q/%q, want both empty",
				cfg.ContactRequestTemplate, cfg.ContactRequestTemplateLanguage)
		}
	})

	t.Run("a language alone is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["contact_request_template_language"] = "en_US"
		_, err := ParseWhatsAppConfig(values)
		if err == nil || !strings.Contains(err.Error(), "contact_request_template_language is set without") {
			t.Errorf("error = %v, want a refusal naming the missing template", err)
		}
	})

	t.Run("a username lookup is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = IdentityLookupWAUsername
		values["identity_claim_key"] = "extension_deadbeef_waUsername"
		values["contact_request_template"] = "request_contact_info_1"
		_, err := ParseWhatsAppConfig(values)
		if err == nil || !strings.Contains(err.Error(), "cannot answer") {
			t.Errorf("error = %v, want a refusal — a shared phone cannot answer a username lookup", err)
		}
	})

	t.Run("run_as is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["run_as"] = "ops"
		values["contact_request_template"] = "request_contact_info_1"
		_, err := ParseWhatsAppConfig(values)
		if err == nil || !strings.Contains(err.Error(), "run_as") {
			t.Errorf("error = %v, want a refusal naming run_as", err)
		}
	})

	t.Run("a non-string template is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["contact_request_template"] = []string{"request_contact_info_1"}
		_, err := ParseWhatsAppConfig(values)
		if err == nil || !strings.Contains(err.Error(), "must be a string") {
			t.Errorf("error = %v, want a refusal naming the type", err)
		}
	})
}

func TestPendingAuthKindMapping(t *testing.T) {
	t.Parallel()
	if got := pendingAuthKind(promptKindContactRequest); got != "consent" {
		t.Errorf("pendingAuthKind(contact_request) = %q, want consent", got)
	}
	if got := pendingAuthKind(promptKindLogin); got != "login" {
		t.Errorf("pendingAuthKind(login) = %q, want login", got)
	}
}

func TestIsIdentityPrompt(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{promptKindLogin, promptKindContactRequest} {
		if !isIdentityPrompt(kind) {
			t.Errorf("isIdentityPrompt(%q) = false", kind)
		}
	}
	for _, kind := range []string{promptKindHIL, promptKindAuth, ""} {
		if isIdentityPrompt(kind) {
			t.Errorf("isIdentityPrompt(%q) = true — only prompts with no task behind them are suppressed by the identity record", kind)
		}
	}
}

func withheldNumberMessage(wamid, body string) (*webhookChangeValue, *inboundMessage) {
	msg := &inboundMessage{FromUserID: "US.13491208655302741918", ID: wamid, Type: "text"}
	msg.Text.Body = body
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	value.Contacts[0].Profile.Name = "Sheena"
	value.Contacts[0].Profile.Username = "realsheenanelson"
	return value, msg
}

func shareEnvelope(msg *inboundMessage) *webhookChangeValue {
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: msg.FromUserID}},
		Messages: []inboundMessage{*msg},
	}
	value.Contacts[0].Profile.Username = "realsheenanelson"
	return value
}

func TestWithheldNumberSenderIsOfferedTheContactShare(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
		c.ContactRequestTemplateLanguage = "en_US"
	})

	value, msg := withheldNumberMessage("wamid.C1", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	if stub.calls != 0 {
		t.Errorf("platform lookups = %d, want 0 — there was no value to look up", stub.calls)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound Graph calls = %d, want exactly the contact-request template", len(raw))
	}
	body := string(raw[0])
	if !strings.Contains(body, `"type":"template"`) {
		t.Errorf("the offer is not a template send: %s", body)
	}
	if !strings.Contains(body, "request_contact_info_1") {
		t.Errorf("the template named is not the configured one: %s", body)
	}
	if !strings.Contains(body, `"recipient":"US.13491208655302741918"`) {
		t.Errorf("a BSUID recipient must travel in \"recipient\": %s", body)
	}
}

func TestWithheldNumberSenderWithoutATemplateStillGetsTold(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := withheldNumberMessage("wamid.C2", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound Graph calls = %d, want the dead-end notice", len(raw))
	}
	body := string(raw[0])
	if strings.Contains(body, "template") {
		t.Errorf("a template was sent with none configured: %s", body)
	}
	if !strings.Contains(body, "does not share a phone number") {
		t.Errorf("notice = %s, want it to name the missing number", body)
	}
}

func TestSecondWithheldMessageGetsAReminderNotASecondTemplate(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
		c.ContactRequestTemplateLanguage = "en_US"
	})

	first, firstMsg := withheldNumberMessage("wamid.C3", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	second, secondMsg := withheldNumberMessage("wamid.C4", "still there?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}

	raw := mock.rawSends()
	if len(raw) != 2 {
		t.Fatalf("sends = %d, want the template plus one reminder", len(raw))
	}
	if strings.Contains(string(raw[1]), `"type":"template"`) {
		t.Errorf("the follow-up got a second billable template: %s", raw[1])
	}
	if !strings.Contains(string(raw[1]), "phone number has not been shared yet") {
		t.Errorf("reminder = %s, want it to point back at the button", raw[1])
	}
}

func TestTappedShareIsAcknowledgedAndResolvesOnTheNextMessage(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{
		Resolution: identity.LookupMatched, ResolvedUserID: "user-uuid",
	}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
	})

	share := contactShare(contactOriginRequest, "", sharedPhone{Phone: "+1 555 123 4567", WaID: "15551234567"})
	if got := a.deliver(shareEnvelope(share), share); got != settleAck {
		t.Fatalf("share settlement = %v, want an ack", got)
	}
	if stub.calls != 0 {
		t.Errorf("platform lookups on the share = %d, want 0 — the share body is not an identity", stub.calls)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("sends after the share = %d, want one acknowledgement", len(raw))
	}
	if body := string(raw[0]); !strings.Contains(body, "phone number has been received") {
		t.Errorf("reply = %s, want the share acknowledged", body)
	}

	value, msg := textMessage("wamid.AFTERSHARE", "15551234567", "what is the weather")
	value.Contacts[0].UserID = "US.13491208655302741918"
	msg.FromUserID = "US.13491208655302741918"
	value.Messages[0] = *msg
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("follow-up settlement = %v, want an ack", got)
	}
	if stub.last.ClaimValue != "15551234567" {
		t.Errorf("claim value = %q, want the disclosed number", stub.last.ClaimValue)
	}
	if stub.last.ClaimKey != defaultPhoneClaimKey {
		t.Errorf("claim key = %q, want %q", stub.last.ClaimKey, defaultPhoneClaimKey)
	}
	if stub.last.ContinuityToken != "US.13491208655302741918" {
		t.Errorf("continuity token = %q, want the BSUID", stub.last.ContinuityToken)
	}
}

func TestHandSharedCardNeverReachesTheLookup(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
	})

	msg := contactShare(contactOriginOther, "BEGIN:VCARD\nFN:Ada\nEND:VCARD",
		sharedPhone{Phone: "+1 555 765 4321", WaID: "15557654321"})
	if got := a.deliver(shareEnvelope(msg), msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	if stub.calls != 0 {
		t.Fatalf("platform lookups = %d, want 0 — a hand-shared card identifies nobody", stub.calls)
	}
	if stub.last.ClaimValue == "15557654321" {
		t.Fatal("a hand-shared number was matched against the claim; that is impersonation")
	}
}

func TestReminderLogNamesWhatIsBeingWaitedOn(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
	})

	first, firstMsg := withheldNumberMessage("wamid.C5", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	second, secondMsg := withheldNumberMessage("wamid.C6", "still there?")
	recs := captureLogs(t, a, func() {
		if got := a.deliver(second, secondMsg); got != settleAck {
			t.Fatalf("second settlement = %v, want an ack", got)
		}
	})
	if findLog(recs, "contact-share request") == nil {
		t.Errorf("no log line naming the contact-share wait; got %v", recs)
	}
	if findLog(recs, "sign-in link") != nil {
		t.Error("the contact-share reminder logged as waiting on a sign-in, which points at the wrong half of the system")
	}
}

func TestDeadEndNamesTheMissingTemplate(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := withheldNumberMessage("wamid.C7", "hello")
	recs := captureLogs(t, a, func() {
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("settlement = %v, want an ack", got)
		}
	})
	if findLog(recs, "no contact_request_template is configured") == nil {
		t.Errorf("no log line naming the unset key; an operator has nothing to act on. got %v", recs)
	}
	if findLog(recs, "asked to sign in") != nil {
		t.Error("the dead end logged as a sign-in offer; nobody was asked to sign in and no claim was looked up")
	}
}

func TestStartupWarnsWhenWithheldNumbersDeadEnd(t *testing.T) {
	const want = "cannot be identified and will be turned away"

	t.Run("a phone lookup with no template warns", func(t *testing.T) {
		mock := newWAMock(t)
		a := authorizingRig(t, mock, &stubAuthorizer{}, nil)
		recs := captureLogs(t, a, a.warnIfWithheldNumbersDeadEnd)
		if findLog(recs, want) == nil {
			t.Errorf("no startup warning; got %v", recs)
		}
	})

	t.Run("a configured template is silent", func(t *testing.T) {
		mock := newWAMock(t)
		a := authorizingRig(t, mock, &stubAuthorizer{}, func(c *WhatsAppConfig) {
			c.ContactRequestTemplate = "request_contact_info_1"
		})
		recs := captureLogs(t, a, a.warnIfWithheldNumbersDeadEnd)
		if findLog(recs, want) != nil {
			t.Error("warned with a template configured")
		}
	})

	t.Run("a username lookup and run_as are silent", func(t *testing.T) {
		for name, tune := range map[string]func(*WhatsAppConfig){
			"username": func(c *WhatsAppConfig) {
				c.IdentityLookupKey = IdentityLookupWAUsername
				c.IdentityClaimKey = "extension_deadbeef_waUsername"
			},
			"run_as": func(c *WhatsAppConfig) { c.RunAs = "ops" },
		} {
			t.Run(name, func(t *testing.T) {
				mock := newWAMock(t)
				a := authorizingRig(t, mock, &stubAuthorizer{}, tune)
				recs := captureLogs(t, a, a.warnIfWithheldNumbersDeadEnd)
				if findLog(recs, want) != nil {
					t.Errorf("%s warned about a remedy it does not need", name)
				}
			})
		}
	})
}

func TestGoldenContactRequestPayload(t *testing.T) {
	t.Parallel()

	const body = `{
	  "object": "whatsapp_business_account",
	  "entry": [{
	    "id": "WABA1",
	    "changes": [{
	      "field": "messages",
	      "value": {
	        "messaging_product": "whatsapp",
	        "metadata": {"display_phone_number": "+1 555 010 0000", "phone_number_id": "555111"},
	        "contacts": [{
	          "profile": {"name": "Sheena Nelson", "username": "realsheenanelson"},
	          "user_id": "US.13491208655302741918"
	        }],
	        "messages": [{
	          "id": "wamid.MOCKIN000001",
	          "timestamp": "1789419331",
	          "type": "contacts",
	          "from_user_id": "US.13491208655302741918",
	          "contacts": [{
	            "name": {"first_name": "Sheena Nelson", "formatted_name": "Sheena Nelson"},
	            "phones": [{"phone": "+15551234567", "type": "MOBILE", "wa_id": "15551234567"}],
	            "origin": "contact_request"
	          }]
	        }]
	      }
	    }]
	  }]
	}`

	var env webhookEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	value := &env.Entry[0].Changes[0].Value
	msg := &value.Messages[0]

	if msg.From != "" || value.Contacts[0].WaID != "" {
		t.Fatalf("payload discloses a number (from=%q wa_id=%q); it is not the withheld case",
			msg.From, value.Contacts[0].WaID)
	}
	if got := msg.senderID(); got != "US.13491208655302741918" {
		t.Errorf("senderID = %q, want the BSUID", got)
	}
	if !sharedTheirOwnNumber(msg) {
		t.Error("sharedTheirOwnNumber = false on a real contact_request payload; a tag or nesting level has drifted")
	}

	a := &Adapter{cfg: &WhatsAppConfig{IdentityLookupKey: IdentityLookupPhone}}
	id := a.resolveIngressIdentity(value, msg)
	if id.LookupValue != "" {
		t.Errorf("LookupValue = %q, want empty — the share body must never reach the claim match", id.LookupValue)
	}
	if id.ContinuityToken != "US.13491208655302741918" {
		t.Errorf("ContinuityToken = %q, want the BSUID", id.ContinuityToken)
	}
}

func TestRefusedTemplateTellsTheSenderAndSuppressesNothing(t *testing.T) {
	mock := newWAMock(t)
	mock.rejectTemplate = true
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "not_approved"
		c.ContactRequestTemplateLanguage = "en_US"
	})

	first, firstMsg := withheldNumberMessage("wamid.R1", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}

	delivered := mock.waitDelivered(1, "a refused template must not leave silence")
	if len(delivered) != 1 {
		t.Fatalf("delivered sends = %d, want only the dead-end notice", len(delivered))
	}
	if !strings.Contains(string(delivered[0]), "does not share a phone number") {
		t.Errorf("notice = %s, want the dead-end wording", delivered[0])
	}
	if strings.Contains(string(delivered[0]), "earlier message") {
		t.Errorf("the sender was pointed at a button that was never delivered: %s", delivered[0])
	}

	if a.pendingAuth != nil {
		row, err := a.pendingAuth.Live(t.Context(), a.gatewayID(), firstMsg.senderID(), a.nowMs())
		if err != nil {
			t.Fatalf("Live: %v", err)
		}
		if row != nil {
			t.Errorf("a suppression record was written for a template that was refused (kind=%q)", row.Kind)
		}
	}

	second, secondMsg := withheldNumberMessage("wamid.R2", "still there?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}
	var templateAttempts int
	for _, body := range mock.attempts() {
		if strings.Contains(string(body), `"type":"template"`) {
			templateAttempts++
		}
	}
	if templateAttempts != 2 {
		t.Errorf("template attempts = %d, want 2 — a refused template must not suppress the retry", templateAttempts)
	}
}

func TestRefusedTemplateLogNamesTheConfigKeys(t *testing.T) {
	mock := newWAMock(t)
	mock.rejectTemplate = true
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "not_approved"
		c.ContactRequestTemplateLanguage = "pt_BR"
	})

	value, msg := withheldNumberMessage("wamid.R3", "hello")
	recs := captureLogs(t, a, func() {
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("settlement = %v, want an ack", got)
		}
		mock.waitDelivered(1, "the refusal notice never reached the sender")
	})
	rec := findLog(recs, "contact-share request undeliverable")
	if rec == nil {
		t.Fatalf("no log line for the refused template; got %v", recs)
	}
	if lvl, _ := rec["level"].(string); lvl != "ERROR" {
		t.Errorf("level = %q, want ERROR — an operator has to go and change something", lvl)
	}
	if got, _ := rec["template"].(string); got != "not_approved" {
		t.Errorf("template field = %q, want the configured name", got)
	}
	if got, _ := rec["language"].(string); got != "pt_BR" {
		t.Errorf("language field = %q, want the configured language", got)
	}
	if hint, _ := rec["hint"].(string); !strings.Contains(hint, "contact_request_template") {
		t.Errorf("hint = %q, want it to name the config key", hint)
	}
}

func TestShareWebhookReadsTheContextContactNotTheBody(t *testing.T) {
	t.Parallel()

	const bsuid = "US.13491208655302741918"
	const contextNumber = "16135550138"
	const bodyNumber = "15559999999"

	msg := contactShare(contactOriginRequest, "", sharedPhone{WaID: bodyNumber, Phone: "+1 555 999 9999"})
	msg.FromUserID = bsuid
	if msg.From != "" {
		t.Fatalf("fixture has from=%q; a withheld-number sender arrives with none", msg.From)
	}

	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: bsuid, WaID: contextNumber}},
		Messages: []inboundMessage{*msg},
	}

	got, exact := senderPhone(value, msg)
	if got == bodyNumber {
		t.Fatalf("senderPhone read the number out of the message body; it must come from contacts[].wa_id, which is Meta stating who sent this message")
	}
	if got != contextNumber {
		t.Fatalf("senderPhone = %q, want %q from contacts[].wa_id", got, contextNumber)
	}
	if !exact {
		t.Error("senderPhone exact = false; the contact was paired to this message on its BSUID, which is certain")
	}

	a := &Adapter{cfg: &WhatsAppConfig{IdentityLookupKey: IdentityLookupPhone}}
	if id := a.resolveIngressIdentity(value, msg); id.LookupValue != contextNumber {
		t.Errorf("LookupValue = %q, want %q — the body must not reach the lookup while the context contact is populated", id.LookupValue, contextNumber)
	}
}

func TestHandSharedCardTakesTheUnsupportedTypeReply(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, nil)

	card := contactShare(contactOriginOther, "BEGIN:VCARD\nEND:VCARD", sharedPhone{WaID: "15559999999"})
	value := shareEnvelope(card)
	value.Contacts[0].WaID = "15551234567"
	if got := a.deliver(value, card); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}

	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("sends = %d, want one reply", len(raw))
	}
	reply := string(raw[0])
	if !strings.Contains(reply, "File type not supported") {
		t.Errorf("reply = %s, want the generic unsupported-type copy", reply)
	}
	if strings.Contains(reply, "phone number has been received") {
		t.Error("a hand-shared card was acknowledged as the sender's own number")
	}
}

func TestShareBeforeDisclosureIsAcknowledgedNotReminded(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.ContactRequestTemplate = "request_contact_info_1"
	})

	first, firstMsg := withheldNumberMessage("wamid.PRE1", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}

	share := contactShare(contactOriginRequest, "", sharedPhone{WaID: "15551234567"})
	if got := a.deliver(shareEnvelope(share), share); got != settleAck {
		t.Fatalf("share settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 2 {
		t.Fatalf("sends = %d, want the template plus one acknowledgement", len(raw))
	}
	reply := string(raw[1])
	if strings.Contains(reply, "Tap the button") || strings.Contains(reply, "has not been shared yet") {
		t.Errorf("a just-tapped share got the reminder: %s", reply)
	}
	if !strings.Contains(reply, "phone number has been received") {
		t.Errorf("reply = %s, want the share acknowledged", reply)
	}
	if strings.Contains(reply, `"type":"template"`) {
		t.Error("a just-tapped share drew a second billable template")
	}
}
