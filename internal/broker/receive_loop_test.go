// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type fakeReceiveBroker struct {
	receiveFn func(ctx context.Context, q Queue) (*Message, error)
}

func (b *fakeReceiveBroker) Connect(context.Context) error    { return nil }
func (b *fakeReceiveBroker) Disconnect(context.Context) error { return nil }
func (b *fakeReceiveBroker) IsConnected() bool                { return true }
func (b *fakeReceiveBroker) CreateQueue(string, QueueOptions) (Queue, error) {
	return nil, nil
}
func (b *fakeReceiveBroker) CreateDirectSubscription() (Queue, error) { return nil, nil }
func (b *fakeReceiveBroker) DeleteQueue(string) error                 { return nil }
func (b *fakeReceiveBroker) DeprovisionQueue(string) error            { return nil }
func (b *fakeReceiveBroker) Subscribe(Queue, string) error            { return nil }
func (b *fakeReceiveBroker) Unsubscribe(Queue, string) error          { return nil }
func (b *fakeReceiveBroker) Publish(context.Context, string, *Message) error {
	return nil
}
func (b *fakeReceiveBroker) PublishGuaranteed(context.Context, string, *Message) error {
	return nil
}
func (b *fakeReceiveBroker) Receive(ctx context.Context, q Queue) (*Message, error) {
	return b.receiveFn(ctx, q)
}
func (b *fakeReceiveBroker) Request(context.Context, string, *Message, time.Duration) (*Message, error) {
	return nil, nil
}
func (b *fakeReceiveBroker) CreateTemporaryQueue() (Queue, error) { return nil, nil }
func (b *fakeReceiveBroker) RequestReplyInbox(context.Context, string, *Message, time.Duration) (*Message, error) {
	return nil, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestRunReceiveLoop_HappyPath_NoHealthProblem(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var handled atomic.Int32
	var callCount atomic.Int32
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			if callCount.Add(1) == 1 {
				return &Message{Topic: "t"}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			handled.Add(1)
			cancel()
		})
		close(done)
	}()
	<-done

	if handled.Load() != 1 {
		t.Fatalf("handle called %d times, want 1", handled.Load())
	}
	if got := h.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil (no problem on happy path)", got)
	}
}

func TestRunReceiveLoop_RetryableError_SetsThenClearsHealth(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			switch callCount.Add(1) {
			case 1:
				return nil, ErrProvisionCooldown
			case 2:
				return &Message{Topic: "t"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			cancel()
		})
		close(done)
	}()
	<-done

	if got := h.Snapshot(); got != nil {
		t.Fatalf("Snapshot() after recovery = %v, want nil", got)
	}
}

func TestRunReceiveLoop_IdleAfterRecovery_ClearsHealthWithoutMessage(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			if callCount.Add(1) == 1 {
				return nil, ErrProvisionCooldown
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{
			Health:       &h,
			PollInterval: 20 * time.Millisecond,
		}, func(*Message) {
			t.Error("handle should not be called; this test never delivers a message")
		})
		close(done)
	}()

	waitForHealthDetail(t, &h, "queue:test")

	deadline := time.After(2 * time.Second)
	for h.Snapshot() != nil {
		select {
		case <-deadline:
			t.Fatal("health never cleared after the queue recovered; RunReceiveLoop is still waiting on a message instead of an idle poll tick")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiveLoop did not return after ctx cancellation")
	}
}

func TestRunReceiveLoop_ConsumeQueueUnavailable_IsRetryableAndTakesPrecedenceOverQueueNotFound(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	consumeErr := fmt.Errorf("solace broker: start persistent receiver for %q: %w: %w",
		"preprov/q/x", ErrConsumeQueueUnavailable, ErrQueueNotFound)
	if !errors.Is(consumeErr, ErrConsumeQueueUnavailable) || !errors.Is(consumeErr, ErrQueueNotFound) {
		t.Fatalf("test setup: consumeErr must wrap both sentinels; got %v", consumeErr)
	}
	proceed := make(chan struct{})
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			switch callCount.Add(1) {
			case 1:
				return nil, consumeErr
			case 2:
				<-proceed
				return &Message{Topic: "t"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			cancel()
		})
		close(done)
	}()

	detail := waitForHealthDetail(t, &h, "queue:test")
	if detail != string(health.DetailConsumeQueueUnavailable) {
		t.Errorf("Detail = %q, want DetailConsumeQueueUnavailable — the ErrQueueNotFound branch wrongly fired first and exited Fatal", detail)
	}
	close(proceed)
	<-done
}

func TestRunReceiveLoop_ProvisionCooldown_ReportsFixedSafeDetail(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	proceed := make(chan struct{})
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			switch callCount.Add(1) {
			case 1:
				return nil, ErrProvisionCooldown
			case 2:
				<-proceed
				return &Message{Topic: "t"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			cancel()
		})
		close(done)
	}()

	detail := waitForHealthDetail(t, &h, "queue:test")
	if detail != string(health.DetailProvisionCooldown) {
		t.Errorf("unexpected Detail for ErrProvisionCooldown: %q", detail)
	}
	close(proceed)
	<-done
}

