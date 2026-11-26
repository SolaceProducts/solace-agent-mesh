// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh/solaceheaders"
)

type GatewayTypeDef struct {
	Type             string
	Name             string
	Description      string
	Warning          string
	YAMLTemplatePath string
	RequiredEnvVars  []string
	Fields           []FieldDef
}

type FieldDef struct {
	Name                  string
	Type                  string
	Label                 string
	Hint                  string
	Placeholder           string
	Order                 float64
	Default               any
	Required              bool
	Secret                bool
	UIVisible             bool
	RuntimeKey            string
	OneOf                 []FieldOption
	Pattern               string
	MinLen                int
	MaxLen                int
	MinValue              *int
	MaxValue              *int
	MinItems              int
	ConditionalDisplay    map[string]any
	RowGroup              string
	ClearWhenHidden       bool
	ExtraValidation       map[string]any
	Inner                 []FieldDef
	AcceptStringShorthand bool
	StringShorthandField  string
}

type FieldOption struct {
	Value       string
	Label       string
	Description string
}

func LookupGatewayType(t string) (GatewayTypeDef, bool) {
	for _, def := range allGatewayTypes {
		if def.Type == t {
			return def, true
		}
	}
	return GatewayTypeDef{}, false
}

func RegisteredGatewayTypes() []string {
	out := make([]string, 0, len(allGatewayTypes))
	for _, def := range allGatewayTypes {
		out = append(out, def.Type)
	}
	return out
}

func AllGatewayTypeDefs() []GatewayTypeDef {
	out := make([]GatewayTypeDef, len(allGatewayTypes))
	copy(out, allGatewayTypes)
	return out
}

func (d GatewayTypeDef) ToUISchemaMap() map[string]any {
	out := make(map[string]any, len(d.Fields))
	for _, f := range d.Fields {
		if !f.UIVisible {
			continue
		}
		out[f.Name] = f.toUIFieldEntry()
	}
	return out
}

func (f FieldDef) toUIFieldEntry() map[string]any {
	m := map[string]any{
		"label":    f.Label,
		"type":     f.Type,
		"required": f.Required,
		"secret":   f.Secret,
		"order":    f.Order,
	}
	if f.Hint != "" {
		m["hint"] = f.Hint
	}
	if f.Placeholder != "" {
		m["placeholder"] = f.Placeholder
	}
	if f.Default != nil {
		m["default"] = f.Default
	}
	if len(f.OneOf) > 0 {
		opts := make([]map[string]any, 0, len(f.OneOf))
		for _, o := range f.OneOf {
			label := o.Label
			if label == "" {
				label = o.Value
			}
			entry := map[string]any{"value": o.Value, "label": label}
			if o.Description != "" {
				entry["description"] = o.Description
			}
			opts = append(opts, entry)
		}
		m["options"] = opts
	}
	if len(f.ConditionalDisplay) > 0 {
		if stringOnly, ok := conditionalDisplayAsStringMap(f.ConditionalDisplay); ok {
			m["conditionalDisplay"] = stringOnly
		} else {
			m["conditionalDisplay"] = f.ConditionalDisplay
		}
	}
	if f.ClearWhenHidden {
		m["clearWhenHidden"] = true
	}
	if f.RowGroup != "" {
		m["rowGroup"] = f.RowGroup
	}
	validation := map[string]any{}
	if f.Pattern != "" {
		validation["pattern"] = f.Pattern
	}
	if f.MinLen > 0 {
		validation["minLength"] = f.MinLen
	}
	if f.MaxLen > 0 {
		validation["maxLength"] = f.MaxLen
	}
	if f.MinValue != nil {
		validation["min"] = *f.MinValue
	}
	if f.MaxValue != nil {
		validation["max"] = *f.MaxValue
	}
	if f.MinItems > 0 {
		validation["minItems"] = f.MinItems
	}
	maps.Copy(validation, f.ExtraValidation)
	if len(validation) > 0 {
		m["validation"] = validation
	}
	if itemEntry, ok := f.singleStringItemEntry(); ok {
		maps.Copy(m, itemEntry)
	}
	return m
}

func (f FieldDef) singleStringItemEntry() (map[string]any, bool) {
	if f.Type != fieldTypeArrayObject || len(f.Inner) != 1 {
		return nil, false
	}
	inner := f.Inner[0]
	if inner.Type != "string" {
		return nil, false
	}
	entry := map[string]any{"itemKey": inner.Name}
	if inner.Label != "" {
		entry["itemLabel"] = inner.Label
	}
	itemValidation := map[string]any{}
	if inner.MinLen > 0 {
		itemValidation["minLength"] = inner.MinLen
	}
	if inner.MaxLen > 0 {
		itemValidation["maxLength"] = inner.MaxLen
	}
	if inner.Pattern != "" {
		itemValidation["pattern"] = inner.Pattern
	}
	if len(itemValidation) > 0 {
		entry["itemValidation"] = itemValidation
	}
	return entry, true
}

func conditionalDisplayAsStringMap(m map[string]any) (map[string]string, bool) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out[k] = s
	}
	return out, true
}

