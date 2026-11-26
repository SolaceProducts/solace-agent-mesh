// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type DevBrokerServer struct {
	mu       sync.Mutex
	listener net.Listener
	clients  map[string]*clientSession
	broker   *DevBroker
	tracer   *TraceWriter
	done     chan struct{}
	logger   *slog.Logger
}

type clientSession struct {
	clientID  string
	queueName string
	queue     Queue
	conn      net.Conn
	writeMu   sync.Mutex
	pushMode  bool
	cancel    context.CancelFunc
	client    *DevBrokerClient
	closed    atomic.Bool
}

func NewDevBrokerServer(broker *DevBroker, logger *slog.Logger) *DevBrokerServer {
	if logger == nil {
		logger = slog.Default()
	}
	if broker == nil {
		broker = NewDevBroker()
		_ = broker.Connect(context.Background())
	}
	return &DevBrokerServer{
		clients: make(map[string]*clientSession),
		broker:  broker,
		done:    make(chan struct{}),
		logger:  logger.With("logger_name", "devbroker-server"),
	}
}

func (s *DevBrokerServer) SetTracer(t *TraceWriter) {
	s.tracer = t
}

func (s *DevBrokerServer) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("devserver: listen on %s: %w", addr, err)
	}
	s.listener = ln
	actualAddr := ln.Addr().String()
	s.logger.Info("dev broker server started", "addr", actualAddr)

	go s.acceptLoop()
	return actualAddr, nil
}

func (s *DevBrokerServer) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *DevBrokerServer) Stop() error {
	close(s.done)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.mu.Lock()
	removed := make([]*clientSession, 0, len(s.clients))
	for clientID := range s.clients {
		removed = append(removed, s.removeClientLocked(clientID))
	}
	s.mu.Unlock()
	releaseBinds(removed...)
	if s.tracer != nil {
		s.tracer.Close()
	}
	return nil
}

func (s *DevBrokerServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.logger.Error("accept error", "error", err)
				continue
			}
		}
		go s.handleClient(conn)
	}
}

func (s *DevBrokerServer) handleClient(conn net.Conn) {
	addr := conn.RemoteAddr().String()
	s.logger.Debug("new client connection", "remote", addr)
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, MaxMessageSize), MaxMessageSize)

	var session *clientSession
	defer func() {
		if session != nil {
			s.removeSession(session)
		}
		_ = conn.Close()
		s.logger.Info("client connection closed", "remote", addr)
	}()

	for scanner.Scan() {
		select {
		case <-s.done:
			return
		default:
		}

		line := scanner.Bytes()
		s.logger.Debug("received command", "remote", addr, "bytes", len(line))
		var cmd Command
		if err := json.Unmarshal(line, &cmd); err != nil {
			s.logger.Warn("invalid JSON from client",
				"error", err,
				"lineLen", len(line),
				"linePreview", string(line[:min(200, len(line))]),
			)
			s.writeToConn(conn, nil, &Response{
				ID:           cmd.ID,
				Status:       StatusError,
				ErrorCode:    ErrInvalidCommand,
				ErrorMessage: "invalid JSON",
			})
			continue
		}

		switch cmd.Cmd {
		case CmdConnect:
			session = s.handleConnect(conn, &cmd)
		case CmdSubscribe:
			s.handleSubscribe(session, &cmd)
		case CmdUnsubscribe:
			s.handleUnsubscribe(session, &cmd)
		case CmdPublish:
			s.handlePublish(session, &cmd)
		case CmdReceive:
			s.handleReceive(session, &cmd)
		case CmdAck:
			s.handleAck(session, &cmd)
		case CmdBind:
			s.handleBind(session, &cmd)
		case CmdUnbind:
			s.handleUnbind(session, &cmd)
		case CmdDisconnect:
			s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
			return
		default:
			s.sendToSession(session, &Response{
				ID:           cmd.ID,
				Status:       StatusError,
				ErrorCode:    ErrInvalidCommand,
				ErrorMessage: fmt.Sprintf("unknown command: %s", cmd.Cmd),
			})
		}
	}
}

