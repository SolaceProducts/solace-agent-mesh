// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
)

var ErrTaskNotFound = errors.New("scheduled task not found")

var ErrTaskAlreadyRunning = errors.New("scheduled task is currently executing")

const interactiveStateErrorMessage = "Task requires user authentication or input which is not available in scheduled (non-interactive) executions. Please ensure the required tool credentials are pre-configured before scheduling this task."

var allowedMetadataKeys = map[string]bool{
	"priority": true,
	"tags":     true,
	"category": true,
	"source":   true,
}

const maxResultSummaryLen = 1000

const maxResultSummaryFullLen = 200_000

const maxErrorMessageLen = 256

type ScheduledTaskService struct {
	cfg          *ScheduledTaskServiceConfig
	repo         *store.ScheduledTaskRepo
	sessRepo     *store.SessionRepo
	chatRepo     *store.ChatTaskRepo
	sessKeyRepo  *store.GatewaySessionKeyRepo
	sseRepo      *store.SSEEventRepo
	core         *GatewayCore
	notifier     *NotificationService
	userNotifier UserNotifier
	logger       *slog.Logger
	sendBudget   time.Duration

	scheduleRequestHandler func(context.Context, *broker.Message)
	quietStoreTimeout      time.Duration

	loopCtx context.Context
	cancel  context.CancelFunc
}

func NewScheduledTaskService(
	cfg *ScheduledTaskServiceConfig,
	repo *store.ScheduledTaskRepo,
	sessRepo *store.SessionRepo,
	chatRepo *store.ChatTaskRepo,
	sessKeyRepo *store.GatewaySessionKeyRepo,
	sseRepo *store.SSEEventRepo,
	logger *slog.Logger,
) *ScheduledTaskService {
	return &ScheduledTaskService{
		cfg:         cfg,
		repo:        repo,
		sessRepo:    sessRepo,
		chatRepo:    chatRepo,
		sessKeyRepo: sessKeyRepo,
		sseRepo:     sseRepo,
		logger:      logger.With("logger_name", "scheduled-tasks"),
	}
}

func (s *ScheduledTaskService) SetNotifier(n *NotificationService) {
	s.notifier = n
}

func (s *ScheduledTaskService) SetUserNotifier(n UserNotifier) {
	s.userNotifier = n
}

func (s *ScheduledTaskService) Notifier() *NotificationService {
	return s.notifier
}

func (s *ScheduledTaskService) Init(core *GatewayCore) error {
	s.core = core
	return nil
}

func (s *ScheduledTaskService) Start(ctx context.Context, wg *sync.WaitGroup) {
	loopCtx, cancel := context.WithCancel(ctx)
	s.loopCtx = loopCtx
	s.cancel = cancel

	wg.Go(func() {
		s.scheduleRequestLoop(loopCtx)
	})
}

func (s *ScheduledTaskService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.logger.Info("scheduled task service stopped")
}

func (s *ScheduledTaskService) ExecuteNow(ctx context.Context, taskID string) (string, error) {
	return s.triggerNow(ctx, taskID, "", store.TriggerTypeManual)
}

func (s *ScheduledTaskService) TriggerTaskNow(ctx context.Context, taskID, triggeredBy string) (string, error) {
	return s.triggerNow(ctx, taskID, triggeredBy, store.TriggerTypeManual)
}

func (s *ScheduledTaskService) triggerNow(ctx context.Context, taskID, triggeredBy, triggerType string) (string, error) {
	task, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("load task %s: %w", taskID, err)
	}
	if task == nil || task.DeletedAt != nil {
		return "", ErrTaskNotFound
	}
	if refusal := s.spentBudgetBlock(ctx, task); refusal != nil {
		return "", refusal
	}
	var triggeredByPtr *string
	if triggeredBy != "" {
		triggeredByPtr = &triggeredBy
	}
	run := s.pendingRun(task, time.Now(), 0, triggerType, triggeredByPtr)
	busy, err := s.repo.CreateExecutionIfIdle(ctx, run)
	switch {
	case err != nil:
		return "", fmt.Errorf("record run of %s: %w", taskID, err)
	case busy:
		return "", ErrTaskAlreadyRunning
	}
	s.emitUserNotification("execution_queued", map[string]any{
		"execution_id": run.ID,
		"task_id":      task.ID,
		"task_name":    task.Name,
		"trigger_type": run.TriggerType,
	}, s.resolveUserID(task), task.CreatedBy)
	return run.ID, nil
}

