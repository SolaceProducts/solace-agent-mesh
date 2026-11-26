// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"encoding/json"
	"strings"
)

type Session struct {
	ID              string  `db:"id"`
	Name            *string `db:"name"`
	UserID          string  `db:"user_id"`
	AgentID         *string `db:"agent_id"`
	ProjectID       *string `db:"project_id"`
	CreatedTime     int64   `db:"created_time"`
	UpdatedTime     int64   `db:"updated_time"`
	DeletedAt       *int64  `db:"deleted_at"`
	DeletedBy       *string `db:"deleted_by"`
	Source          *string `db:"source"`
	LastViewedAt    *int64  `db:"last_viewed_at"`
	HasActivity     bool    `db:"has_activity"`
	ExternalGateway bool    `db:"external_gateway"`
}

type GatewaySessionKey struct {
	GatewayType string `db:"gateway_type"`
	ExternalKey string `db:"external_key"`
	SessionID   string `db:"session_id"`
	CreatedTime int64  `db:"created_time"`
}

const (
	GatewayTypeSlack      = "slack"
	GatewayTypeTeams      = "teams"
	GatewayTypeEmail      = "email"
	GatewayTypeMCP        = "mcp"
	GatewayTypeScheduled  = "scheduled"
	GatewayTypeWhatsApp   = "whatsapp"
	GatewayTypeGoogleChat = "google_chat"
)

const (
	EntrypointTypeWebUI      = "webui"
	EntrypointTypeSlack      = "slack"
	EntrypointTypeTeamsBot   = "teams_bot"
	EntrypointTypeWhatsApp   = "whatsapp"
	EntrypointTypeGoogleChat = "google_chat"
	EntrypointTypeUnknown    = "unknown"
)

const TaskUserUnknown = "unknown"

type ChatTask struct {
	ID             string  `db:"id"`
	SessionID      string  `db:"session_id"`
	UserID         string  `db:"user_id"`
	UserMessage    *string `db:"user_message"`
	MessageBubbles string  `db:"message_bubbles"`
	TaskMetadata   *string `db:"task_metadata"`
	CreatedTime    int64   `db:"created_time"`
	UpdatedTime    *int64  `db:"updated_time"`
	Kind           string  `db:"kind"`
}

const (
	ChatTaskKindNormal       = "task"
	ChatTaskKindNotification = "compaction_notification"
)

type Task struct {
	ID                            string  `db:"id"`
	UserID                        string  `db:"user_id"`
	ParentTaskID                  *string `db:"parent_task_id"`
	StartTime                     int64   `db:"start_time"`
	EndTime                       *int64  `db:"end_time"`
	Status                        *string `db:"status"`
	InitialRequestText            *string `db:"initial_request_text"`
	TotalInputTokens              *int    `db:"total_input_tokens"`
	TotalOutputTokens             *int    `db:"total_output_tokens"`
	TotalCachedInputTokens        *int    `db:"total_cached_input_tokens"`
	TokenUsageDetails             *string `db:"token_usage_details"`
	ExecutionMode                 string  `db:"execution_mode"`
	LastActivityTime              *int64  `db:"last_activity_time"`
	BackgroundExecutionEnabled    bool    `db:"background_execution_enabled"`
	MaxExecutionTimeMs            *int64  `db:"max_execution_time_ms"`
	SessionID                     *string `db:"session_id"`
	EventsBuffered                bool    `db:"events_buffered"`
	EventsConsumed                bool    `db:"events_consumed"`
	Kind                          string  `db:"kind"`
	PostCompactionRemainingTokens *int    `db:"post_compaction_remaining_tokens"`
	AgentName                     *string `db:"agent_name"`
	ErrorMessage                  *string `db:"error_message"`
	HandlerName                   *string `db:"handler_name"`
	HandlerType                   *string `db:"handler_type"`
	TriggerKind                   *string `db:"trigger_kind"`
	Channel                       *string `db:"channel"`
	EntrypointID                  *string `db:"entrypoint_id"`
	EntrypointName                *string `db:"entrypoint_name"`
	TriggerSource                 *string `db:"trigger_source"`
}

