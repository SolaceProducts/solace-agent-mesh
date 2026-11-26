// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func effectiveParameters(pathLevel, operationLevel openapi3.Parameters) openapi3.Parameters {
	if len(pathLevel) == 0 {
		return operationLevel
	}

	overridden := make(map[parameterIdentity]struct{}, len(operationLevel))
	for _, ref := range operationLevel {
		if id, ok := identityOf(ref); ok {
			overridden[id] = struct{}{}
		}
	}

	merged := make(openapi3.Parameters, 0, len(pathLevel)+len(operationLevel))
	merged = append(merged, operationLevel...)
	for _, ref := range pathLevel {
		id, ok := identityOf(ref)
		if !ok {
			continue
		}
		if _, replaced := overridden[id]; replaced {
			continue
		}
		merged = append(merged, ref)
	}
	return merged
}

type parameterIdentity struct {
	name string
	in   string
}

func identityOf(ref *openapi3.ParameterRef) (parameterIdentity, bool) {
	if ref == nil || ref.Value == nil {
		return parameterIdentity{}, false
	}
	name := ref.Value.Name
	if ref.Value.In == string(locHeader) {
		name = strings.ToLower(name)
	}
	return parameterIdentity{name: name, in: ref.Value.In}, true
}
