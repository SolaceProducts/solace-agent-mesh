// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import "net"

func listenTCP4() (net.Listener, error) {
	return net.Listen("tcp4", "127.0.0.1:0")
}
