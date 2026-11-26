// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
)

const Prefix = "/api/v2/chat"

const GateSignedIn apicontract.Gate = "signedIn"

var GateDocs = map[apicontract.Gate]string{
	GateSignedIn:          "any signed-in caller. No scope is required.",
	GateCaller:            "any signed-in caller. No scope is required. The answer describes that caller only. A request with no signed-in caller gets 401.",
	GatePreLogin:          "anyone, signed in or not. The answer describes the deployment and nothing about the caller.",
	GateAgentInvoke:       "any signed-in caller. Whether the caller may use the agent the message names is checked on send, and a caller who may not gets 403.",
	GateAgentVisible:      "any signed-in caller. The list holds only the agents the caller may invoke, so an agent the caller cannot use is left out and is not an error.",
	GateTaskStream:        "the user who started the task, or a caller holding the `activity:*:read` scope. Anyone else gets 404.",
	GateTaskInputReader:   "the user who started the task, or a caller holding the `activity:*:read` scope. Anyone else gets 204 with no body.",
	GateTaskReader:        "the user who started the task, or a caller holding the `activity:*:read` scope. Anyone else gets 404.",
	GateTaskTrace:         "the user who started the task, a caller holding the `activity:*:read` scope, or a caller whose agent trace grants cover every agent that took part in the task. Anyone else gets 404.",
	GateTaskOwner:         "the user who started the task. A read scope does not admit a caller. Anyone else gets 404.",
	GateSessionOwner:      "the user who owns the session. A read scope does not admit a caller. Anyone else gets 404.",
	GateArtifactOwner:     "the user who owns the session the file is stored in. A read scope does not admit a caller. Deleting a file in another user's session and copying from another user's session answer 404. An upload or a copy into a session id that is not the caller's writes to the caller's own storage and never reaches the other user's files.",
	GateArtifactReader:    "the user who owns the session the file is stored in or, for a file in a project, any user who may view that project. A download, a version or a read through a project the caller may not view answers 404, and a list of another user's session is empty.",
	GateScheduledArtifact: "the user who created the scheduled task that produced the file. Anyone else gets 404.",
}

func Access(rt apicontract.Route) openapigen.Access {
	doc, ok := GateDocs[rt.Gate]
	if !ok || doc == "" {
		panic(fmt.Sprintf("%s %s: gate %q has no entry in GateDocs", rt.Method, rt.Path, rt.Gate))
	}
	return openapigen.Access{Gate: doc}
}

const tagGroup = "Chat"

var configTag = apicontract.Tag{Name: "Chat Config", Group: tagGroup, Text: "Read what this gateway offers its chat users."}

type Deps struct {
	ReadConfig                func(ctx context.Context) (ChatConfig, error)
	ReadLoginConfig           func(ctx context.Context) (ChatLoginConfig, error)
	ReadUser                  func(ctx context.Context, req struct{}) (ChatUser, error)
	ReadUserCapabilities      func(ctx context.Context, req ReadUserCapabilitiesRequest) (ChatUserCapabilities, error)
	ReadTask                  func(ctx context.Context, taskID string) (Task, error)
	ExportTask                func(ctx context.Context, taskID string) (apicontract.Download, error)
	CancelTask                func(ctx context.Context, taskID string) (Task, error)
	Send                      func(w http.ResponseWriter, r *http.Request)
	StreamTask                func(w http.ResponseWriter, r *http.Request)
	ReadPendingInput          func(ctx context.Context, taskID string) (*PendingInput, error)
	AnswerInput               func(ctx context.Context, answer UserInputAnswer) (UserInputAnswered, error)
	ListSessions              func(ctx context.Context, req ListSessionsRequest) (apicontract.List[Session], error)
	SearchSessions            func(ctx context.Context, req SearchSessionsRequest) (apicontract.List[Session], error)
	CreateSession             func(ctx context.Context, req CreateSessionRequest) (Session, error)
	GetSession                func(ctx context.Context, req apicontract.IDRequest) (Session, error)
	UpdateSession             func(ctx context.Context, req UpdateSessionRequest) (Session, error)
	DeleteSession             func(ctx context.Context, req apicontract.IDRequest) (struct{}, error)
	SetSessionProject         func(ctx context.Context, req SessionProjectRequest) (Session, error)
	MarkSessionViewed         func(ctx context.Context, req apicontract.IDRequest) (Session, error)
	GenerateSessionTitle      func(ctx context.Context, req GenerateSessionTitleRequest) (Session, error)
	ListSessionMessages       func(ctx context.Context, req SessionMessagesRequest) (apicontract.List[SessionMessage], error)
	ReadUnconsumedEvents      func(ctx context.Context, req UnconsumedEventsRequest) (UnconsumedEvents, error)
	ReadContextUsage          func(ctx context.Context, req ContextUsageRequest) (ContextUsage, error)
	CompactSession            func(ctx context.Context, req CompactSessionRequest) (Compaction, error)
	SaveChatTask              func(ctx context.Context, req SaveChatTaskRequest) (SessionChatTask, error)
	ListChatTasks             func(ctx context.Context, req ListChatTasksRequest) (apicontract.List[SessionChatTask], error)
	ListAgents                func(ctx context.Context, req ListAgentsRequest) (apicontract.List[ChatAgent], error)
	UploadArtifact            func(ctx context.Context, req UploadArtifactRequest) (ChatArtifact, error)
	ListAllArtifacts          func(ctx context.Context, req ListAllArtifactsRequest) (apicontract.List[ChatArtifact], error)
	ListSessionArtifacts      func(ctx context.Context, req ListSessionArtifactsRequest) (apicontract.List[ChatArtifact], error)
	ListArtifactVersions      func(ctx context.Context, req ListArtifactVersionsRequest) (apicontract.List[ArtifactVersion], error)
	DownloadArtifact          func(ctx context.Context, req DownloadArtifactRequest) (apicontract.FileResponse, error)
	DownloadArtifactVersion   func(ctx context.Context, req DownloadArtifactVersionRequest) (apicontract.FileResponse, error)
	DownloadArtifactByURI     func(ctx context.Context, req DownloadArtifactByURIRequest) (apicontract.FileResponse, error)
	DeleteArtifact            func(ctx context.Context, req ArtifactRequest) (struct{}, error)
	CopyArtifacts             func(ctx context.Context, req CopyArtifactsRequest) (ArtifactCopyResult, error)
	DownloadScheduledArtifact func(ctx context.Context, req ScheduledArtifactRequest) (apicontract.FileResponse, error)
}

func Routes(deps Deps) []apicontract.Route {
	return slices.Concat(ConfigRoutes(deps), UserRoutes(deps), MessageRoutes(deps), TaskRoutes(deps), EventRoutes(deps), SessionRoutes(deps), AgentRoutes(deps), ArtifactRoutes(deps), ScheduledArtifactRoutes(deps))
}

func TagGroups() map[string]string { return apicontract.TagGroupsOf(Routes(Deps{})) }

func TagDescriptions() map[string]string { return apicontract.TagDescriptionsOf(Routes(Deps{})) }

func DocPackage() string { return configType().PkgPath() }
