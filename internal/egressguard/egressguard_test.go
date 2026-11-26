// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package egressguard

import (
	"errors"
	"testing"
)

func TestDialControlRefusesBlockedAddressesAndAllowsPrivateOnlyWhenAsked(t *testing.T) {
	for _, tt := range []struct {
		address      string
		allowPrivate bool
		blocked      bool
	}{
		{"169.254.169.254:80", true, true},
		{"[fd00:ec2::254]:80", true, true},
		{"0.0.0.0:443", true, true},
		{"10.1.2.3:443", false, true},
		{"10.1.2.3:443", true, false},
		{"127.0.0.1:8080", false, true},
		{"127.0.0.1:8080", true, false},
		{"203.0.114.7:443", false, false},
		{"example.com:443", true, true},
	} {
		err := DialControl(tt.allowPrivate)("tcp", tt.address, nil)
		if got := errors.Is(err, ErrBlocked); got != tt.blocked {
			t.Errorf("DialControl(%v)(%s) blocked = %v (%v); want %v", tt.allowPrivate, tt.address, got, err, tt.blocked)
		}
	}
}

func TestDialControlMarksOnlyAddressesTheOverrideWouldAllowAsPrivateRange(t *testing.T) {
	for _, tt := range []struct {
		address      string
		allowPrivate bool
		private      bool
	}{
		{"10.1.2.3:443", false, true},
		{"127.0.0.1:8080", false, true},
		{"100.64.0.1:443", false, true},
		{"[::1]:443", false, true},
		{"169.254.169.254:80", false, false},
		{"169.254.169.254:80", true, false},
		{"[fd00:ec2::254]:80", false, false},
		{"0.0.0.0:443", false, false},
		{"224.0.0.1:443", false, false},
		{"example.com:443", false, false},
		{"203.0.114.7:443", false, false},
	} {
		err := DialControl(tt.allowPrivate)("tcp", tt.address, nil)
		if got := errors.Is(err, ErrPrivateRange); got != tt.private {
			t.Errorf("DialControl(%v)(%s) private range = %v (%v); want %v", tt.allowPrivate, tt.address, got, err, tt.private)
		}
		if tt.private && !errors.Is(err, ErrBlocked) {
			t.Errorf("DialControl(%v)(%s) = %v; a private-range refusal must also be ErrBlocked", tt.allowPrivate, tt.address, err)
		}
	}
}
