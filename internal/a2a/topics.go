// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"cmp"
	"fmt"
	"strings"
)

func ns(namespace string) string {
	return strings.TrimRight(namespace, "/")
}

func ValidateNamespace(namespace string) error {
	if namespace == "" {
		return nil
	}
	if strings.HasPrefix(namespace, "/") {
		return fmt.Errorf("namespace %q must not start with '/'", namespace)
	}
	for i, r := range namespace {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '/', r == '-':
		default:
			return fmt.Errorf("namespace %q contains invalid character %q at position %d (allowed: A-Z, a-z, 0-9, '.', '_', '/', '-'); broker wildcards and other special characters are rejected", namespace, r, i)
		}
	}
	for seg := range strings.SplitSeq(strings.TrimRight(namespace, "/"), "/") {
		if seg == ".." {
			return fmt.Errorf("namespace %q contains path-traversal segment '..'", namespace)
		}
	}
	return nil
}

func DiscoveryAgentCardsTopic(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/discovery/agentcards", ns(namespace))
}

func DiscoveryGatewayCardsTopic(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/discovery/gatewaycards", ns(namespace))
}

func DiscoverySubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/discovery/>", ns(namespace))
}

func AWEAgentsRequestTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/awe/agents/request", ns(namespace))
}

func AWEAgentsReplyTopic(namespace, requestID string) string {
	return aweAgentsReplyPrefix(namespace) + requestID
}

func ValidAWEAgentsReplyTopic(namespace, topic string) bool {
	id, ok := strings.CutPrefix(topic, aweAgentsReplyPrefix(namespace))
	return ok && id != "" && !strings.ContainsAny(id, "/*>")
}

func aweAgentsReplyPrefix(namespace string) string {
	return AWEAgentsRequestTopic(namespace) + "/reply/"
}

func AWEAgentsChangedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/awe/agents/changed", ns(namespace))
}

func AWEStatusTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/awe/status", ns(namespace))
}

func GatewayRPCReplyTopic(namespace, rpcID string) string {
	return fmt.Sprintf("%s/sam/v1/gateway/rpc/reply/%s", ns(namespace), rpcID)
}

func EntrypointApplyTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/gateway/entrypoints/apply", ns(namespace))
}

func GWEStatusTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/gwe/status", ns(namespace))
}

func EntrypointQueueNames(namespace, wireID string) []string {
	return []string{
		QueueName(namespace, "gdk", "gateway", wireID),
		QueueName(namespace, "gdk", "task_log", wireID),
		WhatsAppIngressQueueName(namespace, wireID),
		WhatsAppIngressQueueNameV2(namespace, wireID),
		GoogleChatIngressQueueName(namespace, wireID),
	}
}

func LinkAgentRequestQueueName(namespace, agentName string) string {
	return QueueName(namespace, "a2a", agentName)
}

func LinkAgentWakeQueueName(namespace, agentName string) string {
	return QueueName(namespace, "agentlink", "wake", agentName)
}

func LinkAgentQueueNames(namespace, agentName string) []string {
	return []string{
		LinkAgentRequestQueueName(namespace, agentName),
		LinkAgentWakeQueueName(namespace, agentName),
	}
}

func AgentCardRequestTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/discovery/agentcards/request", ns(namespace))
}

func AgentRequestTopic(namespace, agentName string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/request/%s", ns(namespace), agentName)
}

const EvalGatewayID = "evals"

func GatewayStatusTopic(namespace, gatewayID, taskID string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/status/%s/%s", ns(namespace), gatewayID, taskID)
}

func GatewayResponseTopic(namespace, gatewayID, taskID string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/response/%s/%s", ns(namespace), gatewayID, taskID)
}

func GatewayStatusSubscription(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/status/%s/>", ns(namespace), gatewayID)
}

func GatewayResponseSubscription(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/response/%s/>", ns(namespace), gatewayID)
}

func GatewayLogTopic(namespace, gatewayID, taskID string) string {
	return fmt.Sprintf("%s%s%s/%s", ns(namespace), gatewayLogMarker, gatewayID, taskID)
}

func GatewayLogSubscription(namespace, gatewayID string) string {
	return fmt.Sprintf("%s%s%s/>", ns(namespace), gatewayLogMarker, gatewayID)
}

func IsGatewayLogTopic(topic string) bool {
	return strings.Contains(topic, gatewayLogMarker)
}

