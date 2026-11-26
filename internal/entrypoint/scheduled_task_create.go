// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
)

type CreateErrorKind int

const (
	createErrorUnspecified CreateErrorKind = iota
	CreateErrorValidation
	CreateErrorConflict
	CreateErrorNameConflict
	CreateErrorInternal
)

type CreateError struct {
	Kind CreateErrorKind
	msg  string
}

func (e *CreateError) Error() string { return e.msg }

func validationErr(format string, a ...any) *CreateError {
	return &CreateError{Kind: CreateErrorValidation, msg: fmt.Sprintf(format, a...)}
}

type CreateScheduledTaskParams struct {
	ID                 string
	Name               string
	Description        *string
	ScheduleType       string
	ScheduleExpression string
	Timezone           string
	TargetType         string
	TargetName         string
	TaskMessage        string
	TaskMetadata       *string
	NotificationConfig *string
	MaxRetries         int
	RetryDelaySeconds  int
	TimeoutSeconds     int
	Source             string
	Enabled            bool
	CreatedBy          string
}

func (s *ScheduledTaskService) CreateScheduledTask(ctx context.Context, p CreateScheduledTaskParams) (*store.ScheduledTask, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return nil, validationErr("name is required")
	}
	if p.ScheduleType == "" {
		return nil, validationErr("scheduleType is required")
	}
	if p.ScheduleExpression == "" {
		return nil, validationErr("scheduleExpression is required")
	}
	if p.TargetName == "" {
		return nil, validationErr("targetName is required")
	}
	if p.TaskMessage == "" {
		return nil, validationErr("taskMessage is required")
	}
	if p.CreatedBy == "" {
		return nil, validationErr("createdBy is required (the identity the task fires under)")
	}
	if err := ValidateTaskMessage(json.RawMessage(p.TaskMessage)); err != nil {
		return nil, validationErr("%s", err.Error())
	}
	if err := validateServiceLimits(p); err != nil {
		return nil, validationErr("%s", err.Error())
	}
	switch p.ScheduleType {
	case "cron", "interval", "one_time":
	default:
		return nil, validationErr("scheduleType must be one of: cron, interval, one_time")
	}

	targetType := p.TargetType
	if targetType == "" {
		targetType = "agent"
	}
	if targetType != "agent" && targetType != "workflow" {
		return nil, validationErr("targetType must be one of: agent, workflow")
	}
	if targetType == "agent" && s.core != nil {
		if reg := s.core.AgentRegistry(); reg != nil && reg.FindByConfiguredName(p.TargetName) == nil {
			return nil, validationErr("targetName %q is not a registered agent", p.TargetName)
		}
	}

	tz := p.Timezone
	if tz == "" {
		tz = "UTC"
	}
	normalizedTZ, ok := NormalizeUserTimezone(tz)
	if !ok {
		return nil, validationErr("invalid timezone: %s", tz)
	}
	tz = normalizedTZ
	if err := scheduler.ValidateExpression(scheduler.ScheduleType(p.ScheduleType), p.ScheduleExpression, tz); err != nil {
		return nil, validationErr("invalid schedule expression: %s", err.Error())
	}
	id := p.ID
	if id == "" {
		id = idgen.New()
	}
	source := p.Source
	if source == "" {
		source = TaskSourceUI
	}
	if !isServiceSettableTaskSource(source) {
		return nil, validationErr("source must be one of: ui, chat, workflow")
	}

	notificationConfig := p.NotificationConfig
	if source == "chat" && notificationConfig == nil {
		def := defaultNotificationConfig
		notificationConfig = &def
	}
	if notificationConfig != nil {
		if err := ValidateNotificationConfig(*notificationConfig); err != nil {
			return nil, validationErr("invalid notificationConfig: %s", err.Error())
		}
	}

	timeout := p.TimeoutSeconds
	if timeout <= 0 {
		timeout = 3600
	}
	retryDelay := p.RetryDelaySeconds
	if retryDelay <= 0 {
		retryDelay = 60
	}
	owner := p.CreatedBy

	task := &store.ScheduledTask{
		ID:                 id,
		Name:               p.Name,
		Description:        p.Description,
		Namespace:          s.core.Config().Namespace,
		UserID:             &owner,
		CreatedBy:          p.CreatedBy,
		ScheduleType:       p.ScheduleType,
		ScheduleExpression: p.ScheduleExpression,
		Timezone:           tz,
		TargetAgentName:    p.TargetName,
		TargetType:         targetType,
		TaskMessage:        p.TaskMessage,
		TaskMetadata:       p.TaskMetadata,
		NotificationConfig: notificationConfig,
		Enabled:            p.Enabled,
		MaxRetries:         p.MaxRetries,
		RetryDelaySeconds:  retryDelay,
		TimeoutSeconds:     timeout,
		Source:             source,
	}

	if task.Enabled {
		fireImmediately := task.ScheduleType == "interval"
		next, err := FirstRunAt(task, time.Now(), fireImmediately)
		if err != nil {
			return nil, &CreateError{Kind: CreateErrorInternal, msg: err.Error()}
		}
		task.NextRunAt = next
	}

	if err := s.repo.Create(ctx, task); err != nil {
		if dbutil.IsUniqueViolation(err) {
			if rowWithSameID, gerr := s.repo.GetByID(ctx, task.ID); gerr == nil && rowWithSameID != nil {
				return nil, &CreateError{Kind: CreateErrorConflict, msg: fmt.Sprintf("scheduled task %q already exists", task.ID)}
			}
			return nil, &CreateError{Kind: CreateErrorNameConflict, msg: fmt.Sprintf("a scheduled task named %q already exists", task.Name)}
		}
		return nil, &CreateError{Kind: CreateErrorInternal, msg: fmt.Sprintf("create scheduled task: %v", err)}
	}

	return task, nil
}

