// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func bookOf(sources ...entrypoint.CitationSource) *citationBook {
	var b citationBook
	b.add(sources)
	return &b
}

const (
	cnnURL     = "https://www.cnn.com/markets/tsla"
	cnnOther   = "https://www.cnn.com/other"
	financeURL = "https://finance.example.com/q?a=1&b=2"
)

var (
	bidiOverride = string(rune(0x202e))
	zeroWidth    = string(rune(0x200b))
	bidiIsolate  = string(rune(0x2066))
)

func sourcesBlock(lines ...string) string {
	return "\n\n" + sourcesHeading + "\n" + strings.Join(lines, "\n")
}

func TestCitationBookRenderAnswer(t *testing.T) {
	web := bookOf(
		entrypoint.CitationSource{ID: "s0r0", URL: cnnURL, Title: "CNN Markets – TSLA"},
		entrypoint.CitationSource{ID: "s0r1", URL: financeURL},
		entrypoint.CitationSource{ID: "s0r2", URL: cnnOther, Title: "Other"},
		entrypoint.CitationSource{ID: "s0r3", URL: "javascript:alert(1)"},
		entrypoint.CitationSource{ID: "s0r4", URL: "https://example.com/a|b>c d"},
		entrypoint.CitationSource{ID: "s0r5", URL: cnnURL, Title: "The same page again"},
		entrypoint.CitationSource{ID: "s0r6", URL: "ftp://files.example.com/y"},
		entrypoint.CitationSource{ID: "s0r7", URL: "file://host/p"},
		entrypoint.CitationSource{ID: "s0r8", URL: "https://example.com/p?q=a|b>c<d e"},
		entrypoint.CitationSource{ID: "s0r9", URL: "https://ex*am.example.com/p"},
		entrypoint.CitationSource{ID: "research0", URL: "https://research.example.com/r", Title: "Research"},
		entrypoint.CitationSource{ID: "idx0r0"},
	)
	for name, tc := range map[string]struct {
		book *citationBook
		in   string
		want string
	}{
		"a web source shows as a numbered link and is listed": {
			book: web,
			in:   "TSLA is down [[cite:s0r0]].",
			want: "TSLA is down <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a source with no title is listed by its site": {
			book: web,
			in:   "Mixed [[cite:s0r1]].",
			want: "Mixed <" + financeURL + "|[1]>." + sourcesBlock("[1] <"+financeURL+"|finance.example.com>"),
		},
		"sources are numbered in the order the text first cites them": {
			book: web,
			in:   "One [[cite:s0r1]] two [[cite:s0r0]] three [[cite:s0r2]].",
			want: "One <" + financeURL + "|[1]> two <" + cnnURL + "|[2]> three <" + cnnOther + "|[3]>." +
				sourcesBlock("[1] <"+financeURL+"|finance.example.com>", "[2] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[3] <"+cnnOther+"|cnn.com> — Other"),
		},
		"a source cited again keeps its number": {
			book: web,
			in:   "First [[cite:s0r0]] and again [[cite:s0r0]].",
			want: "First <" + cnnURL + "|[1]> and again <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"two ids for one address in one marker are one source": {
			book: web,
			in:   "Twice [[cite:s0r0, s0r5]].",
			want: "Twice <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"two ids for one address in two markers are one source": {
			book: web,
			in:   "First [[cite:s0r0]] then [[cite:s0r5]].",
			want: "First <" + cnnURL + "|[1]> then <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"several ids in one marker become several numbered links": {
			book: web,
			in:   "Mixed signals [[cite:s0r0, s0r1]].",
			want: "Mixed signals <" + cnnURL + "|[1]> <" + financeURL + "|[2]>." +
				sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[2] <"+financeURL+"|finance.example.com>"),
		},
		"a repeated cite prefix is read as the web UI reads it": {
			book: web,
			in:   "See [[cite:s0r0, cite:s0r1]].",
			want: "See <" + cnnURL + "|[1]> <" + financeURL + "|[2]>." +
				sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[2] <"+financeURL+"|finance.example.com>"),
		},
		"space before a comma in a marker is read": {
			book: web,
			in:   "See [[cite:s0r0 , s0r1]].",
			want: "See <" + cnnURL + "|[1]> <" + financeURL + "|[2]>." +
				sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[2] <"+financeURL+"|finance.example.com>"),
		},
		"markers written back to back are kept apart": {
			book: web,
			in:   "Three sources [[cite:s0r0]][[cite:s0r2]][[cite:s0r1]].",
			want: "Three sources <" + cnnURL + "|[1]> <" + cnnOther + "|[2]> <" + financeURL + "|[3]>." +
				sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[2] <"+cnnOther+"|cnn.com> — Other", "[3] <"+financeURL+"|finance.example.com>"),
		},
		"a single bracket marker is read too": {
			book: web,
			in:   "Down [cite:s0r0].",
			want: "Down <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a deep research source is read": {
			book: web,
			in:   "Found [[cite:research0]].",
			want: "Found <https://research.example.com/r|[1]>." + sourcesBlock("[1] <https://research.example.com/r|research.example.com> — Research"),
		},
		"a known and an unknown id in one marker keep the known one": {
			book: web,
			in:   "Mixed [[cite:s0r0, s9r9]].",
			want: "Mixed <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a source nothing can link to is dropped with its space and not listed": {
			book: web,
			in:   "From a document [[cite:idx0r0]].",
			want: "From a document.",
		},
		"an unknown source is dropped": {
			book: web,
			in:   "Unknown [[cite:s9r9]] source.",
			want: "Unknown source.",
		},
		"an address that is not a web address is not linked": {
			book: web,
			in:   "Bad [[cite:s0r3]] and [[cite:s0r6]] and [[cite:s0r7]].",
			want: "Bad and and.",
		},
		"a host Chat would read as markup is not linked": {
			book: web,
			in:   "Odd host [[cite:s0r9]].",
			want: "Odd host.",
		},
		"characters that would end a Chat link are escaped in a path": {
			book: web,
			in:   "Odd [[cite:s0r4]].",
			want: "Odd <https://example.com/a%7Cb%3Ec%20d|[1]>." + sourcesBlock("[1] <https://example.com/a%7Cb%3Ec%20d|example.com>"),
		},
		"characters that would end a Chat link are escaped in a query": {
			book: web,
			in:   "Odd [[cite:s0r8]].",
			want: "Odd <https://example.com/p?q=a%7Cb%3Ec%3Cd%20e|[1]>." + sourcesBlock("[1] <https://example.com/p?q=a%7Cb%3Ec%3Cd%20e|example.com>"),
		},
		"no known sources drops every marker": {
			book: nil,
			in:   "One [[cite:s0r0]] two [[cite:s0r1]].",
			want: "One two.",
		},
		"a marker the text ends in the middle of is held back": {
			book: web,
			in:   "TSLA is down [[cite:s0",
			want: "TSLA is down",
		},
		"the first characters of a marker are held back": {
			book: web,
			in:   "TSLA is down [[ci",
			want: "TSLA is down",
		},
		"a marker inside a code block is left alone": {
			book: web,
			in:   "Use ```\n[[cite:s0r0]]\n``` as the marker.",
			want: "Use ```\n[[cite:s0r0]]\n``` as the marker.",
		},
		"text with other brackets is untouched": {
			book: web,
			in:   "An [array](x) and [1] and [[note]] and [cite: smith 2020].",
			want: "An [array](x) and [1] and [[note]] and [cite: smith 2020].",
		},
		"a citation in a table row is a plain number": {
			book: web,
			in:   "| A | B |\n|---|---|\n| x | fact [[cite:s0r0]] |",
			want: "```\n| A | B |\n| x | fact [1] |\n```" + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a B after a marker is not the bold sentinel": {
			book: web,
			in:   "x[[cite:s0r0]]B[[cite:s0r1]] y",
			want: "x<" + cnnURL + "|[1]>B<" + financeURL + "|[2]> y" +
				sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA", "[2] <"+financeURL+"|finance.example.com>"),
		},
		"a B after a marker before bold text": {
			book: web,
			in:   "Vitamin[[cite:s0r0]]B**x** y",
			want: "Vitamin<" + cnnURL + "|[1]>B*x* y" + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a B after a marker at the end of a heading": {
			book: web,
			in:   "# Plan [[cite:s0r0]]B",
			want: "*Plan <" + cnnURL + "|[1]>B*" + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a B after a marker inside bold text": {
			book: web,
			in:   "**[[cite:s0r0]]B**",
			want: "*<" + cnnURL + "|[1]>B*" + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a citation in an inline code span is a plain number": {
			book: web,
			in:   "Run `code [[cite:s0r0]]` now.",
			want: "Run `code [1]` now." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a citation in the label of a link is a plain number": {
			book: web,
			in:   "[See [[cite:s0r0]]](https://z.example/page) now.",
			want: "<https://z.example/page|See [1]> now." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a citation after an inline code span is a link": {
			book: web,
			in:   "Run `code` [[cite:s0r0]].",
			want: "Run `code` <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
		"a placeholder the answer writes itself is not a citation": {
			book: web,
			in:   "Forged \x00C1\x00 \x01C1\x01 and real [[cite:s0r0]].",
			want: "Forged C1 C1 and real <" + cnnURL + "|[1]>." + sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets – TSLA"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.book.renderAnswer(tc.in); got != tc.want {
				t.Errorf("renderAnswer(%q) =\n%q\nwant\n%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSourceFieldsFromAPageAreNeverReadAsMarkup(t *testing.T) {
	t.Run("a title", func(t *testing.T) {
		book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: "https://a.example/p", Title: "Click [here](https://evil.example/login) now *bold* _it_ `c` ~x~ <b>|"})

		got := book.renderAnswer("Fact [[cite:s0r0]].")

		const want = "Fact <https://a.example/p|[1]>.\n\n*Sources*\n[1] <https://a.example/p|a.example> — Click [here]( now bold it c x b"
		if got != want {
			t.Errorf("renderAnswer =\n%q\nwant\n%q", got, want)
		}
		if links := strings.Count(got, "<"); links != 2 {
			t.Errorf("%d links in %q, want only the number and the source", links, got)
		}
	})

	t.Run("an address", func(t *testing.T) {
		for name, raw := range map[string]string{
			"bold":          "https://a.example/x**y**z",
			"strike":        "https://a.example/a~~b~~c",
			"markdown link": "https://a.example/x[1](https://evil.example/)",
		} {
			t.Run(name, func(t *testing.T) {
				book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: raw})
				link, ok := book.link("s0r0")
				if !ok {
					t.Fatalf("source %q was not accepted", raw)
				}

				got := book.renderAnswer("Fact [[cite:s0r0]].")

				want := "Fact <" + link.target + "|[1]>." + sourcesBlock("[1] <"+link.target+"|a.example>")
				if got != want {
					t.Errorf("renderAnswer =\n%q\nwant\n%q", got, want)
				}
				if links := strings.Count(got, "<"); links != 2 {
					t.Errorf("%d links in %q, want only the number and the source", links, got)
				}
			})
		}
	})
}

func TestAnIDTwoReportsGaveDifferentPagesLinksToNone(t *testing.T) {
	book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})

	added := book.add([]entrypoint.CitationSource{
		{ID: "s0r0", URL: "https://other.example/page", Title: "From a delegated agent"},
		{ID: "s0r1", URL: cnnOther},
	})

	if added != (citationsAdded{changed: 2, collided: 1}) {
		t.Errorf("add = %+v, want the new s0r1 and the collision on s0r0 counted", added)
	}
	if got, want := book.renderAnswer("A [[cite:s0r0]] B [[cite:s0r1]]."), "A B <"+cnnOther+"|[1]>."+sourcesBlock("[1] <"+cnnOther+"|cnn.com>"); got != want {
		t.Errorf("renderAnswer = %q, want the ambiguous id dropped: %q", got, want)
	}
	if again := book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: cnnURL}}); again.changed != 0 {
		t.Errorf("a later report of an ambiguous id changed %d, want nothing", again.changed)
	}
}

