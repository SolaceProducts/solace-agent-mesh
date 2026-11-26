// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics && solace_id

package clianalytics

import (
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func acceptedAccount(storeURL string) productanalytics.AccountSource {
	home, err := settings.ResolveHomeDir()
	if err != nil {
		return nil
	}
	homeData, err := absDir(filepath.Join(home, "data"))
	if err != nil || storeURL != databaseURL(homeData) {
		return nil
	}
	store := activation.NewStore(home)
	return func() (productanalytics.Account, bool) {
		record, err := store.Load()
		if err != nil || record.State() != activation.StateActive {
			return productanalytics.Account{}, false
		}
		return productanalytics.Account{Subject: record.Account.Subject, Email: record.Account.Email}, true
	}
}
