// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package registry

import (
	"context"
	"fmt"
	"sync"
)

type Lister interface {
	ListSkills(ctx context.Context) ([]NamedID, error)
	ListConnectors(ctx context.Context) ([]NamedID, error)
	ListToolsets(ctx context.Context) ([]NamedID, error)
}

type NamedID struct {
	ID   string
	Name string
}

type Registry struct {
	loadOnce sync.Once
	loadErr  error
	lister   Lister

	skillsByName      map[string]string
	skillsByID        map[string]string
	connectorsByName  map[string]string
	connectorsByID    map[string]string
	toolsetsByName    map[string]string
	toolsetsByID      map[string]string
	builtinToolsets   map[string]struct{}
	builtinSkills     map[string]struct{}
	desiredSkills     map[string]struct{}
	desiredConnectors map[string]struct{}
	desiredToolsets   map[string]struct{}
}

func New(lister Lister) *Registry {
	return &Registry{lister: lister}
}

func (r *Registry) Load(ctx context.Context) error {
	r.loadOnce.Do(func() {
		r.loadErr = r.load(ctx)
	})
	return r.loadErr
}

func (r *Registry) load(ctx context.Context) error {
	skills, err := r.lister.ListSkills(ctx)
	if err != nil {
		return fmt.Errorf("registry: list skills: %w", err)
	}
	connectors, err := r.lister.ListConnectors(ctx)
	if err != nil {
		return fmt.Errorf("registry: list connectors: %w", err)
	}
	toolsets, err := r.lister.ListToolsets(ctx)
	if err != nil {
		return fmt.Errorf("registry: list toolsets: %w", err)
	}

	r.skillsByName = indexByName(skills)
	r.skillsByID = indexByID(skills)
	r.connectorsByName = indexByName(connectors)
	r.connectorsByID = indexByID(connectors)
	r.toolsetsByName = indexByName(toolsets)
	r.toolsetsByID = indexByID(toolsets)
	return nil
}

func indexByName(items []NamedID) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		if it.Name == "" || it.ID == "" {
			continue
		}
		out[it.Name] = it.ID
	}
	return out
}

func indexByID(items []NamedID) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		if it.Name == "" || it.ID == "" {
			continue
		}
		out[it.ID] = it.Name
	}
	return out
}

func addNames(set *map[string]struct{}, names []string) {
	if *set == nil {
		*set = map[string]struct{}{}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		(*set)[n] = struct{}{}
	}
}

func resolveIDOrSentinel(byName map[string]string, desired map[string]struct{}, kind, name string) (string, error) {
	if id, ok := byName[name]; ok {
		return id, nil
	}
	if _, ok := desired[name]; ok {
		return name, nil
	}
	return "", fmt.Errorf("%s %q not found on platform and not declared in the desired manifest", kind, name)
}

func (r *Registry) LookupSkillID(name string) (string, error) {
	if _, ok := r.builtinSkills[name]; ok {
		return name, nil
	}
	return resolveIDOrSentinel(r.skillsByName, r.desiredSkills, "skill", name)
}

func (r *Registry) LookupConnectorID(name string) (string, error) {
	return resolveIDOrSentinel(r.connectorsByName, r.desiredConnectors, "connector", name)
}

func (r *Registry) LookupToolsetID(name string) (string, error) {
	return resolveIDOrSentinel(r.toolsetsByName, r.desiredToolsets, "toolset", name)
}

func (r *Registry) SetDesiredSkills(names []string) { addNames(&r.desiredSkills, names) }

func (r *Registry) SetDesiredConnectors(names []string) { addNames(&r.desiredConnectors, names) }

func (r *Registry) SetDesiredToolsets(names []string) { addNames(&r.desiredToolsets, names) }

func (r *Registry) ValidateToolset(name string) error {
	if _, ok := r.toolsetsByName[name]; ok {
		return nil
	}
	if _, ok := r.builtinToolsets[name]; ok {
		return nil
	}
	if _, ok := r.desiredToolsets[name]; ok {
		return nil
	}
	return fmt.Errorf("toolset %q not found on platform and not declared in the desired manifest", name)
}

func (r *Registry) SetBuiltinToolsets(names []string) { addNames(&r.builtinToolsets, names) }

func (r *Registry) SetBuiltinSkills(names []string) { addNames(&r.builtinSkills, names) }

func resolveName(byID map[string]string, kind, id string) (string, error) {
	if name, ok := byID[id]; ok {
		return name, nil
	}
	return "", fmt.Errorf("%s ID %q not found on platform", kind, id)
}

func (r *Registry) LookupSkillName(id string) (string, error) {
	return resolveName(r.skillsByID, "skill", id)
}

func (r *Registry) LookupConnectorName(id string) (string, error) {
	return resolveName(r.connectorsByID, "connector", id)
}

func (r *Registry) LookupToolsetName(id string) (string, error) {
	return resolveName(r.toolsetsByID, "toolset", id)
}
