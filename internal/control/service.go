// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Service struct {
	rt        *runtime.Runtime
	namespace string
	logger    *slog.Logger
	queue     broker.Queue
	cancel    context.CancelFunc
	cfg       ServiceConfig
	sem       chan struct{}
	wg        sync.WaitGroup
}

var _ runtime.Component = (*Service)(nil)

func NewService(rt *runtime.Runtime, namespace string) *Service {
	return &Service{
		rt:        rt,
		namespace: namespace,
		logger:    slog.Default().With("logger_name", "control"),
	}
}

func (s *Service) Init(_ context.Context, cfg config.Config) error {
	s.cfg = parseServiceConfig(cfg)
	return nil
}

func (s *Service) Start(ctx context.Context) error {
	if s.cfg.MaxConcurrentHandlers <= 0 {
		s.cfg.setDefaults()
	}
	s.sem = make(chan struct{}, s.cfg.MaxConcurrentHandlers)

	brk := s.rt.Services().Broker()

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		return err
	}
	s.queue = queue

	topics := a2a.ControlSubscriptionTopics(s.namespace)
	for _, topic := range topics {
		if err := brk.Subscribe(queue, topic); err != nil {
			return err
		}
	}
	s.logger.Info("subscribed to control topics", "count", len(topics))

	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Go(func() { s.receiveLoop(loopCtx) })
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.logger.Warn("control service Stop deadline exceeded with handlers still in flight", "error", ctx.Err())
		return ctx.Err()
	}
}

func (s *Service) Health() error {
	return nil
}

func (s *Service) ownsApp(name string) bool {
	if s.rt.IsDraining() {
		return false
	}
	for _, ci := range s.rt.Components() {
		if ci.Name == name {
			return true
		}
	}
	return false
}

func (s *Service) receiveLoop(ctx context.Context) {
	brk := s.rt.Services().Broker()
	for {
		msg, err := brk.Receive(ctx, s.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		s.wg.Go(func() {
			defer func() { <-s.sem }()
			s.safeHandleMessage(ctx, msg)
		})
	}
}

func (s *Service) safeHandleMessage(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in control message handler", "panic", r, "topic", msg.Topic)
			msg.Ack()
		}
	}()
	s.handleMessage(ctx, msg)
}

func (s *Service) handleMessage(ctx context.Context, msg *broker.Message) {
	parsed, err := a2a.ParseControlTopic(s.namespace, msg.Topic)
	if err != nil {
		s.logger.Warn("invalid control topic", "topic", msg.Topic, "error", err)
		msg.Ack()
		return
	}

	if parsed.AppName != "" && !s.ownsApp(parsed.AppName) {
		msg.Ack()
		return
	}
	if target := msg.UserProperties[TargetProcessProperty]; target != "" && target != runtime.ProcessID() {
		msg.Ack()
		return
	}

	up := a2a.DecodeUserProperties(msg.UserProperties)

	var identity *auth.Identity
	var authErr error
	if pa, ok := s.rt.Services().Authenticator().(auth.PeerAuthenticator); ok {
		identity, authErr = pa.AuthenticateWithSoftTaskBinding(ctx, up.AuthToken, "")
	} else {
		identity, authErr = s.rt.Services().Authenticator().Authenticate(ctx, up.AuthToken, "")
	}
	if authErr != nil {
		s.logger.Warn("authentication failed", "errorType", fmt.Sprintf("%T", authErr))
		s.sendResponse(msg, ErrorResponse(ErrAuthDenied, "Authentication failed"))
		msg.Ack()
		return
	}

	isCustomEndpoint := parsed.AppName != "" && len(parsed.CustomPath) > 0
	if !isCustomEndpoint {
		if authzErr := s.rt.Services().ControlAuthorizer().AuthorizeControl(ctx, identity, parsed.Method, parsed.AppName); authzErr != nil {
			s.logger.Warn("authorization denied",
				"method", parsed.Method,
				"appName", parsed.AppName,
				"error", authzErr,
			)
			s.sendResponse(msg, ErrorResponse(ErrAuthDenied, "Access denied"))
			msg.Ack()
			return
		}
	}

	var resp *Response
	switch {
	case parsed.Resource != "apps":
		resp = ErrorResponse(ErrNotFound, "unknown resource: "+parsed.Resource)

	case parsed.AppName == "" && parsed.Method == "get":
		resp = s.handleListApps()

	case parsed.AppName == "" && parsed.Method == "post":
		resp = s.handleCreateApp(msg.Payload)

	case parsed.AppName != "" && len(parsed.CustomPath) == 0:
		switch parsed.Method {
		case "get":
			resp = s.handleGetApp(parsed.AppName)
		case "put":
			resp = s.handleUpdateApp(parsed.AppName, msg.Payload)
		case "patch":
			resp = s.handlePatchApp(parsed.AppName, msg.Payload)
		case "delete":
			resp = s.handleDeleteApp(parsed.AppName)
		default:
			resp = ErrorResponse(ErrMethodNotAllowed, "method not allowed: "+parsed.Method)
		}

	case parsed.AppName != "" && len(parsed.CustomPath) > 0:
		resp = s.handleCustomEndpoint(ctx, parsed.AppName, parsed.Method, parsed.CustomPath, msg.Payload, identity)

	default:
		resp = ErrorResponse(ErrMethodNotAllowed, "method not allowed: "+parsed.Method)
	}

	if resp != nil {
		s.sendResponse(msg, resp)
	}
	msg.Ack()
}