func TestCitationBookFillsATitleTheFirstReportLacked(t *testing.T) {
	book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})

	added := book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: cnnURL, Title: "CNN Markets"}})

	if added.changed != 1 {
		t.Errorf("add changed %d, want the title counted so the shown answer is written again", added.changed)
	}
	if got, want := book.renderAnswer("A [[cite:s0r0]]."), "A <"+cnnURL+"|[1]>."+sourcesBlock("[1] <"+cnnURL+"|cnn.com> — CNN Markets"); got != want {
		t.Errorf("renderAnswer = %q, want %q", got, want)
	}

	book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: cnnURL, Title: "A different title"}})

	if got := book.renderAnswer("A [[cite:s0r0]]."); !strings.Contains(got, "— CNN Markets") {
		t.Errorf("renderAnswer = %q, want the first title kept", got)
	}
}

func TestCitationBookIsSafeToUseConcurrently(t *testing.T) {
	var book citationBook
	var wg sync.WaitGroup
	defer func() {
		if got := book.renderAnswer("A [[cite:s0r0]] and [[cite:s7r49]]."); !strings.Contains(got, "|[1]>") || !strings.Contains(got, "|[2]>") {
			t.Errorf("renderAnswer = %q, want both sources linked once every report is in", got)
		}
	}()
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := range 50 {
				book.add([]entrypoint.CitationSource{{ID: fmt.Sprintf("s%dr%d", i, j), URL: fmt.Sprintf("%s/%d-%d", cnnURL, i, j)}})
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				book.renderAnswer("A [[cite:s0r0]] and [[cite:s7r49]].")
			}
		}()
	}
	wg.Wait()
}