func (s *ScheduledTaskService) scheduleRequestLoop(ctx context.Context) {
	brk := s.core.svc.Broker()
	ns := s.core.Config().Namespace

	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second

	var q broker.Queue
	for {
		created, err := brk.CreateQueue(a2a.QueueName(ns, "schedule", "request"), broker.QueueOptions{
			Named:     true,
			Exclusive: false,
		})
		if err == nil {
			if subErr := brk.Subscribe(created, a2a.ScheduleRequestSubscription(ns)); subErr != nil {
				err = subErr
			} else {
				q = created
				break
			}
		}
		s.logger.Warn("schedule request: queue setup failed; retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}

	backoff = 100 * time.Millisecond
	for {
		msg, rerr := brk.Receive(ctx, q)
		if rerr != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("schedule request: receive failed; backing off", "error", rerr)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = 100 * time.Millisecond
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		s.handleScheduleRequestRecovering(ctx, msg)
	}
}

func (s *ScheduledTaskService) handleScheduleRequestRecovering(ctx context.Context, msg *broker.Message) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		logFields := []any{"correlationID", msg.CorrelationID, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack())}
		if runtimeErr, ok := r.(runtime.Error); ok {
			logFields = append(logFields, "panicDetail", runtimeErr.Error())
		}
		s.logger.Error("schedule request: handler panicked; dropping request", logFields...)
		msg.Ack()
		s.refusePanickedRequest(ctx, msg)
	}()
	s.requestHandler()(ctx, msg)
}

func (s *ScheduledTaskService) refusePanickedRequest(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn("schedule request: refusal reply after a handler panic failed",
				"correlationID", msg.CorrelationID, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
		}
	}()
	s.publishScheduleReply(ctx, msg.ReplyTo, msg.CorrelationID, a2a.ScheduleResponse{
		RequestID: msg.CorrelationID, OK: false,
		Error: "the scheduled task request could not be processed", ErrorKind: "internal",
	})
}

func (s *ScheduledTaskService) requestHandler() func(context.Context, *broker.Message) {
	if s.scheduleRequestHandler != nil {
		return s.scheduleRequestHandler
	}
	return s.handleScheduleRequest
}

