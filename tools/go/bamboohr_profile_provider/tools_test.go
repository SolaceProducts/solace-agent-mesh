// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func newToolContext(t *testing.T, baseURL string) *sdk.ToolContext {
	t.Helper()
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(map[string]any{
		"bamboohr_subdomain":      "acme",
		"bamboohr_api_key":        "test-key",
		"lookup_key":              "email",
		"cache_ttl_seconds":       60,
		"request_timeout_seconds": 2,
		"base_url":                baseURL,
	})
	return tc
}

func resultPayload(t *testing.T, r *sdk.Result) map[string]any {
	t.Helper()
	if r == nil {
		t.Fatal("nil result")
	}
	if r.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got status=%q message=%q", r.Status, r.Message)
	}
	return r.Data
}

func TestTool_EnrichAuthenticatedUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	res, err := enrichAuthenticatedUser(t.Context(), EnrichParams{LookupValue: "jane.smith@acme.co"}, tc)
	if err != nil {
		t.Fatalf("enrichAuthenticatedUser: %v", err)
	}
	data := resultPayload(t, res)
	profile, ok := data["profile"].(*Profile)
	if !ok || profile == nil {
		t.Fatalf("expected *Profile, got %T (data=%v)", data["profile"], data)
	}
	if profile.WorkEmail != "jane.smith@acme.co" {
		t.Errorf("WorkEmail = %q", profile.WorkEmail)
	}
}

func TestTool_EnrichAuthenticatedUser_MissingConfig(t *testing.T) {
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(map[string]any{})
	res, err := enrichAuthenticatedUser(t.Context(), EnrichParams{LookupValue: "x@acme.co"}, tc)
	if err != nil {
		t.Fatalf("enrichAuthenticatedUser: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("expected error result, got status=%q", res.Status)
	}
	if !strings.Contains(res.Message, "bamboohr_subdomain") {
		t.Errorf("expected config-missing message, got: %q", res.Message)
	}
}

func TestTool_SearchUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	limit := 5
	res, err := searchUsers(t.Context(), SearchUsersParams{Query: "jane", Limit: &limit}, tc)
	if err != nil {
		t.Fatalf("searchUsers: %v", err)
	}
	data := resultPayload(t, res)
	profiles, ok := data["profiles"].([]*Profile)
	if !ok {
		t.Fatalf("expected []*Profile, got %T", data["profiles"])
	}
	if len(profiles) != 1 || profiles[0].WorkEmail != "jane.smith@acme.co" {
		t.Errorf("profiles = %+v", profiles)
	}
}

func TestTool_GetUserProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	res, err := getUserProfile(t.Context(), UserIDParam{UserID: "jane.smith@acme.co"}, tc)
	if err != nil {
		t.Fatalf("getUserProfile: %v", err)
	}
	data := resultPayload(t, res)
	profile, ok := data["profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", data["profile"])
	}
	if profile["id"] != "10" {
		t.Errorf("id = %v", profile["id"])
	}
}

