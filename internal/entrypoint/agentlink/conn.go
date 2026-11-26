// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type conn struct {
	id    string
	a     *Adapter
	agent *agentState
	ws    *websocket.Conn
	out   chan outbound
	wrote chan struct{}
	done  chan struct{}

	closeOnce  sync.Once
	closedFor  string
	deliveries sync.WaitGroup

	consuming       bool
	describeRefused bool
	retired         bool
	inflight        map[uint64]handedTask
	lastDelivery    uint64
	peers           string
}

func newConn(a *Adapter, s *agentState, ws *websocket.Conn) *conn {
	return &conn{
		id:       idgen.New(),
		a:        a,
		agent:    s,
		ws:       ws,
		out:      make(chan outbound, outboxSize),
		wrote:    make(chan struct{}, 1),
		done:     make(chan struct{}),
		inflight: map[uint64]handedTask{},
	}
}

type handedTask struct {
	request *broker.Message
	taskID  string
}

type outbound struct {
	data    []byte
	written func()
}

const (
	heldWatermark          = outboxSize / 2
	closedAnIdleConnection = "agent link closed a connection that sent nothing for its idle limit"
)

func (c *conn) send(f any) bool {
	b, err := json.Marshal(f)
	if err != nil {
		return false
	}
	return c.sendEncoded(b)
}

func (c *conn) sendEncoded(b []byte) bool {
	if !c.fits(b) {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- outbound{data: b}:
		return true
	case <-c.done:
		return false
	default:
		c.a.logger.Warn("agent link connection too slow; closing", "agentName", c.agent.cfg.Name, "connectionID", c.id)
		c.closeWithoutWaiting(websocket.CloseTryAgainLater, "too slow")
		return false
	}
}

func (c *conn) sendHeld(b []byte, written func()) bool {
	if !c.fits(b) {
		return false
	}
	for {
		if c.isClosed() {
			return false
		}
		if len(c.out) < heldWatermark {
			select {
			case c.out <- outbound{data: b, written: written}:
				return true
			case <-c.done:
				return false
			default:
			}
		}
		select {
		case <-c.wrote:
		case <-c.done:
			return false
		}
	}
}

func (c *conn) flushWithin(ctx context.Context) {
	flushed := make(chan struct{})
	select {
	case c.out <- outbound{written: func() { close(flushed) }}:
	case <-c.done:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-flushed:
	case <-c.done:
	case <-ctx.Done():
	}
}

func (c *conn) fits(b []byte) bool {
	if int64(len(b)) <= c.a.cfg.MaxFrameBytes {
		return true
	}
	c.a.logger.Warn("agent link did not send a frame over the frame limit", "agentName", c.agent.cfg.Name, "connectionID", c.id, "bytes", len(b))
	return false
}

func (c *conn) sendError(code, message, ref string) {
	c.send(errorFrame{Type: typeError, Code: code, Message: message, Ref: boundedRef(ref)})
}

func (c *conn) refuseFrame(frameType, code, message string) {
	c.send(errorFrame{Type: typeError, Code: code, Message: message, Frame: frameType})
}

func (c *conn) close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closedFor = reason
		close(c.done)
		c.closeSocket(code, reason)
	})
}

func (c *conn) closeWithoutWaiting(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closedFor = reason
		close(c.done)
		go c.closeSocket(code, reason)
	})
}

func (c *conn) closeSocket(code int, reason string) {
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = c.ws.Close()
}

func (c *conn) deliverableLocked() bool {
	return c.consuming && !c.describeRefused && !c.isClosed()
}

func (c *conn) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *conn) writeLoop() {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-c.done:
			return
		case f := <-c.out:
			if f.data != nil {
				_ = c.ws.SetWriteDeadline(time.Now().Add(idleTimeout))
				if err := c.ws.WriteMessage(websocket.TextMessage, f.data); err != nil {
					c.close(websocket.CloseAbnormalClosure, "write failed")
					return
				}
			}
			if f.written != nil {
				f.written()
			}
			select {
			case c.wrote <- struct{}{}:
			default:
			}
		case <-ping.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(pingInterval)); err != nil {
				c.close(websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		}
	}
}

func (c *conn) readLoop() {
	c.ws.SetReadLimit(c.a.cfg.MaxFrameBytes)
	_ = c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
	})
	for {
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			switch {
			case errors.Is(err, websocket.ErrReadLimit):
				c.close(closeMessageTooLarge, "message too large")
			case isTimeout(err):
				c.a.logger.Info(closedAnIdleConnection, "agentName", c.agent.cfg.Name, "connectionID", c.id,
					"boundTasks", c.agent.openTasksBoundTo(c.id))
				c.close(closeIdle, "idle")
			default:
				c.close(websocket.CloseNormalClosure, "")
			}
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
		if kind != websocket.TextMessage {
			c.close(closeProtocolError, "text messages only")
			return
		}
		if !withinValues(data, maxFrameValues) {
			c.refuseUndecoded(data, errCodeLimit, msgTooManyValues)
			continue
		}
		var f inFrame
		if err := json.Unmarshal(data, &f); err != nil || f.Type == "" {
			c.a.logger.Debug("agent link refused a message it could not decode", "agentName", c.agent.cfg.Name,
				"connectionID", c.id, "bytes", len(data))
			c.refuseUndecoded(data, errCodeBadMessage, msgNotALinkMessage)
			continue
		}
		c.agent.handleFrame(c, f)
	}
}

func (c *conn) refuseUndecoded(data []byte, code, message string) {
	var ids struct {
		Type      string `json:"type"`
		Delivery  uint64 `json:"delivery"`
		TaskID    string `json:"taskId"`
		SubtaskID string `json:"subtaskId"`
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(data, &ids)
	refused := errorFrame{Type: typeError, Code: code, Message: message}
	switch ids.Type {
	case typeDescribe:
		c.agent.refuseDescribe(c, code, message)
		return
	case typeConsume:
		c.refuseFrame(typeConsume, code, message)
		return
	case typeEvent, typeResult:
		refused.Ref = boundedRef(ids.TaskID)
	case typePrompt:
		refused.Ref = boundedRef(ids.TaskID)
		if validTopicLevel(ids.RequestID) {
			refused.RequestID = ids.RequestID
		}
	case typeAck, typeNack:
		if ids.Delivery != 0 {
			refused.Ref = strconv.FormatUint(ids.Delivery, 10)
		}
	case typePeerCall:
		if validTopicLevel(ids.SubtaskID) {
			refused.Ref = ids.SubtaskID
		}
	case typePeerCancel:
		refused.Ref = boundedRef(ids.SubtaskID)
	}
	c.send(refused)
}

func withinValues(data []byte, limit int) bool {
	values := 0
	inString, escaped, inScalar := false, false, false
	for _, b := range data {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString, inScalar = true, false
			values++
		case '{', '[':
			inScalar = false
			values++
		case ',', ':', '}', ']', ' ', '\t', '\n', '\r':
			inScalar = false
		default:
			if !inScalar {
				inScalar = true
				values++
			}
		}
		if values > limit {
			return false
		}
	}
	return true
}

func boundedRef(ref string) string {
	if len(ref) > maxIDBytes {
		return ""
	}
	return ref
}

func isTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	return errors.As(err, &ne) && ne.Timeout()
}
