// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestBrokerCheck_SkipDevModeLocal(t *testing.T) {
	check := NewBrokerCheck()
	got := check.Run(context.Background(), Env{SolaceDevMode: "true"}, ContextLocal)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}

func TestBrokerCheck_MissingURL_LocalSkips(t *testing.T) {
	got := NewBrokerCheck().Run(context.Background(), Env{}, ContextLocal)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}

func TestBrokerCheck_MissingURL_HelmFails(t *testing.T) {
	got := NewBrokerCheck().Run(context.Background(), Env{}, ContextHelm)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
}

func TestBrokerCheck_TCPFailure(t *testing.T) {
	got := NewBrokerCheck().Run(context.Background(), Env{SolaceBrokerURL: "tcp://127.0.0.1:1"}, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "TCP connection failed") {
		t.Errorf("reason: %q", got.Reason)
	}
}

type fakeBroker struct {
	connectErr error
}

func (f *fakeBroker) Connect(context.Context) error    { return f.connectErr }
func (f *fakeBroker) Disconnect(context.Context) error { return nil }
func (f *fakeBroker) IsConnected() bool                { return f.connectErr == nil }
func (f *fakeBroker) CreateQueue(string, broker.QueueOptions) (broker.Queue, error) {
	panic("not used")
}
func (f *fakeBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (f *fakeBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (f *fakeBroker) CreateDirectSubscription() (broker.Queue, error)        { panic("not used") }
func (f *fakeBroker) DeleteQueue(string) error                               { panic("not used") }
func (f *fakeBroker) DeprovisionQueue(string) error                          { panic("not used") }
func (f *fakeBroker) Subscribe(broker.Queue, string) error                   { panic("not used") }
func (f *fakeBroker) Unsubscribe(broker.Queue, string) error                 { panic("not used") }
func (f *fakeBroker) Publish(context.Context, string, *broker.Message) error { panic("not used") }
func (f *fakeBroker) PublishGuaranteed(context.Context, string, *broker.Message) error {
	panic("not used")
}
func (f *fakeBroker) Receive(context.Context, broker.Queue) (*broker.Message, error) {
	panic("not used")
}
func (f *fakeBroker) Request(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	panic("not used")
}

func TestBrokerCheck_AuthSuccess(t *testing.T) {
	addr := startTCPListener(t)
	check := &BrokerCheck{BrokerFactory: func(*slog.Logger, string) (broker.Broker, error) {
		return &fakeBroker{}, nil
	}}
	got := check.Run(context.Background(), Env{SolaceBrokerURL: "tcp://" + addr}, ContextWheel)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
}

func TestBrokerCheck_AuthFailure(t *testing.T) {
	addr := startTCPListener(t)
	check := &BrokerCheck{BrokerFactory: func(*slog.Logger, string) (broker.Broker, error) {
		return &fakeBroker{connectErr: errors.New("bad credentials")}, nil
	}}
	got := check.Run(context.Background(), Env{SolaceBrokerURL: "tcp://" + addr}, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "Broker authentication failed") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestParseBrokerURL(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"tcp://broker.example.com:55443", "broker.example.com", 55443, false},
		{"tcps://broker.example.com", "broker.example.com", defaultBrokerPort, false},
		{"broker.example.com:1234", "broker.example.com", 1234, false},
		{"://bad", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			h, p, err := parseBrokerURL(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("err: got %v, want %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && (h != tt.wantHost || p != tt.wantPort) {
				t.Errorf("got (%s, %d), want (%s, %d)", h, p, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func startTCPListener(t *testing.T) string {
	t.Helper()
	ln, err := listenTCP4()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}
