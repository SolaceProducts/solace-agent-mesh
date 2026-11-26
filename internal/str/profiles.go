// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

type SandboxProfile struct {
	MaxCPUSeconds   int
	MaxFileSizeMB   int
	MaxOpenFiles    int
	MaxProcesses    int
	MaxStackSizeMB  int
	NetworkIsolated bool
	KeepEnv         bool
	WritableVar     bool
}

var Profiles = map[string]SandboxProfile{
	"restrictive": {
		MaxCPUSeconds:   60,
		MaxFileSizeMB:   64,
		MaxOpenFiles:    128,
		MaxProcesses:    64,
		MaxStackSizeMB:  8,
		NetworkIsolated: true,
		KeepEnv:         false,
		WritableVar:     false,
	},
	"standard": {
		MaxCPUSeconds:   300,
		MaxFileSizeMB:   256,
		MaxOpenFiles:    512,
		MaxProcesses:    256,
		MaxStackSizeMB:  8,
		NetworkIsolated: false,
		KeepEnv:         true,
		WritableVar:     false,
	},
	"permissive": {
		MaxCPUSeconds:   600,
		MaxFileSizeMB:   1024,
		MaxOpenFiles:    1024,
		MaxProcesses:    1024,
		MaxStackSizeMB:  16,
		NetworkIsolated: false,
		KeepEnv:         true,
		WritableVar:     true,
	},
}

func GetProfile(name string) SandboxProfile {
	if name == "" {
		name = "standard"
	}
	if p, ok := Profiles[name]; ok {
		return p
	}
	return Profiles["standard"]
}
