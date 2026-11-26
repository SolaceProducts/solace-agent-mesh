// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func auditRecords(records []slog.Record) []map[string]slog.Value {
	var out []map[string]slog.Value
	for _, r := range records {
		if r.Message != auditMsg {
			continue
		}
		attrs := map[string]slog.Value{}
		r.Attrs(func(at slog.Attr) bool {
			attrs[at.Key] = at.Value
			return true
		})
		out = append(out, attrs)
	}
	return out
}

func TestReply_EmitsOutboundAuditWithoutRawBody(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	logs := &recordCollector{}
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))

	const secretText = "the-quarterly-numbers-are-42"
	a.reply("", "15551230001", secretText)
	waitQueueDrained(t, a)

	audits := auditRecords(logs.all())
	if len(audits) != 1 {
		t.Fatalf("want one audit record, got %d", len(audits))
	}
	at := audits[0]
	if got := at["direction"].String(); got != "outbound" {
		t.Errorf("direction = %q", got)
	}
	if got := at["wamid"].String(); !strings.HasPrefix(got, "wamid.OUT-") {
		t.Errorf("wamid = %q, want the id the send returned", got)
	}
	if got := at["deliveryStatus"].String(); got != "sent" {
		t.Errorf("deliveryStatus = %q", got)
	}
	if got := at["contentLen"].Int64(); got != int64(len(secretText)) {
		t.Errorf("contentLen = %d, want %d", got, len(secretText))
	}
	for k, v := range at {
		if strings.Contains(v.String(), secretText) {
			t.Errorf("audit field %q leaks the raw message body", k)
		}
		if strings.Contains(v.String(), "15551230001") {
			t.Errorf("audit field %q leaks the raw phone number", k)
		}
	}
}

func TestAuditOutbound_FailureCarriesMetaErrorCode(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	logs := &recordCollector{}
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))

	sendErr := &graphAPIError{Op: "send", HTTPStatus: 400, Attempts: 1, GraphErr: graphError{Code: 131047}}
	a.auditOutbound(&store.WhatsAppOutbound{Recipient: "15551230001"}, "text", "", []byte("hi"), "", sendErr)

	audits := auditRecords(logs.all())
	if len(audits) != 1 {
		t.Fatalf("want one audit record, got %d", len(audits))
	}
	if got := audits[0]["deliveryStatus"].String(); got != "failed" {
		t.Errorf("deliveryStatus = %q", got)
	}
	if got := audits[0]["metaErrorCode"].Int64(); got != 131047 {
		t.Errorf("metaErrorCode = %d, want 131047", got)
	}
}

