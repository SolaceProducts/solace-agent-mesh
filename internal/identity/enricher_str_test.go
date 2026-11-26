// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	enricherTestNamespace   = "ns"
	enricherTestToolsetID   = "ts-abc"
	enricherTestPackageName = "test_provider"
)

func wireToolName(bareName string) string {
	return enricherTestPackageName + store.PackageToolSeparator + bareName
}

type stubIdentityConfig struct {
	row *store.PlatformIdentityConfig
	err error
}

func (s stubIdentityConfig) Get(_ context.Context) (*store.PlatformIdentityConfig, error) {
	return s.row, s.err
}

type stubToolsetCfg struct {
	mu             sync.Mutex
	requestedPkgID string
	row            *store.ToolsetConfig
}

func (s *stubToolsetCfg) GetToolsetConfig(_ context.Context, packageID string) (*store.ToolsetConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestedPkgID = packageID
	return s.row, nil
}

func (s *stubToolsetCfg) LastRequestedPackageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestedPkgID
}

type stubToolFileLookup struct {
	mu      sync.Mutex
	rows    map[string]string
	err     error
	asksFor []string
}

func newStubToolFileLookup() *stubToolFileLookup {
	return &stubToolFileLookup{rows: map[string]string{}}
}

func (s *stubToolFileLookup) ResolveWireToolName(_ context.Context, _, bareName string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asksFor = append(s.asksFor, bareName)
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.rows[bareName]; ok {
		return v, nil
	}
	return wireToolName(bareName), nil
}

func (s *stubToolFileLookup) setWireName(bareName, wireName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[bareName] = wireName
}

func (s *stubToolFileLookup) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func startEnricher(
	t *testing.T,
	cfg stubIdentityConfig,
	pkgCfg *stubToolsetCfg,
	tfLookup toolFileLookup,
) (*STREnricher, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if tfLookup == nil {
		tfLookup = newStubToolFileLookup()
	}
	e := NewSTREnricher(brk, enricherTestNamespace, cfg, pkgCfg, tfLookup, nil, nil)
	if err := e.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = e.Stop(context.Background())
	})
	return e, brk
}

func simulateSTRTool(
	t *testing.T,
	brk *broker.DevBroker,
	toolName string,
	makeResponse func(corrID, replyTo string, params *remotetool.InvokeParams) remotetool.InvokeResult,
) {
	t.Helper()
	q, err := brk.CreateQueue("str-enricher-"+toolName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	invokeTopic := remotetool.InvokeTopic(enricherTestNamespace, toolName)
	if err := brk.Subscribe(q, invokeTopic); err != nil {
		t.Fatal(err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		msg.Ack()
		var rpc struct {
			ID     string                  `json:"id"`
			Params remotetool.InvokeParams `json:"params"`
		}
		if err := json.Unmarshal(msg.Payload, &rpc); err != nil {
			t.Errorf("simulateSTRTool: unmarshal invoke: %v", err)
			return
		}
		corrID := msg.CorrelationID
		if corrID == "" {
			corrID = rpc.ID
		}
		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		result := makeResponse(corrID, replyTo, &rpc.Params)
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()
}

func wireResultBytes(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	return map[string]any{
		"_schema":         "ToolResult",
		"_schema_version": "1.0",
		"status":          "success",
		"message":         "ok",
		"data":            data,
	}
}

func TestSTREnricher_EnrichAuthenticatedUser_Success(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{
			PackageID:    enricherTestToolsetID,
			ConfigValues: ptrString(`{"users_file":"/tmp/dev-users.json"}`),
		},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("enrich_authenticated_user"), func(_, _ string, params *remotetool.InvokeParams) remotetool.InvokeResult {
		if got, _ := params.Args["lookup_value"].(string); got != "alice@dev.local" {
			t.Errorf("lookup_value arg = %q, want %q", got, "alice@dev.local")
		}
		if got, _ := params.ToolConfig["users_file"].(string); got != "/tmp/dev-users.json" {
			t.Errorf("tool_config.users_file = %q, want %q", got, "/tmp/dev-users.json")
		}
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"profile": map[string]any{
					"id":          "alice@dev.local",
					"displayName": "Alice Anderson",
					"workEmail":   "alice@dev.local",
					"jobTitle":    "Senior Engineer",
					"department":  "Platform",
					"costCenter":  "CC-42",
				},
			}),
			ExecutionTimeMs: 10,
		}
	})

	prof, err := e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	if err != nil {
		t.Fatalf("EnrichAuthenticatedUser: %v", err)
	}
	if prof == nil {
		t.Fatal("EnrichAuthenticatedUser: profile is nil")
	}
	if prof.ID != "alice@dev.local" || prof.DisplayName != "Alice Anderson" || prof.JobTitle != "Senior Engineer" {
		t.Errorf("profile decoded incorrectly: %+v", prof)
	}
	if prof.Extra["costCenter"] != "CC-42" {
		t.Errorf("custom field costCenter did not pass through into Extra: %+v", prof.Extra)
	}
	if pkgCfg.LastRequestedPackageID() != enricherTestToolsetID {
		t.Errorf("tool_config lookup used %q, want %q", pkgCfg.LastRequestedPackageID(), enricherTestToolsetID)
	}
}

