// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type UserNotifier interface {
	SendUserNotification(userID string, eventType string, data any) error
}

type NotificationConfig struct {
	OnSuccess        bool                     `json:"on_success"`
	OnFailure        bool                     `json:"on_failure"`
	IncludeArtifacts bool                     `json:"include_artifacts"`
	Channels         []NotificationChannelDef `json:"channels"`
}

type NotificationChannelDef struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

type notificationRecord struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	SentAt string `json:"sentAt"`
}

func unsentBudgetNotice(exec *store.ScheduledTaskExecution) (lastAttempt time.Time, unsent bool) {
	if exec.BudgetStop == nil || *exec.BudgetStop != store.BudgetStopFirst || exec.NotificationsSent == nil {
		return time.Time{}, false
	}
	var records []notificationRecord
	if err := json.Unmarshal([]byte(*exec.NotificationsSent), &records); err != nil || len(records) == 0 {
		return time.Time{}, false
	}
	for _, rec := range records {
		if rec.Status == "sent" {
			return time.Time{}, false
		}
		if at, err := time.Parse(time.RFC3339, rec.SentAt); err == nil && at.After(lastAttempt) {
			lastAttempt = at
		}
	}
	return lastAttempt, true
}

func budgetNoticeWindowPassed(exec *store.ScheduledTaskExecution, now time.Time) bool {
	return exec.CompletedAt == nil || now.Sub(time.UnixMilli(*exec.CompletedAt)) >= budgetNoticeRetryWindow
}

const (
	webhookTimeout     = 30 * time.Second
	webhookDialTimeout = 10 * time.Second
)

var allowedWebhookMethods = []string{http.MethodPost, http.MethodPut}

var errWebhookDestinationBlocked = errors.New("webhook destination is not allowed")

var errWebhookHostNotVerifiable = errors.New("webhook URL host must resolve to a public address")

func v4Prefix(a, b, c, d byte, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{a, b, c, d}), bits)
}

func v6Prefix(bits int, lead ...byte) netip.Prefix {
	var addr [16]byte
	copy(addr[:], lead)
	return netip.PrefixFrom(netip.AddrFrom16(addr), bits)
}

var (
	thisNetwork        = v4Prefix(0, 0, 0, 0, 8)
	privateClassA      = v4Prefix(10, 0, 0, 0, 8)
	carrierGradeNAT    = v4Prefix(100, 64, 0, 0, 10)
	loopbackV4         = v4Prefix(127, 0, 0, 0, 8)
	azureWireServer    = v4Prefix(168, 63, 129, 16, 32)
	linkLocalV4        = v4Prefix(169, 254, 0, 0, 16)
	privateClassB      = v4Prefix(172, 16, 0, 0, 12)
	protocolAssignment = v4Prefix(192, 0, 0, 0, 24)
	privateClassC      = v4Prefix(192, 168, 0, 0, 16)
	benchmarking       = v4Prefix(198, 18, 0, 0, 15)
	multicastV4        = v4Prefix(224, 0, 0, 0, 4)
	reservedClassE     = v4Prefix(240, 0, 0, 0, 4)
	unspecifiedV6      = netip.PrefixFrom(netip.IPv6Unspecified(), 128)
	loopbackV6         = netip.PrefixFrom(netip.IPv6Loopback(), 128)
	nat64              = v6Prefix(96, 0x00, 0x64, 0xff, 0x9b)
	sixToFour          = v6Prefix(16, 0x20, 0x02)
	uniqueLocalV6      = v6Prefix(7, 0xfc)
	linkLocalV6        = v6Prefix(10, 0xfe, 0x80)
	siteLocalV6        = v6Prefix(10, 0xfe, 0xc0)
	multicastV6        = v6Prefix(8, 0xff)
	nat64LocalUse      = v6Prefix(48, 0x00, 0x64, 0xff, 0x9b, 0x00, 0x01)
)

var blockedPrefixes = []netip.Prefix{
	thisNetwork, privateClassA, carrierGradeNAT, loopbackV4, azureWireServer, linkLocalV4,
	privateClassB, protocolAssignment, privateClassC, benchmarking, multicastV4, reservedClassE,
	unspecifiedV6, loopbackV6, uniqueLocalV6, linkLocalV6, siteLocalV6, multicastV6, nat64LocalUse,
}

var deniedWebhookHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"host":                true,
	"proxy-authorization": true,
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-real-ip":           true,
}

type NotificationService struct {
	brk          broker.Broker
	userNotifier UserNotifier
	logger       *slog.Logger
	httpClient   *http.Client

	skipURLValidation bool
}

func NewNotificationService(
	brk broker.Broker,
	logger *slog.Logger,
) *NotificationService {
	return &NotificationService{
		brk:        brk,
		logger:     logger.With("logger_name", "scheduled-task-notifications"),
		httpClient: newWebhookHTTPClient(net.DefaultResolver.LookupIPAddr),
	}
}

type ipLookup func(ctx context.Context, host string) ([]net.IPAddr, error)

func newWebhookHTTPClient(lookup ipLookup) *http.Client {
	dialer := &net.Dialer{Timeout: webhookDialTimeout}
	return newWebhookHTTPClientWithDial(dialer.DialContext, lookup)
}

func newWebhookHTTPClientWithDial(dial dialFunc, lookup ipLookup) *http.Client {
	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.Proxy = nil
	transport.DialContext = guardedDialContext(dial, lookup)
	transport.TLSHandshakeTimeout = webhookDialTimeout
	return &http.Client{
		Timeout:       webhookTimeout,
		Transport:     transport,
		CheckRedirect: refuseRedirects,
	}
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func guardedDialContext(dial dialFunc, lookup ipLookup) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, errWebhookDestinationBlocked
		}
		resolved, err := lookup(ctx, host)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("webhook host could not be resolved")
		}
		for _, r := range resolved {
			if isBlockedIP(r.IP) {
				return nil, errWebhookDestinationBlocked
			}
		}
		var dialErr error
		for _, r := range resolved {
			conn, err := dial(ctx, network, net.JoinHostPort(r.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, dialErr
	}
}

func (n *NotificationService) SetUserNotifier(u UserNotifier) {
	n.userNotifier = u
}

const defaultNotificationConfig = `{"on_success":true,"on_failure":true,"include_artifacts":true,"channels":[{"type":"inbox"}]}`

const budgetStreakNote = "Later runs of this task stop without another notification until the budget allows them again."

func effectiveNotificationConfig(task *store.ScheduledTask) string {
	if task.NotificationConfig == nil || *task.NotificationConfig == "" {
		return defaultNotificationConfig
	}
	return *task.NotificationConfig
}

func (n *NotificationService) NotifyExecutionComplete(
	ctx context.Context,
	exec *store.ScheduledTaskExecution,
	sessionID string,
	task *store.ScheduledTask,
	artifacts []a2a.NotifyArtifact,
) string {
	var cfg NotificationConfig
	if err := json.Unmarshal([]byte(effectiveNotificationConfig(task)), &cfg); err != nil {
		n.logger.Error("failed to parse notification config", "taskID", task.ID, "error", err)
		return ""
	}

	if !shouldNotify(exec, &cfg) {
		return ""
	}

	payload := n.preparePayload(exec, task, &cfg)

	var records []notificationRecord
	for _, ch := range cfg.Channels {
		var err error
		switch ch.Type {
		case "sse":
			err = n.sendSSE(payload, task)
		case "webhook":
			err = n.sendWebhook(ctx, ch.Config, payload)
		case "email":
			n.logger.Debug("email notification requested (not yet implemented)", "taskID", task.ID)
			continue
		case "inbox":
			var inboxArtifacts []a2a.NotifyArtifact
			if cfg.IncludeArtifacts {
				inboxArtifacts = artifacts
			}
			err = n.sendInbox(ctx, exec, sessionID, task, inboxArtifacts)
		default:
			n.logger.Warn("unknown notification channel type", "type", ch.Type)
			continue
		}

		rec := notificationRecord{
			Type:   ch.Type,
			SentAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
			logFailure := n.logger.Error
			if ch.Type == "webhook" || ch.Type == "sse" {
				logFailure = n.logger.Warn
			}
			logFailure("notification send failed",
				"type", ch.Type, "taskID", task.ID, "error", err)
		} else {
			rec.Status = "sent"
		}
		records = append(records, rec)
	}

	if len(records) == 0 {
		return ""
	}
	data, err := json.Marshal(records)
	if err != nil {
		return ""
	}
	return string(data)
}

func shouldNotify(exec *store.ScheduledTaskExecution, cfg *NotificationConfig) bool {
	switch {
	case exec.BudgetStop != nil:
		return *exec.BudgetStop == store.BudgetStopFirst && cfg.OnFailure
	case !isNotifiableStatus(exec.Status):
		return false
	case exec.Status == "completed":
		return cfg.OnSuccess
	}
	return cfg.OnFailure
}

func (n *NotificationService) preparePayload(
	exec *store.ScheduledTaskExecution,
	task *store.ScheduledTask,
	cfg *NotificationConfig,
) map[string]any {
	payload := map[string]any{
		"task_id":       task.ID,
		"task_name":     task.Name,
		"execution_id":  exec.ID,
		"status":        exec.Status,
		"scheduled_for": exec.ScheduledFor,
		"namespace":     task.Namespace,
	}

	if exec.StartedAt != nil {
		payload["started_at"] = *exec.StartedAt
	}
	if exec.CompletedAt != nil {
		payload["completed_at"] = *exec.CompletedAt
	}
	if task.UserID != nil {
		payload["user_id"] = *task.UserID
	}
	if exec.ErrorMessage != nil {
		payload["error_message"] = *exec.ErrorMessage
	}
	if exec.BudgetStop != nil {
		payload["budget_stop"] = *exec.BudgetStop
	}
	if exec.ResultSummary != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*exec.ResultSummary), &parsed); err == nil {
			payload["result_summary"] = parsed
		}
	}
	if cfg.IncludeArtifacts && exec.Artifacts != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*exec.Artifacts), &parsed); err == nil {
			payload["artifacts"] = parsed
		}
	}

	return payload
}

