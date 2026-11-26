// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics && !solace_id

package clianalytics

import "github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"

func acceptedAccount(string) productanalytics.AccountSource {
	return nil
}