func TestWhatMakesAnIDAmbiguous(t *testing.T) {
	linked := func(book *citationBook) bool {
		return strings.Contains(book.renderAnswer("A [[cite:s0r0]]."), "|[1]>")
	}
	for name, tc := range map[string]struct {
		first, second string
		ambiguous     bool
	}{
		"a trailing slash":                        {"https://a.example/x", "https://a.example/x/", false},
		"another scheme":                          {"http://a.example/x", "https://a.example/x", false},
		"the case of the host":                    {"https://A.Example/x", "https://a.example/x", false},
		"a fragment":                              {"https://a.example/x", "https://a.example/x#top", false},
		"a leading www":                           {"https://www.a.example/x", "https://a.example/x", false},
		"the default port":                        {"https://a.example:443/x", "https://a.example/x", false},
		"an escaped character":                    {"https://a.example/%7Ex", "https://a.example/~x", false},
		"the order of the query":                  {"https://a.example/x?a=1&b=2", "https://a.example/x?b=2&a=1", false},
		"another port":                            {"https://a.example:8443/x", "https://a.example/x", true},
		"a port that is not the scheme's":         {"https://a.example:80/x", "https://a.example/x", true},
		"the case of an escape":                   {"https://a.example/a%2fb", "https://a.example/a%2Fb", false},
		"an escaped slash and a slash":            {"https://a.example/a%2Fb", "https://a.example/a/b", true},
		"queries joined by semicolons":            {"https://a.example/x?a=1;b=2", "https://a.example/x?a=1;b=3", true},
		"queries that do not parse":               {"https://a.example/x?q=%zz", "https://a.example/x?q=%zy", true},
		"another page":                            {"https://a.example/x", "https://a.example/y", true},
		"another query":                           {"https://a.example/x?q=1", "https://a.example/x?q=2", true},
		"another host":                            {"https://a.example/x", "https://b.example/x", true},
		"a first report too long to link":         {"https://b.example/" + strings.Repeat("a", maxLinkTargetRunes+100), "https://a.example/x", true},
		"a first report with another scheme":      {"ftp://b.example/x", "https://a.example/x", true},
		"a plus and its escape":                   {"https://a.example/a+b", "https://a.example/a%2Bb", false},
		"a plus and an escaped space":             {"https://a.example/a+b", "https://a.example/a%20b", true},
		"an escaped percent and an escaped slash": {"https://a.example/a%252F", "https://a.example/a%2F", true},
		"an escaped percent in two cases":         {"https://a.example/a%252f", "https://a.example/a%252F", true},
		"a double slash":                          {"https://a.example/a//b", "https://a.example/a/b", true},
		"a trailing double slash":                 {"https://a.example/x//", "https://a.example/x", true},
		"a semicolon and its escape":              {"https://a.example/a;b", "https://a.example/a%3Bb", false},
		"an escaped question mark and a query":    {"https://a.example/a%3Fb", "https://a.example/a?b", true},
		"the case of the query":                   {"https://a.example/x?q=ABC", "https://a.example/x?q=abc", true},
		"an escaped slash beside an accent":       {"https://a.example/é%2Fb", "https://a.example/é/b", true},
		"an escaped slash beside a space":         {"https://a.example/x%2Fy z", "https://a.example/x/y z", true},
		"a space and its escape":                  {"https://a.example/x y", "https://a.example/x%20y", false},
		"mailto addresses":                        {"mailto:a@x.example", "mailto:b@x.example", true},
		"javascript addresses":                    {"javascript:alert(1)", "javascript:alert(2)", true},
		"hostless addresses":                      {"http:///x", "http:///y", true},
		"addresses with no scheme":                {"a.example/p", "b.example/p", true},
		"a hostless first report":                 {"javascript:alert(1)", "https://a.example/x", true},
		"padding":                                 {" https://a.example/x ", "https://a.example/x", false},
		"port 80 on http":                         {"http://a.example:80/x", "http://a.example/x", false},
		"port 443 on http":                        {"http://a.example:443/x", "http://a.example/x", true},
		"the case of a path":                      {"https://a.example/Wiki", "https://a.example/wiki", true},
		"an escaped tilde in lower case":          {"https://a.example/%7e", "https://a.example/~", false},
		"the case of www":                         {"https://WWW.A.example/x", "https://a.example/x", false},
		"an escaped hyphen":                       {"https://a.example/a%2Db", "https://a.example/a-b", false},
		"an escaped letter":                       {"https://a.example/%41", "https://a.example/A", false},
		"parentheses":                             {"https://a.example/(a)", "https://a.example/%28a%29", false},
		"a report with no address":                {"https://a.example/x", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: tc.first})

			added := book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: tc.second}})

			if got := added.collided > 0; got != tc.ambiguous {
				t.Errorf("add(%q then %q) collided = %v, want %v", tc.first, tc.second, got, tc.ambiguous)
			}
			if tc.ambiguous && linked(book) {
				t.Errorf("a marker of an ambiguous id is linked: %q", book.renderAnswer("A [[cite:s0r0]]."))
			}
		})
	}

	t.Run("a report after the id became ambiguous changes nothing", func(t *testing.T) {
		book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})
		book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: cnnOther}})

		for _, url := range []string{cnnOther, financeURL, cnnURL} {
			if added := book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: url}}); added != (citationsAdded{}) {
				t.Errorf("add(%q) = %+v, want nothing changed", url, added)
			}
		}
	})

	t.Run("a repeat with no title changes nothing", func(t *testing.T) {
		book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})

		if added := book.add([]entrypoint.CitationSource{{ID: "s0r0", URL: cnnURL}}); added != (citationsAdded{}) {
			t.Errorf("add = %+v, want nothing changed", added)
		}
	})
}

