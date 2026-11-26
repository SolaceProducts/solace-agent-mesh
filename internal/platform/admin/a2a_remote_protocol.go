// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	proxya2a "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
)

const (
	ProtocolPinAuto = "auto"
	ProtocolPinV10  = "v1_0"
	ProtocolPinV03  = "v0_3"
)

func A2AProtocolPins() []string {
	return []string{ProtocolPinAuto, ProtocolPinV10, ProtocolPinV03}
}

var proxyProtocolVersions = map[string]string{
	ProtocolPinV10: "1.0",
	ProtocolPinV03: "0.3",
}

const (
	RemoteBindingJSONRPC  = "jsonrpc"
	RemoteBindingHTTPJSON = "http_json"
)

var (
	remoteVersionsFromProxy = map[string]string{"1.0": ProtocolPinV10, "0.3": ProtocolPinV03}
	remoteBindingsFromProxy = map[string]string{"JSONRPC": RemoteBindingJSONRPC, "HTTP+JSON": RemoteBindingHTTPJSON}
)

const (
	maxTrustedCardJWKSURLBytes = 2048

	fieldProtocolVersion     = "protocolVersion"
	fieldTrustedCardJWKSURLs = "trustedCardJwksUrls"
	fieldRequireSignedCard   = "requireSignedCard"
)

type RemoteProtocolStatus struct {
	SelectedVersion string `json:"selectedVersion"`
	Binding         string `json:"binding"`
	Deprecated      bool   `json:"deprecated"`
	CardSignature   string `json:"cardSignature"`
}

func remoteProtocolStatus(card *a2a.AgentCard) *RemoteProtocolStatus {
	if card == nil {
		return nil
	}
	rp, ok := a2a.RemoteProtocolFromCard(*card)
	if !ok {
		return nil
	}
	version, versionOK := remoteVersionsFromProxy[rp.Version]
	binding, bindingOK := remoteBindingsFromProxy[rp.Binding]
	signature := cmp.Or(rp.CardSignature, a2a.CardSignatureUnchecked)
	if !versionOK || !bindingOK || !signature.Known() {
		return nil
	}
	return &RemoteProtocolStatus{SelectedVersion: version, Binding: binding, Deprecated: rp.Deprecated, CardSignature: string(signature)}
}

func validateCardTrustRequest(protocolVersion string, trustedURLs []string) error {
	if protocolVersion != "" && !slices.Contains(A2AProtocolPins(), protocolVersion) {
		return &ValidationError{Message: fmt.Sprintf("protocolVersion must be one of %v", A2AProtocolPins()), Field: fieldProtocolVersion}
	}
	if len(trustedURLs) > proxy.MaxTrustedCardJWKSURLs {
		return &ValidationError{Message: fmt.Sprintf("trustedCardJwksUrls takes at most %d URLs", proxy.MaxTrustedCardJWKSURLs), Field: fieldTrustedCardJWKSURLs}
	}
	for i, raw := range trustedURLs {
		if !isTrustedKeySetURL(raw) {
			return &ValidationError{Message: fmt.Sprintf("trustedCardJwksUrls[%d] must be an absolute https URL (http only on a loopback host) of at most %d characters", i, maxTrustedCardJWKSURLBytes), Field: fieldTrustedCardJWKSURLs}
		}
	}
	return nil
}

func validateCardTrust(agentCardLocation string, requireSignedCard bool, trustedURLs []string) error {
	if agentCardLocation == "none" && (requireSignedCard || len(trustedURLs) > 0) {
		return &ValidationError{Message: "trustedCardJwksUrls and requireSignedCard check a fetched agent card, so they cannot be set when agentCardLocation is 'none'", Field: fieldTrustedCardJWKSURLs}
	}
	if requireSignedCard && len(trustedURLs) == 0 {
		return &ValidationError{Message: "requireSignedCard needs at least one trustedCardJwksUrls entry to verify the signature against", Field: fieldRequireSignedCard}
	}
	return nil
}

func isTrustedKeySetURL(raw string) bool {
	if raw == "" || len(raw) > maxTrustedCardJWKSURLBytes {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && proxy.IsLoopbackHost(u.Hostname()))
}

func marshalTrustedCardJWKSURLs(urls []string) *string {
	if len(urls) == 0 {
		return nil
	}
	return marshalJSONPtr(urls)
}

func storedTrustedCardJWKSURLs(a *store.A2AAgent) []string {
	urls := []string{}
	if a.TrustedCardJWKSURLs != nil {
		_ = json.Unmarshal([]byte(*a.TrustedCardJWKSURLs), &urls)
	}
	return urls
}

func storedProtocolPin(a *store.A2AAgent) string {
	if a.ProtocolVersion == "" {
		return ProtocolPinAuto
	}
	return a.ProtocolVersion
}

func applyCardTrustFields(a *store.A2AAgent, protocolVersion string, trustedURLs []string, requireSignedCard *bool, replace bool) {
	if replace || protocolVersion != "" {
		a.ProtocolVersion = protocolVersion
		if a.ProtocolVersion == "" {
			a.ProtocolVersion = ProtocolPinAuto
		}
	}
	if replace || trustedURLs != nil {
		a.TrustedCardJWKSURLs = marshalTrustedCardJWKSURLs(trustedURLs)
	}
	if replace || requireSignedCard != nil {
		a.RequireSignedCard = requireSignedCard != nil && *requireSignedCard
	}
}

func addCardTrustToProxyConfig(appConfig map[string]any, a *store.A2AAgent) {
	if version, ok := proxyProtocolVersions[a.ProtocolVersion]; ok {
		appConfig["protocol_version"] = version
	}
	if urls := storedTrustedCardJWKSURLs(a); len(urls) > 0 {
		trusted := make([]any, len(urls))
		for i, u := range urls {
			trusted[i] = u
		}
		appConfig["trusted_card_jwks_urls"] = trusted
	}
	if a.RequireSignedCard {
		appConfig["require_signed_card"] = true
	}
}

func addCardTrustToConfigHash(data map[string]any, a *store.A2AAgent) {
	if pin := storedProtocolPin(a); pin != ProtocolPinAuto {
		data["protocolVersion"] = pin
	}
	if urls := storedTrustedCardJWKSURLs(a); len(urls) > 0 {
		data["trustedCardJwksUrls"] = urls
	}
	if a.RequireSignedCard {
		data["requireSignedCard"] = true
	}
}

type AdvertisedInterface struct {
	Version string `json:"version"`
	Binding string `json:"binding"`
}

func advertisedInterfaces(body []byte) []AdvertisedInterface {
	out := []AdvertisedInterface{}
	for _, ri := range proxya2a.UsableInterfaces(body) {
		version, versionOK := remoteVersionsFromProxy[ri.Version]
		binding, bindingOK := remoteBindingsFromProxy[ri.Binding]
		if versionOK && bindingOK {
			out = append(out, AdvertisedInterface{Version: version, Binding: binding})
		}
	}
	return out
}

func cardIsSigned(body []byte) bool {
	var card struct {
		Signatures []json.RawMessage `json:"signatures"`
	}
	return json.Unmarshal(body, &card) == nil && len(card.Signatures) > 0
}
