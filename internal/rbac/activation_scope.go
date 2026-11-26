// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package rbac

const (
	ScopeActivationRead   = "activation:*:read"
	ScopeActivationUpdate = "activation:*:update"
)

func init() {
	scopeMeta[ScopeActivationRead] = scopeMetaEntry{
		name:        "View activation",
		description: "See whether this SAM Desktop install is signed in to Solace ID and has accepted the end user license agreement.",
	}
	scopeMeta[ScopeActivationUpdate] = scopeMetaEntry{
		name:        "Change activation",
		description: "Sign this SAM Desktop install in to Solace ID and accept the end user license agreement.",
	}
}