func TestAnAddressIsMeasuredAsEscaped(t *testing.T) {
	raw := "https://example.com/p?q=" + strings.Repeat("|", 1000)

	if _, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: raw}); ok {
		t.Errorf("an address of about %d runes once escaped was accepted, want over %d rejected", 24+3*1000, maxLinkTargetRunes)
	}
	if maxLinkRunes >= maxMessageRunes {
		t.Errorf("a link of up to %d runes does not fit in a message of %d", maxLinkRunes, maxMessageRunes)
	}
}

func TestPlainChatTextKeepsTheJoinersScriptsNeed(t *testing.T) {
	zwnj, zwj := string(rune(0x200c)), string(rune(0x200d))
	for name, in := range map[string]string{
		"Persian with a non-joiner": "mi" + zwnj + "khaham",
		"a joined emoji":            "family " + string(rune(0x1f468)) + zwj + string(rune(0x1f469)),
	} {
		if got := plainChatText(in); got != in {
			t.Errorf("%s: plainChatText(%q) = %q, want it kept", name, in, got)
		}
		if got := sourceTitle(in); got != in {
			t.Errorf("%s: sourceTitle(%q) = %q, want it kept", name, in, got)
		}
	}
}

func TestSiteNamesWithHiddenCharactersAreNotLinked(t *testing.T) {
	for name, host := range map[string]string{
		"a bidi override":      "www.%E2%80%AEevil.com",
		"a zero width":         "a%E2%80%8Bb.com",
		"a no-break space":     "a%C2%A0b.com",
		"an ideographic space": "a%E3%80%80b.com",
		"an em space":          "a%E2%80%83b.com",
		"a next line":          "a%C2%85b.com",
	} {
		if _, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: "https://" + host + "/"}); ok {
			t.Errorf("%s: a site name with a hidden character was accepted", name)
		}
	}
}

func TestWhereACitationCanBeALink(t *testing.T) {
	book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})
	link0 := "<" + cnnURL + "|[1]>"
	for name, tc := range map[string]struct{ in, want string }{
		"a stray backtick on an earlier line": {"it`s fine\nthen [[cite:s0r0]].", "it`s fine\nthen " + link0 + "."},
		"a closed link before a label":        {"[a](https://z.example/) and [see [[cite:s0r0]]](https://y.example/) ok", "<https://z.example/|a> and <https://y.example/|see [1]> ok"},
		"a link after a closed link":          {"[a](https://z.example/) then [[cite:s0r0]]", "<https://z.example/|a> then " + link0},
		"a table cell with a backtick":        {"| A | B |\n|---|---|\n| `x | y [[cite:s0r0]] |", "```\n| A | B |\n| `x | y [1] |\n```"},
		"after a closed code span":            {"`a` and `b` [[cite:s0r0]]", "`a` and `b` " + link0},
	} {
		t.Run(name, func(t *testing.T) {
			got := book.renderAnswer(tc.in)
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("renderAnswer(%q) =\n%q\nwant it to start\n%q", tc.in, got, tc.want)
			}
		})
	}
}

