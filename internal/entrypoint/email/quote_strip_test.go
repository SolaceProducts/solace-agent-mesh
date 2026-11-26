// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "testing"

func TestStripReplyQuote_OutlookHorizontalRule(t *testing.T) {
	in := `Hi SAM, can you summarize the latest deployment?

________________________________
From: SAM <sam-entrypoint@example.org>
Sent: Sunday, May 4, 2026 12:00 PM
Subject: Re: question

Sure thing!
`
	want := "Hi SAM, can you summarize the latest deployment?"
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_OutlookFromBlock(t *testing.T) {
	in := `What's the status of the deployment?

From: SAM <sam-entrypoint@example.org>
Sent: Sunday, May 4, 2026 12:00 PM
To: jordan.walsh@example.org
Subject: Re: status

Earlier reply text.
`
	want := "What's the status of the deployment?"
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_GmailWroteLine(t *testing.T) {
	in := `My follow-up question is here.

On Sun, May 4, 2026 at 12:00 PM SAM <sam@x.com> wrote:
> Earlier reply
> More earlier reply
`
	want := "My follow-up question is here."
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_OriginalMessageSeparator(t *testing.T) {
	in := `New question for the agent.

-----Original Message-----
From: SAM
Subject: Re: x

Earlier content here.
`
	want := "New question for the agent."
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_QuotedLines(t *testing.T) {
	in := `My follow-up.

> Earlier reply line 1
> Earlier reply line 2
`
	want := "My follow-up."
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_SingleQuoteLineNotStripped(t *testing.T) {
	in := `Question: what does the > sign mean?
> only this one line uses it
The rest is normal.`
	if got := stripReplyQuote(in); got != in {
		t.Errorf("got %q; want unchanged input (only one > line)", got)
	}
}

func TestStripReplyQuote_NoBoundary(t *testing.T) {
	in := "Hello SAM, please tell me the time in Tokyo."
	if got := stripReplyQuote(in); got != in {
		t.Errorf("got %q; want unchanged %q", got, in)
	}
}

func TestStripReplyQuote_AllQuoteFallback(t *testing.T) {
	in := "> Just a quote\n> Another quote\n"
	if got := stripReplyQuote(in); got != in {
		t.Errorf("got %q; want unchanged input %q (verbatim fallback)", got, in)
	}
}

func TestStripReplyQuote_CRLFLineEndings(t *testing.T) {
	in := "Hi.\r\n\r\n________________________________\r\nFrom: SAM\r\nSent: Sun\r\nSubject: x\r\n"
	want := "Hi."
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_EarliestBoundaryWins(t *testing.T) {
	in := `Real content here.

From: SAM
Sent: Sun
Subject: Re

Earlier reply.

> additional quoted thing
> more
`
	want := "Real content here."
	if got := stripReplyQuote(in); got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStripReplyQuote_EmptyInput(t *testing.T) {
	if got := stripReplyQuote(""); got != "" {
		t.Errorf("got %q; want empty string", got)
	}
}

func TestStripReplyQuote_FromColonInProseDoesNotStrip(t *testing.T) {
	in := `From the article I read, the recommendation is unclear.
Can you clarify?`
	if got := stripReplyQuote(in); got != in {
		t.Errorf("got %q; want unchanged input (From: in prose, no Sent:)", got)
	}
}