type stubTokenSigner struct {
	mu     sync.Mutex
	token  string
	corrID string
}

func (s *stubTokenSigner) SignServiceToken(corrID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.corrID = corrID
	return s.token, nil
}

func (s *stubTokenSigner) LastCorrID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrID
}

func TestSTREnricher_PassesAuthToken(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{PackageID: enricherTestToolsetID, ConfigValues: ptrString(`{}`)},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	signer := &stubTokenSigner{token: "test.jwt.token"}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := NewSTREnricher(brk, enricherTestNamespace, cfg, pkgCfg, newStubToolFileLookup(), signer, nil)
	if err := e.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })

	var observedToken string
	q, err := brk.CreateQueue("auth-token-probe", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic(enricherTestNamespace, wireToolName("enrich_authenticated_user"))); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		msg.Ack()
		observedToken = msg.UserProperties[remotetool.PropAuthToken]
		var rpc struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(msg.Payload, &rpc)
		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		resp := a2a.NewResponse(rpc.ID, remotetool.InvokeResult{ToolResult: wireResultBytes(t, map[string]any{"id": "alice@dev.local"})})
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	_, _ = e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	<-done
	if observedToken != "test.jwt.token" {
		t.Errorf("authToken on broker = %q, want %q", observedToken, "test.jwt.token")
	}
	if signer.LastCorrID() == "" {
		t.Error("signer not invoked with a corrID")
	}
}

type errSigner struct{ err error }

func (s errSigner) SignServiceToken(string) (string, error) { return "", s.err }

func TestSTREnricher_SignerError_PropagatesAsLoaderErr(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{PackageID: enricherTestToolsetID, ConfigValues: ptrString(`{}`)},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	want := errors.New("trust key not loaded")
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := NewSTREnricher(brk, enricherTestNamespace, cfg, pkgCfg, newStubToolFileLookup(), errSigner{err: want}, nil)
	if err := e.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })

	_, err := e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	if err == nil || !strings.Contains(err.Error(), "mint enrichment auth token") || !errors.Is(err, want) {
		t.Errorf("err = %v, want wrapped %q + contains \"mint enrichment auth token\"", err, want)
	}
}

func TestSTREnricher_NoSignerOmitsAuthToken(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{PackageID: enricherTestToolsetID, ConfigValues: ptrString(`{}`)},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	var hadAuthTokenKey bool
	q, err := brk.CreateQueue("auth-token-omit-probe", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic(enricherTestNamespace, wireToolName("enrich_authenticated_user"))); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		msg.Ack()
		_, hadAuthTokenKey = msg.UserProperties[remotetool.PropAuthToken]
		var rpc struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(msg.Payload, &rpc)
		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		resp := a2a.NewResponse(rpc.ID, remotetool.InvokeResult{ToolResult: wireResultBytes(t, map[string]any{"id": "x"})})
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	_, _ = e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	<-done
	if hadAuthTokenKey {
		t.Error("authToken header was set on broker call with nil signer; expected omission")
	}
}

func TestSTREnricher_NoToolsetWired_Errors(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	e, _ := startEnricher(t, stubIdentityConfig{row: nil}, pkgCfg, nil)

	if _, err := e.EnrichAuthenticatedUser(t.Context(), "user@dev.local"); err == nil {
		t.Fatal("expected error when no toolset wired")
	}
}

