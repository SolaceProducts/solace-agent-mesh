// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package features

import (
	"cmp"
	"os"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type ReleasePhase string

const (
	PhaseExperimental           ReleasePhase = "experimental"
	PhaseEarlyAccess            ReleasePhase = "early_access"
	PhaseBeta                   ReleasePhase = "beta"
	PhaseControlledAvailability ReleasePhase = "controlled_availability"
	PhaseGeneralAvailability    ReleasePhase = "general_availability"
	PhaseDeprecated             ReleasePhase = "deprecated"
)

type Definition struct {
	Key          string       `yaml:"key"`
	Name         string       `yaml:"name"`
	ReleasePhase ReleasePhase `yaml:"release_phase"`
	Default      bool         `yaml:"default"`
	Description  string       `yaml:"description"`
}

const envPrefix = "SAM_FEATURE_"

type Registry struct {
	mu    sync.RWMutex
	flags map[string]Definition
}

type RegistryOption func(*Registry)

func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{flags: make(map[string]Definition)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Registry) Register(defs ...Definition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range defs {
		r.flags[d.Key] = d
	}
}

func (r *Registry) LoadFromYAML(data []byte) error {
	var catalog struct {
		Features []Definition `yaml:"features"`
	}
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return err
	}
	r.Register(catalog.Features...)
	return nil
}

func (r *Registry) IsEnabled(key string) bool {
	r.mu.RLock()
	def, registered := r.flags[key]
	r.mu.RUnlock()
	if !registered {
		return false
	}
	if v := os.Getenv(envVarName(key)); v != "" {
		v = strings.TrimSpace(v)
		return strings.EqualFold(v, "true") || v == "1"
	}
	return def.Default
}

func (r *Registry) Get(key string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.flags[key]
	return d, ok
}

func (r *Registry) HasEnvOverride(key string) bool {
	_, ok := os.LookupEnv(envVarName(key))
	return ok
}

func (r *Registry) All() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]Definition, 0, len(r.flags))
	for _, d := range r.flags {
		defs = append(defs, d)
	}
	slices.SortFunc(defs, func(a, b Definition) int { return cmp.Compare(a.Key, b.Key) })
	return defs
}

func (r *Registry) UnknownEnvOverrides() []string {
	r.mu.RLock()
	registered := make(map[string]struct{}, len(r.flags))
	for k := range r.flags {
		registered[envVarName(k)] = struct{}{}
	}
	r.mu.RUnlock()

	var unknown []string
	for _, e := range os.Environ() {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		name := e[:i]
		if !strings.HasPrefix(name, envPrefix) {
			continue
		}
		if _, ok := registered[name]; ok {
			continue
		}
		unknown = append(unknown, name)
	}
	slices.Sort(unknown)
	return unknown
}

func envVarName(key string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

func EnvVarName(key string) string {
	return envVarName(key)
}