func (s *ScheduledTaskService) handleScheduleRequest(ctx context.Context, msg *broker.Message) {
	if msg == nil {
		return
	}

	var req a2a.ScheduleRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		s.logger.Warn("schedule request: invalid payload", "error", err)
		s.publishScheduleReply(ctx, msg.ReplyTo, msg.CorrelationID, a2a.ScheduleResponse{
			RequestID: msg.CorrelationID, OK: false,
			Error: "the scheduled task request was malformed and could not be processed", ErrorKind: "validation",
		})
		msg.Ack()
		return
	}

	createdBy := a2a.DecodeUserProperties(msg.UserProperties).UserID
	if createdBy == "" {
		s.logger.Warn("schedule request: no broker-stamped principal; dropping",
			"requestID", req.RequestID, "claimedSender", req.SenderUserID)
		s.replyToSchedule(ctx, req, a2a.ScheduleResponse{
			RequestID: req.RequestID, OK: false,
			Error: "could not schedule the task: no authenticated user on the request", ErrorKind: "validation",
		})
		msg.Ack()
		return
	}
	if req.SenderUserID != "" && req.SenderUserID != createdBy {
		s.logger.Warn("schedule request: sender_user_id does not match broker principal",
			"requestID", req.RequestID, "claimedSender", req.SenderUserID, "brokerPrincipal", createdBy)
	}

	taskMsgBytes, err := json.Marshal([]map[string]any{{"kind": "text", "text": req.TaskMessage}})
	if err != nil {
		s.logger.Warn("schedule request: marshal task message", "error", err)
		s.replyToSchedule(ctx, req, a2a.ScheduleResponse{
			RequestID: req.RequestID, OK: false,
			Error: "could not schedule the task: its message could not be encoded", ErrorKind: "validation",
		})
		msg.Ack()
		return
	}
	var meta *string
	if len(req.Metadata) > 0 {
		b, mErr := json.Marshal(req.Metadata)
		if mErr != nil {
			s.logger.Warn("schedule request: dropping unmarshalable metadata",
				"requestID", req.RequestID, "error", mErr)
		} else {
			sb := string(b)
			meta = &sb
		}
	}

	params := CreateScheduledTaskParams{
		ID:                 req.RequestID,
		Name:               req.Name,
		ScheduleType:       req.ScheduleType,
		ScheduleExpression: req.ScheduleExpression,
		Timezone:           req.Timezone,
		TargetType:         req.TargetType,
		TargetName:         req.TargetName,
		TaskMessage:        string(taskMsgBytes),
		TaskMetadata:       meta,
		MaxRetries:         req.MaxRetries,
		RetryDelaySeconds:  req.RetryDelaySeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		Source:             req.Source,
		Enabled:            true,
		CreatedBy:          createdBy,
	}
	if params.Source == "" {
		params.Source = "workflow"
	}
	if req.Description != "" {
		params.Description = &req.Description
	}

	if _, cerr := s.CreateScheduledTask(ctx, params); cerr != nil {
		if ce, ok := errors.AsType[*CreateError](cerr); ok {
			switch ce.Kind {
			case CreateErrorConflict:
				s.logger.Debug("schedule request: duplicate delivery; idempotent no-op",
					"requestID", req.RequestID, "name", req.Name)
				s.replyToSchedule(ctx, req, a2a.ScheduleResponse{RequestID: req.RequestID, OK: true, TaskID: req.RequestID})
				msg.Ack()
				return
			case CreateErrorValidation, CreateErrorNameConflict:
				s.logger.Warn("schedule request: rejected",
					"requestID", req.RequestID, "name", req.Name, "error", cerr)
				kind := "validation"
				if ce.Kind == CreateErrorNameConflict {
					kind = "name_conflict"
				}
				s.replyToSchedule(ctx, req, a2a.ScheduleResponse{RequestID: req.RequestID, OK: false, Error: ce.Error(), ErrorKind: kind})
				msg.Ack()
				return
			case createErrorUnspecified, CreateErrorInternal:
			}
		}
		s.logger.Error("schedule request: transient create failure; nacking for redelivery",
			"requestID", req.RequestID, "name", req.Name, "error", cerr)
		msg.Nack()
		return
	}
	s.logger.Info("schedule request: created scheduled task",
		"requestID", req.RequestID, "name", req.Name, "target", req.TargetName, "scheduleType", req.ScheduleType)
	s.replyToSchedule(ctx, req, a2a.ScheduleResponse{RequestID: req.RequestID, OK: true, TaskID: req.RequestID})
	msg.Ack()
}

func (s *ScheduledTaskService) replyToSchedule(ctx context.Context, req a2a.ScheduleRequest, resp a2a.ScheduleResponse) {
	s.publishScheduleReply(ctx, req.ReplyTo, req.RequestID, resp)
}

func (s *ScheduledTaskService) publishScheduleReply(ctx context.Context, replyTo, correlationID string, resp a2a.ScheduleResponse) {
	if replyTo == "" {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Warn("schedule request: marshal reply failed", "requestID", correlationID, "error", err)
		return
	}
	if err := s.core.svc.Broker().PublishGuaranteed(ctx, replyTo,
		&broker.Message{Payload: data, CorrelationID: correlationID}); err != nil {
		s.logger.Warn("schedule request: publish reply failed",
			"requestID", correlationID, "replyTo", replyTo, "error", err)
	}
}