func hiddenByTheRule(r rune) bool {
	if r == 0x200c || r == 0x200d {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		(r >= 0xe0100 && r <= 0xe01ef) || r == 0x2800
}

func TestNoHiddenCharacterReachesATitleOrASiteName(t *testing.T) {
	blanks := []rune{0x034f, 0x115f, 0x1160, 0x3164, 0xffa0, 0x2800, 0x00ad, 0x180e, 0xe0001, 0xe0041, 0xe007f, 0x2061, 0xfff9}
	var hidden []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if hiddenByTheRule(r) && !unicode.IsSpace(r) {
			hidden = append(hidden, r)
		}
	}
	hidden = append(hidden, blanks...)
	if len(hidden) < 100 {
		t.Fatalf("only %d hidden characters found; the table is wrong", len(hidden))
	}
	for _, r := range hidden {
		if got := plainChatText("a" + string(r) + "b"); got != "ab" {
			t.Errorf("plainChatText kept U+%04X: %q", r, got)
		}
		if got := sourceTitle("go" + string(r) + "od www.evil.example"); got != "good" {
			t.Errorf("sourceTitle kept U+%04X or an address it hides: %q", r, got)
		}
		for _, hiding := range []string{"see ww" + string(r) + "w.evil.example", "see https" + string(r) + "://evil.example/x", "see " + string(r) + "https://evil.example"} {
			if got := sourceTitle(hiding); got != "see" {
				t.Errorf("sourceTitle(%q) = %q, want the address left out although U+%04X is inside it", hiding, got, r)
			}
		}
		if link, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: "https://go" + string(r) + ".dev/"}); ok {
			t.Errorf("a site name with U+%04X was linked as %q", r, link.site)
		}
	}
}

func TestTheLongestLinkIsKeptWholeAtTheMessageLimit(t *testing.T) {
	host := strings.Repeat("a", maxSiteRunes-len(".com")) + ".com"
	prefix := "https://" + host + "/"
	target := prefix + strings.Repeat("p", maxLinkTargetRunes-len(prefix))
	book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: target, Title: "The longest page"})
	if _, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: target}); !ok {
		t.Fatal("the longest address and site were not accepted")
	}
	longest := maxLinkTargetRunes + maxSiteRunes + 3
	const beforeTheList = len("`[1]`") + len("\n\n*Sources*\n[1] ")
	var distances []int
	for d := longest - 60; d <= longest+10; d += 10 {
		distances = append(distances, d)
	}
	for d := longest - 5; d <= longest+3; d++ {
		distances = append(distances, d)
	}
	for _, d := range distances {
		t.Run(fmt.Sprint(d), func(t *testing.T) {
			before := maxMessageRunes - d - beforeTheList
			rendered := book.renderAnswer(strings.Repeat("word ", before/5) + strings.Repeat("w", before%5) + "`[[cite:s0r0]]`")
			i := strings.Index(rendered, "[1] <")
			if got := utf8.RuneCountInString(rendered[:i]) + len("[1] "); got != maxMessageRunes-d {
				t.Fatalf("the longest link begins %d runes into the message, want %d", got, maxMessageRunes-d)
			}

			assertWholeChunks(t, rendered, splitForChat(rendered, maxMessageRunes), maxMessageRunes)
		})
	}
}

func TestASiteNameIsAtMostAHostNameLong(t *testing.T) {
	for name, tc := range map[string]struct {
		host string
		want bool
	}{
		"a host of 253 runes":   {strings.Repeat("a", 249) + ".com", true},
		"a host of 254 runes":   {strings.Repeat("a", 250) + ".com", false},
		"a host of many bytes":  {strings.Repeat("é", 200) + ".com", true},
		"the longest host name": {strings.Repeat("a", maxSiteRunes-4) + ".com", true},
		"one more":              {strings.Repeat("a", maxSiteRunes-3) + ".com", false},
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: "https://" + tc.host + "/"}); got != tc.want {
				t.Errorf("a host of %d runes accepted = %v, want %v", len(tc.host), got, tc.want)
			}
		})
	}
	if maxLinkRunes != maxLinkTargetRunes+maxSiteRunes+3 {
		t.Errorf("maxLinkRunes = %d, want the longest address, the longest site and the markup around them", maxLinkRunes)
	}
}

func TestOrdinaryCharactersAreKept(t *testing.T) {
	markup := "*_~`<>|"
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.IsGraphic(r) || unicode.IsSpace(r) || hiddenByTheRule(r) || strings.ContainsRune(markup, r) {
			continue
		}
		in := "a" + string(r) + "b"
		if got := plainChatText(in); got != in {
			t.Fatalf("plainChatText kept U+%04X as %q", r, got)
		}
		if got := sourceTitle(in); got != in {
			t.Fatalf("sourceTitle kept U+%04X as %q", r, got)
		}
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r) {
			continue
		}
		raw := "https://a" + string(r) + "b.example/"
		if u, err := url.Parse(raw); err == nil && strings.ContainsRune(u.Hostname(), r) {
			if _, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: raw}); !ok {
				t.Fatalf("a site name with U+%04X was not linked", r)
			}
		}
	}
}

func TestAPaddedAddressIsLinked(t *testing.T) {
	link, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: " https://a.example/x \n"})

	if !ok || link.target != "https://a.example/x" {
		t.Errorf("presentableCitation = %q, accepted = %v, want the address without its padding", link.target, ok)
	}
}

func TestCanonicalPathLeavesAnInvalidEscapeAlone(t *testing.T) {
	if got, want := canonicalPath("/a%zz/b%41"), "/a%zz/bA"; got != want {
		t.Errorf("canonicalPath = %q, want %q", got, want)
	}
}

func TestAJoinerInsideAnAddressIsNotRemoved(t *testing.T) {
	joiner := string(rune(0x200d))
	for _, in := range []string{"see ww" + joiner + "w.evil.example now", "see https" + joiner + "://evil.example/x now"} {
		if got := sourceTitle(in); got != in {
			t.Errorf("sourceTitle(%q) = %q, want it kept as written", in, got)
		}
	}
}

