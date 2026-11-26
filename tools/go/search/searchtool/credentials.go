// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"errors"
	"strings"
)

const endpointOmitted = "[connection details omitted]"

func stripEndpointValues(err error, values ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	stripped := msg
	for _, v := range values {
		if v != "" {
			stripped = strings.ReplaceAll(stripped, v, endpointOmitted)
		}
	}
	if stripped == msg {
		return err
	}
	return errors.New(stripped)
}

func endpointValues(config map[string]any) []string {
	return []string{
		configString(config, "connection_string", ""),
		configString(config, "cloud_id", ""),
	}
}
