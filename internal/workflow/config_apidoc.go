// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (WorkflowConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WorkflowConfig is the parsed top-level workflow definition.",
		Fields: map[string]apidoc.Field{
			"Name":                       {Comment: "from parent config, not from workflow definition"},
			"DisplayName":                {Comment: "card label; a platform deploy always overwrites it with the workflow's resource name, so omit it from spec.appConfig"},
			"Namespace":                  {Comment: "from parent config"},
			"Tools":                      {Doc: "Tools is the raw `tools:` list (one entry per declarative tool config),\nin the same shape agents accept. Parsed from the workflow body, then\ncombined by the loader with any app_config-level entries, which take\nprecedence on a name collision. Workflow tool nodes can invoke any tool\nregistered here. Connector-produced tools share the same namespace."},
			"Connectors":                 {Doc: "Connectors is the raw `connectors:` list (one entry per connector\ninstance), in the same shape agents accept. Read from the same two\nlevels as Tools. Each connector's tool is registered into the workflow's\nshared tool namespace alongside `tools:` entries."},
			"supersededBodyTools":        {Doc: "supersededBodyTools names the body `tools:` entries MergeAppConfigTools-\nAndConnectors dropped in favour of an app_config entry, so Init can say\nso instead of leaving the author's declaration vanish silently."},
			"Toolsets":                   {Doc: "Toolsets is the symbolic `toolsets:` list (toolset or built-in-group\nnames, the same names agents accept in spec.toolsets), declared at the\napp_config level alongside `tools:`. It is a platform authoring\nsurface: the platform expands each name into concrete `tools:` entries\nat deploy time and strips the key. The engine never resolves it — a\nstatically-loaded config that still carries it gets a warning and the\nlist is otherwise ignored (declare `tools:` directly instead)."},
			"WorkflowTimeout":            {Doc: "Timeouts."},
			"CardPublishIntervalSeconds": {Doc: "Agent card publishing."},
			"FailFast":                   {Doc: "Argo-aligned fields."},
			"HILDatabaseURL": {Doc: `HILDatabaseURL is the database the durable workflow_paused store opens for
M4 HIL pause/resume. NOT parsed from the workflow YAML — bootstrap sets it
from the app's resolved session_service.database_url (the shared deployment
DB), so no new operator-facing config is introduced. Empty (or the feature
flag off) disables pause: a pending tool result fails the node as before.`},
			"Decision": {Doc: `Decision configures the model every decision node calls. Set
model_alias to the name of a decision model registered on the platform
(a model with modelType decision), which carries its own endpoint, key
and model; a workflow saved to the platform must use it. Only a workflow
loaded from a config file may instead give api_base (optional; defaults
to the provider's hosted API), api_key and model (pin a version such as
jev-1.13.0) inline, and never together with model_alias. Either form
takes timeout_seconds, max_state_bytes, max_images and max_image_bytes.
A workflow with decision nodes and no decision block does not start:
sending workflow data to a third-party model is an explicit opt-in,
never a default.`},
		},
	}
}

func (DecisionProviderConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DecisionProviderConfig configures the model behind decision nodes.",
		Fields: map[string]apidoc.Field{
			"Provider":      {Comment: "\"typesafe\" (the default), \"openai\" or \"google_ai_studio\""},
			"ModelAlias":    {Comment: "a decision model in the platform model registry; replaces api_base, api_key and model"},
			"APIBase":       {Comment: "defaults to the provider's hosted API"},
			"Model":         {Comment: "pin a version; an alias like jev-latest can change routing under a deployed workflow"},
			"MaxImages":     {Comment: "most images one decision call sends, at most 128; 0 uses the default of 5"},
			"MaxImageBytes": {Comment: "most image bytes one decision call sends, after downscaling, at most 64 MiB; 0 uses the default of 20 MiB"},
			"MaxMedia":      {Comment: "most audio, video and document files one decision call sends, at most 128; 0 uses the default of 2"},
			"MaxMediaBytes": {Comment: "most audio, video and document bytes one decision call sends, at most 64 MiB; 0 uses the default of 20 MiB"},
			"ReplacedWorkflowBlock": {Doc: `ReplacedWorkflowBlock is set by MergeOperatorDecision when an app_config
decision block replaced the workflow's own, so an endpoint error can point
at the app_config block rather than the workflow the author sees.`},
		},
	}
}