func (s *ScheduledTaskService) spentBudgetBlock(ctx context.Context, task *store.ScheduledTask) error {
	if s.core == nil {
		return nil
	}
	runAs := storedRunAs(task)
	caller, err := s.runCaller(ctx, task, runAs, nil)
	if err != nil {
		s.logger.Debug("spent-budget pre-check skipped: the run's caller could not be resolved",
			"taskID", task.ID, "runAs", runAs, "error", err)
		return nil
	}
	key, principal := s.core.budgetUser(caller, runWireUserID(task, runAs))
	if refusal := s.core.BudgetGate().CheckKnownBlock(key); refusal != nil {
		return budget.WithPrincipal(refusal, principal)
	}
	return nil
}

func (s *ScheduledTaskService) ownerCaller(ctx context.Context, task *store.ScheduledTask) (*auth.Subject, error) {
	resolver := s.core.IdentityResolver()
	if resolver == nil {
		return auth.ForUserIDOnly(s.resolveUserID(task)), nil
	}
	return resolver.ResolveIdentity(ctx, task.CreatedBy)
}

func FirstRunAt(task *store.ScheduledTask, now time.Time, fireImmediately bool) (*int64, error) {
	at, ok, err := scheduler.FirstFireTime(scheduler.ScheduleType(task.ScheduleType),
		task.ScheduleExpression, task.Timezone, now, fireImmediately)
	if err != nil {
		return nil, fmt.Errorf("first fire time of scheduled task %s: %w", task.ID, err)
	}
	ms := at.UnixMilli()
	if !ok || (task.ScheduleType == string(scheduler.ScheduleTypeOneTime) && task.LastRunAt != nil && *task.LastRunAt >= ms) {
		return nil, nil
	}
	return &ms, nil
}

const maxChatErrorLen = 4000

func unsuccessfulEnd(task *a2a.Task) (string, bool) {
	if task == nil {
		return "", false
	}
	switch task.Status.State {
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCancelled:
		return failedTaskMessage(task), true
	default:
		return "", false
	}
}

func failedTaskMessage(task *a2a.Task) string {
	if task.Status.Message != nil {
		if text := strings.TrimSpace(extractTextFromMessage(task.Status.Message)); text != "" {
			return text
		}
		for _, part := range task.Status.Message.Parts {
			data, ok := part.Data.(map[string]any)
			if !ok {
				continue
			}
			if msg, _ := data["error_message"].(string); strings.TrimSpace(msg) != "" {
				return strings.TrimSpace(msg)
			}
		}
	}
	if msg, _ := task.Metadata["error"].(string); strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	return fmt.Sprintf("task ended in state %q", task.Status.State)
}

func (s *ScheduledTaskService) emitUserNotification(eventType string, data map[string]any, candidates ...string) {
	if s.userNotifier == nil {
		return
	}
	seen := map[string]bool{}
	for _, uid := range candidates {
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		if err := s.userNotifier.SendUserNotification(uid, eventType, data); err != nil {
			s.logger.Warn("failed to send user notification",
				"eventType", eventType, "userID", uid, "error", err)
		}
	}
}

func (s *ScheduledTaskService) emitSessionCreated(execID, sessionID string, task *store.ScheduledTask, status string) {
	s.emitUserNotification("session_created", map[string]any{
		"execution_id": execID,
		"task_id":      task.ID,
		"task_name":    task.Name,
		"session_id":   sessionID,
		"status":       status,
	}, s.resolveUserID(task), task.CreatedBy)
}

const maxRAGEntriesPerTask = 500

func mergeRAGEntries(existing, entries []ragEntry) (updated []ragEntry, added, replaced int, capped bool) {
	updated = make([]ragEntry, len(existing))
	copy(updated, existing)

	existingKeys := make(map[string]bool, len(updated))
	envelopeAt := make(map[string]int, len(updated))
	for i, e := range updated {
		existingKeys[ragEntryKey(e.envelope)] = true
		if !e.fromSignal {
			continue
		}
		if id, ok := ragEnvelopeIdentity(e.envelope); ok {
			envelopeAt[id] = i
		}
	}
	for _, e := range entries {
		id, hasID := ragEnvelopeIdentity(e.envelope)
		if e.fromSignal && hasID {
			if i, seen := envelopeAt[id]; seen {
				delete(existingKeys, ragEntryKey(updated[i].envelope))
				updated[i] = e
				existingKeys[ragEntryKey(e.envelope)] = true
				replaced++
				continue
			}
		}
		if len(updated) >= maxRAGEntriesPerTask {
			capped = true
			continue
		}
		key := ragEntryKey(e.envelope)
		if existingKeys[key] {
			continue
		}
		updated = append(updated, e)
		existingKeys[key] = true
		if e.fromSignal && hasID {
			envelopeAt[id] = len(updated) - 1
		}
		added++
	}
	return updated, added, replaced, capped
}

