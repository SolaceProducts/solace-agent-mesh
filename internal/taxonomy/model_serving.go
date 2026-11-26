// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

import (
	"net/url"
	"slices"
	"strings"
)

var freeModelAPIBase string

var localModelProviders = []string{"ollama", "sgl", "vllm"}

var loopbackHosts = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "host.docker.internal"}

func ModelServedFrom(provider, apiBase string) string {
	if servesTheFreeModel(apiBase) {
		return ModelTypeFreeModel
	}
	if slices.Contains(localModelProviders, provider) || servesFromThisMachine(apiBase) {
		return ModelTypeLocal
	}
	return ModelTypeHosted
}

func servesTheFreeModel(apiBase string) bool {
	base := strings.TrimRight(freeModelAPIBase, "/")
	return base != "" && (apiBase == base || strings.HasPrefix(apiBase, base+"/"))
}

func servesFromThisMachine(apiBase string) bool {
	if apiBase == "" {
		return false
	}
	parsed, err := url.Parse(apiBase)
	if err != nil {
		return false
	}
	return slices.Contains(loopbackHosts, parsed.Hostname())
}
