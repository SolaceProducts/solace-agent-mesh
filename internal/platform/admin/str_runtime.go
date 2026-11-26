// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"strings"
	"sync"
	"time"
)

const defaultSTRRuntimeTTL = 24 * time.Hour

type STRRuntime struct {
	OS            string
	Arch          string
	PythonVersion string
	LastSeen      time.Time
}

func (r STRRuntime) Target() string {
	return r.OS + "/" + r.Arch
}

type STRRuntimeRegistry struct {
	mu   sync.RWMutex
	ttl  time.Duration
	now  func() time.Time
	seen map[string]STRRuntime
}

func NewSTRRuntimeRegistry() *STRRuntimeRegistry {
	return newSTRRuntimeRegistry(defaultSTRRuntimeTTL, time.Now)
}

func newSTRRuntimeRegistry(ttl time.Duration, now func() time.Time) *STRRuntimeRegistry {
	return &STRRuntimeRegistry{
		ttl:  ttl,
		now:  now,
		seen: make(map[string]STRRuntime),
	}
}

func (r *STRRuntimeRegistry) Observe(os, arch, pythonVersion string) {
	if os == "" || arch == "" {
		return
	}
	key := os + "/" + arch
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[key] = STRRuntime{OS: os, Arch: arch, PythonVersion: pythonVersion, LastSeen: now}
}

func (r *STRRuntimeRegistry) Active() []STRRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-r.ttl)
	out := make([]STRRuntime, 0, len(r.seen))
	for key, rt := range r.seen {
		if rt.LastSeen.Before(cutoff) {
			delete(r.seen, key)
			continue
		}
		out = append(out, rt)
	}
	slices.SortFunc(out, func(a, b STRRuntime) int { return strings.Compare(a.Target(), b.Target()) })
	return out
}
