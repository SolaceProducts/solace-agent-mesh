// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

func TestValidateWebhookURLWith_ChecksEveryResolvedAddressForEveryScheme(t *testing.T) {
	failingLookup := func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("servfail") }
	emptyLookup := func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }
	tests := []struct {
		name    string
		url     string
		lookup  ipLookup
		wantErr bool
	}{
		{"https host resolving to a private address", "https://hooks.example.test/x", stubLookup(new(atomic.Int32), "10.0.0.1"), true},
		{"http host resolving to loopback", "http://hooks.example.test/x", stubLookup(new(atomic.Int32), "127.0.0.1"), true},
		{"blocked address second in the answer", "https://hooks.example.test/x", stubLookup(new(atomic.Int32), "93.184.216.34", "169.254.169.254"), true},
		{"lookup error fails closed", "https://hooks.example.test/x", failingLookup, true},
		{"empty answer fails closed", "https://hooks.example.test/x", emptyLookup, true},
		{"unsupported scheme", "ftp://hooks.example.test/x", stubLookup(new(atomic.Int32), "93.184.216.34"), true},
		{"public https host", "https://hooks.example.test/x", stubLookup(new(atomic.Int32), "93.184.216.34", "8.8.8.8"), false},
		{"public http host", "http://hooks.example.test/x", stubLookup(new(atomic.Int32), "2606:4700:4700::1111"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURLWith(tt.url, tt.lookup)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateWebhookURLWith(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateWebhookURLWith_ReportsAnUnresolvableAndABlockedHostTheSameWay(t *testing.T) {
	blocked := validateWebhookURLWith("https://a.example.test/x", stubLookup(new(atomic.Int32), "10.0.0.1"))
	unresolvable := validateWebhookURLWith("https://a.example.test/x", func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("nxdomain")
	})
	if blocked == nil || unresolvable == nil || blocked.Error() != unresolvable.Error() {
		t.Fatalf("blocked = %v, unresolvable = %v; want the same message for both", blocked, unresolvable)
	}
}
