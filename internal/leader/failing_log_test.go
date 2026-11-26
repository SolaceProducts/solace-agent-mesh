// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

type recordedLog struct {
	msg   string
	attrs map[string]any
}

type errorLogRecorder struct {
	mu   sync.Mutex
	logs []recordedLog
}

func (r *errorLogRecorder) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelError
}

func (r *errorLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	entry := recordedLog{msg: rec.Message, attrs: map[string]any{}}
	rec.Attrs(func(a slog.Attr) bool {
		entry.attrs[a.Key] = a.Value.Any()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, entry)
	return nil
}

func (r *errorLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *errorLogRecorder) WithGroup(string) slog.Handler      { return r }

func (r *errorLogRecorder) find(msg string) (recordedLog, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if l.msg == msg {
			return l, true
		}
	}
	return recordedLog{}, false
}

type fieldedError struct{}

func (fieldedError) Error() string    { return "driver detail that must not be logged" }
func (fieldedError) LogFields() []any { return []any{"errorKind", "connection_refused"} }

func failingJobLog(t *testing.T, jobErr error) recordedLog {
	t.Helper()
	recorder := &errorLogRecorder{}
	brk := newSolaceLike()
	e := leader.New(brk, namespace, leader.RoleGateway, nil, slog.New(recorder))
	job := leader.Job{Name: leader.JobGatewayDataRetention, Every: 5 * time.Millisecond, Run: func(context.Context, leader.Term) error {
		return jobErr
	}}
	if err := e.Register(jobSource{job}); err != nil {
		t.Fatal(err)
	}
	if err := e.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	brk.report(broker.BindActive)

	var entry recordedLog
	eventually(t, "the keeps failing log", func() bool {
		var ok bool
		entry, ok = recorder.find("leader job keeps failing")
		return ok
	})
	return entry
}

func TestTheKeepsFailingLogCarriesTheJobError(t *testing.T) {
	entry := failingJobLog(t, errors.New("database unavailable"))

	got, ok := entry.attrs["error"].(error)
	if !ok || !strings.Contains(got.Error(), "database unavailable") {
		t.Errorf("keeps failing log error field = %v, want the job error", entry.attrs["error"])
	}
}

func TestTheKeepsFailingLogNamesTheFieldsOfALogFielderError(t *testing.T) {
	entry := failingJobLog(t, fieldedError{})

	if got := entry.attrs["errorKind"]; got != "connection_refused" {
		t.Errorf("keeps failing log errorKind = %v, want connection_refused", got)
	}
	if _, ok := entry.attrs["error"]; ok {
		t.Error("keeps failing log carries the error text of a LogFielder error, want only its own fields")
	}
}
