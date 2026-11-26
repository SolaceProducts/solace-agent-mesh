// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"bytes"
	"encoding/json"
	"time"
)

func AppendLanded(stored []Message, storedBefore int, batch []Message) bool {
	if len(batch) == 0 || storedBefore < 0 {
		return false
	}
	for start := storedBefore; start+len(batch) <= len(stored); start++ {
		if SameMessages(stored[start:start+len(batch)], batch) {
			return true
		}
	}
	return false
}

func SameMessages(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameStoredContent(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameStoredContent(a, b Message) bool {
	a.Timestamp, b.Timestamp = time.Time{}, time.Time{}
	a.TaskID, b.TaskID = "", ""
	a.RunFacts, b.RunFacts = nil, nil
	aJSON, errA := json.Marshal(a)
	bJSON, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(aJSON, bJSON)
}
