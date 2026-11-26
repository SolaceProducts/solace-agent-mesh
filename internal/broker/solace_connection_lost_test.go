// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"errors"
	"fmt"
	"testing"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

type allListenersService struct {
	listenerCapturingService
	interruption solace.ServiceInterruptionListener
}

func (s *allListenersService) AddServiceInterruptionListener(l solace.ServiceInterruptionListener) uint64 {
	s.interruption = l
	return 3
}

func TestSolaceBroker_ConnectionLostRunsFirst(t *testing.T) {
	cases := []struct {
		name string
		fire func(*allListenersService)
		want []string
	}{
		{"reconnection attempt", func(s *allListenersService) { s.attempt(reconnectEvent{}) }, []string{"lost", "watcher"}},
		{"service interruption", func(s *allListenersService) { s.interruption(reconnectEvent{}) }, []string{"lost"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
			svc := &allListenersService{}
			var order []string
			if _, err := b.CreateQueue("leader-q", QueueOptions{
				Named:       true,
				Exclusive:   true,
				OnBindState: func(BindState) { order = append(order, "watcher") },
			}); err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			b.OnConnectionLost(func() {
				order = append(order, "lost")
				b.disconnected.Store(true)
			})

			b.mu.Lock()
			b.registerLifecycleListenersLocked(svc)
			b.mu.Unlock()

			tc.fire(svc)
			if len(order) != len(tc.want) {
				t.Fatalf("order %v, want %v", order, tc.want)
			}
			for i := range tc.want {
				if order[i] != tc.want[i] {
					t.Fatalf("order %v, want %v", order, tc.want)
				}
			}
		})
	}
}

func TestSolaceBroker_ConnectionLostNotRunAfterDisconnect(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &listenerCapturingService{}
	called := 0
	b.OnConnectionLost(func() { called++ })

	b.mu.Lock()
	b.registerLifecycleListenersLocked(svc)
	b.mu.Unlock()
	b.disconnected.Store(true)

	svc.attempt(reconnectEvent{})
	if called != 0 {
		t.Fatalf("callback ran %d times after Disconnect, want 0", called)
	}
}

func TestIsPermanentConnectError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"authentication", fmt.Errorf("connect: %w", solace.NewError(&solace.AuthenticationError{}, "login failure", nil)), true},
		{"invalid configuration", solace.NewError(&solace.InvalidConfigurationError{}, "bad property", nil), true},
		{"unresolved host", solace.NewError(&solace.ServiceUnreachableError{}, "unresolved", nil), false},
		{"refused", fmt.Errorf("connect: %w", solace.NewNativeError("refused", subcode.CommunicationError)), false},
		{"unknown message vpn", solace.NewNativeError("vpn", subcode.MsgVpnNotAllowed), true},
		{"invalid client name", solace.NewNativeError("name", subcode.ClientNameInvalid), true},
		{"acl denied", solace.NewNativeError("acl", subcode.ClientAclDenied), true},
		{"username shut down", solace.NewNativeError("shutdown", subcode.ClientUsernameIsShutdown), false},
		{"vpn unavailable", solace.NewNativeError("vpn down", subcode.MsgVpnUnavailable), false},
		{"plain", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanentConnectError(tc.err); got != tc.want {
				t.Errorf("IsPermanentConnectError = %v, want %v", got, tc.want)
			}
		})
	}
}
