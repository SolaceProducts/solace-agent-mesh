// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestTheInventoryReportsThePublishersHost(t *testing.T) {
	s := relayService()
	b := &BrokerIngest{svc: s, surface: SurfaceServer, accept: brokerIngestEvents, logger: slog.New(slog.DiscardHandler)}

	payload, err := json.Marshal(IngestEnvelope{
		Platform: &IngestPlatform{OS: "windows", Arch: "arm64", AppVersion: "1.2.3"},
		Events:   []IngestEvent{{Name: EventInstallInventory, Properties: map[string]any{PropAgentCount: 3}}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b.handle(context.Background(), payload)

	buf := buffered(s)
	if len(buf) != 1 {
		t.Fatalf("buffered = %d, want 1", len(buf))
	}
	ev := buf[0]
	if got := ev.Properties[propOS]; got != "windows" {
		t.Errorf("os = %v, want the publisher's %q", got, "windows")
	}
	if got := ev.Properties[propArch]; got != "arm64" {
		t.Errorf("arch = %v, want the publisher's %q", got, "arm64")
	}
	if got := ev.Properties[propAppVersion]; got != "1.2.3" {
		t.Errorf("appVersion = %v, want the publisher's %q", got, "1.2.3")
	}
}

func TestAForgedPublisherPlatformIsRefused(t *testing.T) {
	reduced := publisherPlatform(&IngestPlatform{
		OS:         "Ubuntu 22.04",
		Arch:       "x86_64",
		AppVersion: "built by alice@example.com",
	})
	if reduced != (ClientPlatform{}) {
		t.Errorf("reduced to %+v, want every unrecognized field emptied", reduced)
	}
}

func TestAnOversizedPayloadIsRefusedBeforeDecoding(t *testing.T) {
	s := relayService()
	b := &BrokerIngest{svc: s, surface: SurfaceServer, accept: brokerIngestEvents, logger: slog.New(slog.DiscardHandler)}

	huge := `{"events":[{"event":"install:inventory","properties":{"agentCount":1,"pad":"` +
		strings.Repeat("a", maxIngestEnvelopeBytes) + `"}}]}`
	b.handle(context.Background(), []byte(huge))

	if n := len(buffered(s)); n != 0 {
		t.Errorf("buffered %d events, want an oversized payload refused", n)
	}
}
