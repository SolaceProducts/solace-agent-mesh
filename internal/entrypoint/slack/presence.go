// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

type slackProactiveAddress struct {
	TeamID    string `json:"team_id"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id,omitempty"`
}
