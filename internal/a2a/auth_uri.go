// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
)

func ToolAuthGrantRef(state string) string {
	if state == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func StateFromAuthURI(authURI string) (string, error) {
	u, err := url.Parse(authURI)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return "", fmt.Errorf("parse auth URI: %w", urlErr.Err)
		}
		return "", errors.New("parse auth URI: malformed")
	}
	state := u.Query().Get("state")
	if state == "" {
		return "", errors.New("auth URI has no state parameter")
	}
	return state, nil
}
