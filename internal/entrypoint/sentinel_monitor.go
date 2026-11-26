// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type SentinelMonitorConfig struct {
	Enabled            bool
	TargetAgent        string
	OnTaskFailure      bool
	OnNegativeFeedback bool
	CooldownSeconds    int
	MaxConcurrent      int
	SystemUser         string

	NotifyOnResult   bool
	NotifyRecipients []string
}

func ParseSentinelMonitorConfig(m map[string]any) SentinelMonitorConfig {
	cfg := SentinelMonitorConfig{
		TargetAgent:        "Observability Agent",
		OnTaskFailure:      true,
		OnNegativeFeedback: true,
		CooldownSeconds:    300,
		MaxConcurrent:      1,
		SystemUser:         "sentinel-monitor",
	}
	if m == nil {
		return cfg
	}
	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v, ok := m["target_agent"].(string); ok && v != "" {
		cfg.TargetAgent = v
	}
	if v, ok := m["on_task_failure"].(bool); ok {
		cfg.OnTaskFailure = v
	}
	if v, ok := m["on_negative_feedback"].(bool); ok {
		cfg.OnNegativeFeedback = v
	}
	if v := getInt(m, "cooldown_seconds"); v > 0 {
		cfg.CooldownSeconds = v
	}
	if v := getInt(m, "max_concurrent"); v > 0 {
		cfg.MaxConcurrent = v
	}
	if v, ok := m["system_user"].(string); ok && v != "" {
		cfg.SystemUser = v
	}
	if v, ok := m["notify_on_result"].(bool); ok {
		cfg.NotifyOnResult = v
	}
	cfg.NotifyRecipients = getStringSlice(m, "notify_recipients")
	return cfg
}

type monitorGateway interface {
	SubmitTaskAttributedTo(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error
	Tasks() TaskRegistry
	PublishNotification(ctx context.Context, recipientUserID string, req a2a.NotifyRequest) error
}

type SentinelMonitorService struct {
	cfg    SentinelMonitorConfig
	gw     monitorGateway
	logger *slog.Logger

	wg sync.WaitGroup

	now func() time.Time

	mu        sync.Mutex
	stopped   bool
	ctx       context.Context
	cancel    context.CancelFunc
	inFlight  int
	lastFired map[string]time.Time
}

func NewSentinelMonitorService(cfg SentinelMonitorConfig, gw monitorGateway, logger *slog.Logger) *SentinelMonitorService {
	return &SentinelMonitorService{
		cfg:       cfg,
		gw:        gw,
		logger:    logger.With("logger_name", "sentinel-monitor"),
		now:       time.Now,
		lastFired: make(map[string]time.Time),
	}
}

func (s *SentinelMonitorService) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()
	s.logger.Info("sentinel monitor started",
		"target", s.cfg.TargetAgent, "cooldown_s", s.cfg.CooldownSeconds, "maxConcurrent", s.cfg.MaxConcurrent)
}

func (s *SentinelMonitorService) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

func (s *SentinelMonitorService) HandleTaskComplete(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
	info := s.gw.Tasks().Get(taskID)
	if isInvestigation(info) {
		s.notifyResult(taskID, task, a2aErr)
		return
	}
	if !s.cfg.OnTaskFailure || a2aErr == nil {
		return
	}
	agent, user := "", ""
	if info != nil {
		agent, user = info.AgentName, info.UserID
	}
	if agent == s.cfg.TargetAgent {
		return
	}
	reason := a2aErr.Message
	prompt := fmt.Sprintf(
		"Automated alert: task %s handled by agent %q failed: %s.\n"+
			"Investigate — pull the failed task and its execution detail, correlate with runtime logs, "+
			"identify the root cause, and state whether immediate action is warranted. Report concisely.",
		taskID, agent, reason)
	s.maybeInvestigate("failure:"+agent, prompt, user)
}

func isInvestigation(info *TaskInfo) bool {
	return info != nil && info.SystemInitiated
}

func (s *SentinelMonitorService) HandleFeedback(taskID, rating, comment, userID string) {
	if !s.cfg.OnNegativeFeedback || rating != "down" {
		return
	}
	if isInvestigation(s.gw.Tasks().Get(taskID)) {
		return
	}
	prompt := fmt.Sprintf(
		"Automated alert: a user gave negative feedback on task %s%s.\n"+
			"Investigate — review the task and its execution, correlate with logs, and report what likely "+
			"drove the dissatisfaction and whether it indicates a systemic issue.",
		taskID, commentSuffix(comment))
	s.maybeInvestigate("feedback:"+taskID, prompt, userID)
}

func commentSuffix(comment string) string {
	if comment == "" {
		return ""
	}
	return fmt.Sprintf(" (comment: %q)", comment)
}

