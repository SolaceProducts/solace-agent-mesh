// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"errors"
	"testing"
)

var errBackendDown = errors.New("backend down")

func TestSaveNextVersion(t *testing.T) {
	tests := []struct {
		name         string
		saveErrs     []error
		wantVersion  int
		wantErr      error
		wantAttempts int
	}{
		{
			name:         "first version free is saved",
			saveErrs:     []error{nil},
			wantVersion:  0,
			wantAttempts: 1,
		},
		{
			name:         "version taken by another writer retries with the next",
			saveErrs:     []error{ErrVersionConflict, ErrVersionConflict, nil},
			wantVersion:  2,
			wantAttempts: 3,
		},
		{
			name:         "other save error is returned without retry",
			saveErrs:     []error{errBackendDown},
			wantErr:      errBackendDown,
			wantAttempts: 1,
		},
		{
			name: "conflict on every attempt gives up after ten",
			saveErrs: []error{
				ErrVersionConflict, ErrVersionConflict, ErrVersionConflict, ErrVersionConflict, ErrVersionConflict,
				ErrVersionConflict, ErrVersionConflict, ErrVersionConflict, ErrVersionConflict, ErrVersionConflict,
			},
			wantErr:      ErrVersionConflict,
			wantAttempts: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := 0
			attempts := 0
			got, err := SaveNextVersion(t.Context(),
				func(context.Context) (int, error) {
					v := next
					next++
					return v, nil
				},
				func(context.Context, int) error {
					e := tt.saveErrs[attempts]
					attempts++
					return e
				},
			)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("SaveNextVersion() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got != tt.wantVersion {
				t.Errorf("SaveNextVersion() = %d, want %d", got, tt.wantVersion)
			}
			if attempts != tt.wantAttempts {
				t.Errorf("SaveNextVersion() attempts = %d, want %d", attempts, tt.wantAttempts)
			}
		})
	}
}

func TestSaveNextVersionReturnsNextVersionError(t *testing.T) {
	saved := false
	_, err := SaveNextVersion(t.Context(),
		func(context.Context) (int, error) { return 0, errBackendDown },
		func(context.Context, int) error {
			saved = true
			return nil
		},
	)
	if !errors.Is(err, errBackendDown) {
		t.Errorf("SaveNextVersion() error = %v, want %v", err, errBackendDown)
	}
	if saved {
		t.Errorf("SaveNextVersion() saved after the next version failed")
	}
}