func TestRunReceiveLoop_RetryableError_NeverLeaksRawErrorIntoDetail(t *testing.T) {
	secretErr := errors.New("dial tcp 10.0.0.5:55555: connection refused (token=secret-abc123)")
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	proceed := make(chan struct{})
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			switch callCount.Add(1) {
			case 1:
				return nil, secretErr
			case 2:
				<-proceed
				return &Message{Topic: "t"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}
	var h health.State
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			cancel()
		})
		close(done)
	}()

	detail := waitForHealthDetail(t, &h, "queue:test")
	if strings.Contains(detail, "secret-abc123") || strings.Contains(detail, "10.0.0.5") {
		t.Fatalf("health Detail leaked the raw error: %q", detail)
	}
	if detail != string(health.DetailTemporaryReceive) {
		t.Errorf("unexpected Detail for a generic error: %q", detail)
	}
	close(proceed)
	<-done
}

func waitForHealthDetail(t *testing.T, h *health.State, id string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		for _, p := range h.Snapshot() {
			if p.ID == id {
				return p.Detail
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for health problem %q", id)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRunReceiveLoop_QueueNotFound_Unplanned_MarksFatal(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	brk := &fakeReceiveBroker{
		receiveFn: func(context.Context, Queue) (*Message, error) {
			return nil, ErrQueueNotFound
		},
	}
	var h health.State
	RunReceiveLoop(context.Background(), brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
		t.Fatal("handle should not be called")
	})

	got := h.Snapshot()
	if len(got) != 1 || got[0].Severity != health.Fatal {
		t.Fatalf("Snapshot() = %v, want one Fatal problem", got)
	}
}

func TestRunReceiveLoop_QueueNotFound_Planned_ClearsHealth(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	brk := &fakeReceiveBroker{
		receiveFn: func(context.Context, Queue) (*Message, error) {
			return nil, ErrQueueNotFound
		},
	}
	var h health.State
	h.Set("queue:test", health.Degraded, "was retrying")

	RunReceiveLoop(context.Background(), brk, q, discardLogger(), ReceiveLoopOptions{
		Health:   &h,
		Teardown: func() bool { return true },
	}, func(*Message) {
		t.Fatal("handle should not be called")
	})

	if got := h.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil after a planned teardown", got)
	}
}

func TestRunReceiveLoop_ContextCancelled_ClearsHealthAndReturns(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			once.Do(func() {
				go func() {
					time.Sleep(150 * time.Millisecond)
					cancel()
				}()
			})
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	var h health.State
	h.Set("queue:test", health.Degraded, "was retrying")

	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
			t.Error("handle should not be called")
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiveLoop did not return after ctx cancellation")
	}
	if got := h.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil after clean shutdown", got)
	}
}

func TestRunReceiveLoop_NilMessage_ContinuesWithoutHandling(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	var callCount atomic.Int32
	brk := &fakeReceiveBroker{
		receiveFn: func(ctx context.Context, _ Queue) (*Message, error) {
			switch callCount.Add(1) {
			case 1:
				return nil, nil
			case 2:
				return &Message{Topic: "t"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}
	var handled atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{}, func(*Message) {
			handled.Add(1)
			cancel()
		})
		close(done)
	}()
	<-done

	if handled.Load() != 1 {
		t.Fatalf("handle called %d times, want 1", handled.Load())
	}
}

func TestRunReceiveLoop_NilHealth_DoesNotPanic(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	brk := &fakeReceiveBroker{
		receiveFn: func(context.Context, Queue) (*Message, error) {
			return nil, ErrQueueNotFound
		},
	}
	RunReceiveLoop(context.Background(), brk, q, discardLogger(), ReceiveLoopOptions{}, func(*Message) {
		t.Fatal("handle should not be called")
	})
}

func TestRunReceiveLoop_LogFieldsAndLoopName_DoNotPanic(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	brk := &fakeReceiveBroker{
		receiveFn: func(context.Context, Queue) (*Message, error) {
			return nil, ErrQueueNotFound
		},
	}
	RunReceiveLoop(context.Background(), brk, q, discardLogger(), ReceiveLoopOptions{
		LoopName:  "discovery",
		LogFields: []any{"handler", "myHandler"},
	}, func(*Message) {
		t.Fatal("handle should not be called")
	})
}

func TestRunReceiveLoop_HandleObservesCancellation_DoesNotReceiveAgain(t *testing.T) {
	q := &SimpleQueue{QName: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	var callCount atomic.Int32
	brk := &fakeReceiveBroker{
		receiveFn: func(context.Context, Queue) (*Message, error) {
			if callCount.Add(1) > 1 {
				t.Error("Receive called again after handle observed ctx cancellation")
			}
			return &Message{Topic: "t"}, nil
		},
	}
	var h health.State

	RunReceiveLoop(ctx, brk, q, discardLogger(), ReceiveLoopOptions{Health: &h}, func(*Message) {
		cancel()
		blocked := make(chan *Message)
		select {
		case blocked <- nil:
		case <-ctx.Done():
		}
	})

	if got := callCount.Load(); got != 1 {
		t.Fatalf("Receive called %d times, want exactly 1", got)
	}
	if got := h.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil after clean cancellation", got)
	}
}
