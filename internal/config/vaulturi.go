// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type VaultFetcher interface {
	Fetch(path string) (map[string]any, error)
}

type VaultRef struct {
	URI   string
	Path  string
	Field string
}

type VaultRefError struct {
	Ref VaultRef
	Err error
}

func (e VaultRefError) Error() string {
	return fmt.Sprintf("%s: %v", e.Ref.URI, e.Err)
}

func (e VaultRefError) Unwrap() error { return e.Err }

var vaultURIPattern = regexp.MustCompile(`vault://[^\s"',}\]]+`)

func VaultRefTokens(text string) []string {
	return vaultURIPattern.FindAllString(text, -1)
}

func FirstVaultRefWithEmptySegment(text string) (string, bool) {
	for _, token := range VaultRefTokens(text) {
		path := strings.TrimPrefix(token, "vault://")
		if i := strings.LastIndex(path, "#"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			continue
		}
		if slices.Contains(strings.Split(path, "/"), "") {
			return token, true
		}
	}
	return "", false
}

func ResolveVaultURIs(text string, fetcher VaultFetcher) (string, []VaultRefError) {
	cache := map[string]map[string]any{}
	var errs []VaultRefError
	expanded := vaultURIPattern.ReplaceAllStringFunc(text, func(match string) string {
		ref, parseErr := parseVaultURI(match)
		if parseErr != nil {
			errs = append(errs, VaultRefError{Ref: VaultRef{URI: match}, Err: parseErr})
			return match
		}
		if fetcher == nil {
			errs = append(errs, VaultRefError{Ref: ref, Err: fmt.Errorf("vault fetcher not configured (set VAULT_ADDR and run `vault login`)")})
			return match
		}
		secret, ok := cache[ref.Path]
		if !ok {
			s, err := fetcher.Fetch(ref.Path)
			if err != nil {
				errs = append(errs, VaultRefError{Ref: ref, Err: err})
				return match
			}
			secret = s
			cache[ref.Path] = s
		}
		val, ok := extractField(secret, ref.Field)
		if !ok {
			errs = append(errs, VaultRefError{Ref: ref, Err: fmt.Errorf("field %q not found at path %s", ref.Field, ref.Path)})
			return match
		}
		if val == "" {
			errs = append(errs, VaultRefError{Ref: ref, Err: fmt.Errorf("field %q at path %s is empty", ref.Field, ref.Path)})
			return match
		}
		return val
	})
	return expanded, dedupVaultErrors(errs)
}

func parseVaultURI(uri string) (VaultRef, error) {
	after := strings.TrimPrefix(uri, "vault://")
	hash := strings.LastIndex(after, "#")
	if hash <= 0 || hash == len(after)-1 {
		return VaultRef{}, fmt.Errorf("malformed vault URI %q: expected vault://<path>#<field>", uri)
	}
	return VaultRef{URI: uri, Path: after[:hash], Field: after[hash+1:]}, nil
}

func extractField(secret map[string]any, field string) (string, bool) {
	if dataLayer, ok := secret["data"].(map[string]any); ok {
		if inner, ok := dataLayer["data"].(map[string]any); ok {
			if v, found := stringFromAny(inner[field]); found {
				return v, true
			}
		}
		if v, found := stringFromAny(dataLayer[field]); found {
			return v, true
		}
	}
	return stringFromAny(secret[field])
}

func stringFromAny(raw any) (string, bool) {
	if raw == nil {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		return fmt.Sprint(v), true
	}
}

func dedupVaultErrors(errs []VaultRefError) []VaultRefError {
	if len(errs) <= 1 {
		return errs
	}
	seen := map[string]struct{}{}
	out := make([]VaultRefError, 0, len(errs))
	for _, e := range errs {
		key := e.Ref.URI + "\x00" + e.Err.Error()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b VaultRefError) int {
		if a.Ref.URI != b.Ref.URI {
			return strings.Compare(a.Ref.URI, b.Ref.URI)
		}
		return strings.Compare(a.Err.Error(), b.Err.Error())
	})
	return out
}

type DefaultVaultFetcher struct {
	Addr   string
	Token  string
	Client *http.Client
}

func NewDefaultVaultFetcher() (*DefaultVaultFetcher, error) {
	addr := strings.TrimSpace(os.Getenv("VAULT_ADDR"))
	if addr == "" {
		return nil, fmt.Errorf("VAULT_ADDR is not set — point it at your Vault server (e.g. https://vault.example.com:8200)")
	}
	token, err := DiscoverVaultToken()
	if err != nil {
		return nil, err
	}
	return &DefaultVaultFetcher{
		Addr:   addr,
		Token:  token,
		Client: &http.Client{Timeout: DefaultVaultTimeout},
	}, nil
}

const DefaultVaultTimeout = 30 * time.Second

const AgentVaultTimeout = 5 * time.Second

func DiscoverVaultToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("VAULT_TOKEN")); t != "" {
		return t, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate $HOME for ~/.vault-token: %w", err)
	}
	path := filepath.Join(home, ".vault-token")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no vault token: $VAULT_TOKEN empty and %s unreadable (%w) — set VAULT_TOKEN or provide the file (`vault login` writes it)", path, err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("%s is empty — set VAULT_TOKEN or write a token to that file", path)
	}
	return t, nil
}

func (f *DefaultVaultFetcher) Fetch(path string) (map[string]any, error) {
	url := strings.TrimRight(f.Addr, "/") + "/v1/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("vault request build: %w", err)
	}
	req.Header.Set("X-Vault-Token", f.Token)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault GET %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("vault GET %s: parse JSON: %w", url, err)
	}
	return doc, nil
}

type EnvVaultFetcher struct {
	Timeout time.Duration
}

func (e EnvVaultFetcher) Fetch(path string) (map[string]any, error) {
	f, err := NewDefaultVaultFetcher()
	if err != nil {
		return nil, fmt.Errorf("vault fetcher from environment: %w", err)
	}
	if e.Timeout > 0 {
		f.Client.Timeout = e.Timeout
	}
	return f.Fetch(path)
}
