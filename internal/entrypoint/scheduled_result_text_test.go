// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStripInternalMarkers(t *testing.T) {
	longOpener := "«status_update:Searching " + strings.Repeat("Revenue grew. ", 20) + " Il a dit «bonjour» ici."
	body := func(n int) string { return strings.Repeat("x", n) }
	wide := func(n int) string { return strings.Repeat("é", n) }
	cases := []struct {
		name, in, want string
	}{
		{"marker at the start", "«artifact_return:a.md»Hello", "Hello"},
		{"marker at the end", "Hello«artifact_return:a.md»", "Hello"},
		{"nothing but a marker", "«artifact_return:a.md»", ""},
		{"a run of markers between sentences is one break", "a«artifact_return:x»«artifact_return:y»b", "a\n\nb"},
		{"space on one side leaves no break", "Hello «status_update:x»World", "Hello World"},
		{"a break before a digit", "Total«artifact_return:x»1 file", "Total\n\n1 file"},
		{"no break before punctuation", "See the chart«artifact_return:c.png».", "See the chart."},
		{"no break inside brackets", "(«artifact_return:x»)", "()"},
		{"an unterminated artifact marker is left alone", "«artifact_return:foo\nreal reply", "«artifact_return:foo\nreal reply"},
		{"an unterminated status_update keeps the reply when a quote follows on the same line", longOpener, longOpener},
		{"a short unterminated status_update keeps the reply when a quote follows", "Done. «status_update:Writing... Answer: 42, he said «bonjour» ok", "Done. «status_update:Writing... Answer: 42, he said «bonjour» ok"},
		{"ordinary guillemets are left alone", "Il a dit «bonjour» ici", "Il a dit «bonjour» ici"},
		{"a status_update of 512 characters is stripped", "A.«status_update:" + body(512) + "»B", "A.\n\nB"},
		{"a status_update of 513 characters is left alone", "A.«status_update:" + body(513) + "»B", "A.«status_update:" + body(513) + "»B"},
		{"an artifact_return of 512 characters is stripped", "A.«artifact_return:" + body(512) + "»B", "A.\n\nB"},
		{"an artifact_return of 513 characters is left alone", "A.«artifact_return:" + body(513) + "»B", "A.«artifact_return:" + body(513) + "»B"},
		{"the bound counts characters, not bytes", "A.«status_update:" + wide(512) + "»B", "A.\n\nB"},
		{"513 multi-byte characters are left alone", "A.«status_update:" + wide(513) + "»B", "A.«status_update:" + wide(513) + "»B"},
		{"a long status_update with no inner guillemet is left alone", "A.«status_update:" + strings.Repeat("word ", 200) + "»B", "A.«status_update:" + strings.Repeat("word ", 200) + "»B"},
		{"a status_update over two lines is left alone", "A.«status_update:line1\nline2»B", "A.«status_update:line1\nline2»B"},
		{"an artifact_return over two lines is left alone", "A.«artifact_return:li\nne»B", "A.«artifact_return:li\nne»B"},
		{"an artifact_return opener before a quote is left alone", "Done «artifact_return:a.md and «x» ok", "Done «artifact_return:a.md and «x» ok"},
		{"a run of markers after a space leaves no break", "Hello «artifact_return:x»«artifact_return:y»World", "Hello World"},
		{"a run of two kinds at the start leaves no break", "«artifact_return:x»«status_update:y»Hello", "Hello"},
		{"two runs are two breaks", "a«artifact_return:x» b«artifact_return:y»c", "a b\n\nc"},
		{"both kinds in one run", "A«artifact_return:x»«§status_update:y»B", "A\n\nB"},
		{"a tab before leaves no break", "A.\t«artifact_return:x»B", "A.\tB"},
		{"a no-break space before leaves no break", "A.\u00a0«artifact_return:x»B", "A.\u00a0B"},
		{"a newline before leaves no break", "A.\n«artifact_return:x»B", "A.\nB"},
		{"a break before an accented letter", "A.«artifact_return:x»Élan", "A.\n\nÉlan"},
		{"a break before a CJK letter", "A.«artifact_return:x»日本", "A.\n\n日本"},
		{"no break before a roman numeral", "A.«artifact_return:x»Ⅳ", "A.Ⅳ"},
		{"a break before a non-ASCII digit", "A.«artifact_return:x»٣", "A.\n\n٣"},
		{"a stray closing guillemet after an artifact_return marker stays", "A«artifact_return:x»B»C", "A\n\nB»C"},
		{"a stray closing guillemet after a status_update marker stays", "A«status_update:x»B»C", "A\n\nB»C"},
		{"a doubled closing guillemet ends an artifact_return only once", "A«artifact_return:x»»B", "A»B"},
		{"an extra opening guillemet is not part of an artifact_return", "««artifact_return:x»B", "«\n\nB"},
	}
	for _, c := range cases {
		if got := stripInternalMarkers(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScheduledResultText(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name   string
		in     *string
		want   string
		wantOK bool
	}{
		{"prefers agentResponseFull", str(`{"agentResponse":"short","agentResponseFull":"the full reply"}`), "the full reply", true},
		{"falls back to agentResponse", str(`{"agentResponse":"just this"}`), "just this", true},
		{"strips artifact_return markers", str(`{"agentResponseFull":"Here you go\n«artifact_return:chart.png»«artifact_return:data.csv»"}`), "Here you go", true},
		{"strips a leading status_update with the section sign", str(`{"agentResponseFull":"«§status_update:Creating sample markdown artifact...»\n\nI created it."}`), "I created it.", true},
		{"strips a status_update wrapped in block delimiters", str(`{"agentResponseFull":"«««status_update:working»»»Done."}`), "Done.", true},
		{"a marker between sentences does not join them", str(`{"agentResponseFull":"in your viewer.«artifact_return:a.md»The artifact was saved."}`), "in your viewer.\n\nThe artifact was saved.", true},
		{"a status_update between sentences does not join them", str(`{"agentResponseFull":"Step one.«status_update:next»Step two."}`), "Step one.\n\nStep two.", true},
		{"an unterminated status_update does not swallow the reply after it", str(`{"agentResponseFull":"«status_update:Searching\nHere is the report: revenue up 5%.\n«artifact_return:r.csv»"}`), "«status_update:Searching\nHere is the report: revenue up 5%.", true},
		{"an unterminated status_update leaves later guillemets alone", str(`{"agentResponseFull":"Done. «status_update:Writing...\nAnswer: 42\n\nIl a dit «bonjour» ici"}`), "Done. «status_update:Writing...\nAnswer: 42\n\nIl a dit «bonjour» ici", true},
		{"leaves ordinary guillemets alone", str(`{"agentResponseFull":"Il a dit «bonjour» ici"}`), "Il a dit «bonjour» ici", true},
		{"trims whitespace", str(`{"agentResponseFull":"  hi  "}`), "hi", true},
		{"empty response", str(`{"agentResponseFull":""}`), "", true},
		{"absent summary", nil, "", true},
		{"empty string summary", str(""), "", true},
		{"unparseable is flagged", str(`not json`), "", false},
	}
	for _, c := range cases {
		got, ok := scheduledResultText(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}

	long := `{"agentResponseFull":"` + strings.Repeat("x", maxInboxResultChars+50) + `"}`
	got, ok := scheduledResultText(str(long))
	if !ok || !strings.HasSuffix(got, "…") || len([]rune(got)) > maxInboxResultChars+1 {
		t.Errorf("expected truncation to ~%d runes + ellipsis, got %d runes ok=%v", maxInboxResultChars, len([]rune(got)), ok)
	}

	emoji := `{"agentResponseFull":"` + strings.Repeat("😀", maxInboxResultChars+10) + `"}`
	if g, _ := scheduledResultText(str(emoji)); !utf8.ValidString(g) {
		t.Error("truncation split a multi-byte rune into invalid UTF-8")
	}
}
