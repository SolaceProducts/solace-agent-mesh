// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

func seedAuthCache(t *testing.T, target, platformURL string) {
	t.Helper()
	rerouteHome(t)
	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	if err := store.Save(target, tokencache.Cached{
		PlatformURL:    platformURL,
		SAMAccessToken: "dummy-token",
		ExpiresAt:      time.Now().Add(time.Hour),
		ObtainedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("save cache: %v", err)
	}
}

func TestAuthListJSON(t *testing.T) {
	seedAuthCache(t, "dev", "https://platform.example.com")

	out, _, err := runRoot(t, "auth", "list", "--format", "json")
	if err != nil {
		t.Fatalf("auth list --format json: %v", err)
	}
	var items []map[string]any
	if jerr := json.Unmarshal([]byte(out), &items); jerr != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", jerr, out)
	}
	found := false
	for _, it := range items {
		if it["target"] == "dev" {
			found = true
			if it["platform"] != "https://platform.example.com" {
				t.Errorf("platform = %v; want https://platform.example.com", it["platform"])
			}
		}
	}
	if !found {
		t.Errorf("JSON list missing target dev: %s", out)
	}
}

func TestAuthStatusJSON(t *testing.T) {
	seedAuthCache(t, "dev", "https://platform.example.com")

	out, _, err := runRoot(t, "auth", "status", "--target", "dev",
		"--url", "https://platform.example.com", "--format", "json")
	if err != nil {
		t.Fatalf("auth status --format json: %v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", jerr, out)
	}
	if obj["loggedIn"] != true {
		t.Errorf("loggedIn = %v; want true", obj["loggedIn"])
	}
	if obj["platform"] != "https://platform.example.com" {
		t.Errorf("platform = %v; want https://platform.example.com", obj["platform"])
	}
}

func TestAuthListJSON_InvalidFormatRejected(t *testing.T) {
	rerouteHome(t)
	_, _, err := runRoot(t, "auth", "list", "--format", "yaml")
	if err == nil {
		t.Fatal("expected error for invalid --format")
	}
}
