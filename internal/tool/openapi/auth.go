// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/base64"
	"net/http"
)

func applyAuth(req *http.Request, auth *AuthConfig, headers map[string]string) {
	if auth != nil {
		switch auth.Type {
		case AuthTypeBearer:
			if auth.Token != "" {
				req.Header.Set("Authorization", "Bearer "+auth.Token)
			}
		case AuthTypeAPIKey:
			applyAPIKey(req, auth)
		case AuthTypeBasic:
			if auth.Username != "" {
				encoded := base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))
				req.Header.Set("Authorization", "Basic "+encoded)
			}
		}
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

func applyAPIKey(req *http.Request, auth *AuthConfig) {
	if auth.APIKey == "" {
		return
	}
	name := auth.HeaderName
	if name == "" {
		name = "X-API-Key"
	}
	switch auth.In {
	case "query":
		q := req.URL.Query()
		q.Set(name, auth.APIKey)
		req.URL.RawQuery = q.Encode()
	case "cookie":
		req.AddCookie(&http.Cookie{Name: name, Value: auth.APIKey})
	default:
		req.Header.Set(name, auth.APIKey)
	}
}
