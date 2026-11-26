// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import "gopkg.in/yaml.v3"

func marshalYAMLBytes(v any) ([]byte, error) {
	return yaml.Marshal(v)
}