func (s *Service) sendResponse(msg *broker.Message, resp *Response) {
	replyTo := msg.ReplyTo
	if replyTo == "" {
		up := a2a.DecodeUserProperties(msg.UserProperties)
		replyTo = up.ReplyTo
	}
	if replyTo == "" {
		s.logger.Warn("no reply-to topic, cannot send response")
		return
	}

	data, err := MarshalResponse(resp)
	if err != nil {
		s.logger.Error("failed to marshal response", "error", err)
		data, _ = MarshalResponse(ErrorResponse(ErrOperationFailed, "internal: failed to marshal response"))
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	brk := s.rt.Services().Broker()
	pubErr := brk.Publish(pubCtx, replyTo, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{ResponderProcessProperty: runtime.ProcessID()},
	})
	if pubErr != nil {
		s.logger.Error("failed to publish response", "topic", replyTo, "error", pubErr)
	}
}

type AppInfo struct {
	Name                string     `json:"name"`
	ManagementEndpoints []Endpoint `json:"management_endpoints,omitempty"`
}

func (s *Service) handleListApps() *Response {
	components := s.rt.Components()
	apps := make([]AppInfo, 0, len(components))
	for _, ci := range components {
		if ci.Name == "control" {
			continue
		}
		info := AppInfo{Name: ci.Name}
		if m, ok := ci.Component.(Manageable); ok {
			info.ManagementEndpoints = m.ManagementEndpoints()
		}
		apps = append(apps, info)
	}
	return SuccessResponse(map[string]any{"apps": apps})
}

func (s *Service) handleGetApp(name string) *Response {
	components := s.rt.Components()
	for _, ci := range components {
		if ci.Name == name {
			info := AppInfo{Name: ci.Name}
			if m, ok := ci.Component.(Manageable); ok {
				info.ManagementEndpoints = m.ManagementEndpoints()
			}
			return SuccessResponse(info)
		}
	}
	return ErrorResponse(ErrNotFound, "app not found: "+name)
}

func (s *Service) handleCreateApp(_ []byte) *Response {
	return ErrorResponse(ErrMethodNotAllowed, "dynamic app creation not yet implemented")
}

func (s *Service) handleUpdateApp(_ string, _ []byte) *Response {
	return ErrorResponse(ErrMethodNotAllowed, "dynamic app update not yet implemented")
}

func (s *Service) handlePatchApp(_ string, _ []byte) *Response {
	return ErrorResponse(ErrMethodNotAllowed, "dynamic app patch not yet implemented")
}

func (s *Service) handleDeleteApp(_ string) *Response {
	return ErrorResponse(ErrMethodNotAllowed, "dynamic app deletion not yet implemented")
}

func (s *Service) handleCustomEndpoint(ctx context.Context, appName, method string, path []string, body []byte, identity *auth.Identity) *Response {
	components := s.rt.Components()
	s.logger.Debug("control: custom endpoint", "app", appName, "method", method, "path", path, "components", len(components))
	for _, ci := range components {
		if ci.Name == appName {
			m, ok := ci.Component.(Manageable)
			if !ok {
				s.logger.Warn("control: component not Manageable", "app", appName, "type", fmt.Sprintf("%T", ci.Component))
				return ErrorResponse(ErrMethodNotAllowed, "component does not support custom endpoints")
			}
			s.logger.Debug("control: dispatching to HandleManagement", "app", appName)
			var rawBody json.RawMessage
			if len(body) > 0 {
				rawBody = json.RawMessage(body)
			}
			result, err := m.HandleManagement(ctx, method, path, rawBody, identity)
			if err != nil {
				if errors.Is(err, ErrResourceNotFound) {
					s.logger.Debug("control: HandleManagement not found", "app", appName, "method", method, "path", path, "error", err)
					return ErrorResponse(ErrNotFound, err.Error())
				}
				if errors.Is(err, ErrResourceConflict) {
					s.logger.Debug("control: HandleManagement conflict", "app", appName, "method", method, "path", path, "error", err)
					return ErrorResponse(ErrConflict, err.Error())
				}
				s.logger.Warn("control: HandleManagement error", "app", appName, "error", err)
				return ErrorResponse(ErrOperationFailed, err.Error())
			}
			return SuccessResponse(result)
		}
	}
	s.logger.Debug("control: app not found", "app", appName)
	return ErrorResponse(ErrNotFound, "app not found: "+appName)
}
