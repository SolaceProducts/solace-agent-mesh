// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"time"
)

type Timestamp int64

func ToTimestamp(ms int64) Timestamp { return Timestamp(ms) }

func (t Timestamp) MarshalJSON() ([]byte, error) {
	ms := int64(t)
	if ms == 0 {
		return json.Marshal("")
	}
	ts := time.UnixMilli(ms).UTC()
	return json.Marshal(ts.Format("2006-01-02T15:04:05.000000+00:00"))
}

func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*t = Timestamp(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("timestamp: expected int64 or string, got %s", string(data))
	}
	if s == "" {
		*t = 0
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04:05.000000+00:00", s)
		if err != nil {
			return fmt.Errorf("timestamp: cannot parse %q: %w", s, err)
		}
	}
	*t = Timestamp(parsed.UnixMilli())
	return nil
}