func (d GatewayTypeDef) SortedFields() []FieldDef {
	out := make([]FieldDef, len(d.Fields))
	copy(out, d.Fields)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

const noLenCap = -1

const fieldTypeArrayObject = "array<object>"

const labelDefaultUserIdentity = "Default user identity"

const (
	labelRunAs = "Run as service account"
	hintRunAs  = "Service account this entrypoint's requests run as. Leave empty for the built-in default service account, which can invoke any agent or workflow; name a scoped service account to narrow what this entrypoint can reach."
)

const MaxPromptFieldLen = 4000

const (
	labelSystemPurpose  = "System purpose"
	hintSystemPurpose   = "Describes to agents what this entrypoint is for. Stamped onto every task this entrypoint submits and available to agent prompts as system_purpose."
	labelResponseFormat = "Response format"
	hintResponseFormat  = "Tells agents how to shape their replies for this entrypoint (length, tone, markdown support). Stamped onto every task this entrypoint submits and available to agent prompts as response_format."
)

const assistantContextOrder = 900

func assistantContextFields() []FieldDef {
	return []FieldDef{
		{
			Name:       "systemPurpose",
			Type:       "textarea",
			Label:      labelSystemPurpose,
			Hint:       hintSystemPurpose,
			Order:      assistantContextOrder,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     MaxPromptFieldLen,
			RuntimeKey: "system_purpose",
		},
		{
			Name:       "responseFormat",
			Type:       "textarea",
			Label:      labelResponseFormat,
			Hint:       hintResponseFormat,
			Order:      assistantContextOrder + 0.1,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     MaxPromptFieldLen,
			RuntimeKey: "response_format",
		},
	}
}

var allGatewayTypes = []GatewayTypeDef{
	withAssistantContext(eventMeshType),
	withAssistantContext(mcpType),
	withAssistantContext(webhookType),
	withAssistantContext(googleChatType),
	agentLinkType,
}

func withAssistantContext(def GatewayTypeDef) GatewayTypeDef {
	def.Fields = slices.Concat(def.Fields, assistantContextFields())
	return def
}

var webhookType = GatewayTypeDef{
	Type:             "webhook",
	Name:             "Webhook",
	Description:      "Accept inbound HTTP requests and dispatch each one to an agent or workflow",
	Warning:          "The route path becomes part of a publicly reachable URL under /gw/<id>/. Protect every route with a token or HMAC signature.",
	YAMLTemplatePath: "webhook_gateway.yaml",
	RequiredEnvVars:  []string{},
	Fields: []FieldDef{
		{
			Name:        "routePath",
			Type:        "string",
			Label:       "Route path",
			Hint:        "Path segment appended to /gw/<gateway_id>/. Must start with '/' and be URL-safe.",
			Placeholder: "/orders",
			Order:       1,
			UIVisible:   true,
			Required:    true,
			Pattern:     `^/[A-Za-z0-9/_.{}-]+$`,
			MinLen:      1,
			MaxLen:      512,
			RuntimeKey:  "route_path",
			ExtraValidation: map[string]any{
				"errorMsg": "Must start with '/' and contain only letters, digits, '-', '_', '.', '/', or {param} wildcards",
			},
		},
		{
			Name:      "method",
			Type:      "select",
			Label:     "HTTP method",
			Hint:      "Method the entrypoint accepts on the route.",
			Order:     2,
			Default:   "POST",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "POST"},
				{Value: "PUT"},
				{Value: "PATCH"},
			},
		},

		{
			Name:      "authMode",
			Type:      "select",
			Label:     "Authentication",
			Hint:      "How the entrypoint validates inbound requests. 'token' compares a shared bearer token; 'hmac' verifies a per-payload signature.",
			Order:     10,
			Default:   "token",
			UIVisible: true,
			Required:  true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "token", Label: "Shared token"},
				{Value: "hmac", Label: "HMAC signature"},
			},
			RuntimeKey: "auth_mode",
		},
		{
			Name:               "authToken",
			Type:               "password",
			Label:              "Shared token",
			Hint:               "Shared secret expected on every request. Token location selects where the caller carries it; the default is an Authorization: Bearer header.",
			Order:              11,
			UIVisible:          true,
			Secret:             true,
			Required:           true,
			MinLen:             8,
			MaxLen:             1024,
			ConditionalDisplay: map[string]any{"authMode": "token"},
			RuntimeKey:         "auth_token",
		},
		{
			Name:      "authTokenLocation",
			Type:      "select",
			Label:     "Token location",
			Hint:      "Where the caller carries the token. Use bearer for senders you control; header or query for senders that cannot set an Authorization header. A token in the query string is recorded by proxies and access logs — prefer a header where the sender allows one.",
			Order:     17,
			Default:   "bearer",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "bearer", Label: "Authorization: Bearer header"},
				{Value: "header", Label: "Custom header"},
				{Value: "query", Label: "Query parameter"},
			},
			ConditionalDisplay: map[string]any{"authMode": "token"},
			RuntimeKey:         "auth_token_location",
		},
		{
			Name:               "authTokenName",
			Type:               "string",
			Label:              "Token header or parameter name",
			Hint:               "Name of the header or query parameter carrying the token, for example X-Webhook-Token.",
			Placeholder:        "X-Webhook-Token",
			Order:              18,
			UIVisible:          true,
			Required:           true,
			MinLen:             1,
			MaxLen:             128,
			ConditionalDisplay: map[string]any{"authMode": "token", "authTokenLocation": []string{"header", "query"}},
			ClearWhenHidden:    true,
			RuntimeKey:         "auth_token_name",
		},
		{
			Name:               "hmacSecret",
			Type:               "password",
			Label:              "HMAC secret",
			Hint:               "Shared secret the sender uses to sign the payload.",
			Order:              12,
			UIVisible:          true,
			Secret:             true,
			Required:           true,
			MinLen:             16,
			MaxLen:             1024,
			ConditionalDisplay: map[string]any{"authMode": "hmac"},
			RuntimeKey:         "hmac_secret",
		},
		{
			Name:      "hmacAlgorithm",
			Type:      "select",
			Label:     "HMAC algorithm",
			Order:     13,
			Default:   "sha256",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "sha256"},
				{Value: "sha512"},
			},
			ConditionalDisplay: map[string]any{"authMode": "hmac"},
			RuntimeKey:         "hmac_algorithm",
		},
		{
			Name:               "hmacHeader",
			Type:               "string",
			Label:              "Signature header",
			Hint:               "HTTP header carrying the signature (e.g. X-Hub-Signature-256).",
			Order:              14,
			Default:            "X-Hub-Signature-256",
			UIVisible:          true,
			MinLen:             1,
			MaxLen:             128,
			ConditionalDisplay: map[string]any{"authMode": "hmac"},
			RuntimeKey:         "hmac_header",
		},
		{
			Name:               "hmacPrefix",
			Type:               "string",
			Label:              "Signature prefix",
			Hint:               "Prefix stripped from the signature header value before hex/base64 decode. GitHub-style senders use \"sha256=\"; leave empty if the header carries the raw signature.",
			Order:              15,
			Default:            "sha256=",
			UIVisible:          true,
			MinLen:             noLenCap,
			MaxLen:             32,
			ConditionalDisplay: map[string]any{"authMode": "hmac"},
			RuntimeKey:         "hmac_prefix",
		},
		{
			Name:    "hmacEncoding",
			Type:    "select",
			Label:   "Signature encoding",
			Hint:    "Encoding of the signature value in the header (after any prefix is stripped). Use hex for GitHub/GitLab; base64 for standard HMAC-SHA256 signers; base64url for PayPal-style URL-safe signers.",
			Order:   16,
			Default: "hex",
			OneOf: []FieldOption{
				{Value: "hex"},
				{Value: "base64"},
				{Value: "base64url"},
				{Value: "base64raw"},
				{Value: "base64rawurl"},
			},
			UIVisible:          true,
			MinLen:             noLenCap,
			MaxLen:             noLenCap,
			ConditionalDisplay: map[string]any{"authMode": "hmac"},
			RuntimeKey:         "hmac_encoding",
		},

		{
			Name:      "targetType",
			Type:      "select",
			Label:     "Target",
			Hint:      "Whether inbound requests are handled by an agent or by a workflow.",
			Order:     19,
			Default:   "agent",
			UIVisible: true,
			Required:  true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "agent", Label: "Agent"},
				{Value: "workflow", Label: "Workflow"},
			},
			RowGroup: "target",
		},
		{
			Name:               "targetAgentName",
			Type:               "agent_select",
			Label:              "Agent",
			Hint:               "Agent that handles inbound requests.",
			Order:              20,
			UIVisible:          true,
			Required:           true,
			MinLen:             noLenCap,
			MaxLen:             noLenCap,
			ConditionalDisplay: map[string]any{"targetType": "agent"},
			ClearWhenHidden:    true,
			RowGroup:           "target",
			RuntimeKey:         "target_agent_name",
		},
		{
			Name:               "targetWorkflowName",
			Type:               "workflow_select",
			Label:              "Workflow",
			Hint:               "Workflow that handles inbound requests.",
			Order:              21,
			UIVisible:          true,
			Required:           true,
			MinLen:             noLenCap,
			MaxLen:             noLenCap,
			ConditionalDisplay: map[string]any{"targetType": "workflow"},
			ClearWhenHidden:    true,
			RowGroup:           "target",
			RuntimeKey:         "target_workflow_name",
		},
		{
			Name:       "inputExpression",
			Type:       "textarea",
			Label:      "Input expression",
			Hint:       "sacexpr expression that builds the agent's input from the request. e.g. \"template:Order {{user_data.path:id}}: {{json://input.payload:}}\".",
			Order:      22,
			UIVisible:  true,
			Required:   true,
			MinLen:     1,
			MaxLen:     noLenCap,
			RuntimeKey: "input_expression",
		},

		{
			Name:       "runAs",
			Type:       "system_user_select",
			Label:      labelRunAs,
			Hint:       hintRunAs,
			Order:      30,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "run_as",
		},

		{
			Name:       "maxBodyBytes",
			Type:       "number",
			Label:      "Max body size (bytes)",
			Hint:       "Reject requests with a body larger than this. Defaults to 1 MiB.",
			Order:      40,
			Default:    1048576,
			UIVisible:  true,
			MinValue:   new(1024),
			MaxValue:   new(52428800),
			RuntimeKey: "max_body_bytes",
		},
	},
}

