// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"errors"
	"testing"
)

func TestResolveOwnerScope(t *testing.T) {
	tests := []struct {
		name          string
		callerUserID  string
		requestedUser string
		isAdmin       bool
		wantOwner     string
		wantErr       error
	}{
		{
			name:          "admin, empty request → tenant-wide",
			callerUserID:  "admin@example.com",
			requestedUser: "",
			isAdmin:       true,
			wantOwner:     "",
		},
		{
			name:          "admin, request other user → returned",
			callerUserID:  "admin@example.com",
			requestedUser: "alice@example.com",
			isAdmin:       true,
			wantOwner:     "alice@example.com",
		},
		{
			name:          "admin, request self → self",
			callerUserID:  "admin@example.com",
			requestedUser: "admin@example.com",
			isAdmin:       true,
			wantOwner:     "admin@example.com",
		},
		{
			name:          "non-admin, empty request → pinned to caller",
			callerUserID:  "alice@example.com",
			requestedUser: "",
			isAdmin:       false,
			wantOwner:     "alice@example.com",
		},
		{
			name:          "non-admin, request self → self",
			callerUserID:  "alice@example.com",
			requestedUser: "alice@example.com",
			isAdmin:       false,
			wantOwner:     "alice@example.com",
		},
		{
			name:          "non-admin, request other → forbidden",
			callerUserID:  "alice@example.com",
			requestedUser: "bob@example.com",
			isAdmin:       false,
			wantErr:       ErrForbiddenCrossUser,
		},
		{
			name:          "non-admin, empty caller, empty request → forbidden, never tenant-wide",
			callerUserID:  "",
			requestedUser: "",
			isAdmin:       false,
			wantErr:       ErrForbiddenCrossUser,
		},
		{
			name:          "non-admin, empty caller, requested self-shaped → forbidden",
			callerUserID:  "",
			requestedUser: "",
			isAdmin:       false,
			wantErr:       ErrForbiddenCrossUser,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, err := ResolveOwnerScope(tt.callerUserID, tt.requestedUser, tt.isAdmin)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
		})
	}
}
