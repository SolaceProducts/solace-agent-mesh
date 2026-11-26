// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

const ProductAnalyticsStateSingletonID = "singleton"

type ProductAnalyticsState struct {
	ID            string `db:"id"`
	InstallID     string `db:"install_id"`
	BindingSecret string `db:"binding_secret"`
	ConsentState  string `db:"consent_state"`
	CreatedTime   int64  `db:"created_time"`
	UpdatedTime   int64  `db:"updated_time"`
}