const AgentLinkGatewayType = "agent_link"

var agentLinkType = GatewayTypeDef{
	Type:             AgentLinkGatewayType,
	Name:             "Agent Link",
	Description:      "Let third-party agents built with the Agent Mesh link SDK join the mesh over one WebSocket each instead of the broker",
	Warning:          "Agents connect to /gw/<id>/v1/connect, each with its own token. Add link agents and choose this entrypoint for them; they are not part of these settings.",
	YAMLTemplatePath: "agent_link_gateway.yaml",
	RequiredEnvVars:  []string{},
	Fields: []FieldDef{
		{
			Name:       "maxFrameBytes",
			Type:       "number",
			Label:      "Max message size (bytes)",
			Hint:       "Largest WebSocket message in either direction. A larger message from an agent closes its connection. Defaults to 8 MiB.",
			Order:      1,
			Default:    8388608,
			UIVisible:  true,
			MinValue:   new(1024),
			MaxValue:   new(67108864),
			RuntimeKey: "max_frame_bytes",
		},
		{
			Name:       "maxInflight",
			Type:       "number",
			Label:      "Max unacknowledged tasks per connection",
			Hint:       "Tasks delivered to one connection that it has not acknowledged yet. Defaults to 16.",
			Order:      2,
			Default:    16,
			UIVisible:  true,
			MinValue:   new(1),
			MaxValue:   new(1024),
			RuntimeKey: "max_inflight",
		},
		{
			Name:       "maxConnectionsPerAgent",
			Type:       "number",
			Label:      "Max connections per agent",
			Hint:       "Connections one agent may hold open at once. Another one is closed. Defaults to 8.",
			Order:      3,
			Default:    8,
			UIVisible:  true,
			MinValue:   new(1),
			MaxValue:   new(256),
			RuntimeKey: "max_connections_per_agent",
		},
		{
			Name:       "maxConnectsPerMinute",
			Type:       "number",
			Label:      "Max connects per minute",
			Hint:       "Connections one agent may open per minute. More are refused with HTTP 429. Defaults to 60.",
			Order:      4,
			Default:    60,
			UIVisible:  true,
			MinValue:   new(1),
			MaxValue:   new(6000),
			RuntimeKey: "max_connects_per_minute",
		},
		{
			Name:       "taskTtlSeconds",
			Type:       "number",
			Label:      "Task time-to-live (seconds)",
			Hint:       "How long a delivered task may stay unanswered before it times out. Defaults to 7200.",
			Order:      5,
			Default:    7200,
			UIVisible:  true,
			MinValue:   new(1),
			MaxValue:   new(604800),
			RuntimeKey: "task_ttl_seconds",
		},
	},
}

const GoogleChatSubscriptionPattern = `^projects/[^/\s]+/subscriptions/[^/\s]+$`

var googleChatType = GatewayTypeDef{
	Type:             "google_chat",
	Name:             "Google Chat",
	Description:      "Connect your AI agent to Google Chat through a Google Workspace Chat app",
	Warning:          "Requires a Google Workspace Chat app and a service account for it.",
	YAMLTemplatePath: "google_chat_gateway.yaml",
	RequiredEnvVars:  []string{},
	Fields: []FieldDef{
		{
			Name:        "pubsub_subscription",
			Type:        "string",
			Label:       "Pub/Sub subscription",
			Hint:        "Full resource name of the subscription the Chat app publishes its events to.",
			Placeholder: "projects/my-project/subscriptions/chat-events",
			Order:       4,
			UIVisible:   false,
			Required:    true,
			Pattern:     GoogleChatSubscriptionPattern,
			MinLen:      1,
			MaxLen:      512,
			ExtraValidation: map[string]any{
				"errorMsg":    "Must be projects/{project}/subscriptions/{subscription}",
				"requiredMsg": "Pub/Sub subscription is required. Use the full resource name, projects/{project}/subscriptions/{subscription}.",
			},
			ConditionalDisplay: map[string]any{"delivery": "pubsub"},
		},
		{
			Name:      "service_account_key",
			Type:      "password",
			Label:     "Service account key",
			Hint:      "JSON key of your own service account, which posts replies and, with Pub/Sub delivery, pulls the subscription. Leave empty to use this deployment's Google identity (Workload Identity on Google Cloud).",
			Order:     5,
			UIVisible: true,
			Secret:    true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "default_agent_name",
			Type:      "agent_select",
			Label:     "Default Agent",
			Hint:      "The agent that handles messages when no specific agent is mentioned.",
			Order:     6,
			Default:   BuiltinOrchestratorName,
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:        "allowed_email_domains",
			Type:        "string",
			Label:       "Allowed email domains",
			Hint:        "Comma-separated Google Workspace domains whose users may message this app. When empty, every domain is allowed; otherwise senders from any other domain are refused before their identity is looked up.",
			Placeholder: "example.com, example.org",
			Order:       7,
			UIVisible:   true,
			MinLen:      noLenCap,
			MaxLen:      noLenCap,
		},
		{
			Name:      "use_user_identity_in_channels",
			Type:      "boolean",
			Label:     "Use real user identity in spaces",
			Hint:      "When enabled, multi-party space messages keep the sending user's identity instead of the run-as Agent Mesh service account. Direct messages always run as the sender.",
			Order:     6.5,
			Default:   false,
			UIVisible: true,
		},
		{
			Name:      "feedback_enabled",
			Type:      "boolean",
			Label:     "Collect feedback",
			Hint:      "Offer thumbs up and down buttons on each answer and record the ratings.",
			Order:     9,
			Default:   false,
			UIVisible: false,
		},
		{
			Name:      "thread_history_limit",
			Type:      "number",
			Label:     "Thread history",
			Hint:      "How many earlier messages of a space thread the agent sees when the app is mentioned in it, up to 100. 0 turns this off. A Google Workspace administrator must first approve the app's chat.app.messages.readonly scope.",
			Order:     10,
			Default:   0,
			UIVisible: true,
			MinValue:  new(0),
			MaxValue:  new(100),
		},
		{
			Name:        "artifact_drive_folder_id",
			Type:        "string",
			Label:       "Drive folder for files",
			Hint:        "A shared drive, or a folder in one, where files the agent makes are put and shared with the person who asked for them, because a Chat app cannot attach files to a message. Use a drive just for this: everyone who can open it can open every file in it, and files stay there after the person's access ends. The app's service account needs to add files to it and to delete or trash them. Nothing is put there for a task that runs as an Agent Mesh service account, as a task in a shared space does unless \"Use real user identity in spaces\" is on; with it on, the link to the file is posted where the whole space can see it. Leave empty to name the files in the reply with a link to the web UI instead.",
			Placeholder: "0AB1cD2eF3gH4iJ5kL",
			Order:       11,
			UIVisible:   true,
			MinLen:      noLenCap,
			MaxLen:      noLenCap,
		},
		{
			Name:      "artifact_drive_access_days",
			Type:      "number",
			Label:     "File access (days)",
			Hint:      "How many days the person who asked for a file can open it in Drive, from 1 to 365. Only used with a Drive folder.",
			Order:     12,
			Default:   30,
			UIVisible: false,
			MinValue:  new(1),
			MaxValue:  new(365),
		},
		{
			Name:      "delivery",
			Type:      "select",
			Label:     "Delivery",
			Hint:      "How Google Chat delivers events. HTTPS supports every feature but needs this entrypoint reachable from Google over public HTTPS. Cloud Pub/Sub needs no inbound access, but a Chat app built as a Google Workspace add-on gets no buttons over it.",
			Order:     1,
			Default:   "https",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "https", Label: "HTTPS endpoint"},
				{Value: "pubsub", Label: "Cloud Pub/Sub"},
			},
		},
		{
			Name:      "auth_audience_type",
			Type:      "select",
			Label:     "Authentication audience",
			Hint:      "HTTPS delivery only: the Chat app's authentication audience. Endpoint URL (the default, and the only option for an add-on Chat app) verifies each request against endpoint_url; project number verifies it against project_number.",
			Order:     26,
			Default:   "endpoint_url",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "project_number", Label: "Project number"},
				{Value: "endpoint_url", Label: "Endpoint URL"},
			},
		},
		{
			Name:      "endpoint_url",
			Type:      "string",
			Label:     "Endpoint URL",
			Hint:      "The https:// URL Google Chat posts events to, ending in /events. Leave empty to use this entrypoint's own address, shown on its Network tab after you create it; enter that address as the Chat app's HTTP endpoint URL.",
			Order:     2,
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "addon_service_account_email",
			Type:      "string",
			Label:     "Add-on service account",
			Hint:      "For a Chat app built as a Google Workspace add-on: the Service account email shown under the app's Connection settings, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com. Leave empty for a Chat app without add-on features.",
			Order:     3,
			UIVisible: true,
			Pattern:   `^service-[0-9]+@gcp-sa-gsuiteaddons\.iam\.gserviceaccount\.com$`,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			ExtraValidation: map[string]any{
				"errorMsg": "Must be the add-on's Google-managed account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
			},
		},
		{
			Name:      "project_number",
			Type:      "string",
			Label:     "Project number",
			Hint:      "Google Cloud project number of the Chat app.",
			Order:     21,
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "run_as",
			Type:      "system_user_select",
			Label:     labelRunAs,
			Hint:      "Agent Mesh service account that messages in shared spaces run as. Leave empty for the built-in default service account; name a scoped service account to narrow what shared spaces can reach.",
			Order:     22,
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "identity_claim_key",
			Type:      "string",
			Label:     "Identity provider claim",
			Hint:      "The identity provider claim the sender's Google Workspace email is matched against.",
			Order:     23,
			Default:   "email",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "min_edit_interval_ms",
			Type:      "number",
			Label:     "Minimum edit interval (ms)",
			Hint:      "Minimum spacing between writes to one space, which keeps streamed replies inside the Chat API's per-space write quota.",
			Order:     24,
			Default:   1100,
			UIVisible: false,
			MinValue:  new(0),
		},
		{
			Name:      "initial_status_message",
			Type:      "string",
			Label:     "Initial status message",
			Hint:      "Status text posted while the agent works on a message.",
			Order:     25,
			Default:   "⏳ Working on it…",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
	},
}