func (s *DevBrokerServer) handleConnect(conn net.Conn, cmd *Command) *clientSession {
	clientID := cmd.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("network-client-%s", idgen.New()[:8])
	}
	queueName := cmd.QueueName
	if queueName == "" {
		queueName = "net-" + clientID
	}

	ctx, cancel := context.WithCancel(context.Background())
	session, evicted, err := s.registerSession(conn, cmd, clientID, queueName, cancel)
	releaseBinds(evicted)
	if err != nil {
		cancel()
		s.logger.Error("failed to create queue", "queue", queueName, "error", err)
		s.writeToConn(conn, nil, &Response{
			ID:           cmd.ID,
			Status:       StatusError,
			ErrorCode:    ErrInternalError,
			ErrorMessage: fmt.Sprintf("create queue: %v", err),
		})
		return nil
	}

	if cmd.PushMode {
		go s.pushLoop(ctx, session)
	}

	s.logger.Debug("client connected", "clientID", clientID, "pushMode", cmd.PushMode, "subs", len(cmd.Subscriptions))
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK, ClientID: clientID})
	return session
}

func (s *DevBrokerServer) registerSession(conn net.Conn, cmd *Command, clientID, queueName string, cancel context.CancelFunc) (session, evicted *clientSession, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evicted = s.removeClientLocked(clientID)

	queue, err := s.broker.CreateQueue(queueName, QueueOptions{})
	if err != nil {
		return nil, evicted, err
	}
	session = &clientSession{
		clientID:  clientID,
		queueName: queueName,
		queue:     queue,
		conn:      conn,
		pushMode:  cmd.PushMode,
		cancel:    cancel,
		client:    s.broker.NewClient(),
	}
	for _, sub := range cmd.Subscriptions {
		if err := s.broker.Subscribe(queue, sub); err != nil {
			s.logger.Error("failed to subscribe", "topic", sub, "queue", queueName, "error", err)
		}
	}
	s.clients[clientID] = session
	return session, evicted, nil
}

func (s *DevBrokerServer) pushLoop(ctx context.Context, session *clientSession) {
	for {
		msg, err := s.broker.Receive(ctx, session.queue)
		if err != nil {
			return
		}
		netMsg := brokerMsgToNetworkMsg(msg)
		s.sendToSession(session, &Response{Status: StatusMessage, Message: netMsg})
	}
}

func (s *DevBrokerServer) handleSubscribe(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}
	if err := s.broker.Subscribe(session.queue, cmd.TopicPattern); err != nil {
		s.logger.Error("subscribe failed", "topic", cmd.TopicPattern, "error", err)
		s.sendToSession(session, &Response{
			ID:           cmd.ID,
			Status:       StatusError,
			ErrorCode:    ErrInternalError,
			ErrorMessage: fmt.Sprintf("subscribe: %v", err),
		})
		return
	}
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
}

func (s *DevBrokerServer) handleUnsubscribe(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}
	_ = s.broker.Unsubscribe(session.queue, cmd.TopicPattern)
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
}

func (s *DevBrokerServer) handlePublish(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}

	msgID := idgen.New()

	payload, _ := json.Marshal(cmd.Payload)
	brokerMsg := &Message{
		Payload:        payload,
		UserProperties: anyMapToStringMap(cmd.UserProperties),
		ReplyTo:        cmd.ReplyTo,
		CorrelationID:  cmd.CorrelationID,
		Headers:        cmd.Headers,
	}

	if err := s.broker.Publish(context.Background(), cmd.Topic, brokerMsg); err != nil {
		s.logger.Error("publish failed", "topic", cmd.Topic, "error", err)
		s.sendToSession(session, &Response{
			ID:           cmd.ID,
			Status:       StatusError,
			ErrorCode:    ErrInternalError,
			ErrorMessage: fmt.Sprintf("publish: %v", err),
		})
		return
	}

	if s.tracer != nil && s.tracer.matchesFilter(cmd.Topic) {
		s.tracer.Trace(TraceRecord{
			Timestamp:      time.Now(),
			Type:           "message",
			SourceClientID: session.clientID,
			Topic:          cmd.Topic,
			UserProperties: cmd.UserProperties,
			Payload:        cmd.Payload,
			MatchingQueues: s.matchingQueueNames(cmd.Topic),
		})
	}

	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK, MessageID: msgID})
}

func (s *DevBrokerServer) matchingQueueNames(topic string) []string {
	return s.broker.MatchingQueues(topic)
}

func (s *DevBrokerServer) handleReceive(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}

	if session.pushMode {
		s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusError, ErrorCode: ErrInternalError, ErrorMessage: "RECEIVE not supported in push mode"})
		return
	}

	timeoutMs := cmd.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	msg, err := s.broker.Receive(ctx, session.queue)
	if err != nil {
		if ctx.Err() != nil {
			s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusTimeout})
		} else {
			s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusError, ErrorCode: ErrInternalError, ErrorMessage: err.Error()})
		}
		return
	}

	netMsg := brokerMsgToNetworkMsg(msg)
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK, Message: netMsg})
}

