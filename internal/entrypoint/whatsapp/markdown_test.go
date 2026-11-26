// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"strings"
	"testing"
)

func TestRenderForWhatsApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "The capital of France is Paris.", "The capital of France is Paris."},
		{"whatsapp-native formatting is untouched", "*bold* _italic_ ~strike~ `code`\n> quote\n1. one\n- two", "*bold* _italic_ ~strike~ `code`\n> quote\n1. one\n- two"},

		{"heading becomes a bold line", "# Summary\nAll good.", "*Summary*\nAll good."},
		{"every heading level", "###### Deep", "*Deep*"},
		{"closing hashes dropped", "## Title ##", "*Title*"},
		{"bold inside a heading is not doubled", "## **Summary**", "*Summary*"},
		{"italic inside a heading survives", "## A *key* point", "*A _key_ point*"},
		{"hashtag is not a heading", "#launch day", "#launch day"},
		{"bare hash drops", "#\nnext", "next"},

		{"double-star bold", "This is **important** now.", "This is *important* now."},
		{"double-underscore bold", "This is __important__ now.", "This is *important* now."},
		{"intraword underscores are an identifier", "call foo__bar__baz", "call foo__bar__baz"},
		{"lone star emphasis stays whatsapp bold", "This is *key* now.", "This is *key* now."},
		{"arithmetic in a heading is not italic", "## Why 2*3*4 matters", "*Why 2*3*4 matters*"},
		{"bold italic", "***both***", "*_both_*"},
		{"italic nested in bold", "**a *b* c**", "*a _b_ c*"},
		{"two one-letter bold spans stay apart", "**a** and **b**", "*a* and *b*"},
		{"two one-digit bold spans stay apart", "Step **1** of **3**", "Step *1* of *3*"},
		{"two one-letter underscore bold spans stay apart", "__a__ and __b__", "*a* and *b*"},
		{"two one-letter bold italic spans stay apart", "***a*** and ***b***", "*_a_* and *_b_*"},
		{"one-letter bold spans in a heading", "## Option **A** vs **B**", "*Option A vs B*"},
		{"longer bold spans stay apart", "**ab** and **cd**", "*ab* and *cd*"},
		{"bold label", "**Note:** read this", "*Note:* read this"},
		{"strikethrough", "~~gone~~", "~gone~"},

		{"link becomes label and bare url", "See [the docs](https://example.com/docs) now.", "See the docs: https://example.com/docs now."},
		{"link whose label is its url", "[https://example.com](https://example.com)", "https://example.com"},
		{"link with a title", `[docs](https://example.com "Docs")`, "docs: https://example.com"},
		{"image", "![chart](https://example.com/c.png)", "chart: https://example.com/c.png"},
		{"autolink", "<https://example.com/a_b_c>", "https://example.com/a_b_c"},
		{"mailto autolink", "<mailto:ops@example.com>", "ops@example.com"},
		{"mailto link labelled with its address", "[ops@example.com](mailto:ops@example.com)", "ops@example.com"},
		{"mailto link with a label", "[email us](mailto:ops@example.com)", "email us: ops@example.com"},
		{"url underscores are not emphasis", "Go to https://example.com/a__b__c for it.", "Go to https://example.com/a__b__c for it."},
		{"url stars are not emphasis", "https://example.com/*x*", "https://example.com/*x*"},
		{"bold url", "**https://example.com**", "*https://example.com*"},
		{"link label keeps its formatting", "[**docs**](https://example.com)", "*docs*: https://example.com"},

		{"trailing paren after a bare url", "(see https://x.com).", "(see https://x.com)."},
		{"code span as a link target", "[x](`a`)", "x: `a`"},

		{"shelf runes in the input", "echo " + shelfOpen + "7" + shelfClose, "echo " + shelfOpen + "7" + shelfClose},
		{"shelf runes with an overflowing index", "x " + shelfOpen + "99999999999999999999" + shelfClose, "x " + shelfOpen + "99999999999999999999" + shelfClose},
		{"shelf runes beside a code span", "see `code` and " + shelfOpen + "0" + shelfClose, "see `code` and " + shelfOpen + "0" + shelfClose},
		{"shelf runes beside a url", "https://x.com/a " + shelfOpen + "0" + shelfClose + " **b**", "https://x.com/a " + shelfOpen + "0" + shelfClose + " *b*"},
		{"lone shelf runes", shelfClose + "1" + shelfOpen, shelfClose + "1" + shelfOpen},

		{"code span is untouched", "Run `**not bold**` and `[x](y)`.", "Run `**not bold**` and `[x](y)`."},
		{"fence loses its language tag", "```python\nprint('**x**')\n```", "```\nprint('**x**')\n```"},
		{"fence content is untouched", "```\n# not a heading\n| a | b |\n|---|---|\n\n\n```", "```\n# not a heading\n| a | b |\n|---|---|\n\n\n```"},
		{"tilde fence becomes backticks", "~~~\ncode\n~~~", "```\ncode\n```"},
		{"inline triple backticks are not a fence", "```inline``` then **bold**", "```inline``` then *bold*"},
		{"unclosed fence is closed", "```go\nfunc main() {}", "```\nfunc main() {}\n```"},
		{
			"fence indented under a list item",
			"1. Install:\n    ```bash\n    pip install foo **x**\n    * not a bullet\n    ```\n2. Run **it**",
			"1. Install:\n```\n    pip install foo **x**\n    * not a bullet\n```\n2. Run *it*",
		},
		{
			"fence nested in a tilde fence",
			"~~~md\n```js\nx\n```\n~~~\nafter **x**",
			"```\n" + brokenFence + "js\nx\n" + brokenFence + "\n```\nafter *x*",
		},
		{
			"fence nested in a longer backtick fence",
			"````\n```\ny\n```\n````",
			"```\n" + brokenFence + "\ny\n" + brokenFence + "\n```",
		},
		{"backticks inside a plain fence", "```\necho ```x```\n```", "```\necho " + brokenFence + "x" + brokenFence + "\n```"},
		{
			"deeper-indented fence inside a block is content",
			"```markdown\n1. Step:\n    ```\n    **x** and * y\n    ```\n```",
			"```\n1. Step:\n    " + brokenFence + "\n    **x** and * y\n    " + brokenFence + "\n```",
		},
		{
			"deep-indented backticks with no closer are prose",
			"Para line one\n    ```\nstill para **x**",
			"Para line one\n    ```\nstill para *x*",
		},
		{
			"tab-indented fence under a list item",
			"- Run:\n\t```sh\n\tmake **all**\n\t```",
			"- Run:\n```\n\tmake **all**\n```",
		},

		{"star bullets", "* one\n* two", "- one\n- two"},
		{"plus bullets", "+ one", "- one"},
		{"nested bullet keeps indent", "- top\n  * nested **x**", "- top\n  - nested *x*"},
		{"thematic break drops", "Above\n\n---\n\nBelow", "Above\n\nBelow"},
		{"star thematic break drops", "Above\n\n* * *\n\nBelow", "Above\n\nBelow"},
		{"blank line runs collapse", "a\n\n\n\nb", "a\n\nb"},
		{"whitespace-only lines are blank", "a\n  \n\t\nb", "a\n\nb"},
		{"only rules and whitespace", "---\n  \n---", ""},
		{"crlf", "# Title\r\nbody", "*Title*\nbody"},

		{
			"one-column table",
			"| Fruit |\n|---|\n| apple |\n| pear |",
			"*Fruit*\n- apple\n- pear",
		},
		{
			"two-column table",
			"Settings:\n\n| Key | Value |\n|:---|---:|\n| **mode** | fast |\n| retries | 3 |\n\nDone.",
			"Settings:\n\n*Key: Value*\n- *mode*: fast\n- *retries*: 3\n\nDone.",
		},
		{
			"wide table becomes labelled blocks",
			"| Name | Role | Team |\n|---|---|---|\n| Ana | Lead | Core |\n| Bo |  | Edge |",
			"*Ana*\nRole: Lead\nTeam: Core\n\n*Bo*\nTeam: Edge",
		},
		{
			"table directly after prose gets a blank line",
			"Here:\n| a | b |\n|---|---|\n| 1 | 2 |",
			"Here:\n\n*a: b*\n- *1*: 2",
		},
		{
			"table cells get inline conversion",
			"| Tool | Link |\n|---|---|\n| sam | [docs](https://example.com) |",
			"*Tool: Link*\n- *sam*: docs: https://example.com",
		},
		{
			"escaped pipe stays in its cell",
			"| Op | Meaning |\n|---|---|\n| a \\| b | either |",
			"*Op: Meaning*\n- *a | b*: either",
		},
		{
			"html break in a cell",
			"| A | B |\n|---|---|\n| x | one<br>two |",
			"*A: B*\n- *x*: one two",
		},
		{
			"ragged rows are padded",
			"| A | B | C |\n|---|---|---|\n| x |",
			"*x*",
		},
		{
			"two-column header keeps the unit",
			"| Region | Revenue (USD M) |\n|---|---|\n| EMEA | 12 |",
			"*Region: Revenue (USD M)*\n- *EMEA*: 12",
		},
		{
			"headers-only table",
			"| a | b |\n|---|---|",
			"*a: b*",
		},
		{
			"a list item ends a table",
			"| a | b |\n|---|---|\n| 1 | 2 |\n- next | pipe",
			"*a: b*\n- *1*: 2\n\n- next | pipe",
		},
		{
			"a hashtag row does not end a table",
			"Rank | Name\n---|---\n#1 | Alice\n#2 | Bob",
			"*Rank: Name*\n- *#1*: Alice\n- *#2*: Bob",
		},
		{
			"a heading ends a table",
			"| a | b |\n|---|---|\n| 1 | 2 |\n## Next | part",
			"*a: b*\n- *1*: 2\n\n*Next | part*",
		},
		{
			"separator with the wrong cell count is not a table",
			"| a | b |\n|---|\n| 1 | 2 |",
			"| a | b |\n|---|\n| 1 | 2 |",
		},
		{
			"separator without dashes is not a table",
			"| a | b |\n| x | y |",
			"| a | b |\n| x | y |",
		},
		{
			"pipe prose without a separator is not a table",
			"Pick a | b\nthen go.",
			"Pick a | b\nthen go.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := renderForWhatsApp(tc.in); got != tc.want {
				t.Errorf("renderForWhatsApp(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			if again := renderForWhatsApp(tc.want); again != tc.want {
				t.Errorf("output is not a fixed point\n once: %q\ntwice: %q", tc.want, again)
			}
		})
	}
}

var brokenFence = "`" + zeroWidthSpace + "`" + zeroWidthSpace + "`"

func TestRejoinBacktickRunsUndoesTheBreak(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"```js", "echo ````x", "a `` b", "plain"} {
		if got := rejoinBacktickRuns(breakBacktickRuns(code)); got != code {
			t.Errorf("rejoin(break(%q)) = %q", code, got)
		}
	}
}

func TestTrimURLTrailer(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://x.com/a).":                            "https://x.com/a",
		"https://x.com/(a))":                           "https://x.com/(a)",
		"https://en.wikipedia.org/wiki/Foo_(bar)":      "https://en.wikipedia.org/wiki/Foo_(bar)",
		"https://x.com/**":                             "https://x.com/",
		"https://x.com/" + strings.Repeat(")", 50_000): "https://x.com/",
	} {
		if got := trimURLTrailer(url); got != want {
			t.Errorf("trimURLTrailer(%.40q) = %.40q, want %.40q", url, got, want)
		}
	}
}
