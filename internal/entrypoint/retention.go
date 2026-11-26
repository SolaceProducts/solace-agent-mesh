// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type DataRetentionConfig struct {
	Enabled               bool
	TaskRetentionDays     int
	FeedbackRetentionDays int
	SSEEventRetentionDays int
	CleanupSSEEvents      bool
	CleanupIntervalHours  int
	BatchSize             int

	SSEConsumedRetentionHours     int
	SSECompletedTaskRetentionDays int

	TaskEventDeltaRetentionDays int

	WhatsAppOutboundRetentionDays int

	SSEEventRetentionSeconds         int
	CleanupIntervalSeconds           int
	SSEConsumedRetentionSeconds      int
	SSECompletedTaskRetentionSeconds int
	TaskEventDeltaRetentionSeconds   int
	WhatsAppOutboundRetentionSeconds int
}

func ParseDataRetentionConfig(m map[string]any) DataRetentionConfig {
	cfg := DataRetentionConfig{
		Enabled:                       true,
		TaskRetentionDays:             90,
		FeedbackRetentionDays:         90,
		SSEEventRetentionDays:         7,
		CleanupSSEEvents:              true,
		CleanupIntervalHours:          24,
		BatchSize:                     1000,
		SSEConsumedRetentionHours:     24,
		SSECompletedTaskRetentionDays: 7,
		TaskEventDeltaRetentionDays:   7,
		WhatsAppOutboundRetentionDays: 30,
	}
	if m == nil {
		return cfg
	}

	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v := getInt(m, "task_retention_days"); v > 0 {
		cfg.TaskRetentionDays = v
	}
	if v := getInt(m, "feedback_retention_days"); v > 0 {
		cfg.FeedbackRetentionDays = v
	}
	if v := getInt(m, "sse_event_retention_days"); v > 0 {
		cfg.SSEEventRetentionDays = v
	}
	if v, ok := m["cleanup_sse_events"].(bool); ok {
		cfg.CleanupSSEEvents = v
	}
	if v := getInt(m, "cleanup_interval_hours"); v > 0 {
		cfg.CleanupIntervalHours = v
	}
	if v := getInt(m, "batch_size"); v > 0 {
		cfg.BatchSize = v
	}
	if v := getInt(m, "sse_consumed_retention_hours"); v > 0 {
		cfg.SSEConsumedRetentionHours = v
	}
	if v := getInt(m, "sse_completed_task_retention_days"); v > 0 {
		cfg.SSECompletedTaskRetentionDays = v
	}
	if v := getInt(m, "task_event_delta_retention_days"); v > 0 {
		cfg.TaskEventDeltaRetentionDays = v
	}
	if v := getInt(m, "whatsapp_outbound_retention_days"); v > 0 {
		cfg.WhatsAppOutboundRetentionDays = v
	}
	if v := getInt(m, "sse_event_retention_seconds"); v > 0 {
		cfg.SSEEventRetentionSeconds = v
	}
	if v := getInt(m, "cleanup_interval_seconds"); v > 0 {
		cfg.CleanupIntervalSeconds = v
	}
	if v := getInt(m, "sse_consumed_retention_seconds"); v > 0 {
		cfg.SSEConsumedRetentionSeconds = v
	}
	if v := getInt(m, "sse_completed_task_retention_seconds"); v > 0 {
		cfg.SSECompletedTaskRetentionSeconds = v
	}
	if v := getInt(m, "task_event_delta_retention_seconds"); v > 0 {
		cfg.TaskEventDeltaRetentionSeconds = v
	}
	if v := getInt(m, "whatsapp_outbound_retention_seconds"); v > 0 {
		cfg.WhatsAppOutboundRetentionSeconds = v
	}
	return cfg
}

const taskEventBlobGuard = 24 * time.Hour

const _ = uint(taskEventBlobGuard - seenBlobTTL - taskEventFlushInterval)

const WhatsAppClaimRetention = 8 * 24 * time.Hour

