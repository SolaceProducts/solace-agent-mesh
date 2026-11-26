// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"strings"
	"testing"
)

func TestCleanForSpeech_DefaultsMatchPython(t *testing.T) {
	opts := DefaultCleanOptions()
	if opts.ReadCodeBlocks != false ||
		opts.ReadImages != true ||
		opts.ReadCitations != true ||
		opts.AddHeaderPauses != true ||
		opts.CitationFormat != "reference {n}" ||
		opts.CodeBlockPrefix != "Code block." ||
		opts.CodeBlockPlaceholder != "Code omitted." {
		t.Errorf("default options drifted from Python: %+v", opts)
	}
}

func TestHandleCitations_WebSearch(t *testing.T) {
	in := "Per [[cite:s1r1]], the answer is 42."
	out := CleanForSpeech(in)
	if !strings.Contains(out, "search 1 result 1") {
		t.Errorf("missing web-search verbalization: %q", out)
	}
	if strings.Contains(out, "[[cite") || strings.Contains(out, "s1r1") {
		t.Errorf("citation marker not stripped: %q", out)
	}
}

func TestHandleCitations_SingleCitations(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"see [[cite:search0]] for details", "source 1"},
		{"see [[cite:search1]] for details", "source 2"},
		{"see [[cite:research0]] for details", "research source 1"},
		{"see [[cite:file2]] for details", "file 3"},
		{"see [[cite:ref0]] for details", "reference 1"},
	}
	for _, tc := range cases {
		out := CleanForSpeech(tc.in)
		if !strings.Contains(out, tc.want) {
			t.Errorf("input %q → %q; want phrase %q", tc.in, out, tc.want)
		}
		if strings.Contains(out, "[[cite") {
			t.Errorf("input %q → %q; raw marker leaked", tc.in, out)
		}
	}
}

func TestHandleCitations_SingleBracketVariant(t *testing.T) {
	out := CleanForSpeech("see [cite:search0] for details")
	if !strings.Contains(out, "source 1") {
		t.Errorf("single-bracket variant not verbalized: %q", out)
	}
}

func TestHandleCitations_Multi(t *testing.T) {
	out := CleanForSpeech("based on [[cite:search0, search1, search2]] this works")
	for _, want := range []string{"source 1", "source 2", "source 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-cite output missing %q: %q", want, out)
		}
	}
	if !strings.Contains(out, "and source 3") {
		t.Errorf("multi-cite missing oxford-style 'and' separator: %q", out)
	}
}

func TestHandleCitations_SimpleNumeric(t *testing.T) {
	out := CleanForSpeech("see [3] for details")
	if !strings.Contains(out, "reference 3") {
		t.Errorf("simple [N] not verbalized: %q", out)
	}
}

func TestHandleCitations_StripWhenDisabled(t *testing.T) {
	opts := DefaultCleanOptions()
	opts.ReadCitations = false

	in := "First [[cite:search0]], second [[cite:s1r1]], third [3], done."
	out := CleanForSpeechWithOptions(in, opts)

	for _, leak := range []string{"cite:", "source", "reference", "search 1", "[3]"} {
		if strings.Contains(out, leak) {
			t.Errorf("ReadCitations=false leaked %q in output: %q", leak, out)
		}
	}
}

func TestPreserveOrderedListNumbers(t *testing.T) {
	in := "Steps:\n1. First step\n2. Second step\n3. Third step"
	out := CleanForSpeech(in)

	for _, want := range []string{"1, First step", "2, Second step", "3, Third step"} {
		if !strings.Contains(out, want) {
			t.Errorf("ordered-list number missing %q in: %q", want, out)
		}
	}
}

func TestCodeBlock_PrefixVsPlaceholder(t *testing.T) {
	in := "Try this:\n```go\nfmt.Println(\"hi\")\n```\nDone."

	def := CleanForSpeech(in)
	if !strings.Contains(def, "Code omitted.") {
		t.Errorf("default options should use placeholder, got: %q", def)
	}
	if strings.Contains(def, "fmt.Println") {
		t.Errorf("default options should not include code body, got: %q", def)
	}

	opts := DefaultCleanOptions()
	opts.ReadCodeBlocks = true
	announced := CleanForSpeechWithOptions(in, opts)
	if !strings.Contains(announced, "Code block.") {
		t.Errorf("ReadCodeBlocks=true should emit prefix, got: %q", announced)
	}
}

func TestReadImages_Toggle(t *testing.T) {
	in := "Logo: ![Solace Logo](https://example.com/logo.png) here."

	kept := CleanForSpeech(in)
	if !strings.Contains(kept, "Solace Logo") {
		t.Errorf("default ReadImages=true should keep alt text, got: %q", kept)
	}

	opts := DefaultCleanOptions()
	opts.ReadImages = false
	stripped := CleanForSpeechWithOptions(in, opts)
	if strings.Contains(stripped, "Solace Logo") {
		t.Errorf("ReadImages=false should drop alt text, got: %q", stripped)
	}
}

func TestHeaderPauses_Toggle(t *testing.T) {
	in := "# Hello\nworld"

	withPause := CleanForSpeech(in)
	if !strings.Contains(withPause, "Hello.") {
		t.Errorf("default AddHeaderPauses=true should append '.', got: %q", withPause)
	}

	opts := DefaultCleanOptions()
	opts.AddHeaderPauses = false
	noPause := CleanForSpeechWithOptions(in, opts)
	if strings.Contains(noPause, "Hello.") {
		t.Errorf("AddHeaderPauses=false should not append '.', got: %q", noPause)
	}
}

func TestHandleCitations_RunsBeforeLinkStrip(t *testing.T) {
	in := "Click [link](http://x) and see [[cite:search0]] too."
	out := CleanForSpeech(in)
	if !strings.Contains(out, "source 1") {
		t.Errorf("citation lost ordering vs link regex: %q", out)
	}
	if !strings.Contains(out, "link") {
		t.Errorf("link text dropped: %q", out)
	}
}
