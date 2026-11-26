// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

var SupportedModelAuthTypes = []string{
	"none",
	"apikey",
	"oauth2",
	"bearer",
	"basic",
	"aws_iam",
	"gcp_service_account",
	"azure_workload_identity",
	"oauth2_delegated",
}