const GoogleChatClaimRetention = 8 * 24 * time.Hour

const TeamsFeedbackCorrelationRetention = 24 * time.Hour

type DataRetentionService struct {
	cfg                 DataRetentionConfig
	taskRepo            *store.TaskRepo
	feedbackRepo        *store.FeedbackRepo
	sseRepo             *store.SSEEventRepo
	taskEventRepo       *store.TaskEventRepo
	whatsappDedupRepo   *store.WhatsAppProcessedRepo
	waOutboundRepo      *store.WhatsAppOutboundRepo
	waConversationRepo  *store.WhatsAppConversationRepo
	waPromptRepo        *store.WhatsAppPendingPromptRepo
	googleChatDedupRepo *store.GoogleChatProcessedRepo
	teamsFeedbackRepo   *store.TeamsFeedbackCorrelationRepo
	mcpArtifactLinkRepo *store.MCPArtifactLinkRepo
	logger              *slog.Logger
}

func (s *DataRetentionService) WithWhatsAppOutbound(outbound *store.WhatsAppOutboundRepo, conversations *store.WhatsAppConversationRepo, prompts *store.WhatsAppPendingPromptRepo) *DataRetentionService {
	s.waOutboundRepo = outbound
	s.waConversationRepo = conversations
	s.waPromptRepo = prompts
	return s
}

func (s *DataRetentionService) WithGoogleChatClaims(repo *store.GoogleChatProcessedRepo) *DataRetentionService {
	s.googleChatDedupRepo = repo
	return s
}

func (s *DataRetentionService) WithTeamsFeedbackCorrelation(repo *store.TeamsFeedbackCorrelationRepo) *DataRetentionService {
	s.teamsFeedbackRepo = repo
	return s
}

func (s *DataRetentionService) WithMCPArtifactLinks(repo *store.MCPArtifactLinkRepo) *DataRetentionService {
	s.mcpArtifactLinkRepo = repo
	return s
}

func NewDataRetentionService(cfg DataRetentionConfig, taskRepo *store.TaskRepo, feedbackRepo *store.FeedbackRepo, sseRepo *store.SSEEventRepo, taskEventRepo *store.TaskEventRepo, whatsappDedupRepo *store.WhatsAppProcessedRepo, logger *slog.Logger) *DataRetentionService {
	return &DataRetentionService{
		cfg:               cfg,
		taskRepo:          taskRepo,
		feedbackRepo:      feedbackRepo,
		sseRepo:           sseRepo,
		taskEventRepo:     taskEventRepo,
		whatsappDedupRepo: whatsappDedupRepo,
		logger:            logger.With("logger_name", "data-retention"),
	}
}

func (s *DataRetentionService) HasWhatsAppOutbound() bool { return s.waOutboundRepo != nil }

func (s *DataRetentionService) HasWhatsAppConversations() bool { return s.waConversationRepo != nil }

func (s *DataRetentionService) HasWhatsAppPrompts() bool { return s.waPromptRepo != nil }

func (s *DataRetentionService) HasGoogleChatClaims() bool { return s.googleChatDedupRepo != nil }

func (s *DataRetentionService) HasMCPArtifactLinks() bool { return s.mcpArtifactLinkRepo != nil }

func (s *DataRetentionService) HasTeamsFeedbackCorrelation() bool { return s.teamsFeedbackRepo != nil }

func (s *DataRetentionService) batchSize() int {
	if s.cfg.BatchSize > 0 {
		return s.cfg.BatchSize
	}
	return 1000
}

func (s *DataRetentionService) cleanupInterval() time.Duration {
	if s.cfg.CleanupIntervalSeconds > 0 {
		return time.Duration(s.cfg.CleanupIntervalSeconds) * time.Second
	}
	return time.Duration(s.cfg.CleanupIntervalHours) * time.Hour
}