const gatewayLogMarker = "/sam/v1/gateway/log/"

func GatewayStreamTaskTopic(namespace, gatewayID, taskID string) string {
	return fmt.Sprintf("%s%s%s/%s", ns(namespace), gatewayStreamTaskMarker, gatewayID, taskID)
}

func GatewayStreamTaskSubscription(namespace, gatewayID string) string {
	return fmt.Sprintf("%s%s%s/>", ns(namespace), gatewayStreamTaskMarker, gatewayID)
}

const gatewayStreamTaskMarker = "/sam/v1/gateway/stream/task/"

func AllGatewayResponseSubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/response/>", ns(namespace))
}

func AllGatewayStatusSubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/gateway/status/>", ns(namespace))
}

func AllAgentRequestSubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/request/>", ns(namespace))
}

func AllNamespaceA2ASubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/>", ns(namespace))
}

func PeerStatusTopic(namespace, delegatingAgent, subTaskID string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/status/%s/%s", ns(namespace), delegatingAgent, subTaskID)
}

func PeerResponseTopic(namespace, delegatingAgent, subTaskID string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/response/%s/%s", ns(namespace), delegatingAgent, subTaskID)
}

func PeerStatusSubscription(namespace, selfAgent string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/status/%s/>", ns(namespace), selfAgent)
}

func PeerResponseSubscription(namespace, selfAgent string) string {
	return fmt.Sprintf("%s/a2a/v1/agent/response/%s/>", ns(namespace), selfAgent)
}

func TrustCardTopic(namespace, componentType, componentID string) string {
	return fmt.Sprintf("%s/a2a/v1/trust/%s/%s", ns(namespace), componentType, componentID)
}

func TrustCardSubscription(namespace string) string {
	return fmt.Sprintf("%s/a2a/v1/trust/*/*", ns(namespace))
}

func ExtractTrustCardInfo(topic, namespace string) (componentType, componentID string, err error) {
	prefix := fmt.Sprintf("%s/a2a/v1/trust/", ns(namespace))
	if !strings.HasPrefix(topic, prefix) {
		return "", "", fmt.Errorf("topic %q does not match trust card pattern", topic)
	}
	rest := topic[len(prefix):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("topic %q has invalid trust card format", topic)
	}
	return parts[0], parts[1], nil
}

func IsTrustCardTopic(topic, namespace string) bool {
	_, _, err := ExtractTrustCardInfo(topic, namespace)
	return err == nil
}

func ModelConfigBootstrapSubscription(namespace string) string {
	return fmt.Sprintf("%sconfiguration/model/bootstrap/>", ns(namespace)+"/")
}

func ModelConfigBootstrapRequestTopic(namespace, modelID string) string {
	return fmt.Sprintf("%sconfiguration/model/bootstrap/%s", ns(namespace)+"/", modelID)
}

func ModelConfigBootstrapResponseTopic(namespace, modelID, componentID string) string {
	return fmt.Sprintf("%sconfiguration/model/response/%s/%s", ns(namespace)+"/", modelID, componentID)
}

func ModelConfigUpdateTopic(namespace, modelIDOrAlias string) string {
	return fmt.Sprintf("%sconfiguration/model/%s", ns(namespace)+"/", modelIDOrAlias)
}

func ControlSubscriptionTopics(namespace string) []string {
	methods := []string{"get", "post", "put", "patch", "delete"}
	topics := make([]string, len(methods))
	for i, m := range methods {
		topics[i] = fmt.Sprintf("%s/sam/v1/control/%s/>", ns(namespace), m)
	}
	return topics
}

func ControlAppsTopic(namespace, method string) string {
	return fmt.Sprintf("%s/sam/v1/control/%s/apps", ns(namespace), method)
}

func ControlAppTopic(namespace, appName, method string) string {
	return fmt.Sprintf("%s/sam/v1/control/%s/apps/%s", ns(namespace), method, appName)
}

func LifecycleTopic(namespace, component, event string) string {
	return fmt.Sprintf("%s/sam/v1/lifecycle/%s/%s", ns(namespace), component, event)
}

func LifecycleSubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/lifecycle/>", ns(namespace))
}

func FeedbackTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/feedback", ns(namespace))
}

func ProductAnalyticsIngestTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/analytics/ingest/server", ns(namespace))
}

func ProductAnalyticsCLIIngestTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/analytics/ingest/cli", ns(namespace))
}

func SyncNotifyTopic(namespace, category string) string {
	return fmt.Sprintf("%s/sam/v1/sync/%s", ns(namespace), category)
}

func SamEventsTopic(namespace, category, action string) string {
	return fmt.Sprintf("%s/sam/events/%s/%s", ns(namespace), category, action)
}

func SamEventsSubscription(namespace, category string) string {
	return fmt.Sprintf("%s/sam/events/%s/>", ns(namespace), category)
}

func SyncNotifySubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/sync/>", ns(namespace))
}

func ControlAppCustomTopic(namespace, appName, method, customPath string) string {
	return fmt.Sprintf("%s/sam/v1/control/%s/apps/%s/%s", ns(namespace), method, appName, customPath)
}

type ParsedControlTopic struct {
	Method     string
	Resource   string
	AppName    string
	CustomPath []string
}

func ParseControlTopic(namespace, topic string) (*ParsedControlTopic, error) {
	prefix := fmt.Sprintf("%s/sam/v1/control/", ns(namespace))
	if !strings.HasPrefix(topic, prefix) {
		return nil, fmt.Errorf("topic %q does not match control pattern", topic)
	}
	rest := topic[len(prefix):]
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("topic %q missing method or resource", topic)
	}

	result := &ParsedControlTopic{
		Method:   parts[0],
		Resource: parts[1],
	}

	if len(parts) >= 3 {
		result.AppName = parts[2]
	}
	if len(parts) >= 4 {
		result.CustomPath = parts[3:]
	}

	return result, nil
}

func AuthzLookupTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/authz/lookup/%s", ns(namespace), gatewayID)
}

func AuthzLookupSubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/authz/lookup/>", ns(namespace))
}

func AuthzLookupGatewayID(topic string) string {
	prefix, ok := cutTopicPrefix(topic, "/sam/v1/authz/lookup/")
	if !ok {
		return ""
	}
	return prefix
}

func cutTopicPrefix(topic, marker string) (string, bool) {
	i := strings.Index(topic, marker)
	if i < 0 {
		return "", false
	}
	rest := topic[i+len(marker):]
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

func AuthzResponseTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/authz/response/%s", ns(namespace), gatewayID)
}

func AuthzRolesChangedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/authz/roles/changed", ns(namespace))
}

func AuthzUsersChangedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/authz/users/changed", ns(namespace))
}

func WebuiSettingsUpdateTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/webuiSettings/update", ns(namespace))
}

func UserUpsertTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/identity/upsert/%s", ns(namespace), gatewayID)
}

func UserUpsertSubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/identity/upsert/>", ns(namespace))
}

func UserUpsertResponseTopic(namespace, gatewayID string) string {
	return fmt.Sprintf("%s/sam/v1/identity/response/%s", ns(namespace), gatewayID)
}

func ToolDeployedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/tool/deployed", ns(namespace))
}

func NotifyRequestTopic(namespace, userID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/request/%s", ns(namespace), notifyTopicSegment.Replace(cmp.Or(userID, "_")))
}

var notifyTopicSegment = strings.NewReplacer("/", "_", "*", "_", ">", "_")

func NotifyRequestSubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/notify/request/>", ns(namespace))
}

func ScheduleRequestTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/schedule/request", ns(namespace))
}

func ScheduleRequestSubscription(namespace string) string {
	return ScheduleRequestTopic(namespace)
}

func ScheduleReplyTopic(namespace, requestID string) string {
	return fmt.Sprintf("%s/sam/v1/schedule/reply/%s", ns(namespace), requestID)
}

func ToolAuthRegisterTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/toolauth/register", ns(namespace))
}

func ToolAuthRegisterSubscription(namespace string) string {
	return ToolAuthRegisterTopic(namespace)
}

func NotifyDeliverTopic(namespace, gatewayType, requestID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/deliver/%s/%s", ns(namespace), gatewayType, requestID)
}

func NotifyDeliverSubscription(namespace, gatewayType string) string {
	return fmt.Sprintf("%s/sam/v1/notify/deliver/%s/>", ns(namespace), gatewayType)
}

func NotifyResolveTopic(namespace, gatewayType string) string {
	return fmt.Sprintf("%s/sam/v1/notify/resolve/%s", ns(namespace), gatewayType)
}