func TestSourceTitle(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":                                    {"Tesla Stock Price", "Tesla Stock Price"},
		"whitespace collapsed":                     {"  Tesla \n  Stock\tPrice ", "Tesla Stock Price"},
		"link characters removed":                  {"A <b> | c", "A b c"},
		"formatting removed":                       {"*bold* _it_ `c` ~x~", "bold it c x"},
		"empty":                                    {"", ""},
		"cut to a bounded length":                  {strings.Repeat("word ", 40), strings.TrimSpace(strings.Repeat("word ", 40)[:maxSourceTitleRunes]) + "…"},
		"short title is not marked":                {"Short", "Short"},
		"www and a digit":                          {"see www.3m.com now", "see now"},
		"www and an accented letter":               {"see www.münchen.de now", "see now"},
		"www and an ideograph":                     {"see www.例え.jp now", "see now"},
		"a word starting with www":                 {"wwwxyz and more", "wwwxyz and more"},
		"www and a hyphen":                         {"www.-x", "www.-x"},
		"a word ending in www and a name":          {"visit Shawww.com now", "visit Sha now"},
		"a scheme with a dot":                      {"see svn.ssh://host/x now", "see now"},
		"a scheme with a hyphen":                   {"see git-ssh://host/x now", "see now"},
		"a scheme of one letter":                   {"see a://x now", "see now"},
		"an address with a digit in its scheme":    {"see s3://bucket/key now", "see now"},
		"an address with a plus in its scheme":     {"see git+ssh://host/x now", "see now"},
		"an address in capitals":                   {"see HTTPS://EVIL.EXAMPLE now", "see now"},
		"www in capitals":                          {"see WWW.EVIL.EXAMPLE now", "see now"},
		"a colon after a word":                     {"Breaking: Tesla", "Breaking: Tesla"},
		"a colon after a quarter":                  {"Q3: results", "Q3: results"},
		"www with no dot":                          {"wwwx and www", "wwwx and www"},
		"an elongated word":                        {"Awww... cute puppies", "Awww... cute puppies"},
		"a word ending in www":                     {"Shawww.", "Shawww."},
		"snake case":                               {"snake_case_name", "snake case name"},
		"a title just under the cap":               {strings.Repeat("a", 79), strings.Repeat("a", 79)},
		"a title at the cap":                       {strings.Repeat("a", 80), strings.Repeat("a", 80)},
		"a title just over the cap":                {strings.Repeat("a", 81), strings.Repeat("a", 80) + "…"},
		"cut between characters":                   {strings.Repeat("é", 100), strings.Repeat("é", maxSourceTitleRunes) + "…"},
		"control characters":                       {"Bad\x00Ti\x07tle\x1b[0m", "BadTitle[0m"},
		"bidi and format controls":                 {"a" + bidiOverride + "b" + zeroWidth + "c" + bidiIsolate + "d", "abcd"},
		"line breaks are spaces":                   {"one\ntwo\r\nthree", "one two three"},
		"web addresses are left out":               {"Click https://evil.example/login) now www.evil.example too", "Click now too"},
		"a scheme alone is left out":               {"see ftp://files.example/x ok", "see ok"},
		"an address after an underscore":           {"Login _https://evil.example/login now", "Login now"},
		"an address after a letter and underscore": {"Login a_www.evil.example now", "Login a now"},
		"an address after a digit":                 {"Login 1https://evil.example/login now", "Login 1 now"},
		"an address after markup":                  {"Login *https://evil.example/login* now", "Login now"},
	} {
		t.Run(name, func(t *testing.T) {
			got := sourceTitle(tc.in)
			if got != tc.want {
				t.Errorf("sourceTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("sourceTitle(%q) = %q, which is not valid text", tc.in, got)
			}
		})
	}
}

func TestChatLinkTarget(t *testing.T) {
	const raw = "https://x.example/p?q=a|b>c<d e"
	if got, want := chatLinkTarget(raw), "https://x.example/p?q=a%7Cb%3Ec%3Cd%20e"; got != want {
		t.Errorf("chatLinkTarget(%q) = %q, want %q", raw, got, want)
	}
}

func TestSourcesListNearTheMessageLimitKeepsEveryLinkWhole(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("a", 1500)
	book := bookOf(
		entrypoint.CitationSource{ID: "s0r0", URL: cnnURL, Title: "CNN Markets"},
		entrypoint.CitationSource{ID: "s0r1", URL: financeURL, Title: "Finance"},
		entrypoint.CitationSource{ID: "s0r2", URL: longURL, Title: "A page with a long address"},
	)
	for _, padding := range []int{maxMessageRunes - 3000, maxMessageRunes - 1700, maxMessageRunes - 1500, maxMessageRunes - 1000, maxMessageRunes - 700, maxMessageRunes - 200, maxMessageRunes - 50, maxMessageRunes - 10} {
		t.Run(fmt.Sprint(padding), func(t *testing.T) {
			text := strings.Repeat("word ", padding/5) + "[[cite:s0r2]] and [[cite:s0r0]] and [[cite:s0r1]]."

			chunks := splitForChat(book.renderAnswer(text), maxMessageRunes)

			for i, chunk := range chunks {
				if opened, closed := strings.Count(chunk, "<"), strings.Count(chunk, ">"); opened != closed {
					t.Errorf("chunk %d has %d link starts and %d ends: %.120q", i, opened, closed, chunk)
				}
				if utf8.RuneCountInString(chunk) > maxMessageRunes {
					t.Errorf("chunk %d is %d runes, over the limit", i, utf8.RuneCountInString(chunk))
				}
			}
		})
	}
}