func (NodeConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "NodeConfig is a union-type config for all node types.",
		Fields: map[string]apidoc.Field{
			"Type":        {Comment: "selects one of the node types below"},
			"DependsOn":   {Comment: "node IDs this node depends on"},
			"When":        {Doc: "Common optional fields (every node type)."},
			"Retry":       {Doc: "Retry policy (agent, switch, map, loop, workflow, tool)."},
			"Instruction": {Doc: "Invocation prompt fields (agent, workflow)."},
			"AgentName":   {Doc: "Agent node fields."},
			"AgentInstanceName": {Doc: `AgentInstanceName is the target's broker name (agent_<uuid>), stamped by
the platform at deploy time from its agent registry. It is a fallback
for when AgentName matches no discovered card — the window right after a
deploy or restart, before the target's next periodic card broadcast — so
the request still reaches a subscriber instead of hanging until the node
timeout (#4402). A live card always wins over it.`},
			"Cases":           {Doc: "Switch node fields."},
			"BodyNode":        {Doc: "Loop node fields.", Comment: "target node ID for loop body"},
			"ItemsExpression": {Doc: "Map node fields.", Comment: "the items to iterate: an expression string, a literal list, or an operator object that resolves to a list"},
			"TemplateNode":    {Comment: "target node ID for map body"},
			"WorkflowName":    {Doc: "Workflow invoke fields."},
			"WaitDuration":    {Doc: "Timer node fields.", Comment: "needs the workflow_durable_waits, workflow_hil_pause_resume and workflow_checkpoint_resume feature flags and the workflow's session_service database, or the workflow does not start. How long the timer waits, such as 90s, 2h or 3d; written as duration. The node completes with output fired_at (when it fired) and scheduled_for (when it was due), up to about 35 seconds late when the AWE that parked the run has stopped and fewer than 2,048 runs of the workflow are due at once. A run whose open steps all wait parks in the database, so the wait outlives the AWE that started it. A timer cannot be a loop body, a map template or an on_exit handler, and takes no timeout or retry_strategy"},
			"Signal":          {Doc: "Wait node fields.", Comment: "needs the same feature flags and database as a timer node. The name of the signal the node waits for: 1 to 64 letters, digits, '_' or '-', unique among the workflow's wait nodes. A sender publishes a user_input_response message (request_id, status, data) to the workflow's notify response topic, <namespace>/sam/v1/notify/response/<workflow>/<request_id>, with request_id signal:<taskId>:<signal>. The broker ACL on that topic decides who can send a signal. The node completes with output the fields of data plus status. A wait node in an evaluation run fails, since nobody sends it a signal. A signal sent before the run reaches the node is dropped. timeout, unset by default, fails the node when no signal arrived in time. A wait node cannot be a loop body, a map template or an on_exit handler, and takes no retry_strategy"},
			"ToolName":        {Doc: "Tool node fields. ToolName names a tool registered in the shared tool\nnamespace — either declared in `tools:` or produced by a connector in\n`connectors:`. Tools and connectors share a single namespace."},
			"Connector":       {Comment: "names a platform connector; the platform resolves it to a concrete tool_name at deploy time"},
			"Tool":            {Comment: "selects one of a multi-tool connector's tools by un-suffixed base name (e.g. slack_send_message); optional when the connector produces exactly one"},
			"Questions":       {Doc: "Decision node fields.", Comment: "required; keyed by question name, not a list. Each entry takes type (boolean, choice or score), instructions, and criteria (option → description; 2-255 options for choice; a list of 2-10 levels, lowest first, for score; for boolean, describe both true and false or leave criteria out). All questions are answered in one provider call; read an answer as <node>.output.answers.<name>.<field>"},
			"RouteOn":         {Comment: "the choice question whose answer selects the branch"},
			"Routes":          {Comment: "choice option → target node ID"},
			"MinConfidence":   {Comment: "below this confidence on route_on, the node escalates"},
			"Escalate":        {Comment: "target node ID for a low-confidence or provider-error outcome"},
			"EscalationMode":  {Comment: "\"reroute\" (the default) or \"handoff\". reroute: once the escalate target finishes, its answer to route_on (<escalate>.output.<route_on>, or answers.<route_on>.choice for a decision target) selects the route or default, recorded as <node>.output.routed_branch, and every route and default target waits for the escalate target. handoff: the escalate target's reply ends the routing"},
			"Default":         {Comment: "target node ID when the chosen option has no route; without it, every route_on option needs a route"},
			"OnProviderError": {Comment: "\"fail\" (the default) or \"escalate\"; escalate covers transient failures (timeout, rate limit, unreachable or 5xx provider, invalid response), an input too large for the call (over the model's context limit, or images over max_images or max_image_bytes or too large to downscale) and a refusal (an OpenAI refusal or a Gemini safety block), while rejected credentials (including Gemini's 400 API_KEY_INVALID), a redirect to a Location that is not a URL, a Google AI Studio redirect to another origin or past 10 hops, and configuration errors always fail the node"},
			"Model":           {Comment: "overrides the provider's model for this node"},
			"Images":          {Comment: "Only for a decision model that reads images (a Jev model, OpenAI, Gemini, or openai/gpt-6-luna-decisions through OpenRouter): a list of templates, each resolving to an image artifact reference (name, name:version or an artifact:// URI) or a list of them. The PNG, JPEG or WebP images go with the input in the same call; one whose longer side is over 8000 px is downscaled to 8000 px, and one that also declares more than 40 megapixels fails the call as input_too_large. For a Jev model on TypeSafe's own API, any image over 40 megapixels fails as input_too_large. An entry that resolves to nothing is skipped, such as an omitted optional input, a null or empty output field, a node that completed without returning any output, or a node the workflow pruned because its when condition was false or its branch was not taken. A missing, non-image or unreadable artifact fails the node. So does an entry that reads a node that failed, a node skipped because an upstream node failed or a when or switch condition referenced an output field its node did not return, the target of a switch case that could not be evaluated (even when the switch took another branch), an output field the node did not return, or a node that has not run and is not upstream of the decision node. An entry that embeds a template in other text, such as {{workflow.input.photo}}:2, is skipped or fails the node as a whole"},
			"ImageResolution": {Comment: "Only for a Gemini decision model, with the decision_media_input feature flag: low, medium, high or ultra_high, the detail Google reads every image in images at. Unset leaves Google's default. Higher detail costs more input tokens"},
			"Documents":       {Comment: "Only for a Gemini decision model, with the decision_media_input feature flag: a list of templates, each resolving to a PDF artifact reference (name, name:version or an artifact:// URI) or a list of them, sent after the images in the same call. Entries are skipped or fail the node as images entries are. A non-PDF artifact fails the node. Documents, audio and video together count against the decision block's max_media and max_media_bytes"},
			"Audio":           {Comment: "Only for a Gemini decision model, with the decision_media_input feature flag: a list of templates, each resolving to an audio artifact reference or a list of them, sent after the documents. WAV, MP3, AIFF, AAC, OGG (Vorbis or Opus), FLAC, M4A, WebM and audio-only MP4 are read; raw PCM is not. Entries are skipped or fail the node as images entries are, and an artifact that is not one of those types fails the node"},
			"Video":           {Comment: "Only for a Gemini decision model, with the decision_media_input feature flag: a list of templates, each resolving to a video artifact reference or a list of them, sent last. MP4 (including M4V), MPEG, MOV, AVI, WebM, WMV, FLV and 3GP are read. Entries are skipped or fail the node as images entries are, and an artifact that is not one of those types fails the node"},
			"VideoResolution": {Comment: "Only for a Gemini decision model, with the decision_media_input feature flag: low, medium or high, the detail Google reads every video in video at. Unset leaves Google's default. It is the main lever on a video's token cost"},
			"implicitWaits": {Doc: `implicitWaits holds dependencies the engine adds rather than the author:
a rerouting decision's route and default targets wait for its escalate
target. Kept apart from DependsOn so input resolution and the agent-card
visualization payload see what was written; node-start events carry
waitsFor() instead, because the run view matches escalation routes on it.`},
		},
	}
}

func (SwitchCase) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SwitchCase is a condition/target pair in a switch node.",
	}
}

func (RetryStrategy) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RetryStrategy configures retry behavior for a node.",
		Fields: map[string]apidoc.Field{
			"RetryPolicy": {Comment: "\"Always\", \"OnFailure\", \"OnError\""},
		},
	}
}

func (BackoffStrategy) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BackoffStrategy configures exponential backoff for retries.",
	}
}

func (ExitHandler) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExitHandler configures cleanup/notification on workflow completion.",
	}
}

func (DependencyGraph) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DependencyGraph is a precomputed DAG for efficient traversal.",
		Fields: map[string]apidoc.Field{
			"Nodes":      {Comment: "node ID → config"},
			"Forward":    {Comment: "node ID → nodes that depend on it"},
			"Reverse":    {Comment: "node ID → nodes it depends on"},
			"InnerNodes": {Comment: "nodes that are loop/map body nodes (not top-level)"},
			"Order":      {Comment: "topological order"},
		},
	}
}
