// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type ProtocolAdapter interface {
	FetchAgentCard(ctx context.Context) (*a2a.AgentCard, error)

	ForwardRequest(ctx context.Context, task *TaskContext, req *a2a.Request) (<-chan ResponseEvent, error)

	CancelTask(ctx context.Context, task *TaskContext) error

	Close() error
}

var ErrAgentCardUntrusted = errors.New("agent card failed its signature policy")

var ErrAgentCardKeysUnavailable = errors.New("trusted keys for the agent card could not be fetched")

type ResponseEvent struct {
	StatusUpdate   *a2a.TaskStatusUpdateEvent
	ArtifactUpdate *a2a.TaskArtifactUpdateEvent
	Task           *a2a.Task
	Error          error
}

type TaskContext struct {
	TaskID           string
	SessionID        string
	TraceID          string
	ReplyToTopic     string
	StatusTopic      string
	UserID           string
	ClientID         string
	IsStreaming      bool
	DownstreamTaskID string
	UserProperties   map[string]string
	CreatedAt        time.Time
	Method           string

	streamedArtifactText  []string
	streamedArtifactIndex map[string]int

	OriginalRequest *a2a.Request
	CodeVerifier    string
	CredentialKey   string
	OAuth2Token     string

	remoteInterface atomic.Pointer[RemoteInterface]
}

func (tc *TaskContext) SetRemoteInterface(ri *RemoteInterface) {
	tc.remoteInterface.Store(ri)
}

func (tc *TaskContext) RemoteInterface() *RemoteInterface {
	return tc.remoteInterface.Load()
}

func (tc *TaskContext) addStreamedArtifactText(artifactID, text string, appendChunk bool) {
	if i, ok := tc.streamedArtifactIndex[artifactID]; ok && appendChunk && artifactID != "" {
		tc.streamedArtifactText[i] += text
		return
	}
	if artifactID != "" {
		if tc.streamedArtifactIndex == nil {
			tc.streamedArtifactIndex = make(map[string]int)
		}
		tc.streamedArtifactIndex[artifactID] = len(tc.streamedArtifactText)
	}
	tc.streamedArtifactText = append(tc.streamedArtifactText, text)
}

type RemoteInterface struct {
	URL     string
	Binding string
	Version string
	Tenant  string
}

const (
	RemoteBindingJSONRPC  = "JSONRPC"
	RemoteBindingHTTPJSON = "HTTP+JSON"
)
