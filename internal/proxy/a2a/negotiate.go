// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

var errNoUsableInterface = errors.New("agent card advertises no A2A interface this proxy can use")

var errUnreadableInterfaces = errors.New("agent card interfaces could not be read")

var errUnsafeTenants = errors.New("agent card's usable interfaces name a tenant that is not a single URL path segment")

func errPinUnsatisfied(pin string) error {
	return fmt.Errorf("agent card advertises no A2A %s interface, and protocol_version pins %s", pin, pin)
}

type cardInterfaces struct {
	SupportedInterfaces []struct {
		URL             string `json:"url"`
		ProtocolBinding string `json:"protocolBinding"`
		ProtocolVersion string `json:"protocolVersion"`
		Tenant          string `json:"tenant"`
	} `json:"supportedInterfaces"`
	URL string `json:"url"`
}

func readCardInterfaces(body []byte, cardURL string) (ifaces cardInterfaces, unread bool, err error) {
	if json.Unmarshal(body, &ifaces) == nil {
		return ifaces, false, nil
	}
	if cardURL == "" {
		return cardInterfaces{}, true, errUnreadableInterfaces
	}
	return cardInterfaces{URL: cardURL}, true, nil
}

func UsableInterfaces(body []byte) []proxy.RemoteInterface {
	var card a2a.AgentCard
	if json.Unmarshal(body, &card) != nil {
		return nil
	}
	ifaces, _, err := readCardInterfaces(body, card.URL)
	if err != nil {
		return nil
	}
	candidates, _ := remoteInterfaces(ifaces)
	return rankInterfaces(candidates, proxy.ProtocolVersionAuto)
}

type negotiation struct {
	ranked           []proxy.RemoteInterface
	streaming        bool
	interfacesUnread bool
	v1Elsewhere      int
	unsafeTenants    int
	cardSignature    a2a.CardSignature
}

func (n *negotiation) chosen() proxy.RemoteInterface {
	return n.ranked[0]
}

func (n *negotiation) otherVersionThan(version string) (proxy.RemoteInterface, bool) {
	for _, ri := range n.ranked {
		if ri.Version != version {
			return ri, true
		}
	}
	return proxy.RemoteInterface{}, false
}

func (n *negotiation) sameOutcome(o *negotiation) bool {
	return n.chosen() == o.chosen() && n.interfacesUnread == o.interfacesUnread &&
		n.v1Elsewhere == o.v1Elsewhere && n.unsafeTenants == o.unsafeTenants &&
		n.cardSignatureOutcome() == o.cardSignatureOutcome()
}

func (n *negotiation) cardSignatureOutcome() a2a.CardSignature {
	return cmp.Or(n.cardSignature, a2a.CardSignatureUnchecked)
}

var interfacePreference = []struct{ version, binding string }{
	{proxy.ProtocolVersion10, proxy.RemoteBindingJSONRPC},
	{proxy.ProtocolVersion10, proxy.RemoteBindingHTTPJSON},
	{proxy.ProtocolVersion03, proxy.RemoteBindingJSONRPC},
}

func negotiate(card cardInterfaces, streaming bool, pin, fixedURL string) (*negotiation, error) {
	ifaces, unsafeTenants := remoteInterfaces(card)
	n, err := negotiateInterfaces(ifaces, streaming, pin, fixedURL)
	if err != nil {
		if unsafeTenants > 0 {
			return nil, errUnsafeTenants
		}
		return nil, err
	}
	n.unsafeTenants = unsafeTenants
	return n, nil
}

