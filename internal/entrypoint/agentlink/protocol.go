// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import "encoding/json"

const (
	subprotocol     = "sam-agent-link.v2"
	protocolVersion = 2
	headerAgent     = "Sam-Agent"

	typeWelcome      = "welcome"
	typeTask         = "task"
	typeCancel       = "cancel"
	typePromptAnswer = "prompt_answer"
	typePeerResult   = "peer_result"
	typePeers        = "peers"
	typeControl      = "control"
	typeError        = "error"

	typeConsume    = "consume"
	typeAck        = "ack"
	typeNack       = "nack"
	typeDescribe   = "describe"
	typeRetire     = "retire"
	typeEvent      = "event"
	typePrompt     = "prompt"
	typeResult     = "result"
	typePeerCall   = "peer_call"
	typePeerCancel = "peer_cancel"

	eventModelStarted  = "model_started"
	eventModelFinished = "model_finished"
	eventModelFailed   = "model_failed"
	eventToolStarted   = "tool_started"
	eventToolFinished  = "tool_finished"
	eventProgress      = "progress"
	eventText          = "text"
	eventThinking      = "thinking"

	stateCompleted = "completed"
	stateFailed    = "failed"
	stateCanceled  = "canceled"
	stateRejected  = "rejected"

	answerAnswered = "answered"
	answerCanceled = "canceled"
	answerTimedOut = "timed_out"

	sessionRun     = "run"
	sessionSession = "session"

	maxStringBytes           = 16384
	maxIDBytes               = 128
	maxNameBytes             = 256
	maxDescriptionBytes      = 4096
	maxSkills                = 32
	maxSkillDescriptionBytes = 1024
	maxFrameValues           = 16384
	msgTooManyValues         = "a message holds at most 16384 JSON values"
	msgNotALinkMessage       = "not a link message"

	closeProtocolError      = 4400
	closeCredentialsRevoked = 4401
	closeIdle               = 4408
	closeTooManyConns       = 4409
	closeMessageTooLarge    = 1009
	errCodeUnknownTask      = "unknown_task"
	errCodeBadMessage       = "bad_message"
	errCodeForbidden        = "forbidden"
	errCodeUnknownType      = "unknown_type"
	errCodeUnknownDeliver   = "unknown_delivery"
	errCodeLimit            = "limit"
	errCodePublishFailed    = "publish_failed"
	errCodePeerUnavailable  = "peer_unavailable"
)

type inFrame struct {
	Type           string           `json:"type"`
	Delivery       uint64           `json:"delivery"`
	TaskID         string           `json:"taskId"`
	Agent          json.RawMessage  `json:"agent"`
	Event          json.RawMessage  `json:"event"`
	RequestID      string           `json:"requestId"`
	TimeoutSeconds float64          `json:"timeoutSeconds"`
	Approval       *promptApproval  `json:"approval"`
	Questions      []promptQuestion `json:"questions"`
	Message        string           `json:"message"`
	State          string           `json:"state"`
	Text           string           `json:"text"`
	Files          []resultFile     `json:"files"`
	Data           json.RawMessage  `json:"data"`
	Error          *resultError     `json:"error"`
	ParentTaskID   string           `json:"parentTaskId"`
	SubtaskID      string           `json:"subtaskId"`
	Peer           string           `json:"peer"`
	CallID         string           `json:"callId"`
}

type promptApproval struct {
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
	Message string          `json:"message"`
}

type promptQuestion struct {
	Question    string         `json:"question"`
	Header      string         `json:"header"`
	Options     []promptOption `json:"options"`
	MultiSelect bool           `json:"multiSelect"`
}

type promptOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type resultFile struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Bytes    string `json:"bytes"`
}

