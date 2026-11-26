// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const buildTargetSourceFallback = "fallback"

type BuildTargetResponse struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Source        string `json:"source"`
	PythonVersion string `json:"pythonVersion,omitempty"`
}

type MixedTargetError struct {
	Targets []string
}

func (e *MixedTargetError) Error() string {
	return fmt.Sprintf("platform reports a mixed STR fleet (%v); set SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH to choose one explicitly", e.Targets)
}

type UndiscoverableTargetError struct {
	Unreachable error
}

func (e *UndiscoverableTargetError) Error() string {
	const pin = "set SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH to build for a specific target"
	if e.Unreachable != nil {
		return fmt.Sprintf("cannot determine toolset build target: platform unreachable (%v); %s", e.Unreachable, pin)
	}
	return "cannot determine toolset build target: no STR runtime has reported its architecture to the platform yet; wait for an STR worker to connect, or " + pin
}

func (e *UndiscoverableTargetError) Unwrap() error { return e.Unreachable }

const buildTargetSourceSTRInitV2 = "str_init"

func (c *Client) GetToolBuildTarget(ctx context.Context) (*BuildTargetResponse, error) {
	var resp BuildTargetResponse
	err := c.doSingle(ctx, http.MethodGet, platform.APIV2Prefix+"/toolsetBuildTarget", nil, &resp)
	if resp.Source == buildTargetSourceSTRInitV2 {
		resp.Source = entplatform.BuildTargetSourceSTRInit
	}
	if err == nil {
		return &resp, nil
	}
	if httpErr, ok := errors.AsType[*HTTPError](err); ok && httpErr.StatusCode == http.StatusConflict {
		if targets := extractConflictTargets(httpErr.Body); len(targets) > 0 {
			return nil, &MixedTargetError{Targets: targets}
		}
	}
	return nil, err
}

func extractConflictTargets(body string) []string {
	var env struct {
		ValidationDetails map[string][]string `json:"validationDetails"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return nil
	}
	return env.ValidationDetails["targets"]
}

type BuildTargetResolver interface {
	Resolve(ctx context.Context) (os, arch, pythonVersion string, err error)
}

type platformBuildTargetResolver struct {
	client *Client

	once  sync.Once
	osS   string
	arch  string
	pyVer string
	err   error
}

func NewPlatformBuildTargetResolver(c *Client) BuildTargetResolver {
	return &platformBuildTargetResolver{client: c}
}

func (r *platformBuildTargetResolver) Resolve(ctx context.Context) (string, string, string, error) {
	r.once.Do(func() {
		envOS := os.Getenv("SAM_TOOL_TARGET_OS")
		envArch := os.Getenv("SAM_TOOL_TARGET_ARCH")
		envPy := os.Getenv("SAM_TOOL_PYTHON_VERSION")
		if envOS != "" && envArch != "" {
			r.osS, r.arch, r.pyVer = envOS, envArch, envPy
			return
		}

		resp, err := r.client.GetToolBuildTarget(ctx)
		if err != nil {
			if mix, ok := errors.AsType[*MixedTargetError](err); ok {
				r.err = mix
				return
			}
			r.err = &UndiscoverableTargetError{Unreachable: err}
			return
		}

		if resp.Source == buildTargetSourceFallback {
			r.err = &UndiscoverableTargetError{}
			return
		}

		r.osS, r.arch, r.pyVer = resp.OS, resp.Arch, resp.PythonVersion
		if envOS != "" {
			r.osS = envOS
		}
		if envArch != "" {
			r.arch = envArch
		}
		if envPy != "" {
			r.pyVer = envPy
		}
	})
	return r.osS, r.arch, r.pyVer, r.err
}
