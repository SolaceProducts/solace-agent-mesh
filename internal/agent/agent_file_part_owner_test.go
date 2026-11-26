// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func TestParseArtifactFilePartURI_ReadsWhatArtifactParseURIReads(t *testing.T) {
	uris := []string{
		"artifact://app/alice/s/report.txt?version=2",
		"artifact://app/alice/s/report.txt",
		"artifact://app/alice/s/my%20report.txt",
		"artifact://app/bob/alice/s%2Freport.txt?version=0",
		"artifact://app/alice%2Fbob/s/report.txt",
		"artifact://app/%61lice/s/report.txt",
		"artifact://app/alice/s/50%2541.txt",
		"artifact://app/x/y/alice/s/report.txt",
		"artifact://app/alice/s/report.txt?version=abc",
		"artifact://app/alice/s/report.txt?version=-1",
		"artifact://app/alice/report.txt",
		"artifact://report.txt:1",
		"ARTIFACT://app/alice/s/report.txt",
		"artifact:alice/s/report.txt",
	}
	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			ref, _, ok := parseArtifactFilePartURI(uri)
			loc, version, wantOK := artifact.ParseURI(uri)
			if ok != wantOK {
				t.Fatalf("agent reads a location = %v, artifact.ParseURI = %v", ok, wantOK)
			}
			if !ok {
				return
			}
			got := artifact.Locator{AppName: ref.appName, UserID: ref.userID, SessionID: ref.sessionID, Filename: ref.filename}
			if got != loc {
				t.Errorf("agent reads %+v, artifact.ParseURI reads %+v", got, loc)
			}
			if (ref.version == nil) != (version == nil) || (version != nil && *ref.version != *version) {
				t.Errorf("agent reads version %v, artifact.ParseURI reads %v", ref.version, version)
			}
		})
	}
}

func TestProcessFileParts_CopiesFromTheOwnerTheGatewayVetted(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 2048
	arts := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = arts
	const secret = "ALICE-SECRET-MARKER"
	saveArtifact(t, arts, "test-agent", "alice", "s", "report.txt", []byte(secret), "text/plain")

	msg := a2a.Message{Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
		URI: "artifact://test-agent/bob/alice/s%2Freport.txt?version=0",
	}}}}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "bob"}, "bob-session", "summarise", -1)

	if strings.Contains(result, secret) || strings.Contains(result, "text/plain") {
		t.Errorf("another user's artifact, or a summary of it, reached the prompt: %q", result)
	}
	copied := arts.Resolve("test-agent", "bob", "bob-session", "report.txt")
	if versions, err := arts.Inner.ListVersions(context.Background(), copied); err != nil || len(versions) != 0 {
		t.Errorf("another user's artifact was copied into bob's session: versions %v, err %v", versions, err)
	}
}