func (s *SentinelMonitorService) evictCooldownsThatCannotSuppressAgain(now time.Time, cooldown time.Duration) {
	for sig, last := range s.lastFired {
		if now.Sub(last) >= cooldown {
			delete(s.lastFired, sig)
		}
	}
}

func (s *SentinelMonitorService) maybeInvestigate(signature, prompt, user string) {
	now := s.now()
	cooldown := time.Duration(s.cfg.CooldownSeconds) * time.Second

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if s.inFlight >= s.cfg.MaxConcurrent {
		s.mu.Unlock()
		s.logger.Debug("investigation skipped: max concurrent reached", "signature", signature, "max", s.cfg.MaxConcurrent)
		return
	}
	s.evictCooldownsThatCannotSuppressAgain(now, cooldown)
	if last, ok := s.lastFired[signature]; ok && now.Sub(last) < cooldown {
		s.mu.Unlock()
		s.logger.Debug("investigation skipped: in cooldown", "signature", signature)
		return
	}
	s.lastFired[signature] = now
	s.inFlight++
	s.wg.Add(1)
	ctx := s.ctx
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.inFlight--
			s.mu.Unlock()
		}()
		s.submit(ctx, signature, prompt, user)
	}()
}

func (s *SentinelMonitorService) systemIdentity() *auth.Subject {
	return auth.ForSentinelMonitor(s.cfg.SystemUser, []string{
		rbac.ScopeAgentInvokeAny, rbac.ScopeWorkflowInvokeAny,
		rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent,
	})
}

func (s *SentinelMonitorService) submit(ctx context.Context, signature, prompt, user string) {
	if user == "" {
		user = s.cfg.SystemUser
	}
	if ctx == nil {
		ctx = context.Background()
	}
	caller := s.systemIdentity()
	taskID := idgen.New()

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{{Kind: "text", Text: prompt}},
		Metadata: map[string]any{
			"agent_name":      s.cfg.TargetAgent,
			"sessionBehavior": "RUN_BASED",
		},
	}
	if err := s.gw.SubmitTaskAttributedTo(ctx, s.cfg.TargetAgent, taskID, msg, caller, user); err != nil {
		if IsBudgetRefusal(err) {
			s.logger.Debug("investigation refused: a budget is spent", "signature", signature, "taskID", taskID, "budgetTier", BudgetRefusalTier(err))
			return
		}
		if IsAgentNotAvailable(err) {
			s.logger.Debug("investigation refused: the target agent is not deployed", "signature", signature, "taskID", taskID)
			return
		}
		s.mu.Lock()
		delete(s.lastFired, signature)
		s.mu.Unlock()
		s.logger.Warn("investigation submit failed", "signature", signature, "taskID", taskID, "error", err)
		return
	}
	s.logger.Info("investigation submitted", "signature", signature, "taskID", taskID, "target", s.cfg.TargetAgent)
}

const investigationDedupPrefix = "investigation:"

const maxNotifyMessageLen = 2000

const notifyPublishTimeout = 10 * time.Second

func (s *SentinelMonitorService) notifyResult(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
	if !s.cfg.NotifyOnResult || len(s.cfg.NotifyRecipients) == 0 {
		return
	}
	s.mu.Lock()
	parent := s.ctx
	s.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), notifyPublishTimeout)
	defer cancel()

	title, message := investigationSummary(task, a2aErr)
	for _, recipient := range s.cfg.NotifyRecipients {
		req := a2a.NotifyRequest{
			RequestID:       idgen.New(),
			RecipientUserID: recipient,
			Mode:            a2a.NotifyModeNotify,
			SenderAgent:     s.cfg.TargetAgent,
			Title:           title,
			Message:         message,
			Dedup:           investigationDedupPrefix + taskID,
			Metadata: map[string]any{
				"investigation_task_id": taskID,
			},
		}
		if err := s.gw.PublishNotification(ctx, recipient, req); err != nil {
			s.logger.Warn("investigation result notification failed", "taskID", taskID, "recipient", recipient, "error", err)
			continue
		}
		s.logger.Info("investigation result notified", "taskID", taskID, "recipient", recipient)
	}
}

func investigationSummary(task *a2a.Task, a2aErr *a2a.Error) (title, message string) {
	if a2aErr != nil {
		return "Observability Agent investigation failed", a2aErr.Message
	}
	report := taskFinalText(task)
	if report == "" {
		report = "(no report text returned)"
	}
	return "Observability Agent investigation complete", truncateText(report, maxNotifyMessageLen)
}

func taskFinalText(task *a2a.Task) string {
	if task == nil || task.Status.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