type TaskTriggerSource struct {
	Route      string `json:"route,omitempty"`
	Event      string `json:"event,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
}

func EncodeTaskTriggerSource(source TaskTriggerSource) *string {
	if source == (TaskTriggerSource{}) {
		return nil
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	text := string(encoded)
	return &text
}

func (t *Task) DecodedTriggerSource() TaskTriggerSource {
	var source TaskTriggerSource
	if t.TriggerSource != nil {
		_ = json.Unmarshal([]byte(*t.TriggerSource), &source)
	}
	return source
}

const (
	TaskKindNormal         = "task"
	TaskKindCompactionCost = "compaction-cost"
)

const (
	TaskHandlerTypeAgent    = "agent"
	TaskHandlerTypeWorkflow = "workflow"
)

type TaskEvent struct {
	ID               string  `db:"id"`
	TaskID           string  `db:"task_id"`
	UserID           *string `db:"user_id"`
	CreatedTime      int64   `db:"created_time"`
	Topic            string  `db:"topic"`
	Direction        string  `db:"direction"`
	Payload          string  `db:"payload"`
	IsDelta          bool    `db:"is_delta"`
	SignalType       *string `db:"signal_type"`
	AgentName        *string `db:"agent_name"`
	ToolName         *string `db:"tool_name"`
	EventSeq         *int64  `db:"event_seq"`
	PublisherID      *string `db:"publisher_id"`
	ConfigPromptBlob *string `db:"config_prompt_blob"`
	ConfigToolsBlob  *string `db:"config_tools_blob"`
	ForwardedFrom    *string `db:"forwarded_from"`
	TraceID          *string `db:"trace_id"`
}

type SSEEventBufferRow struct {
	TaskID          string `db:"task_id"`
	SessionID       string `db:"session_id"`
	UserID          string `db:"user_id"`
	EventSequence   int    `db:"event_sequence"`
	EventType       string `db:"event_type"`
	EventData       string `db:"event_data"`
	CreatedAt       int64  `db:"created_at"`
	Consumed        bool   `db:"consumed"`
	ConsumedAt      *int64 `db:"consumed_at"`
	BrokerMessageID string `db:"broker_message_id"`
}

const (
	SubjectTypeAgent    = "agent"
	SubjectTypeWorkflow = "workflow"
	SubjectTypeUnknown  = "unknown"
)

type Feedback struct {
	ID                   string  `db:"id"`
	TaskID               string  `db:"task_id"`
	UserID               string  `db:"user_id"`
	SessionID            string  `db:"session_id"`
	Rating               string  `db:"rating"`
	Comment              *string `db:"comment"`
	ShareContent         bool    `db:"share_content"`
	SubjectID            string  `db:"subject_id"`
	SubjectType          string  `db:"subject_type"`
	EntrypointType       string  `db:"entrypoint_type"`
	DetailsSubmittedTime *int64  `db:"details_submitted_time"`
	CreatedAt            int64   `db:"created_at"`
	UpdatedAt            int64   `db:"updated_at"`
	Inserted             bool    `db:"-"`
}

type Project struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	UserID         string  `db:"user_id"`
	Description    *string `db:"description"`
	SystemPrompt   *string `db:"system_prompt"`
	DefaultAgentID *string `db:"default_agent_id"`
	IsPinned       bool    `db:"is_pinned"`
	CreatedAt      int64   `db:"created_at"`
	UpdatedAt      *int64  `db:"updated_at"`
	DeletedAt      *int64  `db:"deleted_at"`
	DeletedBy      *string `db:"deleted_by"`
}

type ProjectUser struct {
	ID            string `db:"id"`
	ProjectID     string `db:"project_id"`
	UserID        string `db:"user_id"`
	Role          string `db:"role"`
	AddedAt       int64  `db:"added_at"`
	AddedByUserID string `db:"added_by_user_id"`
}

type ResourceAccessShare struct {
	ID                  string `db:"id"`
	ResourceID          string `db:"resource_id"`
	ResourceType        string `db:"resource_type"`
	SharedWithUserEmail string `db:"shared_with_user_email"`
	AccessLevel         string `db:"access_level"`
	SharedByUserEmail   string `db:"shared_by_user_email"`
	SharedAt            int64  `db:"shared_at"`
	UpdatedAt           *int64 `db:"updated_at"`
}

type PromptGroup struct {
	ID                 string  `db:"id"`
	Name               string  `db:"name"`
	Description        *string `db:"description"`
	Category           *string `db:"category"`
	Command            *string `db:"command"`
	UserID             string  `db:"user_id"`
	AuthorName         *string `db:"author_name"`
	ProductionPromptID *string `db:"production_prompt_id"`
	IsShared           bool    `db:"is_shared"`
	IsPinned           bool    `db:"is_pinned"`
	CreatedAt          int64   `db:"created_at"`
	UpdatedAt          int64   `db:"updated_at"`
}

type Prompt struct {
	ID          string  `db:"id"`
	PromptText  string  `db:"prompt_text"`
	GroupID     string  `db:"group_id"`
	UserID      string  `db:"user_id"`
	Version     int     `db:"version"`
	Name        *string `db:"name"`
	Description *string `db:"description"`
	Category    *string `db:"category"`
	Command     *string `db:"command"`
	CreatedAt   int64   `db:"created_at"`
	UpdatedAt   int64   `db:"updated_at"`
}

type SharedLink struct {
	ShareID               string  `db:"share_id"`
	SessionID             string  `db:"session_id"`
	UserID                string  `db:"user_id"`
	Title                 *string `db:"title"`
	IsPublic              bool    `db:"is_public"`
	RequireAuthentication bool    `db:"require_authentication"`
	AllowedDomains        *string `db:"allowed_domains"`
	CreatedTime           int64   `db:"created_time"`
	UpdatedTime           int64   `db:"updated_time"`
	SnapshotTime          int64   `db:"snapshot_time"`
	DeletedAt             *int64  `db:"deleted_at"`
}

func (s *SharedLink) GetAllowedDomainsList() []string {
	if s.AllowedDomains == nil || *s.AllowedDomains == "" {
		return []string{}
	}
	parts := splitCSV(*s.AllowedDomains)
	if parts == nil {
		return []string{}
	}
	return parts
}

func (s *SharedLink) GetAccessType(hasSharedUsers bool) string {
	if hasSharedUsers {
		return "user_specific"
	}
	if s.AllowedDomains != nil && *s.AllowedDomains != "" {
		return "domain_restricted"
	}
	return "authenticated"
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for part := range strings.SplitSeq(s, ",") {
		v := strings.TrimSpace(part)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

type SharedLinkUser struct {
	ID                  string  `db:"id"`
	ShareID             string  `db:"share_id"`
	UserEmail           string  `db:"user_email"`
	AccessLevel         string  `db:"access_level"`
	AddedAt             int64   `db:"added_at"`
	AddedByUserID       string  `db:"added_by_user_id"`
	OriginalAccessLevel *string `db:"original_access_level"`
	OriginalAddedAt     *int64  `db:"original_added_at"`
	ViewedAt            *int64  `db:"viewed_at"`
}

type SharedWithUserRow struct {
	ShareID     string  `db:"share_id"`
	SessionID   string  `db:"session_id"`
	OwnerUserID string  `db:"owner_user_id"`
	Title       *string `db:"title"`
	AccessLevel string  `db:"access_level"`
	AddedAt     int64   `db:"added_at"`
	ViewedAt    *int64  `db:"viewed_at"`
}

type SharedArtifact struct {
	ShareID         string `db:"share_id"`
	ArtifactURI     string `db:"artifact_uri"`
	ArtifactVersion *int64 `db:"artifact_version"`
	IsPublic        bool   `db:"is_public"`
	CreatedTime     int64  `db:"created_time"`
}