func TestTool_GetTimeOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			_, _ = io.WriteString(w, `[{"employeeId":10,"start":"2026-04-20","end":"2026-04-25","type":"Vacation"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	uid := "jane.smith@acme.co"
	res, err := getTimeOff(t.Context(), GetTimeOffParams{UserID: &uid, Start: new("2026-04-16"), End: new("2026-04-30")}, tc)
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	data := resultPayload(t, res)
	entries, ok := data["entries"].([]TimeOffEntry)
	if !ok {
		t.Fatalf("expected []TimeOffEntry, got %T", data["entries"])
	}
	if len(entries) != 1 || entries[0].Type != "Vacation" {
		t.Errorf("entries = %+v", entries)
	}
	if _, hasResults := data["results"]; hasResults {
		t.Error("single-user result must not include grouped 'results'")
	}
}

func TestTool_GetTimeOff_OmittedDatesUseDefaultWindow(t *testing.T) {
	var sentStart, sentEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			sentStart = r.URL.Query().Get("start")
			sentEnd = r.URL.Query().Get("end")
			_, _ = io.WriteString(w, `[{"employeeId":10,"start":"2026-04-20","end":"2026-04-25","type":"Vacation"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	uid := "jane.smith@acme.co"
	res, err := getTimeOff(t.Context(), GetTimeOffParams{UserID: &uid}, tc)
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	data := resultPayload(t, res)
	if _, ok := data["entries"].([]TimeOffEntry); !ok {
		t.Fatalf("expected []TimeOffEntry, got %T", data["entries"])
	}

	start, err := time.Parse("2006-01-02", sentStart)
	if err != nil {
		t.Fatalf("start query param %q: %v", sentStart, err)
	}
	end, err := time.Parse("2006-01-02", sentEnd)
	if err != nil {
		t.Fatalf("end query param %q: %v", sentEnd, err)
	}
	if got := end.Sub(start); got != 30*24*time.Hour {
		t.Errorf("end - start = %v, want 30 days", got)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if diff := start.Sub(today); diff < -24*time.Hour || diff > 24*time.Hour {
		t.Errorf("start = %s, want today (%s)", sentStart, today.Format("2006-01-02"))
	}
}

func TestTool_GetTimeOff_MultiUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			_, _ = io.WriteString(w, `[
                {"employeeId":10,"start":"2026-04-20","end":"2026-04-25","type":"Vacation"},
                {"employeeId":20,"start":"2026-04-20","end":"2026-04-20","type":"Sick"}
            ]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	res, err := getTimeOff(t.Context(), GetTimeOffParams{
		UserIDs: []string{"jane.smith@acme.co", "alice.boss@acme.co"},
		Start:   new("2026-04-16"), End: new("2026-04-30"),
	}, tc)
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	data := resultPayload(t, res)
	results, ok := data["results"].([]UserTimeOff)
	if !ok {
		t.Fatalf("expected []UserTimeOff, got %T", data["results"])
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].UserID != "jane.smith@acme.co" || results[0].Entries[0].Type != "Vacation" {
		t.Errorf("jane group = %+v", results[0])
	}
	if results[1].UserID != "alice.boss@acme.co" || results[1].Entries[0].Type != "Sick" {
		t.Errorf("alice group = %+v", results[1])
	}
	if data["count"] != 2 {
		t.Errorf("count = %v, want 2", data["count"])
	}
	if _, hasEntries := data["entries"]; hasEntries {
		t.Error("multi-user result must not include flat 'entries'")
	}
}

func TestTool_GetTimeOff_NoUsers(t *testing.T) {
	tc := newToolContext(t, "http://unused.invalid")
	res, err := getTimeOff(t.Context(), GetTimeOffParams{}, tc)
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("expected an error result when no users supplied, got status %q", res.Status)
	}
}

func TestMergeUserIDs(t *testing.T) {
	got := mergeUserIDs(" Jane@acme.co ", []string{"alice@acme.co", "jane@acme.co", "", "  ", "ALICE@acme.co"})
	want := []string{"Jane@acme.co", "alice@acme.co"}
	if len(got) != len(want) {
		t.Fatalf("mergeUserIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mergeUserIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(mergeUserIDs("", nil)) != 0 {
		t.Error("mergeUserIDs of empty inputs should be empty")
	}
}

func TestTool_GetUserProfilePicture(t *testing.T) {
	photoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGDATA"))
	}))
	defer photoSrv.Close()

	payload := `{"employees":[{"id":"10","workEmail":"jane@acme.co","photoUrl":"` + photoSrv.URL + `/jane.jpg"}]}`
	dirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer dirSrv.Close()

	tc := newToolContext(t, dirSrv.URL)
	res, err := getUserProfilePicture(t.Context(), UserIDParam{UserID: "jane@acme.co"}, tc)
	if err != nil {
		t.Fatalf("getUserProfilePicture: %v", err)
	}
	data := resultPayload(t, res)
	if data["mime_type"] != "image/jpeg" {
		t.Errorf("mime_type = %v", data["mime_type"])
	}
	want := base64.StdEncoding.EncodeToString([]byte("JPEGDATA"))
	if data["image_base64"] != want {
		t.Errorf("image_base64 = %v, want %v", data["image_base64"], want)
	}

	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject, got %d", len(res.DataObjects))
	}
	do := res.DataObjects[0]
	if do.Name != "profile_picture.jpg" {
		t.Errorf("DataObject.Name = %q, want profile_picture.jpg", do.Name)
	}
	if do.MIMEType != "image/jpeg" {
		t.Errorf("DataObject.MIMEType = %q, want image/jpeg", do.MIMEType)
	}
	if do.Disposition != sdk.DispositionArtifact {
		t.Errorf("DataObject.Disposition = %q, want artifact", do.Disposition)
	}
	if string(do.Content) != "JPEGDATA" {
		t.Errorf("DataObject.Content = %q, want JPEGDATA", do.Content)
	}
}

func TestTool_GetUserProfilePicture_PNGExtension(t *testing.T) {
	photoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer photoSrv.Close()

	payload := `{"employees":[{"id":"10","workEmail":"jane@acme.co","photoUrl":"` + photoSrv.URL + `/jane"}]}`
	dirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer dirSrv.Close()

	tc := newToolContext(t, dirSrv.URL)
	res, err := getUserProfilePicture(t.Context(), UserIDParam{UserID: "jane@acme.co"}, tc)
	if err != nil {
		t.Fatalf("getUserProfilePicture: %v", err)
	}
	resultPayload(t, res)
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject, got %d", len(res.DataObjects))
	}
	if res.DataObjects[0].Name != "profile_picture.png" {
		t.Errorf("DataObject.Name = %q, want profile_picture.png", res.DataObjects[0].Name)
	}
}

func TestImageExtForMIME(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":                 ".jpg",
		"image/jpeg; charset=binary": ".jpg",
		"image/png":                  ".png",
		"image/gif":                  ".gif",
		"image/webp":                 ".webp",
		"image/svg+xml":              ".svg",
		"application/octet-stream":   ".jpg",
	}
	for mime, want := range cases {
		if got := imageExtForMIME(mime); got != want {
			t.Errorf("imageExtForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestTool_GetUserProfilePicture_NoPhoto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	}))
	defer srv.Close()

	tc := newToolContext(t, srv.URL)
	res, err := getUserProfilePicture(t.Context(), UserIDParam{UserID: "alice.boss@acme.co"}, tc)
	if err != nil {
		t.Fatalf("getUserProfilePicture: %v", err)
	}
	data := resultPayload(t, res)
	if data["mime_type"] != "" || data["image_base64"] != "" {
		t.Errorf("expected empty payload, got %+v", data)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("expected no DataObjects when there is no photo, got %d", len(res.DataObjects))
	}
}

func TestLoadConfig_DefaultsApplied(t *testing.T) {
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(map[string]any{
		"bamboohr_subdomain": "acme",
		"bamboohr_api_key":   "k",
	})
	cfg, errMsg := loadConfig(tc)
	if errMsg != "" {
		t.Fatalf("unexpected errMsg = %q", errMsg)
	}
	if cfg.LookupKey != defaultLookupKey {
		t.Errorf("LookupKey = %q, want %q", cfg.LookupKey, defaultLookupKey)
	}
	if cfg.CacheTTL != defaultCacheTTL {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, defaultCacheTTL)
	}
	if cfg.RequestTimeout != defaultRequestTimeout {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, defaultRequestTimeout)
	}
}

func TestLoadConfig_TTLTooSmall(t *testing.T) {
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(map[string]any{
		"bamboohr_subdomain": "acme",
		"bamboohr_api_key":   "k",
		"cache_ttl_seconds":  10,
	})
	if _, errMsg := loadConfig(tc); !strings.Contains(errMsg, "cache_ttl_seconds") {
		t.Errorf("expected cache_ttl_seconds error, got %q", errMsg)
	}
}

func TestGetTimeOffParamsSchemaRequiresNothing(t *testing.T) {
	if got := sdk.BuildSchema[GetTimeOffParams]().Required; len(got) != 0 {
		t.Errorf("required = %v, want none", got)
	}
}