func TestSTREnricher_ToolNotInToolset_Errors(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{PackageID: enricherTestToolsetID, ConfigValues: ptrString(`{}`)},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	tf := newStubToolFileLookup()
	tf.setWireName("enrich_authenticated_user", "")
	e, _ := startEnricher(t, cfg, pkgCfg, tf)

	_, err := e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	if err == nil {
		t.Fatal("expected error when bare tool name is not present in the toolset")
	}
}

func TestSTREnricher_WireNameLookupError_Surfaces(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{
		row: &store.ToolsetConfig{PackageID: enricherTestToolsetID, ConfigValues: ptrString(`{}`)},
	}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	tf := newStubToolFileLookup()
	tf.setErr(errors.New("db unreachable"))
	e, _ := startEnricher(t, cfg, pkgCfg, tf)

	_, err := e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	if err == nil {
		t.Fatal("expected error when wire-name lookup fails")
	}
	if !strings.Contains(err.Error(), "db unreachable") {
		t.Errorf("error %q should wrap the underlying lookup failure", err)
	}
}

func TestSTREnricher_SearchUsers_DecodesList(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("search_users"), func(_, _ string, params *remotetool.InvokeParams) remotetool.InvokeResult {
		if got, _ := params.Args["query"].(string); got != "ali" {
			t.Errorf("query arg = %q, want \"ali\"", got)
		}
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"profiles": []any{
					map[string]any{"id": "alice@dev.local", "displayName": "Alice Anderson"},
					map[string]any{"id": "anika@dev.local", "displayName": "Anika Park"},
				},
			}),
		}
	})

	profiles, err := e.SearchUsers(t.Context(), "ali", 5)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(profiles))
	}
	if profiles[0].ID != "alice@dev.local" || profiles[1].DisplayName != "Anika Park" {
		t.Errorf("profiles decoded incorrectly: %+v", profiles)
	}
}

func TestSTREnricher_ToolError_Propagates(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("enrich_authenticated_user"), func(_, _ string, _ *remotetool.InvokeParams) remotetool.InvokeResult {
		return remotetool.InvokeResult{
			ToolResult: map[string]any{
				"_schema":         "ToolResult",
				"_schema_version": "1.0",
				"status":          "error",
				"message":         "users_file is required in tool config.",
			},
		}
	})

	_, err := e.EnrichAuthenticatedUser(t.Context(), "alice@dev.local")
	if err == nil {
		t.Fatal("expected error when tool reports status=error")
	}
}

func TestSTREnricher_GetUserProfile_DecodesMap(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("get_user_profile"), func(_, _ string, params *remotetool.InvokeParams) remotetool.InvokeResult {
		if got, _ := params.Args["user_id"].(string); got != "u-42" {
			t.Errorf("user_id arg = %q, want %q", got, "u-42")
		}
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"profile": map[string]any{
					"id":         "u-42",
					"workEmail":  "u@example.com",
					"photoUrl":   "https://example.com/p.jpg",
					"department": "Eng",
				},
			}),
		}
	})

	prof, err := e.GetUserProfile(t.Context(), "u-42")
	if err != nil {
		t.Fatalf("GetUserProfile: %v", err)
	}
	if prof["id"] != "u-42" || prof["photoUrl"] != "https://example.com/p.jpg" {
		t.Errorf("profile decoded incorrectly: %+v", prof)
	}
}

func TestSTREnricher_GetUserProfile_BadShape(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("get_user_profile"), func(_, _ string, _ *remotetool.InvokeParams) remotetool.InvokeResult {
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"profile": "not-a-map",
			}),
		}
	})

	_, err := e.GetUserProfile(t.Context(), "u-42")
	if err == nil {
		t.Fatal("expected error when profile is not a map")
	}
}

