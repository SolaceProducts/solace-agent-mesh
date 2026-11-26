// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"slices"

	"golang.org/x/oauth2/google"
)

var googleTokenURIs = []string{google.JWTTokenURL, "https://accounts.google.com/o/oauth2/token"}

const (
	keyProblemNotServiceAccount = "Service account key must be the JSON key file downloaded for a Google Cloud service account."
	keyProblemDamagedPrivateKey = "Service account key's private_key is damaged. Paste the whole JSON key file, or download a fresh one."
	keyProblemForeignTokenURI   = "Service account key names a token endpoint that is not Google's. Download a fresh key from the Google Cloud console."
)

func ServiceAccountKeyProblem(key string) string {
	return serviceAccountKeyProblem(key, googleTokenURIs)
}

func serviceAccountKeyProblem(key string, allowedTokenURIs []string) string {
	var shape struct {
		Type       string `json:"type"`
		TokenURI   string `json:"token_uri"`
		PrivateKey string `json:"private_key"`
	}
	if json.Unmarshal([]byte(key), &shape) != nil || shape.Type != "service_account" {
		return keyProblemNotServiceAccount
	}
	if !parsesAsPrivateKey(shape.PrivateKey) {
		return keyProblemDamagedPrivateKey
	}
	if shape.TokenURI != "" && !slices.Contains(allowedTokenURIs, shape.TokenURI) {
		return keyProblemForeignTokenURI
	}
	return ""
}

func parsesAsPrivateKey(encoded string) bool {
	der := []byte(encoded)
	if block, _ := pem.Decode(der); block != nil {
		der = block.Bytes
	}
	if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return true
	}
	_, err := x509.ParsePKCS1PrivateKey(der)
	return err == nil
}