func TestAuditInbound_FieldsAndFingerprint(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	logs := &recordCollector{}
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))

	msg := &inboundMessage{ID: "wamid.IN1", Type: "text", From: "15551230001"}
	contentLen, contentSHA := summarizeParts([]a2a.Part{a2a.NewTextPart("hello agent")})
	a.auditInbound(msg, inboundAudit{
		userID: "whatsapp:15551230001", agentName: "Builder",
		taskID: "task-1", traceID: "trace-1", sessionID: "sess-1",
		contentLen: contentLen, contentSHA: contentSHA,
	})

	audits := auditRecords(logs.all())
	if len(audits) != 1 {
		t.Fatalf("want one audit record, got %d", len(audits))
	}
	at := audits[0]
	for k, want := range map[string]string{
		"direction": "inbound", "wamid": "wamid.IN1", "messageType": "text",
		"agentName": "Builder", "taskID": "task-1", "traceID": "trace-1", "sessionID": "sess-1",
	} {
		if got := at[k].String(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	fp := at["userFP"].String()
	if fp == "" || strings.Contains(fp, "15551230001") {
		t.Errorf("userFP = %q; want non-empty fingerprint with no raw phone", fp)
	}
	if at["contentSHA"].String() == "" || at["contentLen"].Int64() != int64(len("hello agent")) {
		t.Errorf("content ref = %q/%d", at["contentSHA"].String(), at["contentLen"].Int64())
	}
}

func TestSendMedia_EmitsOutboundAudit(t *testing.T) {
	t.Parallel()
	logs := &recordCollector{}
	a, m := newOutboundArtifactRig(t, slog.New(logs))

	target := outboundTarget{recipient: "15551230001", taskID: "task-9",
		userID: "user", agentName: "Builder", sessionID: "sess"}
	if !a.queueArtifact(target, "chart.png", "chart.png", nil, "image/png") {
		t.Fatal("queueArtifact refused the file")
	}
	if uploads, _ := m.snapshot(); uploads != 1 {
		t.Fatalf("expected the queued artifact to be uploaded once, got %d", uploads)
	}

	audits := auditRecords(logs.all())
	if len(audits) != 1 {
		t.Fatalf("want one audit record, got %d", len(audits))
	}
	at := audits[0]
	for k, want := range map[string]string{
		"direction": "outbound", "messageType": "image",
		"contentRef": "chart.png", "deliveryStatus": "sent", "taskID": "task-9",
	} {
		if got := at[k].String(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if at["wamid"].String() == "" {
		t.Error("wamid missing from media audit record")
	}
}

func TestGraphAPIError_OmitsRawBodyWhenEnvelopeParsed(t *testing.T) {
	t.Parallel()
	withEnvelope := &graphAPIError{
		Op: "send", HTTPStatus: 400, Attempts: 1,
		GraphErr: graphError{Code: 131047, Message: "Re-engagement message required"},
		Body:     `{"error":{"code":131047,"message":"Re-engagement message required","error_data":{"details":"to: 15551234567"}}}`,
	}
	if s := withEnvelope.Error(); strings.Contains(s, "15551234567") || !strings.Contains(s, "code 131047") {
		t.Errorf("parsed-envelope error must carry the code, not the raw body: %q", s)
	}
	unparsed := &graphAPIError{Op: "send", HTTPStatus: 502, Attempts: 1, Body: strings.Repeat("x", 500)}
	if s := unparsed.Error(); len(s) > 300 {
		t.Errorf("unparsed body must be truncated, got %d chars", len(s))
	}
}

func TestSummarizeParts_CoversTextAndFileBytes(t *testing.T) {
	t.Parallel()
	textOnly, shaText := summarizeParts([]a2a.Part{a2a.NewTextPart("abc")})
	if textOnly != 3 || shaText == "" {
		t.Fatalf("text-only = (%d, %q)", textOnly, shaText)
	}
	withFile, shaFile := summarizeParts([]a2a.Part{
		a2a.NewTextPart("abc"),
		{Kind: "file", File: &a2a.FileContent{Bytes: "QUJD"}},
	})
	if withFile != 6 || shaFile == shaText {
		t.Fatalf("text+file = (%d, %q); want decoded length 3+3 and a digest that changes with file bytes", withFile, shaFile)
	}
	_, shaSame := summarizeParts([]a2a.Part{a2a.NewTextPart("abc")})
	if shaSame != shaText {
		t.Error("digest must be stable for identical content")
	}
}

func TestAuditDigest_CapsLargePayloads(t *testing.T) {
	t.Parallel()
	prefix := strings.Repeat("A", auditDigestCap)
	a1 := []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: prefix + "TAIL-ONE"}}}
	a2p := []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: prefix + "TAIL-TWO-LONGER"}}}

	len1, sha1 := summarizeParts(a1)
	len2, sha2 := summarizeParts(a2p)
	if sha1 != sha2 {
		t.Error("payloads identical up to the cap must share a digest (cap not applied)")
	}
	if len1 == len2 {
		t.Error("lengths must still reflect the full payloads")
	}
	if contentDigest([]byte(prefix+"x")) != contentDigest([]byte(prefix+"y")) {
		t.Error("contentDigest must apply the same cap")
	}
	small1, small2 := contentDigest([]byte("abc")), contentDigest([]byte("abd"))
	if small1 == small2 {
		t.Error("sub-cap payloads must digest their full content")
	}
}
