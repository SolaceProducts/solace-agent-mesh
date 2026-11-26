// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/oauth2/google"
)

const testServiceAccountEmail = "probe@p.iam.gserviceaccount.com"

func testServiceAccountKey(t *testing.T, tokenURL string) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   testServiceAccountEmail,
		"private_key_id": "kid",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(key)
}

func googleServiceAccountKey(t *testing.T) string {
	t.Helper()
	return testServiceAccountKey(t, google.JWTTokenURL)
}

func withKeyField(t *testing.T, key, field, value string) string {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		t.Fatal(err)
	}
	fields[field] = value
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func truncatedPrivateKey(t *testing.T, key string) string {
	t.Helper()
	return withKeyField(t, key, "private_key", keyField(t, key, "private_key")[:200])
}

func keyField(t *testing.T, key, field string) string {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		t.Fatal(err)
	}
	return fields[field]
}

func assertNoKeyMaterial(t *testing.T, text, key string) {
	t.Helper()
	privateKey := keyField(t, key, "private_key")
	body := strings.TrimPrefix(privateKey, "-----BEGIN PRIVATE KEY-----\n")
	for _, secret := range []string{body[:40], "PRIVATE KEY", keyField(t, key, "client_email"), keyField(t, key, "token_uri")} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("text leaks key material %q: %s", secret, text)
		}
	}
}

func TestServiceAccountKeyProblem(t *testing.T) {
	valid := googleServiceAccountKey(t)
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "google token_uri", key: valid, want: ""},
		{name: "legacy google token_uri", key: withKeyField(t, valid, "token_uri", "https://accounts.google.com/o/oauth2/token"), want: ""},
		{name: "no token_uri", key: withKeyField(t, valid, "token_uri", ""), want: ""},
		{name: "not json", key: "not-a-key", want: keyProblemNotServiceAccount},
		{name: "not a service account", key: withKeyField(t, valid, "type", "authorized_user"), want: keyProblemNotServiceAccount},
		{name: "truncated private key", key: truncatedPrivateKey(t, valid), want: keyProblemDamagedPrivateKey},
		{name: "foreign token_uri", key: withKeyField(t, valid, "token_uri", "https://attacker.example/token"), want: keyProblemForeignTokenURI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServiceAccountKeyProblem(tc.key); got != tc.want {
				t.Fatalf("ServiceAccountKeyProblem = %q, want %q", got, tc.want)
			}
		})
	}
}
