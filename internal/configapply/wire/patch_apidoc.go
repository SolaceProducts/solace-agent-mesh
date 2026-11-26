// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Patch[_]) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Patch is a field of a PATCH body: left out when Set is false, sent as null
when Null is true, and sent as Value otherwise. It is the client side of
apiv2.Patch, which tells the same three apart on the server. A copy declares
it with omitzero, so an unset field stays off the wire; generic.ToPatch fills
it in.`,
	}
}
