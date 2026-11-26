// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

const (
	CodeMethodNotFound = -32601
	CodeInternal       = -32603
	CodeNotFound       = -32001
	CodeConflict       = -32002
	CodeForbidden      = -32003
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil extensions.Error>"
	}
	return fmt.Sprintf("extension error %d: %s", e.Code, e.Message)
}

type RequestInfo struct {
	UserID   string
	ClientID string
}

type Handler func(ctx context.Context, info RequestInfo, params json.RawMessage) (any, *Error)

type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]map[string]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]map[string]Handler)}
}

func (d *Dispatcher) Register(uri, method string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	bucket, ok := d.handlers[uri]
	if !ok {
		bucket = make(map[string]Handler)
		d.handlers[uri] = bucket
	}
	bucket[method] = h
}

func (d *Dispatcher) Has(uri, method string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	bucket, ok := d.handlers[uri]
	if !ok {
		return false
	}
	_, ok = bucket[method]
	return ok
}

func (d *Dispatcher) SupportsURI(uri string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	bucket, ok := d.handlers[uri]
	return ok && len(bucket) > 0
}

func (d *Dispatcher) Dispatch(ctx context.Context, uri, method string, info RequestInfo, params json.RawMessage) (any, *Error) {
	d.mu.RLock()
	bucket, ok := d.handlers[uri]
	var h Handler
	if ok {
		h = bucket[method]
	}
	d.mu.RUnlock()

	if h == nil {
		return nil, &Error{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method %q not registered for extension %q", method, uri),
		}
	}
	return h(ctx, info, params)
}