func (s *DevBrokerServer) handleAck(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}
	if s.tracer != nil && s.tracer.TraceAcksEnabled() {
		s.tracer.Trace(TraceRecord{
			Timestamp:      time.Now(),
			Type:           "ack",
			SourceClientID: session.clientID,
			MessageID:      cmd.MessageID,
		})
	}
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
}

func (s *DevBrokerServer) removeSession(session *clientSession) {
	s.mu.Lock()
	var removed *clientSession
	if s.clients[session.clientID] == session {
		removed = s.removeClientLocked(session.clientID)
	}
	s.mu.Unlock()
	releaseBinds(removed)
}

func (s *DevBrokerServer) removeClientLocked(clientID string) *clientSession {
	client, exists := s.clients[clientID]
	if !exists {
		return nil
	}
	client.closed.Store(true)
	client.cancel()
	_ = client.conn.Close()
	_ = s.broker.DeleteQueue(client.queueName)
	delete(s.clients, clientID)
	s.logger.Info("client disconnected", "clientID", clientID)
	return client
}

func releaseBinds(removed ...*clientSession) {
	for _, session := range removed {
		if session != nil {
			_ = session.client.Disconnect(context.Background())
		}
	}
}

func (s *DevBrokerServer) handleBind(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}
	opts := QueueOptions{Named: true, Exclusive: cmd.Exclusive, OnBindState: s.pushBindState(session, cmd.QueueName)}
	if _, err := session.client.CreateQueue(cmd.QueueName, opts); err != nil {
		s.sendToSession(session, &Response{
			ID:           cmd.ID,
			Status:       StatusError,
			ErrorCode:    ErrInternalError,
			ErrorMessage: fmt.Sprintf("bind: %v", err),
		})
		return
	}
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
}

func (s *DevBrokerServer) handleUnbind(session *clientSession, cmd *Command) {
	if session == nil {
		return
	}
	s.broker.releaseBind(session.client.owner, cmd.QueueName)
	s.sendToSession(session, &Response{ID: cmd.ID, Status: StatusOK})
}

func (s *DevBrokerServer) pushBindState(session *clientSession, queueName string) func(BindState) {
	return func(state BindState) {
		if session.closed.Load() {
			return
		}
		s.sendToSession(session, &Response{Status: StatusBindState, QueueName: queueName, BindState: state.String()})
	}
}

func (s *DevBrokerServer) sendToSession(session *clientSession, resp *Response) {
	if session == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("marshal response", "error", err)
		return
	}
	data = append(data, '\n')
	session.writeMu.Lock()
	_, err = session.conn.Write(data)
	session.writeMu.Unlock()
	switch {
	case errors.Is(err, net.ErrClosed):
		s.logger.Debug("devbroker: write to closed client connection dropped", "clientID", session.clientID)
	case err != nil:
		s.logger.Warn("devbroker: write response to client failed", "error", err, "clientID", session.clientID)
	}
}

func (s *DevBrokerServer) writeToConn(conn net.Conn, _ *clientSession, resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("marshal response", "error", err)
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		s.logger.Error("write response", "error", err)
	}
}

func brokerMsgToNetworkMsg(msg *Message) *NetworkMessage {
	var jsonPayload any
	if err := json.Unmarshal(msg.Payload, &jsonPayload); err != nil {
		jsonPayload = string(msg.Payload)
	}
	return &NetworkMessage{
		MessageID:      msg.ID,
		Topic:          msg.Topic,
		Payload:        jsonPayload,
		UserProperties: stringMapToAnyMap(msg.UserProperties),
		ReplyTo:        msg.ReplyTo,
		CorrelationID:  msg.CorrelationID,
		Headers:        msg.Headers,
	}
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func anyMapToStringMap(m map[string]any) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case string:
			cp[k] = val
		case float64:
			cp[k] = fmt.Sprint(val)
		case bool:
			cp[k] = fmt.Sprint(val)
		default:
			if b, err := json.Marshal(val); err == nil {
				cp[k] = string(b)
			} else {
				cp[k] = fmt.Sprint(val)
			}
		}
	}
	return cp
}

func stringMapToAnyMap(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
