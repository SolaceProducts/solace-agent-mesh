// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"fmt"
	"os"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func TestStreamSmoke(t *testing.T) {
	token := os.Getenv("SLACK_SMOKE_BOT_TOKEN")
	channel := os.Getenv("SLACK_SMOKE_CHANNEL")
	if token == "" || channel == "" {
		t.Skip("set SLACK_SMOKE_BOT_TOKEN and SLACK_SMOKE_CHANNEL to run the streaming smoke test")
	}

	api := goslack.New(token)

	startOpts := []goslack.MsgOption{goslack.MsgOptionTaskDisplayMode(goslack.TaskDisplayModeTimeline)}
	if thread := os.Getenv("SLACK_SMOKE_THREAD"); thread != "" {
		startOpts = append(startOpts, goslack.MsgOptionTS(thread))
	}
	if user := os.Getenv("SLACK_SMOKE_USER"); user != "" {
		startOpts = append(startOpts, goslack.MsgOptionRecipientUserID(user))
	}
	if team := os.Getenv("SLACK_SMOKE_TEAM"); team != "" {
		startOpts = append(startOpts, goslack.MsgOptionRecipientTeamID(team))
	}

	_, ts, err := api.StartStream(channel, startOpts...)
	if err != nil {
		t.Fatalf("StartStream failed (does this app/workspace allow streaming?): %v", err)
	}
	t.Logf("started stream ts=%s", ts)

	steps := []func() error{
		func() error {
			c := goslack.NewTaskUpdateChunk("step1", "Searching the catalog")
			c.Status = goslack.TaskCardStatusInProgress
			c.Details = "Querying the product index for matching items"
			_, _, e := api.AppendStream(channel, ts, goslack.MsgOptionChunks(c))
			return e
		},
		func() error {
			c := goslack.NewTaskUpdateChunk("step1", "Searching the catalog")
			c.Status = goslack.TaskCardStatusComplete
			c.Output = "Found 5 matching products"
			_, _, e := api.AppendStream(channel, ts, goslack.MsgOptionChunks(c))
			return e
		},
		func() error {
			c := goslack.NewTaskUpdateChunk("step2", "Summarizing results")
			c.Status = goslack.TaskCardStatusInProgress
			_, _, e := api.AppendStream(channel, ts, goslack.MsgOptionChunks(c))
			return e
		},
		func() error {
			c := goslack.NewTaskUpdateChunk("step2", "Summarizing results")
			c.Status = goslack.TaskCardStatusComplete
			_, _, e := api.AppendStream(channel, ts,
				goslack.MsgOptionChunks(c, goslack.NewMarkdownTextChunk("Here are the *top 5 products* I found for you.")))
			return e
		},
		func() error {
			blocks, ok := BuildMessageBlocks("Summary:\n\n| Feature | Status |\n|---|---|\n| Streaming | ✅ working |\n| Tables | testing |\n")
			if !ok {
				return fmt.Errorf("BuildMessageBlocks returned no blocks for the test table")
			}
			_, _, e := api.AppendStream(channel, ts, goslack.MsgOptionChunks(goslack.NewBlocksChunk(blocks...)))
			return e
		},
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("AppendStream step %d failed: %v", i+1, err)
		}
		time.Sleep(700 * time.Millisecond)
	}

	if _, _, err := api.StopStream(channel, ts); err != nil {
		t.Fatalf("StopStream failed: %v", err)
	}
	t.Logf("stream stopped ts=%s — confirm the timeline rendered in Slack", ts)
}
