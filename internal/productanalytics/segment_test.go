// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func cliRun() RawEvent {
	return RawEvent{
		Name:       EventCLICommandRun,
		Properties: map[string]any{PropCommand: "sam task send", PropOutcome: OutcomeSuccess},
	}
}

func relayService() *Service {
	return NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Config:   ServiceConfig{BatchSize: 1000, RateCapPerMin: 100000},
	})
}

func TestRelayedEventReportsTheReportingHost(t *testing.T) {
	s := relayService()
	host := ClientPlatform{OS: "windows", Arch: "386", AppVersion: "main-v1.2.3"}
	s.EnqueueRelayed(context.Background(), SurfaceCLI, host, []RawEvent{cliRun()})

	buf := buffered(s)
	if len(buf) != 1 {
		t.Fatalf("buffered = %d, want 1", len(buf))
	}
	props := buf[0].Properties
	for key, want := range map[string]string{propOS: "windows", propArch: "386", propAppVersion: "main-v1.2.3"} {
		if got := props[key]; got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
	if props[propOS] == runtime.GOOS && runtime.GOOS != "windows" {
		t.Error("relayed event reported the emitting process's os")
	}
}

func TestRelayedEventStripsAPlatformOutsideTheVocabulary(t *testing.T) {
	forged := []ClientPlatform{
		{OS: "Ubuntu 22.04 on my laptop", Arch: "x86_64", AppVersion: "reported by alice@example.com"},
		{OS: "linux\x00", Arch: "arm64 ", AppVersion: "v1.2.3 (build from /Users/alice/src)"},
		{OS: strings.Repeat("a", 4096), Arch: strings.Repeat("b", 4096), AppVersion: strings.Repeat("c", 4096)},
	}
	for _, host := range forged {
		s := relayService()
		s.EnqueueRelayed(context.Background(), SurfaceCLI, host, []RawEvent{cliRun()})
		buf := buffered(s)
		if len(buf) != 1 {
			t.Fatalf("buffered = %d, want 1", len(buf))
		}
		props := buf[0].Properties
		for _, key := range []string{propOS, propArch, propAppVersion} {
			if got, present := props[key]; present {
				t.Errorf("%s survived as %v, want the field absent", key, got)
			}
		}
	}
}

func TestRelayedEventKeepsOnlyTheFieldsSupplied(t *testing.T) {
	s := relayService()
	s.EnqueueRelayed(context.Background(), SurfaceCLI, ClientPlatform{OS: "darwin"}, []RawEvent{cliRun()})

	props := buffered(s)[0].Properties
	if props[propOS] != "darwin" {
		t.Errorf("os = %v, want darwin", props[propOS])
	}
	for _, key := range []string{propArch, propAppVersion} {
		if got, present := props[key]; present {
			t.Errorf("%s = %v, want the field absent", key, got)
		}
	}
}

func TestRelayedEventTakesTheSurfaceFromTheCaller(t *testing.T) {
	s := relayService()
	raw := cliRun()
	raw.Properties[PropInterface] = SurfaceUI
	s.EnqueueRelayed(context.Background(), SurfaceCLI, localPlatform(), []RawEvent{raw})

	if got := buffered(s)[0].Properties[PropInterface]; got != SurfaceCLI {
		t.Errorf("interface = %v, want cli", got)
	}
}

func TestLocalEnqueueStampsThisProcess(t *testing.T) {
	s := relayService()
	s.Enqueue(context.Background(), SurfaceCLI, []RawEvent{cliRun()})

	props := buffered(s)[0].Properties
	if props[propOS] != runtime.GOOS {
		t.Errorf("os = %v, want %q", props[propOS], runtime.GOOS)
	}
	if props[propArch] != runtime.GOARCH {
		t.Errorf("arch = %v, want %q", props[propArch], runtime.GOARCH)
	}
	if props[propAppVersion] != samversion.Version {
		t.Errorf("appVersion = %v, want %q", props[propAppVersion], samversion.Version)
	}
}

func TestLocalPlatformSurvivesItsOwnVocabulary(t *testing.T) {
	got := ParseClientPlatform(runtime.GOOS, runtime.GOARCH, samversion.Version)
	if got.OS != runtime.GOOS {
		t.Errorf("os %q is not in goosValues", runtime.GOOS)
	}
	if got.Arch != runtime.GOARCH {
		t.Errorf("arch %q is not in goarchValues", runtime.GOARCH)
	}
	if got.AppVersion != samversion.Version {
		t.Errorf("appVersion %q does not match the accepted version shape", samversion.Version)
	}
}

func TestAppVersionShapeAcceptsBuildStamps(t *testing.T) {
	for _, v := range []string{"dev", "main-v2.311.4", "main-v2.311.4-3-gabc1234-dirty", "1.0.0+build.7", "abc1234"} {
		if versionShaped(v) != v {
			t.Errorf("versionShaped(%q) = %q, want it accepted", v, versionShaped(v))
		}
	}
}

func TestAppVersionShapeRefusesText(t *testing.T) {
	for _, v := range []string{
		"",
		"v1.2.3 dirty",
		"built by alice@example.com",
		"/Users/alice/src/sam",
		"https://internal.example.com/build/7",
		"sk-0123456789:abcdef",
		"-leading-dash",
		strings.Repeat("9", 65),
	} {
		if got := versionShaped(v); got != "" {
			t.Errorf("versionShaped(%q) = %q, want it refused", v, got)
		}
	}
}

func TestInstallTraitsStayLocal(t *testing.T) {
	traits := installTraits()
	if traits[propOS] != runtime.GOOS {
		t.Errorf("trait os = %v, want %q", traits[propOS], runtime.GOOS)
	}
	if traits[propArch] != runtime.GOARCH {
		t.Errorf("trait arch = %v, want %q", traits[propArch], runtime.GOARCH)
	}
	if traits[propAppVersion] != samversion.Version {
		t.Errorf("trait appVersion = %v, want %q", traits[propAppVersion], samversion.Version)
	}
}
