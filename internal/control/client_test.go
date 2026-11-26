// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewClient_NamespaceTrailingSlashStripped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input  string
		wantNS string
	}{
		{"ns", "ns"},
		{"ns/", "ns"},
		{"ns///", "ns"},
		{"walter-test/", "walter-test"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			c := NewClient(nil, tt.input)
			if c.namespace != tt.wantNS {
				t.Errorf("NewClient(%q) namespace = %q, want %q", tt.input, c.namespace, tt.wantNS)
			}
			replyPrefix := c.namespace + "/sam/v1/control/reply/"
			if strings.Contains(replyPrefix, "//") {
				t.Errorf("reply topic prefix %q contains '//'; broker rejects empty topic levels", replyPrefix)
			}
			if strings.HasPrefix(replyPrefix, "/") {
				t.Errorf("reply topic prefix %q starts with '/'; broker rejects leading empty levels", replyPrefix)
			}
		})
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("some other failure"), false},
		{"bare ClientError matching", &ClientError{Code: ErrOperationFailed, Message: `instance "agent_x" already exists`}, true},
		{"wrapped ClientError matching", fmt.Errorf("add instance: %w", &ClientError{Code: ErrOperationFailed, Message: `instance "x" already exists`}), true},
		{"ClientError without marker", &ClientError{Code: ErrOperationFailed, Message: "something else"}, false},
		{"plain error with marker", errors.New(`instance "foo" already exists`), true},
		{"marker in different position", errors.New("already exists: instance x"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAlreadyExistsError(tt.err); got != tt.want {
				t.Errorf("IsAlreadyExistsError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