var eventMeshType = GatewayTypeDef{
	Type:             "event_mesh",
	Name:             "Event Mesh",
	Description:      "Connect agents to your Solace event brokers to process real-time events",
	Warning:          "Configure the Solace event broker to source events from. In embedded or desktop mode you may leave the broker fields empty to use the local system broker.",
	YAMLTemplatePath: "event_mesh_gateway.yaml",
	RequiredEnvVars:  []string{},
	Fields: []FieldDef{
		{
			Name:        "broker_url",
			Type:        "string",
			Label:       "Host",
			Hint:        "The host URI of the event broker (e.g., tcps://broker.example.com:55443)",
			Placeholder: "tcps://broker.example.com:55443",
			Order:       1,
			UIVisible:   true,
			Required:    true,
			Pattern:     `^(tcp|tcps|ws|wss)://[^\s]+$`,
			MinLen:      noLenCap,
			MaxLen:      noLenCap,
			ExtraValidation: map[string]any{
				"errorMsg": "Must be a valid broker URL (e.g., tcps://broker.example.com:55443)",
			},
		},
		{
			Name:        "broker_vpn",
			Type:        "string",
			Label:       "Message VPN",
			Placeholder: "default",
			Order:       2,
			UIVisible:   true,
			Required:    true,
			MinLen:      noLenCap,
			MaxLen:      noLenCap,
		},
		{
			Name:        "broker_username",
			Type:        "string",
			Label:       "Client Username",
			Placeholder: "solace-client",
			Order:       3,
			UIVisible:   true,
			Required:    true,
			MinLen:      noLenCap,
			MaxLen:      noLenCap,
		},
		{
			Name:      "broker_password",
			Type:      "password",
			Label:     "Client Password",
			Hint:      "Required. Enter the broker password explicitly — it is never inherited from the Agent Mesh broker.",
			Order:     4,
			UIVisible: true,
			Required:  true,
			Secret:    true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "tls_skip_verify",
			Type:      "select",
			Label:     "TLS Certificate Verification",
			Hint:      "Controls whether the broker's TLS certificate is validated. Skip verification is insecure — development and testing only.",
			Order:     5,
			Default:   "false",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "false", Label: "Verify Certificates (Secure)"},
				{Value: "true", Label: "Skip Verification (Insecure)"},
			},
		},

		{
			Name:      "event_rules",
			Type:      "event_rules",
			Label:     "Event Rules",
			Hint:      "Define rules that process incoming events from the broker and route them to agents.",
			Order:     6,
			Default:   []any{},
			Required:  true,
			UIVisible: true,
			MinItems:  1,
			ExtraValidation: map[string]any{
				"ruleNamePattern":      `^[a-zA-Z0-9_-]+$`,
				"uniqueNames":          true,
				"caseInsensitiveNames": true,
			},
			Inner: eventMeshRuleFields(),
		},
	},
}

