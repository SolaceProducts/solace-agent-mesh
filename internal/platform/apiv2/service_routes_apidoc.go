// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Health) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Health says the platform is up.",
		Fields: map[string]apidoc.Field{
			"Status":  {Doc: "Status is always \"healthy\" while the platform answers."},
			"Service": {Doc: "Service names the service that answered. Always \"Platform Service\"."},
		},
	}
}

func (Version) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Version reports the build versions of the products the platform runs, and the
range of sam CLI versions it supports.`,
		Fields: map[string]apidoc.Field{
			"Products": {Doc: "Products lists the products in the build: Agent Mesh first and the secure tool runtime second."},
			"MinClientVersion": {Doc: `MinClientVersion is the oldest sam CLI version the platform supports. Null when the build sets
no minimum.`},
			"APIVersion": {Doc: `APIVersion is the API contract version, as YYYY-MM-DD, that the sam CLI sends in the SAM-Version
header. Null when the build sets none.`},
		},
	}
}

func (VersionProduct) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "VersionProduct is one product in the build and the version it was built at.",
		Fields: map[string]apidoc.Field{
			"Key": {Doc: `Key identifies the product, such as sam-gateway-go for Agent Mesh or sam-str-go for the secure
tool runtime.`},
			"Name":    {Doc: "Name is the product's display name."},
			"Version": {Doc: "Version is the version the product was built at."},
		},
	}
}
