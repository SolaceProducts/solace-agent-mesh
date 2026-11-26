// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"encoding/json"
	"log/slog"
)

type statusMsg struct {
	Status string `json:"status"`
}

func processStatusLine(line []byte, cb func(string), logger *slog.Logger) {
	if len(line) == 0 {
		return
	}
	var msg statusMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		logger.Debug("ignoring malformed status line", "line", string(line), "error", err)
		return
	}
	if msg.Status != "" && cb != nil {
		cb(msg.Status)
	}
}
