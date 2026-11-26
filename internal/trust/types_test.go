// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrustCard_PublicURL_RoundTrip(t *testing.T) {
	in := TrustCard{
		ComponentType: "gateway",
		ComponentID:   "mcp-a",
		Namespace:     "default",
		PublicURL:     "https://example.com/gw/mcp-a",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out TrustCard
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.PublicURL != in.PublicURL {
		t.Fatalf("PublicURL: want %q, got %q", in.PublicURL, out.PublicURL)
	}
}

func TestTrustCard_PublicURL_OmitsWhenEmpty(t *testing.T) {
	in := TrustCard{ComponentType: "gateway", ComponentID: "mcp-a"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); strings.Contains(got, "public_url") {
		t.Fatalf("expected public_url to be omitted when empty; got %s", got)
	}
}