func TestPresentableCitation(t *testing.T) {
	for name, tc := range map[string]struct {
		url  string
		want bool
	}{
		"https":                          {"https://example.com/a", true},
		"http":                           {"http://example.com/a", true},
		"no host":                        {"http:///x", false},
		"ftp with a host":                {"ftp://files.example.com/y", false},
		"file with a host":               {"file://host/p", false},
		"no scheme":                      {"example.com/a", false},
		"a host with a star":             {"https://ex*am.example.com/p", false},
		"a host with a tilde":            {"https://ex~am.example.com/p", false},
		"a host with an angle bracket":   {"https://ex<am.example.com/p", false},
		"a host with a closing bracket":  {"https://ex>am.example.com/p", false},
		"the longest address kept whole": {"https://example.com/" + strings.Repeat("a", maxLinkTargetRunes-len("https://example.com/")), true},
		"an address of 2000 runes":       {"https://example.com/" + strings.Repeat("a", 2000-len("https://example.com/")), true},
		"an address of 2001 runes":       {"https://example.com/" + strings.Repeat("a", 2001-len("https://example.com/")), false},
		"an address too long to keep":    {"https://example.com/" + strings.Repeat("a", maxLinkTargetRunes), false},
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: tc.url}); got != tc.want {
				t.Errorf("presentableCitation(%q) accepted = %v, want %v", tc.url, got, tc.want)
			}
		})
	}

	t.Run("credentials in the address are not kept", func(t *testing.T) {
		link, ok := presentableCitation(entrypoint.CitationSource{ID: "s0r0", URL: "https://user:secret@example.com/p"})
		if !ok || strings.Contains(link.target, "secret") || strings.Contains(link.target, "user") {
			t.Errorf("presentableCitation target = %q, accepted = %v, want the address without credentials", link.target, ok)
		}
	})
}

func TestPartialMarkersAreHeldBack(t *testing.T) {
	web := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: cnnURL})
	for name, tc := range map[string]struct{ in, want string }{
		"two brackets":            {"Down [[", "Down"},
		"c":                       {"Down [[c", "Down"},
		"ci":                      {"Down [[ci", "Down"},
		"cit":                     {"Down [[cit", "Down"},
		"cite":                    {"Down [[cite", "Down"},
		"cite and a colon":        {"Down [[cite:", "Down"},
		"an id begun":             {"Down [[cite:s0", "Down"},
		"a single bracket marker": {"Down [cite:s0", "Down"},
		"a marker across a line":  {"Down [[cite:abc\nmore", "Down [[cite:abc\nmore"},
		"one bracket alone":       {"Down [", "Down ["},
		"another word":            {"Down [[note", "Down [[note"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := web.renderAnswer(tc.in); got != tc.want {
				t.Errorf("renderAnswer(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHostileTitleCarriesOnlyItsWordsIntoTheList(t *testing.T) {
	book := bookOf(entrypoint.CitationSource{ID: "s0r0", URL: "https://a.example/p", Title: "Click https://evil.example/login) \x00now" + bidiOverride + " *bold*"})

	got := book.renderAnswer("Fact [[cite:s0r0]].")

	const want = "Fact <https://a.example/p|[1]>.\n\n*Sources*\n[1] <https://a.example/p|a.example> — Click now bold"
	if got != want {
		t.Errorf("renderAnswer =\n%q\nwant\n%q", got, want)
	}
}

func TestAnswerMarkersBecomeNumberedLinks(t *testing.T) {
	sources := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{
		Kind: "data", Data: map[string]any{
			"type": "rag_info_update", "search_type": "web_search",
			"sources": []any{map[string]any{"citationId": "s0r0", "sourceUrl": cnnURL, "title": "CNN Markets"}},
		},
	}}}}}
	text := func(s string) *a2a.TaskStatusUpdateEvent {
		return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: s}}}}}
	}
	const link = "<" + cnnURL + "|[1]>"
	const listed = "[1] <" + cnnURL + "|cnn.com> — CNN Markets"

	t.Run("streamed", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-c1"), dmMeta())
		a.OnStatusUpdate(t.Context(), tid("task-c1"), sources, nil)
		a.OnStatusUpdate(t.Context(), tid("task-c1"), text("TSLA is down [[cite:s0r0]]."), nil)
		a.queueFor(tid("task-c1"), st).Drain(5 * time.Second)

		writes, texts := chat.snapshot()
		if !anyTextContains(texts, "TSLA is down "+link+".") || !anyTextContains(texts, listed) {
			t.Errorf("messages = %q, want the marker as a numbered link and the source listed", texts)
		}
		for _, w := range writes {
			if strings.ContainsAny(w.text, "\x00\x01") || strings.Contains(w.text, "[[") || strings.Contains(w.text, "cite:") {
				t.Errorf("write %q shows a raw marker or a placeholder byte", w.text)
			}
		}
	})

	t.Run("streamed with a marker split across deltas", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-c4"), dmMeta())
		a.OnStatusUpdate(t.Context(), tid("task-c4"), sources, nil)
		for _, delta := range []string{"TSLA is down [", "[ci", "te:s0", "r0]]."} {
			a.OnStatusUpdate(t.Context(), tid("task-c4"), text(delta), nil)
		}
		a.queueFor(tid("task-c4"), st).Drain(5 * time.Second)

		writes, texts := chat.snapshot()
		if !anyTextContains(texts, "TSLA is down "+link+".") {
			t.Errorf("messages = %q, want the marker as a numbered link", texts)
		}
		for _, w := range writes {
			if strings.ContainsAny(w.text, "\x00\x01") || strings.Contains(w.text, "[[") || strings.Contains(w.text, "cite:") {
				t.Errorf("write %q shows a raw marker or a placeholder byte", w.text)
			}
		}
	})

	t.Run("written whole", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		submitForTest(t, a, tid("task-c2"), dmMeta())
		a.OnStatusUpdate(t.Context(), tid("task-c2"), sources, nil)
		a.OnFinalResponse(t.Context(), tid("task-c2"), completedTask("TSLA is down [[cite:s0r0]]."), nil)

		_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
			return anyTextContains(texts, link) && anyTextContains(texts, listed)
		})
		if anyTextContains(texts, "[[cite") {
			t.Errorf("messages = %q, want no raw marker", texts)
		}
	})

	t.Run("a link is withdrawn when another report gives its id a different page", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-c5"), dmMeta())
		a.OnStatusUpdate(t.Context(), tid("task-c5"), sources, nil)
		a.OnStatusUpdate(t.Context(), tid("task-c5"), text("TSLA is down [[cite:s0r0]]."), nil)
		a.queueFor(tid("task-c5"), st).Drain(5 * time.Second)
		if _, texts := chat.snapshot(); !anyTextContains(texts, link) {
			t.Fatalf("messages = %q, want the link before the collision", texts)
		}
		other := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{
			Kind: "data", Data: map[string]any{
				"type": "rag_info_update", "search_type": "web_search",
				"sources": []any{map[string]any{"citationId": "s0r0", "sourceUrl": cnnOther, "title": "From a delegated agent"}},
			},
		}}}}}

		a.OnStatusUpdate(t.Context(), tid("task-c5"), other, nil)

		_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
			return len(texts) == 1 && strings.Contains(texts[0], "TSLA is down.") && !strings.Contains(texts[0], cnnURL)
		})
		if anyTextContains(texts, "*Sources*") {
			t.Errorf("messages = %q, want the withdrawn source gone from the list", texts)
		}
	})

	t.Run("a source learned after the text that cites it", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-c3"), dmMeta())
		a.OnStatusUpdate(t.Context(), tid("task-c3"), text("TSLA is down [[cite:s0r0]]."), nil)
		q := a.queueFor(tid("task-c3"), st)
		q.Drain(5 * time.Second)
		if _, texts := chat.snapshot(); anyTextContains(texts, link) {
			t.Fatalf("messages = %q, want no link before the source is known", texts)
		}

		a.OnStatusUpdate(t.Context(), tid("task-c3"), sources, nil)

		writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
			return anyTextContains(texts, "TSLA is down "+link+".") && anyTextContains(texts, listed)
		})
		if len(texts) != 1 {
			t.Errorf("messages = %q, want the answer edited in place, not posted again", texts)
		}
		for _, w := range writes {
			if strings.Contains(w.text, "[[") || strings.Contains(w.text, "cite:") {
				t.Errorf("write %q shows a raw marker", w.text)
			}
		}
	})
}

