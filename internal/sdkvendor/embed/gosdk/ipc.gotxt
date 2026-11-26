// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ipcClient struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
	broken bool
}

// ErrIPCConnBroken is returned by CallLLM once a prior call left the framed
// stream indeterminate (an abandoned read, a failed write, or a response-id
// mismatch). The client cannot be recovered — there is no reconnect path — so
// callers should stop retrying and escalate/abort rather than loop.
var ErrIPCConnBroken = errors.New("samtoolsdk: IPC connection unusable after a prior failed call")

var defaultLLMCallTimeout = 4 * time.Minute

type ipcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type ipcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ipcError       `json:"error,omitempty"`
}

type ipcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ipcError) Error() string {
	return fmt.Sprintf("IPC error %d: %s", e.Code, e.Message)
}

type llmCallParams struct {
	SystemPrompt string  `json:"system_prompt"`
	UserPrompt   string  `json:"user_prompt"`
	Temperature  float64 `json:"temperature"`
}

type llmCallResult struct {
	Text string `json:"text"`
}

func newIPCClient(socketPath string) (*ipcClient, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to IPC socket %s: %w", socketPath, err)
	}
	return &ipcClient{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}, nil
}

func (c *ipcClient) CallLLM(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultLLMCallTimeout)
		defer cancel()
	}

	raw, err := c.roundTrip(ctx, "llm/call", llmCallParams{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Temperature:  temperature,
	})
	if err != nil {
		return "", err
	}

	var result llmCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("unmarshaling LLM result: %w", err)
	}

	return result.Text, nil
}

func (c *ipcClient) roundTrip(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	req := ipcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.broken {
		return nil, ErrIPCConnBroken
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("llm call: %w", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling IPC request: %w", err)
	}
	data = append(data, '\n')

	stop := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			unblockPendingIOOrCloseConn(c.conn)
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-watcherDone
		_ = c.conn.SetDeadline(time.Time{})
	}()

	if _, writeErr := c.conn.Write(data); writeErr != nil {
		c.broken = true
		if ctx.Err() != nil {
			return nil, fmt.Errorf("llm call: %w (io: %v)", ctx.Err(), writeErr)
		}
		return nil, fmt.Errorf("writing IPC request: %w", writeErr)
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		c.broken = true
		if ctx.Err() != nil {
			return nil, fmt.Errorf("llm call: %w (io: %v)", ctx.Err(), err)
		}
		return nil, fmt.Errorf("reading IPC response: %w", err)
	}

	var resp ipcResponse
	if unmarshalErr := json.Unmarshal(line, &resp); unmarshalErr != nil {
		return nil, fmt.Errorf("unmarshaling IPC response: %w", unmarshalErr)
	}
	if resp.ID != id {
		c.broken = true
		return nil, fmt.Errorf("IPC response id mismatch: got %q, want %q", resp.ID, id)
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Result, nil
}

func (c *ipcClient) Close() error {
	return c.conn.Close()
}

func unblockPendingIOOrCloseConn(conn net.Conn) {
	if err := conn.SetDeadline(time.Now()); err != nil {
		_ = conn.Close()
	}
}
