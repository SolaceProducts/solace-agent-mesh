// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const usageSummaryGolden = "testdata/usage_summary.json"

var updateUsageSummary = flag.Bool("update-usage-summary", false, "rewrite "+usageSummaryGolden)

var usageDayRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func TestUsageSummary_MatchesTheReviewedBody(t *testing.T) {
	api := newUsageAPI(t)
	api.file(1_234, nil)
	today := time.Now().UTC().Format(time.DateOnly)

	var body any
	require.NoError(t, json.Unmarshal(api.callRaw(http.MethodGet, "/usage/summary?startDate="+today+"&endDate="+today, nil), &body))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(maskClock(body)))
	got := buf.Bytes()

	if *updateUsageSummary {
		require.NoError(t, os.WriteFile(usageSummaryGolden, got, 0o644))
	}
	want, err := os.ReadFile(usageSummaryGolden)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

func maskClock(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = maskClock(e)
		}
	case []any:
		for i, e := range v {
			v[i] = maskClock(e)
		}
	case string:
		switch {
		case usageInstantRE.MatchString(`"` + v + `"`):
			return "<instant>"
		case usageDayRE.MatchString(v):
			return "<day>"
		}
	}
	return v
}
