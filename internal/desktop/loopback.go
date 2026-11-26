// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import "net"

func rewriteBindHostToLoopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if isWildcardBindHost(host) {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return addr
}

const atsRejectedIPv6WildcardHost = "[::]"

func isWildcardBindHost(host string) bool {
	switch host {
	case "", "::", atsRejectedIPv6WildcardHost, "0.0.0.0":
		return true
	default:
		return false
	}
}
