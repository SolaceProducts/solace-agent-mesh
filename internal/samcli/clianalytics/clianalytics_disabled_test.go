// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package clianalytics

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestSessionIDIsEmpty(t *testing.T) {
	if got := SessionID(); got != "" {
		t.Errorf("SessionID() = %q, want empty — this build reports nothing, so it must mint no identifier", got)
	}
}

func TestTrackCommandAndReportAreNoOps(t *testing.T) {
	cmd := &cobra.Command{Use: "run"}
	if got := TrackCommand(cmd); got != cmd {
		t.Errorf("TrackCommand returned %v, want the command it was given", got)
	}

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Report panicked: %v", p)
		}
	}()
	Report(context.Background(), cmd, nil, Options{})
	Report(context.Background(), cmd, errors.New("command failed"), Options{
		DatabaseURL: "postgres://ignored",
		DataDir:     t.TempDir(),
		Gateway:     Gateway{BaseURL: "https://gateway.example", Token: "t"},
	})
}

func TestGatewayAddressed(t *testing.T) {
	if (Gateway{}).Addressed() {
		t.Error("a Gateway with no BaseURL is not addressed")
	}
	if !(Gateway{BaseURL: "https://gateway.example"}).Addressed() {
		t.Error("a Gateway with a BaseURL is addressed")
	}
}
