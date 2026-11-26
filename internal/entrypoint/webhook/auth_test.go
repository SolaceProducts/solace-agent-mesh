// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

func hmacSum(secret, body string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

func signHex(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyToken(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *TokenAuthConfig
		header  map[string]string
		query   string
		wantErr bool
	}{
		{name: "bearer ok", cfg: &TokenAuthConfig{Location: "bearer", Value: "s3cr3t-value"}, header: map[string]string{"Authorization": "Bearer s3cr3t-value"}},
		{name: "bearer wrong", cfg: &TokenAuthConfig{Location: "bearer", Value: "s3cr3t-value"}, header: map[string]string{"Authorization": "Bearer nope"}, wantErr: true},
		{name: "bearer missing", cfg: &TokenAuthConfig{Location: "bearer", Value: "s3cr3t-value"}, wantErr: true},
		{name: "empty location defaults to bearer", cfg: &TokenAuthConfig{Value: "s3cr3t-value"}, header: map[string]string{"Authorization": "Bearer s3cr3t-value"}},
		{name: "header ok", cfg: &TokenAuthConfig{Location: "header", Name: "X-API-Key", Value: "keyvalue1"}, header: map[string]string{"X-API-Key": "keyvalue1"}},
		{name: "header wrong", cfg: &TokenAuthConfig{Location: "header", Name: "X-API-Key", Value: "keyvalue1"}, header: map[string]string{"X-API-Key": "x"}, wantErr: true},
		{name: "query ok", cfg: &TokenAuthConfig{Location: "query", Name: "token", Value: "queryvalue"}, query: "?token=queryvalue"},
		{name: "query wrong", cfg: &TokenAuthConfig{Location: "query", Name: "token", Value: "queryvalue"}, query: "?token=z", wantErr: true},
		{name: "unknown location", cfg: &TokenAuthConfig{Location: "cookie", Value: "queryvalue"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/h"+tt.query, nil)
			for k, v := range tt.header {
				req.Header.Set(k, v)
			}
			err := verifyToken(tt.cfg, req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyToken_BearerCaseInsensitive(t *testing.T) {
	cfg := &TokenAuthConfig{Location: "bearer", Value: "tok-longenough"}
	for _, h := range []string{"Bearer tok-longenough", "bearer tok-longenough", "BEARER tok-longenough", "Bearer  tok-longenough"} {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("Authorization", h)
		if err := verifyToken(cfg, req); err != nil {
			t.Errorf("Authorization %q should pass: %v", h, err)
		}
	}
	req := httptest.NewRequest("POST", "/h", nil)
	req.Header.Set("Authorization", "tok-longenough")
	if err := verifyToken(cfg, req); err == nil {
		t.Error("bare token without Bearer scheme should fail")
	}
}

func TestVerifyHMAC(t *testing.T) {
	body := `{"a":1}`

	t.Run("hex ok", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", "sha256="+signHex("secret-key-long", body))
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Prefix: "sha256=", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err != nil {
			t.Fatalf("verifyHMAC: %v", err)
		}
	})
	t.Run("wrong secret", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", signHex("secret-key-long", body))
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "other-secret-long", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err == nil {
			t.Fatal("want error for wrong secret")
		}
	})
	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/h", nil)
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err == nil {
			t.Fatal("want error for missing header")
		}
	})
	t.Run("prefix mismatch rejected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", signHex("secret-key-long", body))
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Prefix: "sha256=", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err == nil {
			t.Fatal("signature without the configured prefix must be rejected")
		}
	})
	t.Run("bad hex rejected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", "nothex!!")
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err == nil {
			t.Fatal("undecodable signature must be rejected")
		}
	})
	t.Run("base64", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte("secret-key-long"))
		mac.Write([]byte(body))
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Encoding: "base64"}
		if err := verifyHMAC(cfg, req, []byte(body)); err != nil {
			t.Fatalf("verifyHMAC base64: %v", err)
		}
	})
	t.Run("sha512 and cross-algo rejection", func(t *testing.T) {
		mac := hmac.New(sha512.New, []byte("secret-key-long"))
		mac.Write([]byte(body))
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", hex.EncodeToString(mac.Sum(nil)))
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha512", Secret: "secret-key-long", Encoding: "hex"}
		if err := verifyHMAC(cfg, req, []byte(body)); err != nil {
			t.Fatalf("verifyHMAC sha512: %v", err)
		}
		cfg256 := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: "secret-key-long", Encoding: "hex"}
		if err := verifyHMAC(cfg256, req, []byte(body)); err == nil {
			t.Fatal("sha256 verifier should reject a sha512 signature")
		}
	})
}

func TestVerifyHMAC_Base64Variants(t *testing.T) {
	secret := "secret-key-long"
	body := `{"a":1}`
	raw := hmacSum(secret, body)

	encodings := []struct {
		name    string
		encoded string
	}{
		{"base64", base64.StdEncoding.EncodeToString(raw)},
		{"base64url", base64.URLEncoding.EncodeToString(raw)},
		{"base64raw", base64.RawStdEncoding.EncodeToString(raw)},
		{"base64rawurl", base64.RawURLEncoding.EncodeToString(raw)},
	}

	for _, enc := range encodings {
		t.Run("valid/"+enc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/h", nil)
			req.Header.Set("X-Sig", enc.encoded)
			cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: secret, Encoding: enc.name}
			if err := verifyHMAC(cfg, req, []byte(body)); err != nil {
				t.Fatalf("verifyHMAC %s: %v", enc.name, err)
			}
		})
	}

	t.Run("cross-encoding rejected", func(t *testing.T) {
		urlEncoded := base64.URLEncoding.EncodeToString(raw)
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set("X-Sig", urlEncoded)
		cfg := &HMACAuthConfig{Header: "X-Sig", Algo: "sha256", Secret: secret, Encoding: "base64"}
		if urlEncoded == base64.StdEncoding.EncodeToString(raw) {
			t.Fatal("test invariant broken: base64url and base64 encodings must differ for this digest")
		}
		if err := verifyHMAC(cfg, req, []byte(body)); err == nil {
			t.Fatal("base64url-encoded sig should fail base64 (StdEncoding) verifier")
		}
	})
}