func eventMeshRuleFields() []FieldDef {
	return []FieldDef{
		{
			Name:      "name",
			Type:      "string",
			Label:     "Rule name",
			Hint:      "Unique rule identifier within this entrypoint. Case-insensitive uniqueness applies.",
			Order:     1,
			Required:  true,
			UIVisible: true,
			Pattern:   `^[a-zA-Z0-9_-]+$`,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:        "subscriptions",
			Type:        fieldTypeArrayObject,
			Label:       "Subscriptions",
			Hint:        "Topics this rule listens on. Required unless existingQueueName is set (then the queue's owner manages subscriptions).",
			Placeholder: `[{topic: "events/orders/>"}]`,
			Order:       2,
			Required:    false,
			UIVisible:   true,
			MinItems:    1,
			Inner: []FieldDef{
				{
					Name:      "topic",
					Type:      "string",
					Label:     "Topic",
					Hint:      "Solace topic (supports wildcards * and >).",
					Order:     1,
					Required:  true,
					UIVisible: true,
					MinLen:    noLenCap,
					MaxLen:    noLenCap,
				},
				{
					Name:      "qos",
					Type:      "number",
					Label:     "QoS",
					Hint:      "Quality of service — 0 (best-effort, direct) or 1 (guaranteed). Defaults to 1.",
					Order:     2,
					Default:   1,
					UIVisible: false,
					MinValue:  new(0),
					MaxValue:  new(1),
				},
			},
		},
		{
			Name:       "existingQueueName",
			Type:       "string",
			Label:      "Existing queue name",
			Hint:       "Bind to a broker queue that already exists instead of letting Agent Mesh create one. The queue's owner provisions it and manages its topic subscriptions; Agent Mesh only consumes. Mutually exclusive with subscriptions. Undeploy leaves the queue in place.",
			Order:      2.5,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "existing_queue_name",
		},
		{
			Name:      "messageFormat",
			Type:      "select",
			Label:     "Message format",
			Hint:      "Inbound payload encoding. Drives how the entrypoint decodes incoming broker messages before invoking the target.",
			Order:     3,
			Default:   "json",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "json"},
				{Value: "text"},
				{Value: "xml"},
				{Value: "raw_bytes"},
				{Value: "protobuf"},
				{Value: "structured"},
			},
			RuntimeKey: "payload_format",
		},
		{
			Name:       "payloadEncoding",
			Type:       "string",
			Label:      "Payload encoding",
			Hint:       "Character encoding for text-shaped payloads. Defaults to utf-8.",
			Order:      4,
			Default:    "utf-8",
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "payload_encoding",
		},
		{
			Name:       "eventFilter",
			Type:       "object",
			Label:      "Event filter",
			Hint:       "Deterministic test on each incoming message, evaluated before any task starts. A message it does not match is acknowledged and dropped: no task, no model call. Written as a sacexpr invoke: {function, params: {positional: [...]}}, where each param is a literal, \"evaluate_expression(<expr>)\" reading input.payload, input.topic, input.topic_levels, input.user_properties or static, or a nested {invoke: {...}}. Functions: equal, not_equal, greater_than, less_than, greater_than_or_equal, less_than_or_equal, and_op, or_op, not_op, in_op, if_else, subtract, multiply, divide, modulus, negate, power. and_op/or_op stop at the first operand that decides, and if_else evaluates only its chosen branch. A message the filter cannot decide is delivered, not dropped, and logged at WARN, whatever function the filter uses: a field it reads is missing or null, the payload is empty, a field is read from a payload that is not a JSON object, the compared values are of different kinds (text, number, boolean, list or object; in_op against a list needs the kind of the list items), in_op looks inside a number or boolean, a calculation (subtract, multiply, divide, modulus, negate or power) gives a result that is not a finite number, such as the square root of a negative number or an overflow, or and_op, or_op, not_op, an if_else condition or the filter itself yields something other than true or false (nothing counts as true or false by truthiness). To test that a key is present, use in_op with the key and evaluate_expression(input.payload:) or evaluate_expression(input.user_properties:); a field that is absent and not guarded this way is delivered. The input.payload: form needs a JSON object payload: on a text payload in_op searches the text for the key; on a JSON list it looks for the key among the text items, so an empty list, a list of only nulls or a mixed list like [\"a\", 1] gives false when the key is not there, while a list holding only numbers, booleans, objects or lists cannot be decided and the message is delivered. The filter is a cost control, not an admission boundary. A param that mentions evaluate_expression but is not written exactly as evaluate_expression(<expr>), a bare input.payload:<path> without the wrapper, a comparison of two literals, a null param and a NaN or infinite number are refused when the rule is saved. Unset: every message starts a task.",
			Order:      4.5,
			UIVisible:  false,
			RuntimeKey: "event_filter",
		},

		{
			Name:       "targetAgent",
			Type:       "string",
			Label:      "Target agent",
			Hint:       "Name of the agent that handles messages matching this rule. Mutually exclusive with targetWorkflowName.",
			Order:      5,
			UIVisible:  true,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "target_agent_name",
		},
		{
			Name:       "targetWorkflowName",
			Type:       "string",
			Label:      "Target workflow",
			Hint:       "Name of the workflow that handles messages matching this rule. Mutually exclusive with targetAgent.",
			Order:      6,
			UIVisible:  true,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "target_workflow_name",
		},
		{
			Name:      "promptTemplate",
			Type:      "textarea",
			Label:     "Prompt template",
			Hint:      "Required for agent targets, including a rule with no target (it routes to the Orchestrator). Not used for workflow targets — they take inputExpression instead. Template rendered against the incoming message to build the agent input. {payload} and {topic} are expanded; other {payload.path.to.field} forms extract from the JSON payload.",
			Order:     7,
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "responseFormat",
			Type:      "textarea",
			Label:     "Response format (this rule)",
			Hint:      "Appended to this rule's prompt as literal text, for agent targets in this rule only (workflow targets ignore it). Distinct from the entrypoint-level responseFormat, which is stamped as task metadata and reaches only agents with inject_response_format enabled.",
			Order:     7.5,
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:       "inputExpression",
			Type:       "string",
			Label:      "Input expression",
			Hint:       "Raw sacexpr expression used to build the input to a workflow target. \"input.payload:\" delivers the payload as the workflow's text input (workflow.input.text): a payload that parses as JSON is re-serialized as JSON text, any other payload passes through as-is. If the workflow declares an input_schema, it reads a JSON object payload as its input fields, unless the schema also accepts the payload as text and the object fails the schema or names an undeclared field. A payload the schema accepts neither way fails the run before any node runs. Set it on every workflow target: without it the workflow receives empty input. Not used for agent targets.",
			Order:      8,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "input_expression",
		},

		{
			Name:       "defaultUserIdentity",
			Type:       "string",
			Label:      labelDefaultUserIdentity,
			Hint:       "Static identity to attribute incoming events to (e.g. \"sfdc_event_user\"). Used for RBAC and audit. Overridden by userIdentityExpression when both are set.",
			Order:      9,
			UIVisible:  true,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "default_user_identity",
		},
		{
			Name:       "userIdentityExpression",
			Type:       "string",
			Label:      "User identity expression",
			Hint:       "sacexpr expression that resolves to the user identity per message (e.g. \"input.user_properties.requester\").",
			Order:      10,
			UIVisible:  true,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "user_identity_expression",
		},
		{
			Name:       "runAs",
			Type:       "system_user_select",
			Label:      labelRunAs,
			Hint:       "Service account this rule's events run as when no per-message identity resolves. Leave empty for the built-in default service account; name a scoped service account to narrow what this rule can reach.",
			Order:      10.5,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "run_as",
		},

		{
			Name:       "forwardContext",
			Type:       "object",
			Label:      "Forward context",
			Hint:       "Map of context-key → sacexpr expression. Evaluated against the incoming message and forwarded to the output handler's expression context (accessible via user_data.forward_context).",
			Order:      11,
			UIVisible:  true,
			RuntimeKey: "forward_context",
		},
		{
			Name:      "structuredInvocation",
			Type:      "object",
			Label:     "Structured invocation",
			Hint:      "Schema-validated invocation for agent targets. The agent validates its output against outputSchema, retrying on failure, and the result is published as structured_output; a failed validation goes to errorOutput. The validated result is carried only in structured_output, so it is published with responseType structured, the default for such a rule; text is empty and full holds only a reference to the result. A workflow target ignores these schemas and applies its own.",
			Order:     12,
			UIVisible: true,
			Inner: []FieldDef{
				{
					Name:      "inputSchema",
					Type:      "object",
					Label:     "Input schema",
					Hint:      "JSON Schema describing the agent input shape. Any schema other than a single text property is checked against the rule's input, which the agent receives as JSON: the whole event payload, or the value inputExpression selects when it is set and is not a prompt template.",
					Order:     1,
					UIVisible: true,
				},
				{
					Name:      "outputSchema",
					Type:      "object",
					Label:     "Output schema",
					Hint:      "JSON Schema the agent's output must satisfy.",
					Order:     2,
					UIVisible: true,
				},
			},
			RuntimeKey: "structured_invocation",
		},
		{
			Name:                  "acknowledgmentPolicy",
			Type:                  "object",
			Label:                 "Acknowledgment policy",
			Hint:                  "When to acknowledge the broker. Object form for full control; string shorthand (\"on_receive\" / \"on_completion\") expands to {mode: <string>}.",
			Order:                 13,
			UIVisible:             true,
			AcceptStringShorthand: true,
			StringShorthandField:  "mode",
			RuntimeKey:            "acknowledgment_policy",
			Inner: []FieldDef{
				{
					Name:      "mode",
					Type:      "select",
					Label:     "Mode",
					Hint:      "on_receive: ack immediately on receipt (default). on_completion: defer ack until the target completes.",
					Order:     1,
					Default:   "on_receive",
					UIVisible: true,
					MinLen:    noLenCap,
					MaxLen:    noLenCap,
					OneOf: []FieldOption{
						{Value: "on_receive"},
						{Value: "on_completion"},
					},
				},
				{
					Name:       "timeoutSeconds",
					Type:       "number",
					Label:      "Timeout (seconds)",
					Hint:       "Deferred-ACK timeout. After this many seconds with no completion signal, the deferred ack settles per onFailure. Default 300.",
					Order:      2,
					Default:    300,
					UIVisible:  true,
					MinValue:   new(1),
					RuntimeKey: "timeout_seconds",
				},
				{
					Name:       "onFailure",
					Type:       "object",
					Label:      "On failure",
					Hint:       "What to do when the deferred ack settles as a failure.",
					Order:      3,
					UIVisible:  true,
					RuntimeKey: "on_failure",
					Inner: []FieldDef{
						{
							Name:      "action",
							Type:      "select",
							Label:     "Action",
							Order:     1,
							Default:   "nack",
							UIVisible: true,
							MinLen:    noLenCap,
							MaxLen:    noLenCap,
							OneOf: []FieldOption{
								{Value: "nack"},
								{Value: "ack"},
							},
						},
						{
							Name:       "nackOutcome",
							Type:       "select",
							Label:      "Nack outcome",
							Hint:       "Used only when action=nack. rejected: drop the message; failed: redeliver.",
							Order:      2,
							Default:    "rejected",
							UIVisible:  true,
							MinLen:     noLenCap,
							MaxLen:     noLenCap,
							RuntimeKey: "nack_outcome",
							OneOf: []FieldOption{
								{Value: "rejected"},
								{Value: "failed"},
							},
						},
					},
				},
			},
		},

		{
			Name:      "successOutput",
			Type:      "object",
			Label:     "Success output",
			Hint:      "How to publish the target's successful response.",
			Order:     14,
			UIVisible: true,
			Inner:     eventMeshOutputFields(false),
		},
		{
			Name:      "errorOutput",
			Type:      "object",
			Label:     "Error output",
			Hint:      "How to publish errors from the target.",
			Order:     15,
			UIVisible: true,
			Inner:     eventMeshOutputFields(true),
		},

		{
			Name:       "artifactProcessing",
			Type:       "object",
			Label:      "Artifact processing",
			Hint:       "Extract artifacts from the incoming message and attach them to the dispatched task. See sacexpr docs for expression syntax.",
			Order:      16,
			UIVisible:  false,
			RuntimeKey: "artifact_processing",
			Inner: []FieldDef{
				{
					Name:       "extractExpression",
					Type:       "string",
					Label:      "Extract expression",
					Hint:       "sacexpr expression that resolves to the artifact source(s) — a single item or a list. Each item is processed against artifactDefinition.",
					Order:      1,
					UIVisible:  false,
					MinLen:     noLenCap,
					MaxLen:     noLenCap,
					RuntimeKey: "extract_artifacts_expression",
				},
				{
					Name:       "artifactDefinition",
					Type:       "object",
					Label:      "Artifact definition",
					Hint:       "Template for one artifact. Expressions are evaluated per item from extractExpression.",
					Order:      2,
					UIVisible:  false,
					RuntimeKey: "artifact_definition",
					Inner: []FieldDef{
						{Name: "filename", Type: "string", Label: "Filename", Order: 1, UIVisible: false, MinLen: noLenCap, MaxLen: noLenCap},
						{Name: "content", Type: "string", Label: "Content", Order: 2, UIVisible: false, MinLen: noLenCap, MaxLen: noLenCap},
						{Name: "mimeType", Type: "string", Label: "MIME type", Order: 3, UIVisible: false, MinLen: noLenCap, MaxLen: noLenCap, RuntimeKey: "mime_type"},
						{Name: "contentEncoding", Type: "string", Label: "Content encoding", Order: 4, UIVisible: false, MinLen: noLenCap, MaxLen: noLenCap, RuntimeKey: "content_encoding"},
					},
				},
			},
		},
	}
}