func extractRAGFromParts(msg *a2a.Message) []map[string]any {
	return ragEnvelopes(extractRAGEntries(msg))
}

type ragEntry struct {
	envelope   map[string]any
	fromSignal bool
}

func ragEnvelopes(entries []ragEntry) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, maps.Clone(e.envelope))
	}
	return out
}

func extractRAGEntries(msg *a2a.Message) []ragEntry {
	if msg == nil {
		return nil
	}
	var entries []ragEntry
	for _, part := range msg.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		data, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := data["type"].(string)
		var entry map[string]any
		fromSignal := false
		switch typ {
		case "tool_result":
			entry, ok = ragEnvelopeFromToolResult(data)
		case string(a2a.SignalRAGInfoUpdate):
			entry, ok = ragEnvelopeFromInfoUpdate(data)
			fromSignal = true
		default:
			continue
		}
		if ok {
			entries = append(entries, ragEntry{envelope: entry, fromSignal: fromSignal})
		}
	}
	return entries
}

func ragEnvelopeFromToolResult(data map[string]any) (map[string]any, bool) {
	resultData, ok := data["result_data"].(map[string]any)
	if !ok {
		return nil, false
	}
	ragMeta, ok := resultData["rag_metadata"].(map[string]any)
	if !ok {
		return nil, false
	}
	sources, ok := ragMeta["sources"].([]any)
	if !ok || len(sources) == 0 {
		return nil, false
	}
	return ragMeta, true
}

func ragEnvelopeFromInfoUpdate(data map[string]any) (map[string]any, bool) {
	sources, ok := data["sources"].([]any)
	if !ok || len(sources) == 0 {
		return nil, false
	}
	searchType, _ := data["search_type"].(string)
	if searchType == "" {
		searchType = defaultRAGSearchType
	}
	query, _ := data["query"].(string)
	title, _ := data["title"].(string)
	timestamp, _ := data["timestamp"].(string)
	isComplete, _ := data["is_complete"].(bool)
	searchTurn := 0
	if v, ok := data["search_turn"].(float64); ok {
		searchTurn = int(v)
	}
	envelope := map[string]any{
		"query":      query,
		"title":      title,
		"searchType": searchType,
		"timestamp":  timestamp,
		"sources":    sources,
		"isComplete": isComplete,
	}
	envelope["searchTurn"] = searchTurn
	return envelope, true
}

func ragEnvelopeIdentity(entry map[string]any) (string, bool) {
	searchType, ok := entry["searchType"].(string)
	if !ok {
		return "", false
	}
	if _, hasSources := entry["sources"]; !hasSources {
		return "", false
	}
	query, _ := entry["query"].(string)
	turn := 0
	switch v := entry["searchTurn"].(type) {
	case int:
		turn = v
	case float64:
		turn = int(v)
	}
	return fmt.Sprintf("%s\x00%s\x00%d", searchType, query, turn), true
}

const defaultRAGSearchType = "deep_research"

func ragEntryKey(entry map[string]any) string {
	for _, field := range []string{"source_url", "url", "source"} {
		if v, ok := entry[field].(string); ok && v != "" {
			return v
		}
	}
	data, _ := json.Marshal(entry)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func extractRAGFromTask(task *a2a.Task) []map[string]any {
	if task == nil {
		return nil
	}

	var entries []map[string]any
	seen := make(map[string]bool)

	addFromMessage := func(msg *a2a.Message) {
		for _, entry := range extractRAGFromParts(msg) {
			key := ragEntryKey(entry)
			if !seen[key] {
				entries = append(entries, entry)
				seen[key] = true
			}
		}
	}

	addFromMessage(task.Status.Message)

	for i := range task.History {
		addFromMessage(&task.History[i])
	}

	return entries
}

func mergeRAGData(fromResponse, accumulated []map[string]any) []map[string]any {
	if len(fromResponse) == 0 && len(accumulated) == 0 {
		return nil
	}
	merged := make([]map[string]any, 0, len(fromResponse)+len(accumulated))
	seen := make(map[string]bool, len(fromResponse)+len(accumulated))
	for _, entry := range fromResponse {
		key := ragEntryKey(entry)
		if !seen[key] {
			merged = append(merged, entry)
			seen[key] = true
		}
	}
	for _, entry := range accumulated {
		key := ragEntryKey(entry)
		if !seen[key] {
			merged = append(merged, entry)
			seen[key] = true
		}
	}
	return merged
}

func buildTaskSnapshot(task *store.ScheduledTask, logger *slog.Logger) *string {
	if task == nil {
		return nil
	}
	var taskMessage any
	if err := json.Unmarshal([]byte(task.TaskMessage), &taskMessage); err != nil {
		taskMessage = task.TaskMessage
	}
	snap := map[string]any{
		"name":               task.Name,
		"description":        task.Description,
		"scheduleType":       task.ScheduleType,
		"scheduleExpression": task.ScheduleExpression,
		"timezone":           task.Timezone,
		"targetAgentName":    task.TargetAgentName,
		"targetType":         task.TargetType,
		"taskMessage":        taskMessage,
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to build task snapshot", "taskID", task.ID, "error", err)
		}
		return nil
	}
	s := string(blob)
	return &s
}