func (s *DataRetentionService) sseEventCutoff(now time.Time) time.Time {
	if s.cfg.SSEEventRetentionSeconds > 0 {
		return now.Add(-time.Duration(s.cfg.SSEEventRetentionSeconds) * time.Second)
	}
	return now.Add(-time.Duration(s.cfg.SSEEventRetentionDays) * 24 * time.Hour)
}

func (s *DataRetentionService) taskEventDeltaCutoff(now time.Time) time.Time {
	if s.cfg.TaskEventDeltaRetentionSeconds > 0 {
		return now.Add(-time.Duration(s.cfg.TaskEventDeltaRetentionSeconds) * time.Second)
	}
	return now.Add(-time.Duration(s.cfg.TaskEventDeltaRetentionDays) * 24 * time.Hour)
}

func (s *DataRetentionService) whatsAppOutboundRetention() time.Duration {
	if s.cfg.WhatsAppOutboundRetentionSeconds > 0 {
		return time.Duration(s.cfg.WhatsAppOutboundRetentionSeconds) * time.Second
	}
	return time.Duration(s.cfg.WhatsAppOutboundRetentionDays) * 24 * time.Hour
}

func (s *DataRetentionService) sseConsumedCutoff(now time.Time) time.Time {
	if s.cfg.SSEConsumedRetentionSeconds > 0 {
		return now.Add(-time.Duration(s.cfg.SSEConsumedRetentionSeconds) * time.Second)
	}
	return now.Add(-time.Duration(s.cfg.SSEConsumedRetentionHours) * time.Hour)
}

func (s *DataRetentionService) sseCompletedTaskCutoff(now time.Time) time.Time {
	if s.cfg.SSECompletedTaskRetentionSeconds > 0 {
		return now.Add(-time.Duration(s.cfg.SSECompletedTaskRetentionSeconds) * time.Second)
	}
	return now.Add(-time.Duration(s.cfg.SSECompletedTaskRetentionDays) * 24 * time.Hour)
}

func (s *DataRetentionService) logPolicy() {
	interval := s.cleanupInterval()
	if !s.cfg.Enabled {
		s.logger.Info("data retention disabled; sweeping only the entrypoint tables with platform-set horizons",
			"whatsappClaimRetention", WhatsAppClaimRetention,
			"googleChatClaimRetention", GoogleChatClaimRetention,
			"teamsFeedbackCorrelationRetention", TeamsFeedbackCorrelationRetention,
			"whatsappOutboundRetentionDays", s.cfg.WhatsAppOutboundRetentionDays,
			"cleanupInterval", interval)
	} else {
		s.logger.Info("data retention service initialized",
			"taskRetentionDays", s.cfg.TaskRetentionDays,
			"feedbackRetentionDays", s.cfg.FeedbackRetentionDays,
			"sseEventRetentionDays", s.cfg.SSEEventRetentionDays,
			"cleanupSSEEvents", s.cfg.CleanupSSEEvents,
			"cleanupInterval", interval,
			"batchSize", s.cfg.BatchSize,
		)
	}
}

func (s *DataRetentionService) cleanup(ctx context.Context) error {
	now := time.Now()
	return errors.Join(
		s.cleanupEntrypointTables(ctx, now, s.whatsAppOutboundRetention()),
		s.cleanupTaskHistory(ctx, now),
	)
}

func (s *DataRetentionService) cleanupEntrypointTables(ctx context.Context, now time.Time, outboundRetention time.Duration) error {
	var errs []error
	if s.whatsappDedupRepo != nil {
		errs = append(errs, s.pruneWhatsAppClaims(ctx, now))
	}
	if s.waOutboundRepo != nil {
		errs = append(errs, s.pruneWhatsAppOutbound(ctx, now.Add(-outboundRetention)))
	}
	if s.googleChatDedupRepo != nil {
		errs = append(errs, s.pruneGoogleChatClaims(ctx, now))
	}
	if s.teamsFeedbackRepo != nil {
		errs = append(errs, s.pruneTeamsFeedbackCorrelation(ctx, now))
	}
	return errors.Join(errs...)
}

