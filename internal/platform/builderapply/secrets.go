// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func (s *Service) reclassifySecretRotations(ctx context.Context, kp *kindPlan, mayCompare bool) {
	switch kp.componentKind {
	case kindConnector, kindGateway, kindA2AAgent, kindAgent:
	default:
		return
	}

	kept := kp.plan.Unchanged[:0]
	for _, op := range kp.plan.Unchanged {
		if !mayCompare {
			if fields := unverifiableSecretFields(kp.componentKind, kp.desiredByName[op.Name]); len(fields) > 0 {
				d := kp.desiredByName[op.Name]
				d.decline("not applied without " + updateScopeName(kp.componentKind) +
					", and left unchanged: " + strings.Join(fields, ", "))
				kp.desiredByName[op.Name] = d
			}
			kept = append(kept, op)
			continue
		}

		rotated, err := s.secretsRotated(ctx, kp.componentKind, op, kp.desiredByName[op.Name])
		if err != nil {
			d := kp.desiredByName[op.Name]
			d.decline("could not verify whether stored credentials changed; treating this component as unchanged")
			kp.desiredByName[op.Name] = d
			kept = append(kept, op)
			continue
		}
		if rotated {
			kp.plan.Updates = append(kp.plan.Updates, op)
			if kp.rotatedField == nil {
				kp.rotatedField = map[string]string{}
			}
			kp.rotatedField[op.Name] = rotatedFieldName(kp.componentKind)
			continue
		}
		kept = append(kept, op)
	}
	kp.plan.Unchanged = kept
}

func unverifiableSecretFields(componentKind string, d desiredComponent) []string {
	if componentKind == kindAgent {
		return d.suppliedToolsetSecrets
	}
	if componentKind == kindA2AAgent {
		if d.a2aRotatesCredential {
			return []string{"agentAuthConfig"}
		}
		return nil
	}
	values := candidateValues(componentKind, d)
	if !carriesAnyLiteralSecret(componentKind, d, values) {
		return nil
	}
	var names []string
	switch componentKind {
	case kindConnector:
		c, _ := d.create.(entplatform.CreateConnectorRequest)
		names = entplatform.ConnectorSecretFieldNames(c.Type, c.Subtype)
	case kindGateway:
		g, _ := d.create.(entplatform.CreateGatewayRequest)
		names = entplatform.GatewaySecretFieldNames(g.Type)
	}
	var supplied []string
	for _, name := range names {
		if v, ok := values[name].(string); ok && v != "" &&
			!entplatform.IsSecretPlaceholder(v) && !entplatform.IsRedactedSecret(v) {
			supplied = append(supplied, name)
		}
	}
	slices.Sort(supplied)
	return supplied
}

func updateScopeName(componentKind string) string {
	_, update, ok := scopesForKind(componentKind)
	if !ok {
		return "the update scope for this resource"
	}
	return update
}

func rotatedFieldName(componentKind string) string {
	if componentKind == kindAgent {
		return "toolsetConfigs"
	}
	if componentKind == kindA2AAgent {
		return "agentAuthConfig"
	}
	return "values"
}

func (s *Service) secretsRotated(ctx context.Context, componentKind string, op generic.Op, d desiredComponent) (bool, error) {
	if op.ID == "" {
		return false, nil
	}
	if componentKind == kindAgent {
		return s.agentToolsetSecretsRotated(ctx, op, d)
	}
	if componentKind == kindA2AAgent {
		return d.a2aRotatesCredential, nil
	}
	values := candidateValues(componentKind, d)
	if values == nil {
		return false, nil
	}
	if componentKind == kindConnector {
		return entplatform.ConnectorSecretsDiffer(ctx, s.deps.Connectors, op.ID, values)
	}
	return entplatform.GatewaySecretsDiffer(ctx, s.deps.Gateways, op.ID, values)
}

func carriesAnyLiteralSecret(componentKind string, d desiredComponent, values map[string]any) bool {
	var names []string
	switch componentKind {
	case kindConnector:
		c, ok := d.create.(entplatform.CreateConnectorRequest)
		if !ok {
			return false
		}
		names = entplatform.ConnectorSecretFieldNames(c.Type, c.Subtype)
	case kindGateway:
		g, ok := d.create.(entplatform.CreateGatewayRequest)
		if !ok {
			return false
		}
		names = entplatform.GatewaySecretFieldNames(g.Type)
	}

	for _, name := range names {
		v, ok := values[name].(string)
		if !ok || v == "" {
			continue
		}
		if entplatform.IsSecretPlaceholder(v) || entplatform.IsRedactedSecret(v) {
			continue
		}
		return true
	}
	return false
}

func candidateValues(componentKind string, d desiredComponent) map[string]any {
	switch componentKind {
	case kindConnector:
		c, ok := d.create.(entplatform.CreateConnectorRequest)
		if !ok {
			return nil
		}
		v, _ := c.Values.(map[string]any)
		return stripPlaceholderSecrets(entplatform.ConnectorSecretFieldNames(c.Type, c.Subtype), v)
	case kindGateway:
		g, ok := d.create.(entplatform.CreateGatewayRequest)
		if !ok {
			return nil
		}
		v, _ := g.Values.(map[string]any)
		return stripPlaceholderSecrets(entplatform.GatewaySecretFieldNames(g.Type), v)
	}
	return nil
}

func stripPlaceholderSecrets(secretNames []string, values map[string]any) map[string]any {
	if len(values) == 0 || len(secretNames) == 0 {
		return values
	}
	var out map[string]any
	for _, name := range secretNames {
		s, ok := values[name].(string)
		if !ok || !entplatform.IsSecretPlaceholder(s) {
			continue
		}
		if out == nil {
			out = maps.Clone(values)
		}
		delete(out, name)
	}
	if out == nil {
		return values
	}
	return out
}