const (
	successResponseTypeHint = "Which slice of the A2A task response to publish. text: final text only. full: entire task response JSON. structured: task_response:structured_output, an agent target's schema-validated output when structuredInvocation is set (the default for such a rule), or a workflow target's output. error: the task error. custom: free-form, see customExpression."
	errorResponseTypeHint   = "Which slice of the error payload to publish. The error payload carries only the error, never a result. text: the failure message. error: task_response:a2a_task_response.error, {code, message}. full: {error: {code, message}}. structured: always null. custom: free-form, see customExpression; input.payload:data[0] publishes null here."
)

func eventMeshOutputFields(isError bool) []FieldDef {
	defaultResponseType := "text"
	responseTypeHint := successResponseTypeHint
	if isError {
		defaultResponseType = "error"
		responseTypeHint = errorResponseTypeHint
	}
	return []FieldDef{
		{
			Name:      "enabled",
			Type:      "boolean",
			Label:     "Enabled",
			Hint:      "Publish the output when this is true. When false, the entrypoint swallows the response.",
			Order:     1,
			Default:   true,
			UIVisible: true,
		},
		{
			Name:      "topic",
			Type:      "string",
			Label:     "Topic",
			Hint:      "Topic to publish the response to. Static when topicType=static; interpolated as a sacexpr template when topicType=dynamic.",
			Order:     2,
			UIVisible: true,
			Pattern:   `^[a-zA-Z0-9*>/_.\-{}:|' \[\],]+$`,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
		},
		{
			Name:      "topicType",
			Type:      "select",
			Label:     "Topic type",
			Hint:      "static: publish to the literal topic. dynamic: evaluate topic as a sacexpr template against the response.",
			Order:     3,
			Default:   "static",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "static"},
				{Value: "dynamic"},
			},
		},
		{
			Name:       "format",
			Type:       "select",
			Label:      "Payload format",
			Hint:       "Encoding of the published payload. Defaults to json.",
			Order:      4,
			Default:    "json",
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "payload_format",
			OneOf: []FieldOption{
				{Value: "json"},
				{Value: "yaml"},
				{Value: "text"},
				{Value: "csv"},
			},
		},
		{
			Name:       "payloadEncoding",
			Type:       "string",
			Label:      "Payload encoding",
			Hint:       "Character encoding for text-shaped payloads. Defaults to utf-8.",
			Order:      5,
			Default:    "utf-8",
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "payload_encoding",
		},
		{
			Name:      "responseType",
			Type:      "select",
			Label:     "Response type",
			Hint:      responseTypeHint,
			Order:     6,
			Default:   defaultResponseType,
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "text", Description: "task_response:text"},
				{Value: "full", Description: "task_response:a2a_task_response"},
				{Value: "structured", Description: "task_response:structured_output (validated agent output, or the workflow's output)"},
				{Value: "error", Description: "task_response:a2a_task_response.error"},
				{Value: "custom", Description: "evaluate customExpression"},
			},
		},
		{
			Name:               "customExpression",
			Type:               "string",
			Label:              "Custom expression",
			Hint:               "sacexpr expression evaluated against the response. Required when responseType=custom; ignored otherwise.",
			Order:              7,
			UIVisible:          false,
			ConditionalDisplay: map[string]any{"responseType": "custom"},
			MinLen:             noLenCap,
			MaxLen:             noLenCap,
		},
		{
			Name:       "outputSchema",
			Type:       "object",
			Label:      "Output schema",
			Hint:       "JSON Schema. The published payload is validated against this schema; onValidationError controls what happens on failure.",
			Order:      8,
			UIVisible:  false,
			RuntimeKey: "output_schema",
		},
		{
			Name:       "onValidationError",
			Type:       "select",
			Label:      "On validation error",
			Hint:       "log: emit a warning and publish anyway (default). drop: emit a warning and swallow the message.",
			Order:      9,
			Default:    "log",
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "on_validation_error",
			OneOf: []FieldOption{
				{Value: "log"},
				{Value: "drop"},
			},
		},
		{
			Name:       "outputTransforms",
			Type:       fieldTypeArrayObject,
			Label:      "Output transforms",
			Hint:       "sacexpr transform pipeline applied to the response before publishing. Each entry mirrors the sacexpr.TransformConfig shape (type, source_expression, dest_expression, etc.).",
			Order:      10,
			UIVisible:  false,
			RuntimeKey: "output_transforms",
		},
		{
			Name:       "maxFileSizeForBase64Bytes",
			Type:       "number",
			Label:      "Max file size for base64 (bytes)",
			Hint:       "Cap on file-part sizes that get base64-inlined in the published payload. Larger files become URI references instead.",
			Order:      11,
			UIVisible:  false,
			MinValue:   new(0),
			RuntimeKey: "max_file_size_for_base64_bytes",
		},
		{
			Name:       "deliveryMode",
			Type:       "select",
			Label:      "Delivery mode",
			Hint:       "direct: publish without a broker acknowledgement (default). guaranteed: publish as a guaranteed message and wait for the broker to acknowledge it; with acknowledgmentPolicy.mode on_completion the input event is settled only after that acknowledgement.",
			Order:      12,
			Default:    solaceheaders.DeliveryDirect,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     noLenCap,
			RuntimeKey: "delivery_mode",
			OneOf: []FieldOption{
				{Value: solaceheaders.DeliveryDirect},
				{Value: solaceheaders.DeliveryGuaranteed},
			},
		},
		{
			Name:       "headers",
			Type:       "object",
			Label:      "Solace message headers",
			Hint:       "Solace headers to set on the published message, keyed by the Micro-Integration header name: " + strings.Join(solaceheaders.Names(), ", ") + ". solace_dmqEligible, solace_timeToLive and solace_expiration need deliveryMode=guaranteed. A value is a literal, or a template when it contains {{...}}.",
			Order:      13,
			UIVisible:  false,
			RuntimeKey: "headers",
		},
		{
			Name:       "userProperties",
			Type:       "object",
			Label:      "User properties",
			Hint:       "User properties to set on the published message, keyed by property name. A value is a literal, or a template when it contains {{...}}.",
			Order:      14,
			UIVisible:  false,
			RuntimeKey: "user_properties",
		},
	}
}

