// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import "sort"

const redactedSentinel = "<REDACTED>"

func redactedKeySet(values any) map[string]struct{} {
	out := map[string]struct{}{}
	m, ok := values.(map[string]any)
	if !ok {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok && s == redactedSentinel {
			out[k] = struct{}{}
		}
	}
	return out
}

func stripRedactedNested(desired, actual any) (any, any) {
	am, aok := actual.(map[string]any)
	dm, dok := desired.(map[string]any)
	if !aok || !dok {
		return desired, actual
	}
	cleanD := make(map[string]any, len(dm))
	cleanA := make(map[string]any, len(am))
	for k, av := range am {
		if s, ok := av.(string); ok && s == redactedSentinel {
			continue
		}
		cd, ca := stripRedactedNested(dm[k], av)
		cleanA[k] = ca
		if _, inD := dm[k]; inD {
			cleanD[k] = cd
		}
	}
	for k, dv := range dm {
		if _, inA := am[k]; inA {
			continue
		}
		cleanD[k] = dv
	}
	return cleanD, cleanA
}

func secretFieldsSet(prefix string, desired, actual any) []string {
	var out []string
	collectSecretFieldsSet(prefix, desired, actual, &out)
	sort.Strings(out)
	return out
}

func collectSecretFieldsSet(prefix string, desired, actual any, out *[]string) {
	dm, _ := asMap(desired)
	am, _ := asMap(actual)
	for k, av := range am {
		dv, set := dm[k]
		if !set || dv == nil {
			continue
		}
		path := joinPath(prefix, k)
		if s, ok := av.(string); ok && s == redactedSentinel {
			*out = append(*out, path)
			continue
		}
		collectSecretFieldsSet(path, dv, av, out)
	}
}
