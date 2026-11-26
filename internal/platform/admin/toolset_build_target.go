// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

const (
	defaultBuildTargetOS   = "linux"
	defaultBuildTargetArch = "arm64"
)

const (
	BuildTargetSourceSTRInit  = "str-init"
	buildTargetSourceFallback = "fallback"
)

type ToolsetBuildTargetResponse struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Source        string `json:"source"`
	PythonVersion string `json:"pythonVersion,omitempty"`
}

type MultipleBuildTargetsError struct {
	Targets []string
}

func (e *MultipleBuildTargetsError) Error() string {
	return "multiple distinct STR runtimes are active; set SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH to choose one explicitly"
}

func (r *STRRuntimeRegistry) BuildTarget() (ToolsetBuildTargetResponse, error) {
	fallback := ToolsetBuildTargetResponse{OS: defaultBuildTargetOS, Arch: defaultBuildTargetArch, Source: buildTargetSourceFallback}
	if r == nil {
		return fallback, nil
	}
	active := r.Active()
	switch len(active) {
	case 0:
		return fallback, nil
	case 1:
		return ToolsetBuildTargetResponse{
			OS:            active[0].OS,
			Arch:          active[0].Arch,
			Source:        BuildTargetSourceSTRInit,
			PythonVersion: active[0].PythonVersion,
		}, nil
	default:
		targets := make([]string, len(active))
		for i, rt := range active {
			targets[i] = rt.Target()
		}
		return ToolsetBuildTargetResponse{}, &MultipleBuildTargetsError{Targets: targets}
	}
}
