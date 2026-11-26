// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package capabilities

import (
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var catalogYAML []byte

type Maturity string

const (
	MaturityUnavailable  Maturity = "unavailable"
	MaturityStable       Maturity = "stable"
	MaturityBeta         Maturity = "beta"
	MaturityEarlyAccess  Maturity = "early_access"
	MaturityExperimental Maturity = "experimental"
)

var knownMaturities = map[Maturity]bool{
	MaturityUnavailable:  true,
	MaturityStable:       true,
	MaturityBeta:         true,
	MaturityEarlyAccess:  true,
	MaturityExperimental: true,
}

var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "illumos": true, "ios": true, "js": true, "linux": true,
	"netbsd": true, "openbsd": true, "plan9": true, "solaris": true,
	"wasip1": true, "windows": true,
}

type Entry struct {
	Key         string              `yaml:"key"`
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Maturity    Maturity            `yaml:"maturity"`
	Platforms   map[string]Maturity `yaml:"platforms"`
}

type Resolved struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Status         Maturity `json:"status"`
	Platform       string   `json:"platform"`
	HasEnvOverride bool     `json:"hasEnvOverride"`
}

type catalogFile struct {
	Capabilities []Entry `yaml:"capabilities"`
}

type reason int

const (
	reasonAvailable reason = iota
	reasonEnvDisabled
	reasonPlatformExcluded
	reasonUnconfigured
)

type Registry struct {
	goos    string
	entries []Entry
}

func NewRegistry(logger *slog.Logger) *Registry {
	return NewRegistryForPlatform(hostGOOS(), logger)
}

func NewRegistryForPlatform(goos string, logger *slog.Logger) *Registry {
	var cf catalogFile
	if err := yaml.Unmarshal(catalogYAML, &cf); err != nil {
		panic(fmt.Sprintf("capabilities: malformed embedded catalog.yaml: %v", err))
	}
	r := &Registry{goos: goos, entries: cf.Capabilities}
	if logger != nil {
		for _, w := range r.Lint() {
			logger.Warn(w)
		}
	}
	return r
}

func hostGOOS() string {
	if g := os.Getenv("SAM_CAPABILITY_FAKE_GOOS"); g != "" {
		return g
	}
	return runtime.GOOS
}

func (r *Registry) All() []Resolved {
	out := make([]Resolved, len(r.entries))
	for i, e := range r.entries {
		status, _ := r.resolve(e)
		out[i] = Resolved{
			Key:            e.Key,
			Name:           e.Name,
			Description:    e.Description,
			Status:         status,
			Platform:       r.goos,
			HasEnvOverride: r.hasEnvOverride(e.Key),
		}
	}
	return out
}

func (r *Registry) Resolve(key string) Maturity {
	for _, e := range r.entries {
		if e.Key == key {
			status, _ := r.resolve(e)
			return status
		}
	}
	return MaturityUnavailable
}

func (r *Registry) resolve(e Entry) (Maturity, reason) {
	return resolveFor(e, r.goos, r.hasEnvOverride(e.Key))
}

func resolveFor(e Entry, goos string, envDisabled bool) (Maturity, reason) {
	if envDisabled {
		return MaturityUnavailable, reasonEnvDisabled
	}
	if m, ok := e.Platforms[goos]; ok {
		if m == MaturityUnavailable {
			return MaturityUnavailable, reasonPlatformExcluded
		}
		return m, reasonAvailable
	}
	if e.Maturity != "" {
		if e.Maturity == MaturityUnavailable {
			return MaturityUnavailable, reasonPlatformExcluded
		}
		return e.Maturity, reasonAvailable
	}
	return MaturityUnavailable, reasonUnconfigured
}

func (r *Registry) hasEnvOverride(key string) bool {
	v := strings.ToLower(os.Getenv("SAM_CAPABILITY_" + envKey(key)))
	return v == "false" || v == "0"
}

func envKey(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

func (r *Registry) Lint() []string {
	var warns []string
	keys := make(map[string]bool, len(r.entries))
	for _, e := range r.entries {
		keys[e.Key] = true
		if e.Maturity != "" && !knownMaturities[e.Maturity] {
			warns = append(warns, fmt.Sprintf("capability %q: unknown maturity %q", e.Key, e.Maturity))
		}
		for goos, m := range e.Platforms {
			if !knownGOOS[goos] {
				warns = append(warns, fmt.Sprintf("capability %q: unknown platform %q in platforms", e.Key, goos))
			}
			if !knownMaturities[m] {
				warns = append(warns, fmt.Sprintf("capability %q: unknown maturity %q for platform %q", e.Key, m, goos))
			}
		}
		if hosts := r.unconfiguredHosts(e); len(hosts) > 0 {
			warns = append(warns, fmt.Sprintf("capability %q will be unavailable on %v — no baseline maturity and platform unlisted; set maturity: or add explicit entries if unintended", e.Key, hosts))
		}
	}
	for _, env := range unknownEnvOverrides(keys) {
		warns = append(warns, fmt.Sprintf("%s is set but matches no capability key", env))
	}
	return warns
}

func (r *Registry) unconfiguredHosts(e Entry) []string {
	var hosts []string
	for goos := range knownGOOS {
		if _, rsn := resolveFor(e, goos, false); rsn == reasonUnconfigured {
			hosts = append(hosts, goos)
		}
	}
	slices.Sort(hosts)
	return hosts
}

func unknownEnvOverrides(keys map[string]bool) []string {
	const prefix = "SAM_CAPABILITY_"
	valid := make(map[string]bool, len(keys))
	for k := range keys {
		valid[prefix+envKey(k)] = true
	}
	var unknown []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, prefix) || name == prefix+"FAKE_GOOS" {
			continue
		}
		if !valid[name] {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	return unknown
}