func TestSTREnricher_GetTimeOff_DecodesEntries(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("get_time_off"), func(_, _ string, params *remotetool.InvokeParams) remotetool.InvokeResult {
		if got, _ := params.Args["user_id"].(string); got != "u-42" {
			t.Errorf("user_id arg = %q", got)
		}
		if got, _ := params.Args["start"].(string); got != "2026-01-01" {
			t.Errorf("start arg = %q", got)
		}
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"entries": []any{
					map[string]any{"start": "2026-01-10", "end": "2026-01-12", "type": "Vacation", "amount": "3"},
					map[string]any{"start": "2026-02-01", "end": "2026-02-01", "type": "Sick", "amount": "1"},
				},
			}),
		}
	})

	entries, err := e.GetTimeOff(t.Context(), "u-42", "2026-01-01", "2026-03-01")
	if err != nil {
		t.Fatalf("GetTimeOff: %v", err)
	}
	if len(entries) != 2 || entries[0].Type != "Vacation" || entries[1].Amount != "1" {
		t.Errorf("entries decoded incorrectly: %+v", entries)
	}
}

func TestSTREnricher_GetUserProfilePicture_DecodesBase64(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("get_user_profile_picture"), func(_, _ string, _ *remotetool.InvokeParams) remotetool.InvokeResult {
		res := wireResultBytes(t, map[string]any{
			"image_base64": "UE5HQllURVM=",
			"mime_type":    "image/png",
		})
		res["data_objects"] = []any{
			map[string]any{
				"name":        "profile_picture.png",
				"mime_type":   "image/png",
				"disposition": "artifact",
				"content":     "UE5HQllURVM=",
				"is_binary":   true,
			},
		}
		return remotetool.InvokeResult{ToolResult: res}
	})

	img, mime, err := e.GetUserProfilePicture(t.Context(), "u-42")
	if err != nil {
		t.Fatalf("GetUserProfilePicture: %v", err)
	}
	if string(img) != "PNGBYTES" {
		t.Errorf("image bytes = %q, want %q", string(img), "PNGBYTES")
	}
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
}

func TestSTREnricher_GetUserProfilePicture_EmptyImage(t *testing.T) {
	t.Parallel()
	pkgCfg := &stubToolsetCfg{}
	cfg := stubIdentityConfig{row: &store.PlatformIdentityConfig{
		ToolsetID: ptrString(enricherTestToolsetID),
		LookupKey: "email",
	}}
	e, brk := startEnricher(t, cfg, pkgCfg, nil)

	simulateSTRTool(t, brk, wireToolName("get_user_profile_picture"), func(_, _ string, _ *remotetool.InvokeParams) remotetool.InvokeResult {
		return remotetool.InvokeResult{
			ToolResult: wireResultBytes(t, map[string]any{
				"image_base64": "",
				"mime_type":    "",
			}),
		}
	})

	img, mime, err := e.GetUserProfilePicture(t.Context(), "u-42")
	if err != nil {
		t.Fatalf("GetUserProfilePicture: %v", err)
	}
	if img != nil || mime != "" {
		t.Errorf("expected empty image; got img=%v mime=%q", img, mime)
	}
}

func TestExtractProfile_Branches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      map[string]any
		wantNil bool
		wantErr bool
	}{
		{"nil map", nil, true, false},
		{"absent key", map[string]any{}, true, false},
		{"explicit null", map[string]any{"profile": nil}, true, false},
		{"string not object", map[string]any{"profile": "nope"}, false, true},
		{"missing id", map[string]any{"profile": map[string]any{"displayName": "Alice"}}, false, true},
		{"happy", map[string]any{"profile": map[string]any{"id": "u-1", "costCenter": "CC-42"}}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := extractProfile(tc.in, "profile")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr && (p == nil) != tc.wantNil {
				t.Fatalf("profile nil = %v, want %v", p == nil, tc.wantNil)
			}
		})
	}
}

func TestExtractProfileList_BadShape(t *testing.T) {
	t.Parallel()
	if _, err := extractProfileList(map[string]any{"profiles": "not-a-list"}, "profiles", nil); err == nil {
		t.Error("expected error for non-array")
	}
}

func TestExtractProfileList_NilAndMissing(t *testing.T) {
	t.Parallel()
	if ps, err := extractProfileList(nil, "profiles", nil); err != nil || len(ps) != 0 {
		t.Errorf("nil map: got (%v, %v)", ps, err)
	}
	if ps, err := extractProfileList(map[string]any{}, "profiles", nil); err != nil || len(ps) != 0 {
		t.Errorf("missing key: got (%v, %v)", ps, err)
	}
}

