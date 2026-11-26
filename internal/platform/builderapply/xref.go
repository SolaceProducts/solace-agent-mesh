// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type xrefRegistry struct {
	idsByName map[string]map[string]string
	pending   map[string]map[string]struct{}
}

func newXRefRegistry() *xrefRegistry {
	return &xrefRegistry{
		idsByName: map[string]map[string]string{},
		pending:   map[string]map[string]struct{}{},
	}
}

func (r *xrefRegistry) put(kind, name, id string) {
	if name == "" || id == "" {
		return
	}
	if r.idsByName[kind] == nil {
		r.idsByName[kind] = map[string]string{}
	}
	r.idsByName[kind][name] = id
}

func (r *xrefRegistry) expect(kind, name string) {
	if name == "" {
		return
	}
	if r.pending[kind] == nil {
		r.pending[kind] = map[string]struct{}{}
	}
	r.pending[kind][name] = struct{}{}
}

func (r *xrefRegistry) LookupID(kind, name string) (string, error) {
	if id, ok := r.idsByName[kind][name]; ok {
		return id, nil
	}
	if _, pending := r.pending[kind][name]; pending {
		return name, nil
	}
	return "", fmt.Errorf("no %s named %q exists on the platform or in this build", kind, name)
}

func (r *xrefRegistry) LookupName(kind, id string) (string, error) {
	for name, known := range r.idsByName[kind] {
		if known == id {
			return name, nil
		}
	}
	return "", fmt.Errorf("no %s with id %q", kind, id)
}

func (r *xrefRegistry) resolvePending(kind, value string) (id string, isPending, resolved bool) {
	if _, pending := r.pending[kind][value]; !pending {
		return "", false, false
	}
	id, resolved = r.idsByName[kind][value]
	return id, true, resolved
}

func (s *Service) seedXRefRegistry(ctx context.Context, req ApplyBuildRequest) (*xrefRegistry, error) {
	reg := newXRefRegistry()

	connectors, err := s.deps.listConnectors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	for _, c := range connectors {
		reg.put("connector", c.Name, c.ID)
	}

	skills, err := s.deps.listSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	for _, sk := range skills {
		reg.put("skill", sk.Name, sk.ID)
	}

	originalLive := maps.Clone(reg.idsByName["connector"])
	referenced := connectorNamesReferenced(req)

	for _, c := range req.Components {
		if c.Kind != kindConnector {
			continue
		}
		resolved := connectorNameFromArtifact(c)
		if resolved == "" || resolved == c.Name {
			reg.expect("connector", c.Name)
			continue
		}
		id, existsUnderArtifactName := originalLive[resolved]
		if !existsUnderArtifactName {
			reg.expect("connector", c.Name)
			continue
		}
		shadowed, taken := originalLive[c.Name]
		if taken && shadowed != id {
			if _, isReferenced := referenced[c.Name]; isReferenced {
				return nil, &entplatform.ValidationError{Message: fmt.Sprintf(
					"connector component %q declares itself %q, but an agent in this build references %q, which is a different connector; rename the component or drop the reference so it is unambiguous",
					c.Name, resolved, c.Name)}
			}
			continue
		}
		reg.put("connector", c.Name, id)
	}
	return reg, nil
}

func connectorNamesReferenced(req ApplyBuildRequest) map[string]struct{} {
	out := map[string]struct{}{}
	for _, c := range req.Components {
		for _, name := range c.Connectors {
			out[name] = struct{}{}
		}
	}
	return out
}

func connectorNameFromArtifact(c BuildComponentInput) string {
	d, err := desiredConnector(c.Name, c.Content)
	if err != nil {
		return ""
	}
	create, ok := d.create.(entplatform.CreateConnectorRequest)
	if !ok {
		return ""
	}
	return create.Name
}

func bindAgentReferences(d *desiredComponent, in BuildComponentInput, reg *xrefRegistry) error {
	create, ok := d.create.(entplatform.CreateAgentRequest)
	if !ok {
		return nil
	}
	if in.Connectors != nil {
		create.Connectors = dedupe(in.Connectors)
		d.markAuthored("connectors")
	}
	stripBuiltinSkillIDs(&create)
	if err := generic.ResolveForward(&create, reg); err != nil {
		return fmt.Errorf("agent %q: %w", in.Name, err)
	}
	slices.Sort(create.Connectors)
	applySkillRefPrecedence(&create)
	d.create = create
	d.planProto = create
	return nil
}

func resolveAgentSentinels(create *entplatform.CreateAgentRequest, reg *xrefRegistry) error {
	swap := func(kind string, refs []string) error {
		for i, ref := range refs {
			id, isPending, resolved := reg.resolvePending(kind, ref)
			if !isPending {
				continue
			}
			if !resolved {
				return fmt.Errorf("%s %q was planned for this build but was never created", kind, ref)
			}
			refs[i] = id
		}
		return nil
	}
	if err := swap("connector", create.Connectors); err != nil {
		return err
	}
	slices.Sort(create.Connectors)
	return swap("skill", create.SkillIDs)
}

func applySkillRefPrecedence(create *entplatform.CreateAgentRequest) {
	hasM2M := len(create.SkillIDs) > 0
	if !hasM2M || len(create.SkillRefs) == 0 {
		return
	}
	kept := make([]string, 0, len(create.SkillRefs))
	for _, ref := range create.SkillRefs {
		if entplatform.KeepSkillRef(ref, hasM2M) {
			kept = append(kept, ref)
		}
	}
	if len(kept) == 0 {
		create.SkillRefs = nil
		return
	}
	create.SkillRefs = kept
}

func stripBuiltinSkillIDs(create *entplatform.CreateAgentRequest) {
	if len(create.SkillIDs) == 0 {
		return
	}
	builtins := builtinSkillNameSet()
	kept := make([]string, 0, len(create.SkillIDs))
	for _, id := range create.SkillIDs {
		if _, isBuiltin := builtins[id]; isBuiltin {
			continue
		}
		kept = append(kept, id)
	}
	if len(kept) == 0 {
		create.SkillIDs = nil
		return
	}
	create.SkillIDs = kept
}
