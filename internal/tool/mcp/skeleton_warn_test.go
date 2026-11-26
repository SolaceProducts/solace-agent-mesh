// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func skeletonOverflow(canary string) []ContentItem {
	body := map[string]any{"isLast": false}
	for i := range 6 {
		body[fmt.Sprintf("pageToken%d", i)] = canary + strings.Repeat("x", 900)
	}
	b, _ := json.Marshal(body)
	return []ContentItem{jsonItem("x.json", string(b))}
}

func TestBuildResponseDigest_WarnsOnceWhenTheSkeletonIsTooLarge(t *testing.T) {
	const canary = "PAGE-TOKEN-CANARY"
	h, shared := newCaptureLogger()
	cfg := DefaultHandlerConfig()
	cfg.Logger = slog.New(h)

	if d := buildResponseDigest(skeletonOverflow(canary), cfg, "jira_search"); d["response_skeleton"] != nil {
		t.Errorf("an oversized skeleton was kept: %v", d["response_skeleton"])
	}
	records := shared.snapshot()
	if len(records) != 1 || records[0].Level != slog.LevelWarn || records[0].Attrs["toolName"] != "jira_search" {
		t.Fatalf("records = %+v, want one WARN naming the tool", records)
	}
	if logged := fmt.Sprint(records[0]); strings.Contains(logged, canary) {
		t.Errorf("the WARN carries the response: %s", logged)
	}

	cfg.Logger = nil
	if d := buildResponseDigest(skeletonOverflow(canary), cfg, "jira_search"); d["response_skeleton"] != nil {
		t.Errorf("an oversized skeleton was kept with no logger: %v", d["response_skeleton"])
	}
}

func TestBuildSkeleton_FitsThreeTokensAtTheirCap(t *testing.T) {
	token := verbatimOfMarshaledLen(maxVerbatimValueBytes)
	body := map[string]any{"isLast": false, "total": 250, "nextPageToken": token, "prevPageToken": token, "cursor": token}
	if sk, ok := buildSkeleton(body, 2, 256, 50).(map[string]any); !ok || sk["cursor"] != token {
		t.Errorf("skeleton of three tokens at the cap = %v, want it kept with each token whole", sk)
	}
}
