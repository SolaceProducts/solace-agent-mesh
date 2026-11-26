// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

const (
	ProviderSentinelEnterprise = "enterprise"
	ProviderSentinelGeneric    = "generic"
)

func IsSentinelProvider(name string) bool {
	return name == ProviderSentinelEnterprise || name == ProviderSentinelGeneric
}

func ProviderUnsetOrSentinel(name string) bool {
	return name == "" || IsSentinelProvider(name)
}

func ProviderCannotMatch(provider, configured string) bool {
	return configured != "" && ProviderUnsetOrSentinel(provider) && provider != configured
}