func (s *ScheduledTaskService) resolveUserID(task *store.ScheduledTask) string {
	return task.ResolveOwnerUserID()
}

func (s *ScheduledTaskService) buildMessage(
	task *store.ScheduledTask,
	execID, sessionID string,
) a2a.Message {
	text := s.renderTemplate(task, execID)

	metadata := map[string]any{
		"agent_name":      task.TargetAgentName,
		"sessionBehavior": "RUN_BASED",
		"returnArtifacts": true,
	}

	if task.TaskMetadata != nil {
		var taskMeta map[string]any
		if err := json.Unmarshal([]byte(*task.TaskMetadata), &taskMeta); err == nil {
			for k, v := range taskMeta {
				if allowedMetadataKeys[k] {
					metadata[k] = v
				}
			}
		}
	}

	metadata["source"] = a2a.SourceScheduler
	metadata[a2a.MetadataScheduledTaskID] = task.ID
	s.allowQuietEnd(metadata, task)

	parts := []a2a.Part{{Kind: "text", Text: text}}
	parts = append(parts, s.dataParts(task, execID)...)

	return a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     parts,
		ContextID: sessionID,
		Metadata:  metadata,
	}
}

func decodeTaskMessage(raw string) (parts []map[string]any, text string) {
	if err := json.Unmarshal([]byte(raw), &parts); err == nil {
		return parts, ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return nil, s
	}
	return nil, raw
}

func isDataPart(p map[string]any) bool {
	if k, _ := p["kind"].(string); k == "data" {
		return true
	}
	t, _ := p["type"].(string)
	return t == "data"
}

func isTextPart(p map[string]any) bool {
	if k, _ := p["kind"].(string); k == "text" {
		return true
	}
	t, _ := p["type"].(string)
	return t == "text"
}

func looksLikeDataObject(value string) bool {
	var obj map[string]any
	if err := json.Unmarshal([]byte(value), &obj); err != nil {
		return false
	}
	return isDataPart(obj)
}

func looksLikeMalformedPartsArray(value string) bool {
	var arr []any
	return json.Unmarshal([]byte(value), &arr) == nil
}

func (s *ScheduledTaskService) dataParts(task *store.ScheduledTask, execID string) []a2a.Part {
	logger := s.log().With("taskID", task.ID, "execID", execID)

	parts, text := decodeTaskMessage(task.TaskMessage)
	if parts == nil {
		switch {
		case looksLikeDataObject(text):
			logger.Warn("taskMessage is a single JSON object, not an array of parts — " +
				"treating it as plain text; wrap it in [ ] to deliver a data part")
		case looksLikeMalformedPartsArray(text):
			logger.Warn("taskMessage is a JSON array but not a uniform array of part objects — " +
				"treating it as plain text; no data will be delivered")
		}
		return nil
	}

	candidates := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if isDataPart(p) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if task.TargetType != "workflow" {
		logger.Debug("dropping data parts for non-workflow target", "count", len(candidates))
		return nil
	}

	out := make([]a2a.Part, 0, len(candidates))
	for _, p := range candidates {
		d, ok := p["data"]
		if !ok || d == nil {
			logger.Warn("scheduled task data part has no data — skipping")
			continue
		}
		if _, isObj := d.(map[string]any); !isObj {
			logger.Warn("scheduled task data part is not a JSON object — the workflow will ignore it",
				"dataType", fmt.Sprintf("%T", d))
		}
		out = append(out, a2a.NewDataPart(s.substituteTemplateVars(d, task, execID)))
	}
	if len(out) > 1 {
		logger.Warn("scheduled task has multiple data parts — the workflow only consumes the first",
			"count", len(out))
	}
	return out
}

