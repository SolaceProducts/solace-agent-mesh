// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func TestWindowTitle(t *testing.T) {
	envVar := features.EnvVarName(hideLinuxStageLabelFlag)
	tests := []struct {
		name string
		goos string
		env  string
		want string
	}{
		{"linux is marked Experimental by default", "linux", "", "Solace Agent Mesh (Experimental)"},
		{"linux hides the label when the flag is set", "linux", "true", "Solace Agent Mesh"},
		{"linux keeps the label when the flag is false", "linux", "false", "Solace Agent Mesh (Experimental)"},
		{"darwin is GA", "darwin", "", "Solace Agent Mesh"},
		{"windows is GA", "windows", "", "Solace Agent Mesh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envVar, tt.env)
			flags := features.NewRegistry()
			if err := features.LoadFlags(flags); err != nil {
				t.Fatalf("LoadFlags: %v", err)
			}
			if got := windowTitle(tt.goos, flags); got != tt.want {
				t.Errorf("windowTitle(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}