func (n *NotificationService) sendSSE(payload map[string]any, task *store.ScheduledTask) error {
	if n.userNotifier == nil {
		return nil
	}
	users := taskOwners(task)
	if len(users) == 0 {
		return nil
	}

	var firstErr error
	for _, uid := range users {
		if err := n.userNotifier.SendUserNotification(uid, "session_created", payload); err != nil {
			n.logger.Warn("failed to send SSE user notification",
				"userID", uid, "taskID", task.ID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func taskOwners(task *store.ScheduledTask) []string {
	users := make([]string, 0, 2)
	seen := map[string]bool{}
	addUser := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		users = append(users, s)
	}
	if task.UserID != nil {
		addUser(*task.UserID)
	}
	addUser(task.CreatedBy)
	return users
}

const maxInboxResultChars = 12000

var internalMarkerRe = regexp.MustCompile(`(?:«+§?status_update:[^»«\n]{0,512}»+|«artifact_return:[^»«\n]{0,512}»)+`)

func stripInternalMarkers(text string) string {
	var b strings.Builder
	last := 0
	for _, loc := range internalMarkerRe.FindAllStringIndex(text, -1) {
		b.WriteString(text[last:loc[0]])
		before, _ := utf8.DecodeLastRuneInString(text[:loc[0]])
		after, _ := utf8.DecodeRuneInString(text[loc[1]:])
		if loc[0] > 0 && !unicode.IsSpace(before) && (unicode.IsLetter(after) || unicode.IsDigit(after)) {
			b.WriteString("\n\n")
		}
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func scheduledResultText(resultSummary *string) (string, bool) {
	if resultSummary == nil || *resultSummary == "" {
		return "", true
	}
	var summary struct {
		AgentResponse     string `json:"agentResponse"`
		AgentResponseFull string `json:"agentResponseFull"`
	}
	if err := json.Unmarshal([]byte(*resultSummary), &summary); err != nil {
		return "", false
	}
	raw := summary.AgentResponseFull
	if raw == "" {
		raw = summary.AgentResponse
	}
	body := strings.TrimSpace(stripInternalMarkers(raw))
	if runes := []rune(body); len(runes) > maxInboxResultChars {
		body = string(runes[:maxInboxResultChars]) + "…"
	}
	return body, true
}

func scheduledNotificationTitle(taskName string, exec *store.ScheduledTaskExecution) string {
	if exec.BudgetStop != nil {
		return taskName + " stopped by a usage budget"
	}
	switch exec.Status {
	case "completed":
		return taskName + " completed"
	case "failed":
		return taskName + " failed"
	case "timeout":
		return taskName + " timed out"
	}
	return taskName + " finished"
}

const scheduledInboxDedupPrefix = "scheduled_task:"

func (n *NotificationService) sendInbox(ctx context.Context, exec *store.ScheduledTaskExecution, sessionID string, task *store.ScheduledTask, artifacts []a2a.NotifyArtifact) error {
	users := taskOwners(task)
	if len(users) == 0 {
		return fmt.Errorf("task %s has no owner to notify", task.ID)
	}

	title := scheduledNotificationTitle(task.Name, exec)
	message := fmt.Sprintf("Status: %s", exec.Status)
	if body, ok := scheduledResultText(exec.ResultSummary); !ok {
		n.logger.Warn("scheduled result_summary unparseable; sending status-only notification",
			"taskID", task.ID, "execID", exec.ID)
	} else if body != "" {
		message = body
	}
	if exec.ErrorMessage != nil && *exec.ErrorMessage != "" {
		message = *exec.ErrorMessage
	}
	if exec.BudgetStop != nil {
		message += " " + budgetStreakNote
	}
	dedup := scheduledInboxDedupPrefix + exec.ID
	baseMeta := map[string]any{
		"scheduled_task_id": task.ID,
		"execution_id":      exec.ID,
		"status":            exec.Status,
	}
	ownerID := task.ResolveOwnerUserID()
	linkOwnerSession := sessionID != "" && exec.Status == "completed"

	var firstErr error
	for _, uid := range users {
		meta := baseMeta
		if linkOwnerSession && uid == ownerID {
			meta = maps.Clone(baseMeta)
			meta["session_id"] = sessionID
			meta["link_label"] = "View result"
			if task.TargetAgentName != "" {
				meta["agent_id"] = task.TargetAgentName
			}
		}
		req := a2a.NotifyRequest{
			RequestID:       idgen.New(),
			RecipientUserID: uid,
			Mode:            a2a.NotifyModeNotify,
			SenderAgent:     "scheduled_task",
			Category:        a2a.CategoryScheduledTask,
			Title:           title,
			Message:         message,
			Metadata:        meta,
			Artifacts:       artifacts,
			Dedup:           dedup,
		}
		data, err := json.Marshal(req)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := n.brk.PublishGuaranteed(ctx, a2a.NotifyRequestTopic(task.Namespace, uid), &broker.Message{Payload: data}); err != nil {
			n.logger.Warn("failed to publish inbox NotifyRequest",
				"userID", uid, "taskID", task.ID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (n *NotificationService) sendWebhook(ctx context.Context, config map[string]any, payload map[string]any) error {
	rawURL, _ := config["url"].(string)
	if rawURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	if !n.skipURLValidation {
		if err := validateWebhookURL(rawURL); err != nil {
			return fmt.Errorf("webhook URL validation: %w", err)
		}
	}

	method, _ := config["method"].(string)
	if method == "" {
		method = "POST"
	}
	method = strings.ToUpper(method)
	if err := validateWebhookMethod(method); err != nil {
		return err
	}

	headers, _ := config["headers"].(map[string]any)

	for k := range headers {
		if deniedWebhookHeaders[strings.ToLower(k)] {
			return fmt.Errorf("webhook headers contain denied security-sensitive key: %s", k)
		}
	}

	webhookPayload := formatWebhookPayload(payload, config)

	body, err := json.Marshal(webhookPayload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", withoutRequestURL(err))
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if s, ok := v.(string); ok {
			req.Header.Set(k, s)
		}
	}

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", withoutRequestURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func validateWebhookURL(rawURL string) error {
	return validateWebhookURLWith(rawURL, net.DefaultResolver.LookupIPAddr)
}

func validateWebhookURLWith(rawURL string, lookup ipLookup) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", withoutRequestURL(err))
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s'; only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	ctx, cancel := context.WithTimeout(context.Background(), webhookDialTimeout)
	defer cancel()
	addrs, err := lookup(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		return errWebhookHostNotVerifiable
	}
	for _, a := range addrs {
		if isBlockedIP(a.IP) {
			return errWebhookHostNotVerifiable
		}
	}

	return nil
}

func validateWebhookMethod(method string) error {
	if !slices.Contains(allowedWebhookMethods, method) {
		return fmt.Errorf("webhook method must be one of: %s", strings.Join(allowedWebhookMethods, ", "))
	}
	return nil
}

func withoutRequestURL(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return urlErr.Err
	}
	return err
}

func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	octets := addr.As16()
	switch {
	case nat64.Contains(addr):
		return netip.AddrFrom4([4]byte(octets[12:16])), true
	case sixToFour.Contains(addr):
		return netip.AddrFrom4([4]byte(octets[2:6])), true
	}
	return netip.Addr{}, false
}

func isBlockedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if embedded, ok := embeddedIPv4(addr); ok {
		addr = embedded
	}
	return slices.ContainsFunc(blockedPrefixes, func(p netip.Prefix) bool { return p.Contains(addr) })
}

func formatWebhookPayload(payload map[string]any, config map[string]any) any {
	webhookType, _ := config["webhook_type"].(string)

	switch webhookType {
	case "slack":
		return formatSlackPayload(payload)
	case "teams":
		return formatTeamsPayload(payload)
	default:
		return payload
	}
}

func formatSlackPayload(payload map[string]any) map[string]any {
	status := fmt.Sprint(payload["status"])
	taskName := fmt.Sprint(payload["task_name"])
	execID := fmt.Sprint(payload["execution_id"])

	statusEmoji := "pass"
	if status != "completed" {
		statusEmoji = "fail"
	}

	return map[string]any{
		"text": fmt.Sprintf("Scheduled Task: %s (%s)", taskName, statusEmoji),
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": fmt.Sprintf(
						"*Scheduled Task Execution*\n\n*Task:* %s\n*Status:* %s\n*Execution ID:* `%s`",
						taskName, status, execID,
					),
				},
			},
		},
	}
}

func formatTeamsPayload(payload map[string]any) map[string]any {
	status := fmt.Sprint(payload["status"])
	taskName := fmt.Sprint(payload["task_name"])
	execID := fmt.Sprint(payload["execution_id"])

	statusColor := "00FF00"
	if status != "completed" {
		statusColor = "FF0000"
	}

	return map[string]any{
		"@type":      "MessageCard",
		"@context":   "https://schema.org/extensions",
		"summary":    fmt.Sprintf("Scheduled Task: %s", taskName),
		"themeColor": statusColor,
		"title":      fmt.Sprintf("Scheduled Task Execution: %s", status),
		"sections": []map[string]any{
			{
				"facts": []map[string]any{
					{"name": "Task", "value": taskName},
					{"name": "Status", "value": status},
					{"name": "Execution ID", "value": execID},
				},
			},
		},
	}
}

func ValidateNotificationConfig(raw string) error {
	var cfg NotificationConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("invalid notification config JSON: %w", err)
	}

	for _, ch := range cfg.Channels {
		switch ch.Type {
		case "sse", "email", "inbox":
		case "webhook":
			rawURL, _ := ch.Config["url"].(string)
			if rawURL == "" {
				return fmt.Errorf("webhook channel requires a 'url' in config")
			}
			if err := validateWebhookURL(rawURL); err != nil {
				return fmt.Errorf("webhook URL validation: %w", err)
			}
			if method, _ := ch.Config["method"].(string); method != "" {
				if err := validateWebhookMethod(strings.ToUpper(method)); err != nil {
					return err
				}
			}
			headers, _ := ch.Config["headers"].(map[string]any)
			for k := range headers {
				if deniedWebhookHeaders[strings.ToLower(k)] {
					return fmt.Errorf("webhook headers contain denied security-sensitive key: %s", k)
				}
			}
		default:
			return fmt.Errorf("unknown notification channel type: %s", ch.Type)
		}
	}

	return nil
}