func (s *ScheduledTaskService) log() *slog.Logger {
	if s.logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.logger
}

func (s *ScheduledTaskService) renderTemplate(task *store.ScheduledTask, execID string) string {
	parts, text := decodeTaskMessage(task.TaskMessage)
	if parts == nil {
		return s.replaceTemplateVars(text, task, execID)
	}

	var texts []string
	for _, part := range parts {
		if t, ok := part["text"].(string); ok {
			texts = append(texts, t)
		}
	}
	return s.replaceTemplateVars(strings.Join(texts, "\n"), task, execID)
}

func (s *ScheduledTaskService) replaceTemplateVars(text string, task *store.ScheduledTask, execID string) string {
	loc, err := time.LoadLocation(task.Timezone)
	if err != nil {
		loc = time.UTC
	}
	runDate := time.Now().In(loc).Format("2006-01-02 15:04:05 MST")

	text = strings.ReplaceAll(text, "{{schedule.name}}", task.Name)
	text = strings.ReplaceAll(text, "{{schedule.run_date}}", runDate)
	text = strings.ReplaceAll(text, "{{schedule.run_count}}", fmt.Sprintf("%d", task.RunCount))
	text = strings.ReplaceAll(text, "{{execution.id}}", execID)
	return text
}

func (s *ScheduledTaskService) substituteTemplateVars(v any, task *store.ScheduledTask, execID string) any {
	switch val := v.(type) {
	case string:
		return s.replaceTemplateVars(val, task, execID)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, elem := range val {
			out[k] = s.substituteTemplateVars(elem, task, execID)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = s.substituteTemplateVars(elem, task, execID)
		}
		return out
	default:
		return val
	}
}

func (s *ScheduledTaskService) dispatchNotifications(ctx context.Context, execID, sessionID string, task *store.ScheduledTask, now time.Time) bool {
	if s.notifier == nil {
		return true
	}

	exec, err := s.repo.GetExecutionByID(ctx, execID)
	if err != nil || exec == nil {
		s.logger.Error("failed to load execution for notifications", "execID", execID, "error", err)
		return true
	}

	if exec.RefusalNotice != nil && *exec.RefusalNotice == store.RefusalNoticeRepeat {
		s.logger.Debug("scheduled run repeats a configuration refusal whose notice was delivered; not notifying",
			"execID", exec.ID, "taskID", exec.ScheduledTaskID)
		return true
	}

	var artifacts []a2a.NotifyArtifact
	if isNotifiableStatus(exec.Status) && notificationConfigIncludesArtifacts(effectiveNotificationConfig(task)) {
		artifacts = s.buildNotifyArtifacts(ctx, exec, task)
	}
	sent := s.notifier.NotifyExecutionComplete(ctx, exec, sessionID, task, artifacts)
	if sent == "" {
		return true
	}
	if err := s.repo.UpdateExecution(ctx, execID, map[string]any{
		"notifications_sent": sent,
	}); err != nil {
		s.logger.Warn("failed to record a scheduled run's notifications", "execID", execID, "error", err)
	}
	exec.NotificationsSent = &sent
	_, owed := unsentBudgetNotice(exec)
	return !owed || budgetNoticeWindowPassed(exec, now)
}

func isNotifiableStatus(status string) bool {
	switch status {
	case "completed", "failed", "timeout":
		return true
	}
	return false
}

func notificationConfigIncludesArtifacts(cfgJSON string) bool {
	if cfgJSON == "" {
		return false
	}
	var cfg NotificationConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return false
	}
	return cfg.IncludeArtifacts
}

