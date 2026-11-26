// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const DefaultExecutionClass = "shared"

type classPolicy struct {
	serves string
}

func newClassPolicy(cfg WorkerConfig) classPolicy {
	return classPolicy{serves: strings.TrimSpace(cfg.ExecutionClass)}
}

func (p classPolicy) servesClass(class string) bool {
	if class == "" {
		class = DefaultExecutionClass
	}
	if p.isDefaultSTR() {
		return class == DefaultExecutionClass
	}
	return class == p.serves
}

func (p classPolicy) isDefaultSTR() bool {
	return p.serves == ""
}

func (p classPolicy) describe() string {
	if p.isDefaultSTR() {
		return DefaultExecutionClass
	}
	return p.serves
}

func classWorkerID(class string) string {
	return class + "-tools-worker"
}

func validClassName(class string) bool {
	for i, r := range class {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return class != ""
}

func isolatedToolExecutionEnabled() bool {
	r := features.NewRegistry()
	if err := features.LoadFlags(r); err != nil {
		return false
	}
	return r.IsEnabled(features.KeyIsolatedToolExecution)
}