func NotifyResolveSubscription(namespace, gatewayType string) string {
	return NotifyResolveTopic(namespace, gatewayType)
}

func notifyResolveReplyPrefix(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/notify/resolve-reply/", ns(namespace))
}

func NotifyResolveReplyTopic(namespace, requestID string) string {
	return notifyResolveReplyPrefix(namespace) + requestID
}

func ValidNotifyResolveReplyTopic(namespace, topic string) bool {
	id, ok := strings.CutPrefix(topic, notifyResolveReplyPrefix(namespace))
	return ok && id != "" && !strings.ContainsAny(id, "/*>")
}

func NotifyInboxCreatedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/notify/inboxCreated", ns(namespace))
}

func UserEventTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/gateway/userEvent", ns(namespace))
}

func NotifyPresenceTopic(namespace, userID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/presence/%s", ns(namespace), userID)
}

func NotifyPresenceSubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/notify/presence/>", ns(namespace))
}

func NotifyReplyTopic(namespace, requestID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/reply/%s", ns(namespace), requestID)
}

func NotifyReplySubscription(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/notify/reply/>", ns(namespace))
}

func NotifyResponseTopic(namespace, requesterID, requestID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/response/%s/%s", ns(namespace), requesterID, requestID)
}

func NotifyResponseSubscription(namespace, requesterID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/response/%s/>", ns(namespace), requesterID)
}

func AgentForwardTopic(namespace, agentName string) string {
	return fmt.Sprintf("%s/sam/v1/agent/forward/%s", ns(namespace), agentName)
}

func WorkflowForwardTopic(namespace, workflowName string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/forward/%s", ns(namespace), workflowName)
}

func WorkflowDeadlineTopic(namespace, workflowName, executionID string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/deadline/%s/%s", ns(namespace), workflowName, executionID)
}

func WorkflowDeadlineSubscription(namespace, workflowName string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/deadline/%s/>", ns(namespace), workflowName)
}

func WorkflowDeadlineTopicPrefix(namespace, workflowName string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/deadline/%s/", ns(namespace), workflowName)
}

func WorkflowWakeTopic(namespace, workflowName, executionID string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/wake/%s/%s", ns(namespace), workflowName, executionID)
}

func WorkflowWakeSubscription(namespace, workflowName string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/wake/%s/>", ns(namespace), workflowName)
}

func WorkflowWakeTopicPrefix(namespace, workflowName string) string {
	return fmt.Sprintf("%s/sam/v1/workflow/wake/%s/", ns(namespace), workflowName)
}

func WorkflowSignalRequestID(taskID, signal string) string {
	return "signal:" + taskID + ":" + signal
}

func NotifyResponseTopicPrefix(namespace, requesterID string) string {
	return fmt.Sprintf("%s/sam/v1/notify/response/%s/", ns(namespace), requesterID)
}

func SpendEventTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/governance/spend", ns(namespace))
}

func BudgetStateTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/governance/budgetState", ns(namespace))
}

func SpendFiledTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/governance/spendFiled", ns(namespace))
}

func BudgetsChangedTopic(namespace string) string {
	return fmt.Sprintf("%s/sam/v1/governance/budgetsChanged", ns(namespace))
}

func QueueName(namespace, subsystem string, parts ...string) string {
	segments := append([]string{strings.Trim(namespace, "/"), "q", subsystem}, parts...)
	return strings.Join(segments, "/")
}

func WhatsAppIngressQueueName(namespace, gatewayID string) string {
	return QueueName(namespace, "gdk", "whatsapp_ingress", gatewayID)
}

func WhatsAppIngressQueueNameV2(namespace, gatewayID string) string {
	return QueueName(namespace, "gdk", "whatsapp_ingress", gatewayID, "v2")
}

func WhatsAppDoorbellTopic(namespace, gatewayID string) string {
	return ns(namespace) + whatsAppDoorbellMarker + gatewayID
}

func IsWhatsAppDoorbellTopic(topic string) bool {
	return strings.Contains(topic, whatsAppDoorbellMarker)
}

const whatsAppDoorbellMarker = "/sam/v1/gdk/whatsapp/doorbell/"

func GoogleChatIngressQueueName(namespace, gatewayID string) string {
	return QueueName(namespace, "gdk", "googlechat_ingress", gatewayID)
}