func (s *ScheduledTaskService) buildNotifyArtifacts(ctx context.Context, exec *store.ScheduledTaskExecution, task *store.ScheduledTask) []a2a.NotifyArtifact {
	if s.sessKeyRepo == nil {
		return nil
	}
	artSvc := s.core.Services().Artifacts()
	if artSvc == nil {
		return nil
	}
	mapping, err := s.sessKeyRepo.GetByGatewayAndExternalKeys(ctx, store.GatewayTypeScheduled, []string{exec.ID})
	if err != nil {
		s.logger.Warn("notify artifacts: session lookup failed; delivering text only",
			"execID", exec.ID, "error", err)
		return nil
	}
	session, ok := mapping[exec.ID]
	if !ok || session == "" {
		return nil
	}
	owner := exec.ArtifactUserID(task)
	loc := artSvc.Resolve("", owner, session, "")
	keys, err := artSvc.Inner.ListKeys(ctx, artifact.KeyScope{AppName: loc.AppName, UserID: loc.UserID, SessionID: loc.SessionID})
	if err != nil {
		s.logger.Warn("notify artifacts: listing session artifacts failed; delivering text only",
			"execID", exec.ID, "session", session, "error", err)
		return nil
	}
	userFiles := &notifyUserFiles{s: s, execID: exec.ID, session: session, owner: owner, artSvc: artSvc}
	out := make([]a2a.NotifyArtifact, 0, len(keys))
	for _, name := range keys {
		if name == "" || artifact.IsInternalArtifact(name) {
			continue
		}
		notify := a2a.NotifyArtifact{
			AgentName: "",
			UserID:    owner,
			SessionID: session,
			Filename:  name,
		}
		version, deliver := userFiles.versionToDeliver(ctx, name)
		if !deliver {
			continue
		}
		notify.Version = version
		out = append(out, notify)
	}
	userFiles.logLeftOut()
	if len(out) == 0 {
		return nil
	}
	return out
}

type notifyUserFiles struct {
	s       *ScheduledTaskService
	execID  string
	session string
	owner   string
	artSvc  *artifact.ScopedService

	loaded     bool
	referenced ReferencedUserFiles
	notNamed   int
	olderNamed int
	lookupFail int
	firstErr   error
}

func (n *notifyUserFiles) versionToDeliver(ctx context.Context, name string) (*int, bool) {
	if _, userScoped := artifact.ResolveFilename(name); !userScoped {
		return nil, true
	}
	if !n.loaded {
		n.referenced, n.loaded = n.s.executionReferencedUserFiles(ctx, n.execID, n.session, n.owner), true
	}
	named, isNamed := n.referenced[name]
	if !isNamed {
		n.notNamed++
		return nil, false
	}
	latest, err := LatestArtifactVersion(ctx, n.artSvc, n.owner, n.session, name)
	switch {
	case err != nil:
		n.lookupFail++
		if n.firstErr == nil {
			n.firstErr = err
		}
		return nil, false
	case latest != named:
		n.olderNamed++
		return nil, false
	}
	return &latest, true
}

func (n *notifyUserFiles) logLeftOut() {
	if n.notNamed > 0 {
		n.s.logger.Debug("notify artifacts: user-scoped files the execution does not reference left out",
			"execID", n.execID, "count", n.notNamed)
	}
	if n.olderNamed > 0 {
		n.s.logger.Warn("notify artifacts: user-scoped files left out, a later version was written after the execution named them",
			"execID", n.execID, "count", n.olderNamed)
	}
	if n.lookupFail > 0 {
		n.s.logger.Warn("notify artifacts: user-scoped files left out, their latest version could not be read",
			"execID", n.execID, "count", n.lookupFail, "error", n.firstErr)
	}
}

func (s *ScheduledTaskService) executionReferencedUserFiles(ctx context.Context, execID, session, owner string) ReferencedUserFiles {
	if s.chatRepo == nil {
		s.logger.Warn("notify artifacts: user-scoped files left out, no chat store to tell which ones the execution produced",
			"execID", execID)
		return nil
	}
	tasks, err := s.chatRepo.ListBySession(ctx, session)
	if err != nil {
		s.logger.Warn("notify artifacts: user-scoped files left out, the execution's chat tasks could not be read",
			"execID", execID, "session", session, "error", err)
		return nil
	}
	referenced, err := UserScopedFilesReferencedBy(tasks, owner)
	if err != nil {
		s.logger.Warn("notify artifacts: chat tasks left out of the files the execution references",
			"execID", execID, "session", session, "error", err)
	}
	return referenced
}

