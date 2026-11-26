// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const RESTTimestampLayout = apicontract.InstantLayout

type RESTTimestamp int64

func restTimestamp(ms int64) RESTTimestamp { return RESTTimestamp(ms) }

func nullableRESTTimestamp(ms *int64) *RESTTimestamp {
	if ms == nil {
		return nil
	}
	return new(RESTTimestamp(*ms))
}

func (t RESTTimestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t RESTTimestamp) String() string {
	return time.UnixMilli(int64(t)).UTC().Format(RESTTimestampLayout)
}

func (t *RESTTimestamp) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("timestamp: expected an ISO 8601 string, got %s", string(data))
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return fmt.Errorf("timestamp: cannot parse %q as ISO 8601", raw)
	}
	*t = RESTTimestamp(parsed.UTC().UnixMilli())
	return nil
}
