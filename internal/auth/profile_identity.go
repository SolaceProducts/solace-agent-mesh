// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "encoding/json"

func ReconcileProfileIdentity(identity *Identity, a2aUserConfig string) string {
	if identity == nil || a2aUserConfig == "" {
		return a2aUserConfig
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(a2aUserConfig), &cfg); err != nil {
		return a2aUserConfig
	}
	raw, ok := cfg["user_profile"]
	if !ok {
		return a2aUserConfig
	}
	var profile map[string]any
	if err := json.Unmarshal(raw, &profile); err != nil || profile == nil {
		return a2aUserConfig
	}

	caller := CallerFromIdentity(identity)
	profile["id"] = caller.Sub
	if caller.Email != "" {
		profile["work_email"] = caller.Email
	} else {
		delete(profile, "work_email")
	}

	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return a2aUserConfig
	}
	cfg["user_profile"] = profileJSON
	out, err := json.Marshal(cfg)
	if err != nil {
		return a2aUserConfig
	}
	return string(out)
}
