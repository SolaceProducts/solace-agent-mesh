// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const (
	linkPullEntrypointID = "0190f5ec-0000-7000-8000-00000000e001"
	linkPullEntrypoints  = `[{"id":"0190f5ec-0000-7000-8000-00000000e001","name":"partner-link","entrypointType":"agent_link"}]`
)

func linkAgentPlatform(t *testing.T, linkAgents string) *reconcile.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/linkAgents", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, decodeRows(t, linkAgents))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/entrypoints", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, decodeRows(t, linkPullEntrypoints))
	})
	return newV2PlatformClient(t, mux)
}

func decodeRows(t *testing.T, rows string) []map[string]any {
	t.Helper()
	var decoded []map[string]any
	if err := yaml.Unmarshal([]byte(rows), &decoded); err != nil {
		t.Fatalf("rows %s: %v", rows, err)
	}
	return decoded
}

func TestPullLinkAgents_WritesWhatApplyReadsBackWithoutTheDigestAndWithPlaceholdersForSecrets(t *testing.T) {
	tests := []struct {
		name     string
		row      string
		wantSpec map[string]any
	}{
		{
			name: "an enabled link agent that stays connected",
			row:  `{"id":"a1","name":"Claims Desk","description":"Answers claims questions.","entrypointId":"` + linkPullEntrypointID + `","enabled":true,"peers":[],"shareUserProfile":false,"wake":null}`,
			wantSpec: map[string]any{
				"entrypoint": "partner-link",
			},
		},
		{
			name: "a disabled link agent with every setting",
			row: `{"id":"a1","name":"Claims Desk","entrypointId":"` + linkPullEntrypointID + `","enabled":false,"owner":"alice@example.com","team":"Claims Operations",` +
				`"peers":["policy-desk"],"shareUserProfile":true,"wake":{"url":"https://runtime.example.com/invocations","tokenUrl":"https://idp.example.com/oauth2/token",` +
				`"clientId":"claims-client","clientSecret":"<REDACTED>","scope":null,"headers":{"X-Session":"claims"},"timeoutSeconds":600,"onStart":false}}`,
			wantSpec: map[string]any{
				"entrypoint":       "partner-link",
				"enabled":          false,
				"owner":            "alice@example.com",
				"team":             "Claims Operations",
				"peers":            []any{"policy-desk"},
				"shareUserProfile": true,
				"wake": map[string]any{
					"url":            "https://runtime.example.com/invocations",
					"tokenUrl":       "https://idp.example.com/oauth2/token",
					"clientId":       "claims-client",
					"clientSecret":   "${LINKAGENT_CLAIMS_DESK_CLIENTSECRET}",
					"headers":        map[string]any{"X-Session": "claims"},
					"timeoutSeconds": 600,
					"onStart":        false,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := linkAgentPlatform(t, "["+tt.row+"]")
			res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

			entries, names, err := pullLinkAgents(t.Context(), c, Options{}, res)

			if err != nil {
				t.Fatalf("pullLinkAgents: %v", err)
			}
			if len(entries) != 1 || entries[0].relPath != "linkAgents/Claims Desk.yaml" || len(names) != 1 || names[0] != "Claims Desk" {
				t.Fatalf("entries = %v, names = %v; want the one link agent under linkAgents/", relPaths(entries), names)
			}
			var doc resourceDoc
			if err := yaml.Unmarshal(entries[0].content, &doc); err != nil {
				t.Fatalf("pulled file is not YAML: %v\n%s", err, entries[0].content)
			}
			if doc.Kind != "linkAgent" || doc.Name != "Claims Desk" {
				t.Errorf("pulled header = kind %q name %q, want linkAgent and Claims Desk", doc.Kind, doc.Name)
			}
			if diff := cmp.Diff(tt.wantSpec, doc.Spec); diff != "" {
				t.Errorf("pulled spec (-want +got):\n%s", diff)
			}
			if strings.Contains(string(entries[0].content), redactedSentinel) {
				t.Errorf("the pulled file carries the redaction marker in place of a placeholder:\n%s", entries[0].content)
			}
			for _, w := range res.SecretPlaceholders {
				if w.FieldPath == "tokenSha256" {
					t.Errorf("the pull reports a placeholder for tokenSha256, which a link agent that exists does not need: %+v", w)
				}
			}
		})
	}
}

func TestPullLinkAgents_APlatformThatDoesNotServeThemIsSkippedWithAWarning(t *testing.T) {
	c := newV2PlatformClient(t, http.NewServeMux())
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, names, err := pullLinkAgents(t.Context(), c, Options{}, res)

	if err != nil || len(entries) != 0 || len(names) != 0 {
		t.Fatalf("pullLinkAgents = %v, %v, %v; want nothing and no error", relPaths(entries), names, err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "skipping linkAgents") {
		t.Errorf("warnings = %q, want one saying link agents were skipped", res.Warnings)
	}
}