func (s *DataRetentionService) cleanupTaskHistory(ctx context.Context, now time.Time) error {
	if !s.cfg.Enabled {
		return nil
	}
	var errs []error

	taskCutoff := now.Add(-time.Duration(s.cfg.TaskRetentionDays) * 24 * time.Hour)
	tasksDeleted, err := s.taskRepo.DeleteOlderThan(ctx, taskCutoff, s.cfg.BatchSize)
	if err != nil {
		errs = append(errs, fmt.Errorf("delete old tasks: %w", err))
	} else if tasksDeleted > 0 {
		s.logger.Info("deleted old tasks", "count", tasksDeleted, "cutoff", taskCutoff.UnixMilli())
	}

	if s.taskEventRepo != nil {
		deltaCutoff := s.taskEventDeltaCutoff(now)
		deltasDeleted, derr := s.taskEventRepo.DeleteDeltasOlderThan(ctx, deltaCutoff, s.cfg.BatchSize)
		if derr != nil {
			errs = append(errs, fmt.Errorf("delete old task event deltas: %w", derr))
		} else if deltasDeleted > 0 {
			s.logger.Info("deleted old task event deltas", "count", deltasDeleted, "cutoff", deltaCutoff.UnixMilli())
		}
		errs = append(errs, s.deleteUnreferencedTaskEventBlobs(ctx, now))
	}

	if s.mcpArtifactLinkRepo != nil {
		linksDeleted, lerr := s.mcpArtifactLinkRepo.DeleteOlderThan(ctx, taskCutoff, s.cfg.BatchSize)
		if lerr != nil {
			errs = append(errs, fmt.Errorf("delete old MCP artifact links: %w", lerr))
		} else if linksDeleted > 0 {
			s.logger.Info("deleted old MCP artifact links", "count", linksDeleted, "cutoff", taskCutoff.UnixMilli())
		}
	}

	feedbackCutoff := now.Add(-time.Duration(s.cfg.FeedbackRetentionDays) * 24 * time.Hour)
	feedbackDeleted, err := s.feedbackRepo.DeleteOlderThan(ctx, feedbackCutoff, s.cfg.BatchSize)
	if err != nil {
		errs = append(errs, fmt.Errorf("delete old feedback: %w", err))
	} else if feedbackDeleted > 0 {
		s.logger.Info("deleted old feedback", "count", feedbackDeleted, "cutoff", feedbackCutoff.UnixMilli())
	}

	if s.cfg.CleanupSSEEvents {
		errs = append(errs, s.cleanupSSEEvents(ctx, now))
	}
	return errors.Join(errs...)
}