type resultError struct {
	Category string `json:"category"`
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

type agentEvent struct {
	Kind       string          `json:"kind"`
	Model      string          `json:"model"`
	Text       string          `json:"text"`
	ToolCalls  []eventToolCall `json:"toolCalls"`
	Usage      *eventUsage     `json:"usage"`
	PricingKey string          `json:"pricingKey"`
	Category   string          `json:"category"`
	Provider   string          `json:"provider"`
	CallID     string          `json:"callId"`
	Name       string          `json:"name"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	Failed     bool            `json:"failed"`
	Done       bool            `json:"done"`
}

type eventToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type eventUsage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CachedInput int  `json:"cachedInput"`
	CacheWrite  *int `json:"cacheWrite"`
	Reasoning   int  `json:"reasoning"`
}

type describedAgent struct {
	DisplayName    string             `json:"displayName"`
	Description    string             `json:"description"`
	Version        string             `json:"version"`
	Skills         []describedSkill   `json:"skills"`
	Tools          []describedTool    `json:"tools"`
	RequiredScopes []string           `json:"requiredScopes"`
	InputSchema    map[string]any     `json:"inputSchema"`
	OutputSchema   map[string]any     `json:"outputSchema"`
	WelcomeMessage string             `json:"welcomeMessage"`
	Suggestions    []describedPrompt  `json:"suggestions"`
	Peers          []string           `json:"peers"`
	Streaming      bool               `json:"streaming"`
	Declared       *declaredFacts     `json:"declared"`
	Health         []describedProblem `json:"health"`
}

type describedSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type describedTool struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	RequiredScopes []string `json:"requiredScopes"`
}

type describedPrompt struct {
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	AutoSend bool   `json:"autoSend"`
}

type declaredFacts struct {
	Framework  string            `json:"framework"`
	Owner      string            `json:"owner"`
	Team       string            `json:"team"`
	SDK        declaredSDK       `json:"sdk"`
	DataAccess []declaredData    `json:"dataAccess"`
	MCPServers []declaredMCPLink `json:"mcpServers"`
}

type declaredSDK struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type declaredData struct {
	System         string `json:"system"`
	Classification string `json:"classification"`
	Access         string `json:"access"`
}

type declaredMCPLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type describedProblem struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

type outFrame struct {
	Type        string          `json:"type"`
	Protocol    int             `json:"protocol,omitempty"`
	Agent       string          `json:"agent,omitempty"`
	Connection  string          `json:"connection,omitempty"`
	MaxInflight int             `json:"maxInflight,omitempty"`
	Limits      *linkLimits     `json:"limits,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Delivery    uint64          `json:"delivery,omitempty"`
	TaskID      string          `json:"taskId,omitempty"`
	Redelivered bool            `json:"redelivered,omitempty"`
	Task        *taskObject     `json:"task,omitempty"`
	RequestID   string          `json:"requestId,omitempty"`
	Status      string          `json:"status,omitempty"`
	Approved    *bool           `json:"approved,omitempty"`
	Answers     []string        `json:"answers,omitempty"`
	SubtaskID   string          `json:"subtaskId,omitempty"`
	State       string          `json:"state,omitempty"`
	Text        *string         `json:"text,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Error       *peerError      `json:"error,omitempty"`
	Peers       *[]peerEntry    `json:"peers,omitempty"`
	Truncated   bool            `json:"truncated,omitempty"`
}

type errorFrame struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Ref       string `json:"ref,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Frame     string `json:"frame,omitempty"`
}

type linkLimits struct {
	MaxFrameBytes            int64 `json:"maxFrameBytes"`
	MaxStringBytes           int   `json:"maxStringBytes"`
	MaxIDBytes               int   `json:"maxIdBytes"`
	MaxNameBytes             int   `json:"maxNameBytes"`
	MaxToolCallsPerModelCall int   `json:"maxToolCallsPerModelCall"`
	MaxModelsPerTask         int   `json:"maxModelsPerTask"`
	MaxOpenPeerCalls         int   `json:"maxOpenPeerCalls"`
	MaxOpenPrompts           int   `json:"maxOpenPrompts"`
	MaxToolScopes            int   `json:"maxToolScopes"`
	MaxFailureMessageBytes   int   `json:"maxFailureMessageBytes"`
	MaxFrameValues           int   `json:"maxFrameValues"`
	MaxPromptQuestions       int   `json:"maxPromptQuestions"`
	MaxPromptOptions         int   `json:"maxPromptOptions"`
	MaxPromptTextBytes       int   `json:"maxPromptTextBytes"`
	MaxQuestionHeaderChars   int   `json:"maxQuestionHeaderChars"`
	MaxApprovalArgs          int   `json:"maxApprovalArgs"`
	MaxApprovalArgsBytes     int   `json:"maxApprovalArgsBytes"`
	MaxDescriptionBytes      int   `json:"maxDescriptionBytes"`
	MaxSkills                int   `json:"maxSkills"`
	MaxSkillDescriptionBytes int   `json:"maxSkillDescriptionBytes"`
	TaskTTLSeconds           int   `json:"taskTtlSeconds"`
	MaxCallDepth             int   `json:"maxCallDepth"`
	MaxConnectionsPerAgent   int   `json:"maxConnectionsPerAgent"`
	MaxToolPayloadBytes      int   `json:"maxToolPayloadBytes"`
}

type peerError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type peerEntry struct {
	Name        string           `json:"name"`
	DisplayName string           `json:"displayName"`
	Description string           `json:"description"`
	Skills      []describedSkill `json:"skills"`
	OnDemand    bool             `json:"onDemand"`
}

type taskObject struct {
	ContextID         string         `json:"contextId"`
	Text              string         `json:"text"`
	Files             []taskFile     `json:"files"`
	Input             any            `json:"input,omitempty"`
	Workflow          *taskWorkflow  `json:"workflow,omitempty"`
	SessionBehavior   string         `json:"sessionBehavior"`
	User              taskUser       `json:"user"`
	CallDepth         int            `json:"callDepth"`
	Trigger           string         `json:"trigger"`
	TraceID           string         `json:"traceId"`
	CalledBy          string         `json:"calledBy"`
	GrantedToolScopes []string       `json:"grantedToolScopes"`
	CanPromptUser     bool           `json:"canPromptUser"`
	RequestContext    taskRequestCtx `json:"requestContext"`
	Metadata          map[string]any `json:"metadata"`
}

type taskFile struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type taskWorkflow struct {
	Name   string `json:"name"`
	NodeID string `json:"nodeId"`
}

type taskUser struct {
	ID string `json:"id"`
}

type taskRequestCtx struct {
	SystemPurpose       string `json:"systemPurpose"`
	ResponseFormat      string `json:"responseFormat"`
	ProjectID           string `json:"projectId"`
	ConversationContext string `json:"conversationContext"`
	UserProfile         string `json:"userProfile"`
	UserTimezone        string `json:"userTimezone"`
}

type jsonrpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
