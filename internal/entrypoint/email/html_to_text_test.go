// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "testing"

func TestHTMLToText_Plain(t *testing.T) {
	in := `<html><body><p>Hello, world.</p></body></html>`
	want := "Hello, world."
	if got := htmlToText(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestHTMLToText_BlockElementsBecomeNewlines(t *testing.T) {
	in := `<div>Hello,</div><div>What skills do you have?</div>`
	want := "Hello,\n\nWhat skills do you have?"
	if got := htmlToText(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestHTMLToText_BrTag(t *testing.T) {
	in := `<p>Line one.<br>Line two.</p>`
	want := "Line one.\nLine two."
	if got := htmlToText(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestHTMLToText_EntitiesDecoded(t *testing.T) {
	in := `<p>Costs &lt; $5 &amp; ships fast.</p>`
	want := "Costs < $5 & ships fast."
	if got := htmlToText(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestHTMLToText_SkipsScriptAndStyle(t *testing.T) {
	in := `<html><head><style>div { color: red; }</style></head><body>` +
		`<script>alert('x');</script>` +
		`<p>Real content.</p></body></html>`
	want := "Real content."
	if got := htmlToText(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestHTMLToText_OutlookReferenceContainer(t *testing.T) {
	in := `<html><body>` +
		`<div style="font-family: Aptos"><br><br></div>` +
		`<div id="mail-editor-reference-message-container">` +
		`<div id="mail-editor-reference-message-container">` +
		`<div class="ms-outlook-mobile-reference-message">Hello,</div>` +
		`<div class="ms-outlook-mobile-reference-message"><br></div>` +
		`<div class="ms-outlook-mobile-reference-message">What skills do you have?</div>` +
		`</div></div></body></html>`
	got := htmlToText(in)
	if !contains(got, "Hello,") || !contains(got, "What skills do you have?") {
		t.Errorf("got %q; expected Outlook content extracted", got)
	}
	if got == "" {
		t.Errorf("got empty string for non-empty Outlook HTML body")
	}
}

func TestHTMLToText_EmptyInput(t *testing.T) {
	if got := htmlToText(""); got != "" {
		t.Errorf("got %q; want empty string", got)
	}
}

func TestHTMLToText_OnlyWhitespaceTags(t *testing.T) {
	in := `<html><body><div><br><br></div></body></html>`
	if got := htmlToText(in); got != "" {
		t.Errorf("got %q; want empty for whitespace-only HTML", got)
	}
}

func TestHTMLToText_NbspBecomesSpace(t *testing.T) {
	in := `<p>Hello&nbsp;there.</p>`
	got := htmlToText(in)
	if contains(got, "&nbsp;") {
		t.Errorf("got %q; entity not decoded", got)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
