// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package features

import _ "embed"

//go:embed features.yaml
var featuresYAML []byte

func LoadFlags(r *Registry) error {
	return r.LoadFromYAML(featuresYAML)
}