func TestExtractProfileList_SkipsMalformedRow(t *testing.T) {
	t.Parallel()
	in := map[string]any{"profiles": []any{
		map[string]any{"id": "u-1", "displayName": "Alice"},
		map[string]any{"displayName": "no id here"},
		map[string]any{"id": "u-3", "displayName": "Carol"},
	}}
	got, err := extractProfileList(in, "profiles", nil)
	if err != nil {
		t.Fatalf("extractProfileList: %v", err)
	}
	if len(got) != 2 || got[0].ID != "u-1" || got[1].ID != "u-3" {
		t.Errorf("expected the two valid rows, got %+v", got)
	}
}

func TestLoadToolConfig(t *testing.T) {
	t.Parallel()
	makeRow := func(s string) *store.ToolsetConfig {
		return &store.ToolsetConfig{PackageID: "p", ConfigValues: &s}
	}
	cases := []struct {
		name      string
		row       *store.ToolsetConfig
		toolName  string
		wantErr   bool
		wantEmpty bool
		wantKeys  []string
	}{
		{"nil row", nil, "any", false, true, nil},
		{"nil ConfigValues", &store.ToolsetConfig{PackageID: "p", ConfigValues: nil}, "any", false, true, nil},
		{"empty ConfigValues", makeRow(""), "any", false, true, nil},
		{"json null", makeRow("null"), "any", false, true, nil},
		{"bad json", makeRow(`{bad`), "any", true, false, nil},

		{"legacy flat passes through", makeRow(`{"bamboohr_subdomain":"example","bamboohr_api_key":"sk"}`), "enrich_authenticated_user", false, false, []string{"bamboohr_subdomain", "bamboohr_api_key"}},

		{"nested projects the matching tool", makeRow(`{"enrich_authenticated_user":{"bamboohr_subdomain":"example","bamboohr_api_key":"sk"},"find_users":{"bamboohr_subdomain":"example"}}`), "enrich_authenticated_user", false, false, []string{"bamboohr_subdomain", "bamboohr_api_key"}},

		{"nested with no entry for tool returns empty", makeRow(`{"enrich_authenticated_user":{"k":"v"}}`), "search_users", false, true, nil},
	}
	e := &STREnricher{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e.toolsetCR = &fakeToolsetRepo{row: tc.row}
			m, err := e.loadToolConfig(t.Context(), "p", tc.toolName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if tc.wantEmpty && len(m) != 0 {
				t.Errorf("expected empty map, got %v", m)
			}
			for _, k := range tc.wantKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("missing key %q in %v", k, m)
				}
			}
		})
	}
}

type fakeToolsetRepo struct{ row *store.ToolsetConfig }

func (f *fakeToolsetRepo) GetToolsetConfig(_ context.Context, _ string) (*store.ToolsetConfig, error) {
	return f.row, nil
}

func ptrString(s string) *string { return &s }

func TestSTREnricher_HasTool_Present(t *testing.T) {
	t.Parallel()
	files := newStubToolFileLookup()
	e := NewSTREnricher(nil, enricherTestNamespace, nil, nil, files, nil, nil)

	ok, err := e.HasTool(context.Background(), enricherTestToolsetID, "search_users")
	if err != nil || !ok {
		t.Fatalf("HasTool = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestSTREnricher_HasTool_Absent(t *testing.T) {
	t.Parallel()
	files := newStubToolFileLookup()
	files.setWireName("search_users", "")
	e := NewSTREnricher(nil, enricherTestNamespace, nil, nil, files, nil, nil)

	ok, err := e.HasTool(context.Background(), enricherTestToolsetID, "search_users")
	if err != nil || ok {
		t.Fatalf("HasTool = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestSTREnricher_HasTool_LookupError(t *testing.T) {
	t.Parallel()
	files := newStubToolFileLookup()
	files.err = errors.New("db down")
	e := NewSTREnricher(nil, enricherTestNamespace, nil, nil, files, nil, nil)

	ok, err := e.HasTool(context.Background(), enricherTestToolsetID, "search_users")
	if err == nil || ok {
		t.Fatalf("HasTool = (%v, %v), want (false, error)", ok, err)
	}
}
