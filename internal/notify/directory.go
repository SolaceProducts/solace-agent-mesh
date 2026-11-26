// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"strings"
)

type Channel struct {
	Type   string
	Config map[string]any
}

type Directory interface {
	ChannelsFor(ctx context.Context, userID string) ([]Channel, error)
}

type configDirectory struct {
	byUser map[string][]Channel
}

func newConfigDirectory(entries map[string][]Channel) *configDirectory {
	byUser := make(map[string][]Channel, len(entries))
	for user, chs := range entries {
		byUser[user] = chs
	}
	return &configDirectory{byUser: byUser}
}

func (d *configDirectory) ChannelsFor(_ context.Context, userID string) ([]Channel, error) {
	return d.byUser[userID], nil
}

func filterByTypes(channels []Channel, types []string) []Channel {
	if len(types) == 0 {
		return channels
	}
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[strings.ToLower(t)] = true
	}
	out := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		if want[strings.ToLower(ch.Type)] {
			out = append(out, ch)
		}
	}
	return out
}
