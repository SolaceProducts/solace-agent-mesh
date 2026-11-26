// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"maps"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const authTypeNone = "none"

func emptyMeansPreserve(desired *string, stored string) {
	if *desired == "" {
		*desired = stored
	}
}

type a2aAuthDecision struct {
	Config        any
	Type          string
	EffectiveType string
	Rotates       bool
}

func decideA2AAuth(authoredConfig any, authoredType, storedType string) (a2aAuthDecision, error) {
	preserve := a2aAuthDecision{EffectiveType: storedType}

	if authoredConfig == nil && authoredType == "" {
		return preserve, nil
	}

	effective := cmp.Or(authoredType, configAuthType(authoredConfig), storedType)

	if effective != "" && !entplatform.IsKnownA2AAuthType(effective) {
		return preserve, fmt.Errorf("unknown auth type %q; must be one of %s",
			effective, strings.Join(entplatform.A2AAuthTypes(), ", "))
	}

	if mismatch := contradictorySecretKey(authoredConfig, effective); mismatch != "" {
		return preserve, fmt.Errorf("auth config declares type %q but carries %q, which belongs to a different auth type",
			effective, mismatch)
	}

	if effective == authTypeNone {
		if authoredType != authTypeNone {
			return preserve, nil
		}
		return a2aAuthDecision{
			Config:        map[string]any{"type": authTypeNone},
			Type:          authTypeNone,
			EffectiveType: authTypeNone,
		}, nil
	}

	cleaned := withoutUnfilledSecrets(authoredConfig, effective)
	rotates := configCarriesLiteralSecret(cleaned, effective)

	if !rotates && !hasKeysBeyondType(cleaned) && effective == storedType {
		return preserve, nil
	}

	out := map[string]any{"type": effective}
	maps.Copy(out, cleaned)
	out["type"] = effective

	return a2aAuthDecision{
		Config:        out,
		Type:          authoredType,
		EffectiveType: effective,
		Rotates:       rotates,
	}, nil
}

func decideA2AAuthSurface(field string, authored any, authoredType string, stored any, storedType string) (decision a2aAuthDecision, note string, err error) {
	authored, kept := keepStoredAuthOverPlaceholders(authored, authoredType, stored, storedType)
	if len(kept) > 0 {
		note = fmt.Sprintf("kept these stored %s fields over a <<__SAM_REQUIRED__>> placeholder: %s", field, strings.Join(kept, ", "))
	}
	decision, err = decideA2AAuth(authored, authoredType, storedType)
	return decision, note, err
}

func keepStoredAuthOverPlaceholders(authored any, authoredType string, stored any, storedType string) (any, []string) {
	authoredMap, authoredIsMap := authored.(map[string]any)
	storedMap, storedIsMap := stored.(map[string]any)
	if !authoredIsMap || !storedIsMap || cmp.Or(authoredType, configAuthType(authored), storedType) != storedType {
		return authored, nil
	}
	secrets := map[string]struct{}{}
	for _, key := range entplatform.A2AAuthSecretWireFields(storedType) {
		secrets[key] = struct{}{}
	}
	out := maps.Clone(authoredMap)
	return out, keepStoredOverPlaceholders(out, storedMap, secrets)
}

func contradictorySecretKey(config any, effective string) string {
	m, ok := config.(map[string]any)
	if !ok {
		return ""
	}
	own := map[string]struct{}{}
	for _, k := range entplatform.A2AAuthSecretWireFields(effective) {
		own[k] = struct{}{}
	}
	for _, authType := range entplatform.A2AAuthTypes() {
		if authType == effective {
			continue
		}
		for _, k := range entplatform.A2AAuthSecretWireFields(authType) {
			if _, isOwn := own[k]; isOwn {
				continue
			}
			if v, present := m[k].(string); present && v != "" &&
				!entplatform.IsSecretPlaceholder(v) && !entplatform.IsRedactedSecret(v) {
				return k
			}
		}
	}
	return ""
}

func configAuthType(config any) string {
	m, ok := config.(map[string]any)
	if !ok {
		return ""
	}
	t, _ := m["type"].(string)
	return t
}

func withoutUnfilledSecrets(config any, authType string) map[string]any {
	m, ok := config.(map[string]any)
	if !ok {
		return nil
	}
	out := maps.Clone(m)
	for _, key := range entplatform.A2AAuthSecretWireFields(authType) {
		s, isString := out[key].(string)
		if !isString {
			continue
		}
		if s == "" || entplatform.IsSecretPlaceholder(s) || entplatform.IsRedactedSecret(s) {
			delete(out, key)
		}
	}
	return out
}

func configCarriesLiteralSecret(cleaned map[string]any, authType string) bool {
	for _, key := range entplatform.A2AAuthSecretWireFields(authType) {
		if s, ok := cleaned[key].(string); ok && s != "" {
			return true
		}
	}
	return false
}

func hasKeysBeyondType(cleaned map[string]any) bool {
	for key := range cleaned {
		if key != "type" {
			return true
		}
	}
	return false
}
