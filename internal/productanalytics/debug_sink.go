// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"sync"
)

const (
	EnvDebugSinkFile = "SAM_PRODUCT_ANALYTICS_DEBUG_FILE"
	EnvDebugSink     = "SAM_PRODUCT_ANALYTICS_DEBUG"
)

type debugRecord struct {
	EventID       string         `json:"eventId"`
	DistinctID    string         `json:"distinctId,omitempty"`
	SessionID     string         `json:"sessionId,omitempty"`
	PublisherID   string         `json:"publisherId"`
	Seq           uint64         `json:"seq"`
	Name          string         `json:"name"`
	OccurredAt    string         `json:"occurredAt"`
	ReceivedAt    string         `json:"receivedAt"`
	SchemaVersion int            `json:"schemaVersion"`
	Properties    map[string]any `json:"properties,omitempty"`
}

type DebugSink struct {
	origin string

	mu     sync.Mutex
	w      io.Writer
	closer io.Closer
}

func NewDebugSink(w io.Writer) *DebugSink {
	return &DebugSink{w: w}
}

func (d *DebugSink) describe() string {
	if d.origin == "" {
		return "product analytics debug sink"
	}
	return "product analytics debug sink configured by " + d.origin
}

func withoutPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("%s: %w", pathErr.Op, pathErr.Err)
	}
	return err
}

func NewDebugSinkFromEnv() (*DebugSink, error) {
	if path := os.Getenv(EnvDebugSinkFile); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open the file named by %s: %w", EnvDebugSinkFile, withoutPath(err))
		}
		return &DebugSink{origin: EnvDebugSinkFile, w: f, closer: f}, nil
	}
	raw := os.Getenv(EnvDebugSink)
	if raw == "" {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s as a boolean: %w", EnvDebugSink, err)
	}
	if !enabled {
		return nil, nil
	}
	return &DebugSink{origin: EnvDebugSink, w: os.Stderr}, nil
}

func (d *DebugSink) Send(_ context.Context, events []Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.w == nil {
		return nil
	}
	enc := json.NewEncoder(d.w)
	for _, ev := range events {
		if err := enc.Encode(recordFor(ev)); err != nil {
			return NonRetryable(fmt.Errorf("write a line to the %s: %w", d.describe(), withoutPath(err)))
		}
	}
	return nil
}

func recordFor(ev Event) debugRecord {
	return debugRecord{
		EventID:       ev.EventID,
		DistinctID:    ev.DistinctID,
		SessionID:     ev.SessionID,
		PublisherID:   ev.PublisherID,
		Seq:           ev.Seq,
		Name:          ev.Name,
		OccurredAt:    ev.OccurredAt.UTC().Format(TimestampLayout),
		ReceivedAt:    ev.ReceivedAt.UTC().Format(TimestampLayout),
		SchemaVersion: ev.SchemaVersion,
		Properties:    ev.Properties,
	}
}

func (d *DebugSink) Flush(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.w.(*os.File)
	if !ok || d.closer == nil {
		return nil
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync the %s: %w", d.describe(), withoutPath(err))
	}
	return nil
}

func (d *DebugSink) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closer == nil {
		return nil
	}
	err := d.closer.Close()
	d.closer, d.w = nil, nil
	if err != nil {
		return fmt.Errorf("close the %s: %w", d.describe(), withoutPath(err))
	}
	return nil
}
