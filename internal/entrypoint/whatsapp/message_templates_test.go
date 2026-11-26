// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const graphTemplateListFixture = `{
  "data": [
    {
      "name": "request_contact_info_3",
      "parameter_format": "POSITIONAL",
      "components": [
        {
          "type": "BODY",
          "text": "Hi {{1}}, having your phone number helps us serve you faster. Tap below to share your contact info.",
          "example": {"body_text": [["John"]]}
        },
        {
          "type": "BUTTONS",
          "buttons": [{"type": "REQUEST_CONTACT_INFO", "text": "Share Contact Info"}]
        }
      ],
      "language": "en_US",
      "status": "APPROVED",
      "category": "UTILITY",
      "library_template_name": "request_contact_info_3",
      "id": "1772111900608818"
    },
    {
      "name": "share_contact_request",
      "parameter_format": "POSITIONAL",
      "components": [
        {"type": "BODY", "text": "I can't tell who you are yet. Tap the button below to share your number."},
        {"type": "BUTTONS", "buttons": [{"type": "REQUEST_CONTACT_INFO", "text": "Share Contact Info"}]}
      ],
      "language": "en_US",
      "status": "APPROVED",
      "category": "UTILITY",
      "library_template_name": "request_contact_info_1",
      "id": "1403166401262580"
    },
    {
      "name": "request_contact_info_2",
      "parameter_format": "POSITIONAL",
      "components": [
        {"type": "BODY", "text": "I can't tell who you are yet. Tap the button below to share your number."},
        {"type": "BUTTONS", "buttons": [{"type": "REQUEST_CONTACT_INFO", "text": "Share Contact Info"}]}
      ],
      "language": "en_US",
      "status": "APPROVED",
      "category": "UTILITY",
      "library_template_name": "request_contact_info_2",
      "id": "1162456973105437"
    },
    {
      "name": "greetings",
      "parameter_format": "POSITIONAL",
      "components": [{"type": "BODY", "text": "Hello, it is really nice to meet you, how are you today?"}],
      "language": "en",
      "status": "APPROVED",
      "category": "MARKETING",
      "sub_category": "CUSTOM",
      "id": "2115160679372533"
    }
  ],
  "paging": {"cursors": {"before": "QVFIVGZA", "after": "QVFIVDQx"}}
}`

func templateServer(t *testing.T, status int, body string, capture func(*http.Request)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func templateByName(t *testing.T, rows []MessageTemplate, name string) MessageTemplate {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("template %q not in the result", name)
	return MessageTemplate{}
}

func TestListMessageTemplates_ReadsTheRealPayload(t *testing.T) {
	t.Parallel()
	var gotPath, gotAuth, gotQuery string
	base := templateServer(t, http.StatusOK, graphTemplateListFixture, func(r *http.Request) {
		gotPath, gotAuth, gotQuery = r.URL.Path, r.Header.Get("Authorization"), r.URL.RawQuery
	})

	res := ListMessageTemplates(t.Context(), "tok-abc", "123456789", ProbeOptions{BaseURL: base})
	if res.ErrorClass != "" || res.Hint != "" {
		t.Fatalf("lookup failed: class %q, hint %q", res.ErrorClass, res.Hint)
	}
	if len(res.Templates) != 4 {
		t.Fatalf("templates = %d, want all 4 including the unusable ones", len(res.Templates))
	}

	if want := "/" + defaultGraphAPIVersion + "/123456789/message_templates"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
	if strings.Contains(gotQuery, "access_token") || strings.Contains(gotQuery, "tok-abc") {
		t.Errorf("query = %q carries the access token", gotQuery)
	}

	for _, name := range []string{"share_contact_request", "request_contact_info_2"} {
		row := templateByName(t, res.Templates, name)
		if !row.Usable {
			t.Errorf("%s usable = false, blockers %v, want usable", name, row.Blockers)
		}
	}

	withVar := templateByName(t, res.Templates, "request_contact_info_3")
	if withVar.Usable {
		t.Error("request_contact_info_3 is usable, but its body carries {{1}} and this entrypoint sends no components")
	}
	if len(withVar.Blockers) != 1 || withVar.Blockers[0] != TemplateBlockerHasVariables {
		t.Errorf("blockers = %v, want exactly [%s]", withVar.Blockers, TemplateBlockerHasVariables)
	}

	noButton := templateByName(t, res.Templates, "greetings")
	if noButton.Usable {
		t.Error("greetings is usable, but it has no Share Contact Info button")
	}
	if len(noButton.Blockers) != 1 || noButton.Blockers[0] != TemplateBlockerNoContactButton {
		t.Errorf("blockers = %v, want exactly [%s]", noButton.Blockers, TemplateBlockerNoContactButton)
	}

	if got := templateByName(t, res.Templates, "greetings").Language; got != "en" {
		t.Errorf("greetings language = %q, want en — not the en_US the others carry", got)
	}
	if res.HasMore {
		t.Error("HasMore = true; the fixture's paging carries cursors but no next page")
	}
}

func TestListMessageTemplates_UsableFirst(t *testing.T) {
	t.Parallel()
	base := templateServer(t, http.StatusOK, graphTemplateListFixture, nil)
	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})

	var sawUnusable bool
	for _, row := range res.Templates {
		if !row.Usable {
			sawUnusable = true
			continue
		}
		if sawUnusable {
			t.Fatalf("usable template %q appears after an unusable one", row.Name)
		}
	}
	if res.Templates[0].Name != "request_contact_info_2" {
		t.Errorf("first row = %q, want the alphabetically first usable template", res.Templates[0].Name)
	}
}

func TestListMessageTemplates_Blockers(t *testing.T) {
	t.Parallel()
	const body = `{"data":[
		{"name":"pending","language":"en_US","status":"PENDING","components":[
			{"type":"BODY","text":"no variables here"},
			{"type":"BUTTONS","buttons":[{"type":"REQUEST_CONTACT_INFO","text":"Share Contact Info"}]}]},
		{"name":"header_var","language":"en_US","status":"APPROVED","components":[
			{"type":"HEADER","text":"Hello {{1}}"},
			{"type":"BODY","text":"no variables here"},
			{"type":"BUTTONS","buttons":[{"type":"REQUEST_CONTACT_INFO","text":"Share Contact Info"}]}]},
		{"name":"url_var","language":"en_US","status":"APPROVED","components":[
			{"type":"BODY","text":"plain"},
			{"type":"BUTTONS","buttons":[
				{"type":"REQUEST_CONTACT_INFO","text":"Share Contact Info"},
				{"type":"URL","text":"Open","url":"https://example.test/{{1}}"}]}]},
		{"name":"named_var","language":"en_US","status":"APPROVED","components":[
			{"type":"BODY","text":"Hi {{customer_name}}"},
			{"type":"BUTTONS","buttons":[{"type":"REQUEST_CONTACT_INFO","text":"Share Contact Info"}]}]},
		{"name":"hopeless","language":"en_US","status":"REJECTED","components":[
			{"type":"BODY","text":"Hi {{1}}"}]}
	]}`
	base := templateServer(t, http.StatusOK, body, nil)
	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})

	for _, tc := range []struct {
		name string
		want []TemplateBlocker
	}{
		{"pending", []TemplateBlocker{TemplateBlockerNotApproved}},
		{"header_var", []TemplateBlocker{TemplateBlockerHasVariables}},
		{"url_var", []TemplateBlocker{TemplateBlockerHasVariables}},
		{"named_var", []TemplateBlocker{TemplateBlockerHasVariables}},
		{"hopeless", []TemplateBlocker{TemplateBlockerNoContactButton, TemplateBlockerHasVariables, TemplateBlockerNotApproved}},
	} {
		row := templateByName(t, res.Templates, tc.name)
		if row.Usable {
			t.Errorf("%s usable = true, want blocked by %v", tc.name, tc.want)
		}
		if len(row.Blockers) != len(tc.want) {
			t.Errorf("%s blockers = %v, want %v", tc.name, row.Blockers, tc.want)
			continue
		}
		for i := range tc.want {
			if row.Blockers[i] != tc.want[i] {
				t.Errorf("%s blockers = %v, want %v", tc.name, row.Blockers, tc.want)
				break
			}
		}
	}
}

func TestListMessageTemplates_RequiresBothInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, token, waba string }{
		{"no token", "", "123"},
		{"no account id", "tok", ""},
		{"blank account id", "tok", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := ListMessageTemplates(t.Context(), tc.token, tc.waba, ProbeOptions{BaseURL: "http://127.0.0.1:1"})
			if res.ErrorClass != ProbeClassIncomplete {
				t.Errorf("class = %q, want %q without a network call", res.ErrorClass, ProbeClassIncomplete)
			}
		})
	}
}

func TestListMessageTemplates_GraphErrorIsClassified(t *testing.T) {
	t.Parallel()
	const graphErr = `{"error":{"message":"Unsupported get request. Object with ID '555111' does not exist","type":"GraphMethodException","code":100,"error_subcode":33}}`
	base := templateServer(t, http.StatusBadRequest, graphErr, nil)

	res := ListMessageTemplates(t.Context(), "tok", "555111", ProbeOptions{BaseURL: base})
	if res.Templates != nil {
		t.Errorf("templates = %v, want nil on a failure", res.Templates)
	}
	if res.ErrorClass == "" || res.Hint == "" {
		t.Fatalf("a Graph rejection must carry a class and a hint, got %q / %q", res.ErrorClass, res.Hint)
	}
	if strings.Contains(res.Hint, "555111") || strings.Contains(res.Hint, "Unsupported get request") {
		t.Errorf("hint relays Meta's own text: %q", res.Hint)
	}
	if res.MetaErrorCode != 100 || res.MetaErrorSubcode != 33 {
		t.Errorf("Meta codes = %d/%d, want 100/33 for correlating against Meta's dashboard", res.MetaErrorCode, res.MetaErrorSubcode)
	}
}

func TestListMessageTemplates_NonJSONSuccess(t *testing.T) {
	t.Parallel()
	base := templateServer(t, http.StatusOK, "<html><body>Authentication required</body></html>", nil)

	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})
	if res.ErrorClass != ProbeClassUnknown {
		t.Errorf("class = %q, want %q", res.ErrorClass, ProbeClassUnknown)
	}
	if !strings.Contains(res.Hint, "intercepting") {
		t.Errorf("hint = %q, want it to point at the network rather than the credentials", res.Hint)
	}
}

func TestListMessageTemplates_HasMore(t *testing.T) {
	t.Parallel()
	const body = `{"data":[{"name":"a","language":"en","status":"APPROVED","components":[]}],
		"paging":{"next":"https://graph.facebook.com/v26.0/1/message_templates?after=XYZ"}}`
	base := templateServer(t, http.StatusOK, body, nil)

	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})
	if !res.HasMore {
		t.Error("HasMore = false, want true so the picker can say the list is partial")
	}
}

func TestTemplateBodyTruncation(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", templateBodyCap+50)
	body := `{"data":[{"name":"long","language":"en","status":"APPROVED","components":[{"type":"BODY","text":"` + long + `"}]}]}`
	base := templateServer(t, http.StatusOK, body, nil)

	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})
	got := res.Templates[0].Body
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated body must say so; got %q", got[max(0, len(got)-8):])
	}
	if n := len([]rune(got)); n != templateBodyCap+1 {
		t.Errorf("body = %d runes, want %d plus the ellipsis", n, templateBodyCap)
	}
	if strings.Contains(got, "�") {
		t.Error("body carries a replacement character; the cut landed mid-rune")
	}
}

func TestTemplateBodyPrefersTheBodyComponent(t *testing.T) {
	t.Parallel()
	const body = `{"data":[{"name":"ordered","language":"en","status":"APPROVED","components":[
		{"type":"FOOTER","text":"footer text"},
		{"type":"BODY","text":"body text"}]}]}`
	base := templateServer(t, http.StatusOK, body, nil)

	res := ListMessageTemplates(t.Context(), "tok", "1", ProbeOptions{BaseURL: base})
	if got := res.Templates[0].Body; got != "body text" {
		t.Errorf("body = %q, want the BODY component regardless of order", got)
	}
}
