// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"slices"
	"testing"
)

func TestToolsetBuildTarget_SingleFleet(t *testing.T) {
	reg := NewSTRRuntimeRegistry()
	reg.Observe("linux", "amd64", "3.11")

	got, err := reg.BuildTarget()
	if err != nil {
		t.Fatalf("BuildTarget: %v", err)
	}
	want := ToolsetBuildTargetResponse{OS: "linux", Arch: "amd64", Source: BuildTargetSourceSTRInit, PythonVersion: "3.11"}
	if got != want {
		t.Errorf("response = %+v; want %+v", got, want)
	}
}

func TestToolsetBuildTarget_SingleFleet_NoPythonVersion(t *testing.T) {
	reg := NewSTRRuntimeRegistry()
	reg.Observe("linux", "amd64", "")

	got, err := reg.BuildTarget()
	if err != nil {
		t.Fatalf("BuildTarget: %v", err)
	}
	if got.PythonVersion != "" {
		t.Errorf("PythonVersion = %q; want it empty", got.PythonVersion)
	}
}

func TestToolsetBuildTarget_MixedFleet(t *testing.T) {
	reg := NewSTRRuntimeRegistry()
	reg.Observe("linux", "arm64", "3.11")
	reg.Observe("linux", "amd64", "3.11")

	_, err := reg.BuildTarget()

	multi, ok := errors.AsType[*MultipleBuildTargetsError](err)
	if !ok {
		t.Fatalf("err = %v; want *MultipleBuildTargetsError", err)
	}
	if want := []string{"linux/amd64", "linux/arm64"}; !slices.Equal(multi.Targets, want) {
		t.Errorf("targets = %v; want %v", multi.Targets, want)
	}
}

func TestToolsetBuildTarget_EmptyFleet(t *testing.T) {
	got, err := NewSTRRuntimeRegistry().BuildTarget()
	if err != nil {
		t.Fatalf("BuildTarget: %v", err)
	}
	want := ToolsetBuildTargetResponse{OS: defaultBuildTargetOS, Arch: defaultBuildTargetArch, Source: buildTargetSourceFallback}
	if got != want {
		t.Errorf("response = %+v; want %+v", got, want)
	}
}