func negotiateInterfaces(ifaces []proxy.RemoteInterface, streaming bool, pin, fixedURL string) (*negotiation, error) {
	if fixedURL != "" {
		atFixedURL := slices.DeleteFunc(slices.Clone(ifaces), func(ri proxy.RemoteInterface) bool {
			return !sameURL(ri.URL, fixedURL)
		})
		if len(atFixedURL) == 0 {
			return awayFromFixedURL(ifaces, streaming, pin)
		}
		ifaces = atFixedURL
	}
	ranked := rankInterfaces(ifaces, pin)
	if len(ranked) == 0 {
		if pin != proxy.ProtocolVersionAuto {
			return nil, errPinUnsatisfied(pin)
		}
		return nil, errNoUsableInterface
	}
	return &negotiation{ranked: ranked, streaming: streaming}, nil
}

func awayFromFixedURL(ifaces []proxy.RemoteInterface, streaming bool, pin string) (*negotiation, error) {
	if pin != proxy.ProtocolVersionAuto && len(rankInterfaces(ifaces, pin)) == 0 {
		return nil, errPinUnsatisfied(pin)
	}
	n := staticNegotiation(pin, streaming)
	if pin == proxy.ProtocolVersionAuto {
		for _, ri := range ifaces {
			if ri.Version == proxy.ProtocolVersion10 && (ri.Binding == proxy.RemoteBindingJSONRPC || ri.Binding == proxy.RemoteBindingHTTPJSON) {
				n.v1Elsewhere++
			}
		}
	}
	return n, nil
}

func remoteInterfaces(card cardInterfaces) (ifaces []proxy.RemoteInterface, unsafeTenants int) {
	legacy := proxy.RemoteInterface{URL: card.URL, Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}
	if len(card.SupportedInterfaces) == 0 {
		return []proxy.RemoteInterface{legacy}, 0
	}
	ifaces = make([]proxy.RemoteInterface, 0, len(card.SupportedInterfaces)+1)
	for _, si := range card.SupportedInterfaces {
		version, ok := versionFamily(si.ProtocolVersion)
		if !ok {
			continue
		}
		ri := proxy.RemoteInterface{
			URL:     si.URL,
			Binding: strings.ToUpper(si.ProtocolBinding),
			Version: version,
			Tenant:  si.Tenant,
		}
		if ri.Binding == proxy.RemoteBindingHTTPJSON && !isPathSegment(ri.Tenant) {
			unsafeTenants++
			continue
		}
		ifaces = append(ifaces, ri)
	}
	if card.URL != "" {
		ifaces = append(ifaces, legacy)
	}
	return ifaces, unsafeTenants
}

func isPathSegment(s string) bool {
	return s != "." && s != ".." && url.PathEscape(s) == s
}

func sameURL(a, b string) bool {
	return comparableURL(a) == comparableURL(b)
}

var defaultPorts = map[string]string{"http": "80", "https": "443"}

func comparableURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && port != defaultPorts[u.Scheme] {
		host = net.JoinHostPort(host, port)
	}
	u.Host = host
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String()
}

func versionFamily(v string) (string, bool) {
	switch {
	case v == "" || v == "1" || strings.HasPrefix(v, "1."):
		return proxy.ProtocolVersion10, true
	case v == proxy.ProtocolVersion03 || strings.HasPrefix(v, proxy.ProtocolVersion03+"."):
		return proxy.ProtocolVersion03, true
	default:
		return "", false
	}
}

func rankInterfaces(ifaces []proxy.RemoteInterface, pin string) []proxy.RemoteInterface {
	var ranked []proxy.RemoteInterface
	for _, pref := range interfacePreference {
		if pin != proxy.ProtocolVersionAuto && pref.version != pin {
			continue
		}
		for _, ri := range ifaces {
			if ri.Version == pref.version && ri.Binding == pref.binding {
				ranked = append(ranked, ri)
				break
			}
		}
	}
	return ranked
}

func staticNegotiation(pin string, streaming bool) *negotiation {
	version := proxy.ProtocolVersion03
	if pin == proxy.ProtocolVersion10 {
		version = proxy.ProtocolVersion10
	}
	return &negotiation{
		ranked:    []proxy.RemoteInterface{{Binding: proxy.RemoteBindingJSONRPC, Version: version}},
		streaming: streaming,
	}
}