func (s *ScheduledTaskService) saveChatTask(
	ctx context.Context,
	task *store.ScheduledTask,
	sessionID string,
	a2aTaskID string,
	a2aTask *a2a.Task,
	errMsg string,
	ragData []map[string]any,
) {
	if s.chatRepo == nil {
		return
	}

	isError := errMsg != ""

	userMessage := extractUserMessage(task.TaskMessage)
	userID := s.resolveUserID(task)

	var bubbles []map[string]any

	if userMessage != "" {
		bubbles = append(bubbles, map[string]any{
			"id":   idgen.New(),
			"type": "user",
			"text": userMessage,
		})
	}

	if isError {
		bubbles = append(bubbles, map[string]any{
			"id":      idgen.New(),
			"type":    "agent",
			"text":    errMsg,
			"parts":   []map[string]any{{"kind": "text", "text": errMsg}},
			"isError": true,
		})
	} else if a2aTask != nil {
		agentText, artifacts := extractChatContent(a2aTask, sessionID)

		if agentText != "" || len(artifacts) > 0 {
			var parts []map[string]any
			if agentText != "" {
				parts = append(parts, map[string]any{"kind": "text", "text": agentText})
			}

			var artifactTextBuf strings.Builder
			for _, art := range artifacts {
				name, _ := art["name"].(string)
				artifactTextBuf.WriteString("\n\u00abartifact_return:")
				artifactTextBuf.WriteString(name)
				artifactTextBuf.WriteString("\u00bb")
				parts = append(parts, art)
			}

			bubbles = append(bubbles, map[string]any{
				"id":      idgen.New(),
				"type":    "agent",
				"text":    agentText + artifactTextBuf.String(),
				"parts":   parts,
				"isError": false,
			})
		}
	}

	if len(bubbles) == 0 {
		return
	}

	bubblesJSON, err := json.Marshal(bubbles)
	if err != nil {
		s.logger.Warn("failed to marshal chat bubbles", "sessionID", sessionID, "error", err)
		return
	}

	status := "completed"
	if isError {
		status = "error"
	}
	taskMetadata := map[string]any{
		"schemaVersion": 1,
		"status":        status,
		"agentName":     task.TargetAgentName,
		"source":        "scheduler",
	}

	if len(ragData) > 0 {
		for _, entry := range ragData {
			if _, ok := entry["taskId"]; !ok {
				entry["taskId"] = a2aTaskID
			}
		}
		taskMetadata["ragData"] = ragData
		s.logger.Debug("including RAG data in ChatTask",
			"sessionID", sessionID, "ragEntries", len(ragData))
	}

	metadataJSON, err := json.Marshal(taskMetadata)
	if err != nil {
		s.logger.Warn("failed to marshal task metadata", "sessionID", sessionID, "error", err)
		return
	}

	metadataStr := string(metadataJSON)
	ct := &store.ChatTask{
		ID:             a2aTaskID,
		SessionID:      sessionID,
		UserID:         userID,
		UserMessage:    &userMessage,
		MessageBubbles: string(bubblesJSON),
		TaskMetadata:   &metadataStr,
	}
	if err := s.chatRepo.Create(ctx, ct); err != nil {
		s.logger.Warn("failed to create ChatTask for scheduled execution",
			"sessionID", sessionID, "a2aTaskID", a2aTaskID, "error", err)
		return
	}
	s.logger.Info("created ChatTask for scheduled execution",
		"sessionID", sessionID, "userID", task.UserID, "a2aTaskID", a2aTaskID,
		"bubbles", len(bubbles), "isError", isError)

	if s.sessRepo != nil {
		if err := s.sessRepo.Touch(ctx, sessionID); err != nil {
			s.logger.Debug("failed to bump session updated_time",
				"sessionID", sessionID, "error", err)
		}
	}
}

func extractUserMessage(taskMessage string) string {
	parts, text := decodeTaskMessage(taskMessage)
	if parts == nil {
		return text
	}
	for _, part := range parts {
		if !isTextPart(part) {
			continue
		}
		if t, ok := part["text"].(string); ok {
			return t
		}
	}
	return ""
}

func extractChatContent(task *a2a.Task, sessionID string) (string, []map[string]any) {
	var agentText string
	var parts chatArtifactParts

	if task.Status.Message != nil {
		agentText = extractTextFromMessage(task.Status.Message)
		extractArtifactsFromMessage(task.Status.Message, parts.add)
	}

	for i := range task.History {
		msg := &task.History[i]
		extractArtifactsFromMessage(msg, parts.add)
	}

	for _, key := range []string{"produced_artifacts", "artifact_manifest"} {
		addManifestArtifacts(task.Metadata[key], sessionID, parts.add)
	}

	for _, art := range task.Artifacts {
		name := art.Name
		if name == "" {
			name = art.ArtifactID
		}
		parts.add(name, fmt.Sprintf("artifact://%s/%s", sessionID, url.PathEscape(name)), "")
	}

	return agentText, parts.list
}

type chatArtifactParts struct {
	list    []map[string]any
	indexOf map[string]int
}

