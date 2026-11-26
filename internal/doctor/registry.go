// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

func BuildChecksForContext(c Context) []Check {
	common := []Check{
		NewBrokerCheck(),
		NewLLMCheck(),
		NewDatabaseTCPCheck(),
		NewDatabaseAuthCheck(),
		NewObjectStorageTCPCheck(),
		NewObjectStorageAuthCheck(),
		NewTLSCheck(),
		NewOIDCCheck(),
	}
	if c == ContextHelm {
		return common
	}
	return append(common, NewRuntimeVersionCheck(), NewPortAvailabilityCheck())
}