func TestCitationBookRenderFile(t *testing.T) {
	book := bookOf(
		entrypoint.CitationSource{ID: "s0r0", URL: cnnURL, Title: "CNN Markets – TSLA"},
		entrypoint.CitationSource{ID: "s0r1", URL: financeURL},
		entrypoint.CitationSource{ID: "s0r2", URL: cnnOther, Title: `Go [beta](javascript:x) \ docs`},
		entrypoint.CitationSource{ID: "s0r3", URL: `https://example.com/q?x=a\)`},
		entrypoint.CitationSource{ID: "idx0r0"},
	)
	const (
		cited      = "# Notes\n\nTesla rose.[[cite:s0r0]] Rates held.[[cite:s0r1]]\n"
		cnnLink    = "[1](<https://www.cnn.com/markets/tsla>)"
		cnnSources = "\n\n## Sources\n\n1. <https://www.cnn.com/markets/tsla> — CNN Markets – TSLA\n"
	)
	for name, tc := range map[string]struct {
		in       string
		markdown bool
		want     string
	}{
		"markdown links each number and lists the sources": {
			in: cited, markdown: true,
			want: "# Notes\n\nTesla rose." + cnnLink + " Rates held.[2](<https://finance.example.com/q?a=1&b=2>)\n\n## Sources\n\n" +
				"1. <https://www.cnn.com/markets/tsla> — CNN Markets – TSLA\n" +
				"2. <https://finance.example.com/q?a=1&b=2>\n",
		},
		"plain text writes plain numbers and lines": {
			in:   cited,
			want: "# Notes\n\nTesla rose.[1] Rates held.[2]\n\nSources\n\n[1] https://www.cnn.com/markets/tsla — CNN Markets – TSLA\n[2] https://finance.example.com/q?a=1&b=2\n",
		},
		"text after a number does not make it a link elsewhere": {
			in: "Go 1.22 shipped [[cite:s0r0]](2024).", markdown: true,
			want: "Go 1.22 shipped " + cnnLink + "(2024)." + cnnSources,
		},
		"the file's own link definition does not capture a number": {
			in: "Tesla rose.[[cite:s0r0]]\n\n[1]: https://example.com/notes\n", markdown: true,
			want: "Tesla rose." + cnnLink + "\n\n[1]: https://example.com/notes" + cnnSources,
		},
		"a number in an inline code span is plain": {
			in: "Run `go[[cite:s0r0]]` now.", markdown: true,
			want: "Run `go[1]` now." + cnnSources,
		},
		"a file that ends in an open marker keeps it": {
			in: "Tesla rose.[[cite:s0r0]] See the wiki [[", markdown: true,
			want: "Tesla rose." + cnnLink + " See the wiki [[" + cnnSources,
		},
		"a backslash in an address stays in the address": {
			in: "Q.[[cite:s0r3]]", markdown: true,
			want: "Q.[1](<https://example.com/q?x=a%5C)>)\n\n## Sources\n\n1. <https://example.com/q?x=a%5C)>\n",
		},
		"a title cannot become a markdown link": {
			in: "Go.[[cite:s0r2]]", markdown: true,
			want: "Go.[1](<https://www.cnn.com/other>)\n\n## Sources\n\n1. <https://www.cnn.com/other> — Go \\[beta\\](javascript:x) \\\\ docs\n",
		},
		"a title is written as it is in plain text": {
			in:   "Go.[[cite:s0r2]]",
			want: "Go.[1]\n\nSources\n\n[1] https://www.cnn.com/other — Go [beta](javascript:x) \\ docs\n",
		},
		"nothing linkable is cited": {
			in: "A document.[[cite:idx0r0]]\n", markdown: true,
			want: "A document.[[cite:idx0r0]]\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := book.renderFile(tc.in, tc.markdown); got != tc.want {
				t.Errorf("renderFile = %q, want %q", got, tc.want)
			}
		})
	}
}