func (c *chatArtifactParts) add(name, uri, mimeType string) {
	if name == "" || strings.HasPrefix(name, webContentArtifactPrefix) {
		return
	}
	if i, seen := c.indexOf[name]; seen {
		file := c.list[i]["file"].(map[string]any)
		if uriVersion(uri) > uriVersion(file["uri"].(string)) {
			file["uri"] = uri
		}
		return
	}
	if c.indexOf == nil {
		c.indexOf = map[string]int{}
	}
	c.indexOf[name] = len(c.list)
	file := map[string]any{"name": name, "uri": uri}
	if mimeType != "" {
		file["mime_type"] = mimeType
	}
	c.list = append(c.list, map[string]any{"kind": "artifact", "status": "completed", "name": name, "file": file})
}

func addManifestArtifacts(manifest any, sessionID string, add func(name, uri, mimeType string)) {
	list, _ := manifest.([]any)
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			name, _ = entry["filename"].(string)
		}
		mimeType, _ := entry["mime_type"].(string)
		uri := fmt.Sprintf("artifact://%s/%s", sessionID, url.PathEscape(name))
		if version := DataIntPtr(entry["version"]); version != nil {
			uri = fmt.Sprintf("%s?version=%d", uri, *version)
		}
		add(name, uri, mimeType)
	}
}

func uriVersion(uri string) int {
	if _, version := artifactURINameAndVersion(uri); version != nil {
		return *version
	}
	return -1
}

func extractTextFromMessage(msg *a2a.Message) string {
	var sb strings.Builder
	for _, part := range msg.Parts {
		if part.Kind == "text" && part.Text != "" {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func extractArtifactsFromMessage(msg *a2a.Message, add func(name, uri, mimeType string)) {
	for _, part := range msg.Parts {
		if part.Kind == "file" && part.File != nil && part.File.URI != "" {
			name := part.File.Name
			if name == "" {
				if idx := strings.LastIndex(part.File.URI, "/"); idx >= 0 {
					name = part.File.URI[idx+1:]
				} else {
					name = part.File.URI
				}
			}
			add(name, part.File.URI, part.File.MIMEType)
		}
	}
}

func buildResultSummary(task *a2a.Task, ragData []map[string]any) string {
	if task == nil {
		return ""
	}

	var agentText string
	if task.Status.Message != nil {
		var b strings.Builder
		for _, part := range task.Status.Message.Parts {
			if part.Kind == "text" && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		agentText = b.String()
	}

	result := make(map[string]any)
	if agentText != "" {
		result["agentResponse"] = truncate(agentText, maxResultSummaryLen)
		result["agentResponseFull"] = truncate(agentText, maxResultSummaryFullLen)
	}

	if len(task.History) > 0 {
		messages := make([]map[string]string, 0, len(task.History))
		for _, msg := range task.History {
			for _, part := range msg.Parts {
				if part.Kind == "text" && part.Text != "" {
					messages = append(messages, map[string]string{
						"role": string(msg.Role),
						"text": truncate(part.Text, maxResultSummaryLen),
					})
				}
			}
		}
		if len(messages) > 0 {
			result["messages"] = messages
		}
	}

	if len(ragData) > 0 {
		result["ragData"] = ragData
	}

	if marker := artifactReturnMarkers(task); marker != "" {
		if v, ok := result["agentResponseFull"].(string); ok {
			result["agentResponseFull"] = truncate(v+marker, maxResultSummaryFullLen)
		}
		if v, ok := result["agentResponse"].(string); ok {
			result["agentResponse"] = truncate(v+marker, maxResultSummaryLen)
		}
	}

	if len(result) == 0 {
		return ""
	}
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(data)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

func artifactReturnMarkers(task *a2a.Task) string {
	if task == nil || len(task.Artifacts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, art := range task.Artifacts {
		if art.Name == "" {
			continue
		}
		b.WriteString("\n«artifact_return:")
		b.WriteString(art.Name)
		b.WriteString("»")
	}
	return b.String()
}

func buildArtifactsSummary(task *a2a.Task) string {
	if task == nil || len(task.Artifacts) == 0 {
		return ""
	}

	var artifacts []map[string]any
	for _, art := range task.Artifacts {
		entry := map[string]any{
			"name": art.Name,
		}
		if art.Description != "" {
			entry["description"] = art.Description
		}
		artifacts = append(artifacts, entry)
	}

	data, err := json.Marshal(artifacts)
	if err != nil {
		return ""
	}
	return string(data)
}

func sanitizeError(msg string) string {
	if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
		msg = msg[:idx]
	}
	return truncate(msg, maxErrorMessageLen)
}

func strPtr(s string) *string {
	return &s
}