var mcpType = GatewayTypeDef{
	Type:             "mcp",
	Name:             "MCP",
	Description:      "Expose Agent Mesh agents as Model Context Protocol tools for Claude Code, MCP Inspector, IDEs, and other MCP-compatible clients",
	Warning:          "When enableAuth is on, your IdP client must allowlist the per-entrypoint redirect URI <external-base>/gw/<gateway_id>/oauth/callback before clients can sign in",
	YAMLTemplatePath: "mcp_gateway.yaml",
	RequiredEnvVars:  []string{},
	Fields: []FieldDef{
		{
			Name:      "enableAuth",
			Type:      "select",
			Label:     "Enable OAuth authentication",
			Hint:      "Controls authentication only: turning it off does NOT opt the entrypoint out of RBAC. When on, the entrypoint requires a Bearer token on every tool call and joins the cluster's OAuth flow (per-entrypoint signer, IdP from EXTERNAL_AUTH_PROVIDER, scopes from the cluster RBAC catalog). When off, no token is required and calls run as defaultUserIdentity, or as the entrypoint's service account when that field is empty — and are still authorized against that principal's scopes, which also narrows the tools/list response. No-auth / local-dev only.",
			Order:     1,
			Default:   "true",
			Required:  true,
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "true", Label: "Enabled (OAuth required)"},
				{Value: "false", Label: "Disabled (default identity)"},
			},
			RuntimeKey: "enable_auth",
		},
		{
			Name:               "defaultUserIdentity",
			Type:               "string",
			Label:              labelDefaultUserIdentity,
			Hint:               "User identity attributed to all tool calls when enableAuth is off. Optional — leave empty to run those calls as the entrypoint's service account, which is what carries RBAC scopes. Rejected at startup when enableAuth is on (HTTP transport carries identity via the Bearer token, not this field).",
			Order:              3,
			UIVisible:          true,
			MinLen:             noLenCap,
			MaxLen:             255,
			ConditionalDisplay: map[string]any{"enableAuth": "false"},
			RuntimeKey:         "default_user_identity",
		},
		{
			Name:               "runAs",
			Type:               "system_user_select",
			Label:              labelRunAs,
			Hint:               "Service account unauthenticated tool calls run as when no default user identity is set. Only applies when OAuth is disabled; OAuth-authenticated calls keep the caller's identity. Leave empty for the built-in default service account, which can invoke any agent or workflow; name a scoped service account to narrow what this entrypoint can reach.",
			Order:              3.5,
			UIVisible:          false,
			MinLen:             noLenCap,
			MaxLen:             noLenCap,
			ConditionalDisplay: map[string]any{"enableAuth": "false"},
			RuntimeKey:         "run_as",
		},
		{
			Name:               "allowedRedirectUris",
			Type:               fieldTypeArrayObject,
			Label:              "Allowed MCP-client redirect URIs",
			Hint:               "Allowlist of MCP-client redirect URIs the entrypoint accepts on /oauth/authorize. Loopback hosts (http://127.0.0.1, http://localhost) match any port per RFC 8252; everything else must match exactly. Empty list with enableAuth on is a known security gap — RFC 7591 DCR is unauthenticated, so any caller could register an attacker-controlled redirect URI. The entrypoint emits a startup WARN when empty.",
			Order:              4,
			Default:            []any{},
			UIVisible:          true,
			ConditionalDisplay: map[string]any{"enableAuth": "true"},
			RuntimeKey:         "allowed_redirect_uris",
			Inner: []FieldDef{
				{
					Name:      "uri",
					Type:      "string",
					Label:     "Redirect URI",
					Hint:      "Absolute URI. Use http://127.0.0.1 or http://localhost for any-port loopback matching.",
					Order:     1,
					Required:  true,
					UIVisible: true,
					MinLen:    1,
					MaxLen:    1024,
				},
			},
		},

		{
			Name:       "serverName",
			Type:       "string",
			Label:      "MCP server name",
			Hint:       "Name reported to MCP clients in server metadata. Defaults to 'SAM MCP Entrypoint'.",
			Order:      10,
			Default:    "SAM MCP Entrypoint",
			UIVisible:  true,
			MinLen:     1,
			MaxLen:     255,
			RuntimeKey: "server_name",
		},
		{
			Name:       "serverDescription",
			Type:       "string",
			Label:      "MCP server description",
			Hint:       "Description reported to MCP clients in server metadata.",
			Order:      11,
			UIVisible:  true,
			MinLen:     noLenCap,
			MaxLen:     1024,
			RuntimeKey: "server_description",
		},

		{
			Name:      "includeTools",
			Type:      fieldTypeArrayObject,
			Label:     "Include tools",
			Hint:      "Allowlist of tool-name patterns to expose. Empty = expose all. Patterns are exact match (case-insensitive) or regex when they contain special chars. Filters check against agent name, skill name, AND final tool name.",
			Order:     20,
			Default:   []any{},
			UIVisible: true,
			Inner: []FieldDef{
				{
					Name:      "pattern",
					Type:      "string",
					Label:     "Pattern",
					Order:     1,
					Required:  true,
					UIVisible: true,
					MinLen:    1,
					MaxLen:    255,
				},
			},
			RuntimeKey: "include_tools",
		},
		{
			Name:      "excludeTools",
			Type:      fieldTypeArrayObject,
			Label:     "Exclude tools",
			Hint:      "Denylist of tool-name patterns. Takes precedence over includeTools when both match.",
			Order:     21,
			Default:   []any{},
			UIVisible: true,
			Inner: []FieldDef{
				{
					Name:      "pattern",
					Type:      "string",
					Label:     "Pattern",
					Order:     1,
					Required:  true,
					UIVisible: true,
					MinLen:    1,
					MaxLen:    255,
				},
			},
			RuntimeKey: "exclude_tools",
		},

		{
			Name:      "corsAllowedOrigins",
			Type:      fieldTypeArrayObject,
			Label:     "CORS allowed origins",
			Hint:      "Allowlist of browser Origin values accepted from browser-based MCP clients (MCPJam, MCP Inspector). Empty = allow all (server-to-server MCP clients never trigger CORS, so wide-open is harmless for them). Required for browser clients on a different origin than the entrypoint — without a matching origin the browser's preflight OPTIONS is blocked. Typical local-dev values: http://127.0.0.1:3010 (MCPJam), http://localhost:6274 (MCP Inspector).",
			Order:     22,
			Default:   []any{},
			UIVisible: true,
			Inner: []FieldDef{
				{
					Name:      "origin",
					Type:      "string",
					Label:     "Origin",
					Hint:      "Exact browser Origin, e.g. http://localhost:6274 (scheme + host + port, no trailing slash).",
					Order:     1,
					Required:  true,
					UIVisible: true,
					MinLen:    1,
					MaxLen:    1024,
				},
			},
			RuntimeKey: "cors_allowed_origins",
		},
		{
			Name:      "enableFileInput",
			Type:      "radio",
			Label:     "Accept files",
			Hint:      "Lets MCP clients send files with a tool call.",
			Order:     23,
			Default:   "false",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "false", Label: "Disabled"},
				{Value: "true", Label: "Enabled"},
			},
			RuntimeKey: "enable_file_input",
		},

		{
			Name:       "corsAllowedOriginRegex",
			Type:       "string",
			Label:      "CORS allowed origin regex",
			Hint:       "Optional regex matched in addition to corsAllowedOrigins, for clients on drifting ports (e.g. MCP Inspector picks a random localhost port each launch). Auto-anchored in ^(?:…)$. Example: ^http://localhost:\\d+$",
			Order:      29,
			UIVisible:  false,
			MinLen:     noLenCap,
			MaxLen:     1024,
			RuntimeKey: "cors_allowed_origin_regex",
		},
		{
			Name:       "taskTimeoutSeconds",
			Type:       "number",
			Label:      "Task timeout (seconds)",
			Hint:       "How long the entrypoint waits for an agent to complete a tool call before timing out the MCP response. Defaults to 300.",
			Order:      30,
			Default:    300,
			UIVisible:  false,
			MinValue:   new(1),
			MaxValue:   new(3600),
			RuntimeKey: "task_timeout_seconds",
		},
		{
			Name:      "userIdClaim",
			Type:      "select",
			Label:     "User ID claim",
			Hint:      "OIDC claim canonicalized as the Agent Mesh user_id. All choices fall back to 'sub' when the primary claim is missing or carries a sentinel. Defaults to email.",
			Order:     31,
			Default:   "email",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "email"},
				{Value: "sub"},
				{Value: "upn"},
				{Value: "preferred_username"},
			},
			RuntimeKey:         "user_id_claim",
			ConditionalDisplay: map[string]any{"enableAuth": "true"},
		},
		{
			Name:      "transport",
			Type:      "select",
			Label:     "Transport",
			Hint:      "MCP transport. 'sse' (alias 'http') serves the modern MCP Streamable HTTP transport. 'stdio' disables the HTTP surface (advanced use only).",
			Order:     32,
			Default:   "sse",
			UIVisible: false,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "sse", Label: "sse (MCP Streamable HTTP)"},
				{Value: "http", Label: "http (alias for sse)"},
				{Value: "stdio", Label: "stdio"},
			},
			RuntimeKey: "transport",
		},

		{
			Name:      "stateRedirectionEnabled",
			Type:      "select",
			Label:     "Enable OAuth state redirection",
			Hint:      "When on, this entrypoint routes its OAuth callback through a single dispatcher entrypoint (the WebUI's /api/v1/auth/callback by default) instead of its own /gw/<slug>/oauth/callback, so the IdP only needs one redirect URI allowlisted for every MCP entrypoint. Leave the dispatcher URL below blank to auto-derive it from the cluster WebUI base. Turn off only for a self-managed IdP that allowlists each entrypoint's own callback.",
			Order:     33,
			Default:   "true",
			UIVisible: true,
			MinLen:    noLenCap,
			MaxLen:    noLenCap,
			OneOf: []FieldOption{
				{Value: "true", Label: "Enabled (recommended)"},
				{Value: "false", Label: "Disabled (legacy per-entrypoint callback)"},
			},
			RuntimeKey:         "state_redirection_enabled",
			ConditionalDisplay: map[string]any{"enableAuth": "true"},
		},
		{
			Name:               "oauthDispatcherUrl",
			Type:               "string",
			Label:              "OAuth dispatcher URL",
			Hint:               "Absolute URL of the dispatcher entrypoint's OAuth callback the IdP redirects to (e.g. https://your-host/api/v1/auth/callback). Leave blank to auto-derive it from the cluster WebUI base — set it only to target a non-WebUI dispatcher. The dispatcher verifies the state JWS and forwards to this entrypoint's /oauth/finish.",
			Order:              34,
			UIVisible:          true,
			MinLen:             noLenCap,
			MaxLen:             1024,
			Pattern:            `^https?://[^\s]+$`,
			RuntimeKey:         "oauth_dispatcher_url",
			ConditionalDisplay: map[string]any{"enableAuth": "true", "stateRedirectionEnabled": "true"},
		},
	},
}