func (s *DataRetentionService) pruneWhatsAppClaims(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-WhatsAppClaimRetention)
	deleted, err := s.whatsappDedupRepo.DeleteOlderThan(ctx, cutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("delete old whatsapp dedup claims: %w", err)
	}
	if deleted > 0 {
		s.logger.Info("deleted old whatsapp dedup claims", "count", deleted, "cutoff", cutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) pruneWhatsAppOutbound(ctx context.Context, cutoff time.Time) error {
	var errs []error
	settled, err := s.waOutboundRepo.PruneTerminal(ctx, cutoff, s.batchSize())
	if err != nil {
		errs = append(errs, fmt.Errorf("prune settled whatsapp outbound rows: %w", err))
	}
	stale, err := s.waOutboundRepo.PruneStale(ctx, cutoff, s.batchSize())
	if err != nil {
		errs = append(errs, fmt.Errorf("prune stale whatsapp outbound rows: %w", err))
	}
	errs = append(errs, s.pruneWhatsAppConversations(ctx, cutoff), s.pruneWhatsAppPrompts(ctx, cutoff))
	if settled+stale > 0 {
		s.logger.Info("pruned whatsapp outbound ledger",
			"settled", settled, "stale", stale, "cutoff", cutoff.UnixMilli())
	}
	return errors.Join(errs...)
}

func (s *DataRetentionService) deleteUnreferencedTaskEventBlobs(ctx context.Context, now time.Time) error {
	blobs := s.taskEventRepo.Blobs()
	ready, err := blobs.ReferenceIndexesReady(ctx)
	if err != nil {
		return fmt.Errorf("check task event blob reference indexes: %w", err)
	}
	if !ready {
		s.logger.Info("skipping unreferenced task event blob sweep until its indexes are built")
		return nil
	}
	blobCutoff := now.Add(-taskEventBlobGuard)
	blobsDeleted, err := blobs.DeleteUnreferencedOlderThan(ctx, blobCutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("delete unreferenced task event blobs: %w", err)
	}
	if blobsDeleted > 0 {
		s.logger.Info("deleted unreferenced task event blobs", "count", blobsDeleted, "cutoff", blobCutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) pruneWhatsAppConversations(ctx context.Context, cutoff time.Time) error {
	if s.waConversationRepo == nil {
		return nil
	}
	deleted, err := s.waConversationRepo.PruneStale(ctx, cutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("prune whatsapp conversations: %w", err)
	}
	if deleted > 0 {
		s.logger.Info("pruned whatsapp conversations", "count", deleted, "cutoff", cutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) pruneWhatsAppPrompts(ctx context.Context, cutoff time.Time) error {
	if s.waPromptRepo == nil {
		return nil
	}
	deleted, err := s.waPromptRepo.PruneExpired(ctx, cutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("prune whatsapp pending prompts: %w", err)
	}
	if deleted > 0 {
		s.logger.Info("pruned whatsapp pending prompts", "count", deleted, "cutoff", cutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) pruneGoogleChatClaims(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-GoogleChatClaimRetention)
	deleted, err := s.googleChatDedupRepo.DeleteOlderThan(ctx, cutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("delete old google chat dedup claims: %w", err)
	}
	if deleted > 0 {
		s.logger.Info("deleted old google chat dedup claims", "count", deleted, "cutoff", cutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) pruneTeamsFeedbackCorrelation(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-TeamsFeedbackCorrelationRetention)
	deleted, err := s.teamsFeedbackRepo.DeleteOlderThan(ctx, cutoff, s.batchSize())
	if err != nil {
		return fmt.Errorf("delete old teams feedback correlations: %w", err)
	}
	if deleted > 0 {
		s.logger.Info("deleted old teams feedback correlations", "count", deleted, "cutoff", cutoff.UnixMilli())
	}
	return nil
}

func (s *DataRetentionService) cleanupSSEEvents(ctx context.Context, now time.Time) error {
	var errs []error
	consumedDeleted, err := s.sseRepo.DeleteConsumedTasksBefore(ctx, s.sseConsumedCutoff(now).UnixMilli())
	if err != nil {
		errs = append(errs, fmt.Errorf("delete consumed sse events: %w", err))
	}

	completedDeleted, err := s.sseRepo.DeleteForTasksEndedBefore(ctx, s.sseCompletedTaskCutoff(now).UnixMilli())
	if err != nil {
		errs = append(errs, fmt.Errorf("delete completed-task sse events: %w", err))
	}

	backstopCutoff := s.sseEventCutoff(now)
	backstopDeleted, err := s.sseRepo.DeleteOlderThan(ctx, backstopCutoff, s.cfg.BatchSize)
	if err != nil {
		errs = append(errs, fmt.Errorf("delete old sse events: %w", err))
	}

	total := consumedDeleted + completedDeleted + backstopDeleted
	if total > 0 {
		s.logger.Info("deleted sse events",
			"consumed", consumedDeleted,
			"completed", completedDeleted,
			"backstop", backstopDeleted,
			"total", total,
		)
	} else {
		s.logger.Debug("sse event cleanup: nothing to delete")
	}
	return errors.Join(errs...)
}
