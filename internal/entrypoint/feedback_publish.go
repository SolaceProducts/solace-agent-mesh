// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/stim"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

const (
	feedbackEventCreated = "created"
	feedbackEventUpdated = "updated"
)

const DefaultFeedbackPublishTimeout = 5 * time.Second

type FeedbackPublishParams struct {
	Core           *GatewayCore
	Feedback       *store.Feedback
	TaskRepo       *store.TaskRepo
	EventRepo      *store.TaskEventRepo
	Logger         *slog.Logger
	PublishTimeout time.Duration
}

func RecordFeedback(ctx context.Context, repo *store.FeedbackRepo, submission store.FeedbackSubmission, publish FeedbackPublishParams) (store.FeedbackResult, error) {
	result, err := repo.Submit(ctx, submission)
	if err != nil || !result.Changed() {
		return result, err
	}
	stored := result.Feedback
	comment := ""
	if stored.Comment != nil {
		comment = *stored.Comment
	}
	publish.Core.FireFeedback(stored.TaskID, stored.Rating, comment, stored.UserID)
	publish.Feedback = stored
	PublishFeedbackEvent(ctx, publish)
	return result, nil
}

func PublishFeedbackEvent(ctx context.Context, p FeedbackPublishParams) {
	feedback := p.Feedback
	resolved := p.Core.ResolveFeedbackPublishing()
	if !resolved.PublishFeedback {
		return
	}

	brk := p.Core.Services().Broker()
	if brk == nil {
		return
	}

	event := feedbackEventUpdated
	if feedback.Inserted {
		event = feedbackEventCreated
	}

	level := resolved.FeedbackTaskInfoLevel
	if !feedback.ShareContent {
		level = webuisettings.FeedbackTaskInfoLevelNone
	}
	if !webuisettings.IsValidFeedbackTaskInfoLevel(level) {
		p.Logger.Warn("unknown feedback task info level; falling back to none",
			"taskID", feedback.TaskID, "level", level)
		level = webuisettings.FeedbackTaskInfoLevelNone
	}

	payload := map[string]any{
		"feedback": map[string]any{
			"event":           event,
			"task_id":         feedback.TaskID,
			"session_id":      feedback.SessionID,
			"feedback_type":   feedback.Rating,
			"feedback_text":   feedback.Comment,
			"user_id":         feedback.UserID,
			"entrypoint_type": feedback.EntrypointType,
			"subject_id":      feedback.SubjectID,
			"subject_type":    feedback.SubjectType,
			"created_at":      epochMsToISO8601(feedback.CreatedAt),
			"updated_at":      epochMsToISO8601(feedback.UpdatedAt),
		},
	}
	if level != webuisettings.FeedbackTaskInfoLevelNone {
		attachFeedbackTaskInfo(ctx, p, payload, level)
	}

	topic := a2a.FeedbackTopic(p.Core.Config().Namespace)

	data, err := json.Marshal(payload)
	if err != nil {
		p.Logger.Warn("marshal feedback event error", "taskID", feedback.TaskID, "topic", topic, "event", event, "error", err)
		return
	}

	timeout := p.PublishTimeout
	if timeout <= 0 {
		timeout = DefaultFeedbackPublishTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := brk.PublishGuaranteed(pubCtx, topic, &broker.Message{Payload: data}); err != nil {
		p.Logger.Warn("publish feedback event error", "taskID", feedback.TaskID, "topic", topic, "event", event, "error", err)
	}
}

func attachFeedbackTaskInfo(ctx context.Context, p FeedbackPublishParams, payload map[string]any, level string) {
	feedback := p.Feedback
	task, err := p.TaskRepo.GetByID(ctx, feedback.TaskID)
	if err != nil {
		p.Logger.Warn("fetch task for feedback publish failed", "taskID", feedback.TaskID, "error", err)
		return
	}
	if task == nil {
		return
	}

	capBytes := p.Core.FeedbackMaxPayloadSizeBytes()

	if level == webuisettings.FeedbackTaskInfoLevelStim {
		stimDoc := buildStimForFeedbackTask(ctx, p, task)
		if stimDoc != nil {
			payload["task_stim_data"] = stimDoc
			if payloadWithinCap(payload, capBytes) {
				return
			}
			p.Logger.Warn("feedback stim payload exceeded cap; falling back to summary",
				"taskID", feedback.TaskID, "capBytes", capBytes)
			delete(payload, "task_stim_data")
			payload["truncation_details"] = map[string]any{
				"strategy": "fallback_to_summary",
				"reason":   "payload_too_large",
			}
		} else {
			payload["truncation_details"] = map[string]any{
				"strategy": "fallback_to_summary",
				"reason":   "stim_build_failed",
			}
		}
	}

	payload["task_summary"] = taskSummaryFields(task)
	if payloadWithinCap(payload, capBytes) {
		return
	}
	p.Logger.Warn("feedback summary payload exceeded cap; dropping summary",
		"taskID", feedback.TaskID, "capBytes", capBytes)
	delete(payload, "task_summary")
	payload["truncation_details"] = map[string]any{
		"strategy": "drop_summary",
		"reason":   "payload_too_large",
	}
}

func buildStimForFeedbackTask(ctx context.Context, p FeedbackPublishParams, task *store.Task) map[string]any {
	if p.EventRepo == nil {
		return nil
	}
	parentChain, err := p.TaskRepo.GetParentChain(ctx, task.ID, 10)
	if err != nil {
		p.Logger.Warn("get parent chain for feedback stim publish failed", "taskID", task.ID, "error", err)
		return nil
	}
	descendants, err := p.TaskRepo.GetAllDescendantIDs(ctx, task.ID)
	if err != nil {
		p.Logger.Warn("get descendants for feedback stim publish failed", "taskID", task.ID, "error", err)
		return nil
	}
	idSet := make(map[string]bool)
	var allIDs []string
	for _, id := range parentChain {
		if !idSet[id] {
			idSet[id] = true
			allIDs = append(allIDs, id)
		}
	}
	for _, id := range descendants {
		if !idSet[id] {
			idSet[id] = true
			allIDs = append(allIDs, id)
		}
	}
	events, err := p.EventRepo.GetByTaskIDs(ctx, allIDs)
	if err != nil {
		p.Logger.Warn("get events for feedback stim publish failed", "taskID", task.ID, "error", err)
		return nil
	}
	return stim.BuildDocument(StimHeaderFromTask(task, len(idSet), len(descendants) > 0), StimEventsFromStore(events))
}

func payloadWithinCap(payload map[string]any, capBytes int64) bool {
	if capBytes <= 0 {
		return true
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return int64(len(data)) <= capBytes
}

func taskSummaryFields(task *store.Task) map[string]any {
	return map[string]any{
		"id":                           task.ID,
		"user_id":                      task.UserID,
		"start_time":                   task.StartTime,
		"end_time":                     task.EndTime,
		"status":                       task.Status,
		"initial_request_text":         task.InitialRequestText,
		"parent_task_id":               task.ParentTaskID,
		"total_input_tokens":           task.TotalInputTokens,
		"total_output_tokens":          task.TotalOutputTokens,
		"total_cached_input_tokens":    task.TotalCachedInputTokens,
		"token_usage_details":          parseTokenUsageDetails(task.TokenUsageDetails),
		"execution_mode":               task.ExecutionMode,
		"last_activity_time":           task.LastActivityTime,
		"background_execution_enabled": task.BackgroundExecutionEnabled,
		"max_execution_time_ms":        task.MaxExecutionTimeMs,
		"session_id":                   task.SessionID,
		"events_buffered":              task.EventsBuffered,
		"events_consumed":              task.EventsConsumed,
	}
}

func parseTokenUsageDetails(raw *string) any {
	if raw == nil {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(*raw), &out); err != nil {
		return *raw
	}
	return out
}

func epochMsToISO8601(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	return t.Format("2006-01-02T15:04:05.000000+00:00")
}

func StimHeaderFromTask(task *store.Task, totalTasks int, includesChildTasks bool) stim.Header {
	status := "completed"
	if task.Status != nil {
		status = *task.Status
	}
	endTime := task.StartTime
	if task.EndTime != nil {
		endTime = *task.EndTime
	}
	requestText := ""
	if task.InitialRequestText != nil {
		requestText = *task.InitialRequestText
	}
	return stim.Header{
		RootTaskID:         task.ID,
		UserID:             task.UserID,
		StartTime:          task.StartTime,
		EndTime:            endTime,
		Status:             status,
		InitialRequestText: requestText,
		TotalTasks:         totalTasks,
		IncludesChildTasks: includesChildTasks,
	}
}

func StimEventsFromStore(events []*store.TaskEvent) []stim.Event {
	out := make([]stim.Event, len(events))
	for i, evt := range events {
		out[i] = stim.Event{
			TaskID:      evt.TaskID,
			CreatedTime: evt.CreatedTime,
			Topic:       evt.Topic,
			Direction:   evt.Direction,
			Payload:     evt.Payload,
			UserID:      evt.UserID,
		}
	}
	return out
}
