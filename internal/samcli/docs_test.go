// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !embeddocs

package samcli

import (
	"strings"
	"testing"
)

func TestNewDocsCmd(t *testing.T) {
	cmd := newDocsCmd()
	if cmd.Use != "docs" {
		t.Errorf("Use = %q, want docs", cmd.Use)
	}
	f := cmd.Flags().Lookup("port")
	if f == nil {
		t.Fatal("port flag not registered")
	}
	if f.DefValue != "8585" {
		t.Errorf("port default = %q, want 8585", f.DefValue)
	}
	if f.Shorthand != "p" {
		t.Errorf("port shorthand = %q, want p", f.Shorthand)
	}
}

func TestDocsCmdUnavailableBuildFailsFastNamingTheBuildTag(t *testing.T) {
	cmd := newDocsCmd()
	cmd.SetContext(t.Context())
	cmd.SetArgs(nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when docs are not embedded")
	}
	if !strings.Contains(err.Error(), "embeddocs") {
		t.Errorf("error %q should name the embeddocs build tag", err)
	}
}
