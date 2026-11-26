// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package samcli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

type ingestSpy struct {
	mu       sync.Mutex
	calls    int
	commands []string
}

func (s *ingestSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []struct {
			Properties map[string]any `json:"properties"`
		} `json:"events"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	s.calls++
	for _, ev := range body.Events {
		if command, ok := ev.Properties[productanalytics.PropCommand].(string); ok {
			s.commands = append(s.commands, command)
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (s *ingestSpy) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([]string(nil), s.commands...)
}

func TestARunThatAddressedAGatewayIsReportedThere(t *testing.T) {
	requireProductAnalytics(t)
	isolateGatewayRecording(t)
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)
	t.Setenv("SAM_DATA_DIR", "")
	grantConsentAt(t, home)

	spy := &ingestSpy{}
	srv := httptest.NewServer(spy)
	t.Cleanup(srv.Close)

	local := &countingSink{}
	root := &cobra.Command{Use: "sam", SilenceUsage: true, SilenceErrors: true}
	tracked := clianalytics.TrackCommand(&cobra.Command{
		Use: "send",
		RunE: func(*cobra.Command, []string) error {
			recordAddressedGateway(srv.URL, "", "gateway-token", true, true)
			return nil
		},
	})
	root.AddCommand(tracked)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"send"})

	if err := executeTree(root, clianalytics.Options{Sink: local}); err != nil {
		t.Fatalf("executeTree: %v", err)
	}

	calls, commands := spy.snapshot()
	if calls != 1 {
		t.Fatalf("gateway received %d reports, want 1", calls)
	}
	if len(commands) != 1 || commands[0] != "sam send" {
		t.Errorf("reported commands = %v, want [sam send]", commands)
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none: the run belongs to the gateway's install", len(events))
	}
}

func TestARunThatAddressedNoGatewayStaysLocal(t *testing.T) {
	requireProductAnalytics(t)
	isolateGatewayRecording(t)
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)
	t.Setenv("SAM_DATA_DIR", "")
	grantConsentAt(t, home)

	spy := &ingestSpy{}
	srv := httptest.NewServer(spy)
	t.Cleanup(srv.Close)

	local := &countingSink{}
	root := &cobra.Command{Use: "sam", SilenceUsage: true, SilenceErrors: true}
	tracked := clianalytics.TrackCommand(&cobra.Command{
		Use:  "init",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.AddCommand(tracked)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"init"})

	if err := executeTree(root, clianalytics.Options{Sink: local}); err != nil {
		t.Fatalf("executeTree: %v", err)
	}

	if calls, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("gateway received %d reports, want none for a local run", calls)
	}
	if events := local.all(); len(events) != 1 {
		t.Fatalf("local sink delivered %d events, want 1", len(events))
	}
}
