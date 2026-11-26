// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturingProvider struct{ sent []*Message }

func (p *capturingProvider) Send(_ context.Context, msg *Message) (string, error) {
	p.sent = append(p.sent, msg)
	return "msg-1", nil
}
func (p *capturingProvider) TestConnection(context.Context) error { return nil }
func (p *capturingProvider) Close() error                         { return nil }

func dispatchSendEmail(t *testing.T, attachments string, versions ...string) (*tool.Result, *capturingProvider) {
	t.Helper()
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{
		AgentName: "test-agent", UserID: "test-user", SessionID: "test-session",
		ArtifactService: art,
	}
	loc := art.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, "report.txt")
	for _, content := range versions {
		_, err := art.Inner.Save(t.Context(), loc, []byte(content), "text/plain")
		require.NoError(t, err)
	}

	conn := mustTestConnector(t)
	provider := &capturingProvider{}
	conn.provider = provider
	set := tool.NewSet()
	require.NoError(t, set.Register(NewSendEmailTool(conn)))

	result, err := set.Dispatch(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "send_email",
		Arguments: `{"to": ["user@example.com"], "subject": "Report", "body": "Attached.", "attachments": ` + attachments + `}`,
	}, toolCtx)
	require.NoError(t, err)
	return result, provider
}

func TestSendEmail_DispatchSendsPinnedAttachment(t *testing.T) {
	result, provider := dispatchSendEmail(t, `["report.txt:0"]`, "mine", "a sibling's")

	require.Equal(t, tool.StatusSuccess, result.Status, result.Message)
	require.Len(t, provider.sent, 1)
	require.Len(t, provider.sent[0].Attachments, 1, "the attachment was dropped")
	assert.Equal(t, "report.txt", provider.sent[0].Attachments[0].Filename)
	assert.Equal(t, "mine", string(provider.sent[0].Attachments[0].Content))
	assert.Equal(t, "text/plain", provider.sent[0].Attachments[0].MIMEType)
}

func TestSendEmail_MalformedAttachmentsFail(t *testing.T) {
	conn := mustTestConnector(t)
	provider := &capturingProvider{}
	conn.provider = provider
	toolCtx := &tool.Context{AgentName: "a", UserID: "u", SessionID: "s"}

	result, err := NewSendEmailTool(conn).Execute(t.Context(), map[string]any{
		"to":          []any{"user@example.com"},
		"subject":     "Report",
		"body":        "Attached.",
		"attachments": []any{map[string]any{"filename": "report.txt"}},
	}, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Empty(t, provider.sent)
}

func TestSendEmail_DispatchMissingAttachmentFails(t *testing.T) {
	result, provider := dispatchSendEmail(t, `["nope.txt"]`, "mine")

	assert.Equal(t, tool.StatusError, result.Status)
	assert.Empty(t, provider.sent)
}

func TestLoadAttachments_MissingIsAttachmentError(t *testing.T) {
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{AgentName: "a", UserID: "u", SessionID: "s", ArtifactService: art}
	sendTool := NewSendEmailTool(mustTestConnector(t))

	_, err := sendTool.loadAttachments(t.Context(), []string{"nope.txt"}, toolCtx)
	var attachErr *AttachmentError
	require.ErrorAs(t, err, &attachErr)
	assert.Equal(t, "not_found", attachErr.Reason)
	assert.Equal(t, "nope.txt", attachErr.Filename)
	assert.Equal(t, `attachment "nope.txt" not found`, err.Error())

	result := mapError(err)
	assert.Equal(t, "not_found", result.Data["reason"])
	assert.NotContains(t, result.Data, "max_size_bytes", "a missing file has no size limit to report")
}
