// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestToolsetConfigPatch(t *testing.T) {
	tools := []apiv2.ToolsetTool{
		toolWithConfigKeys("lookup", "region", "api_key", "retries"),
		toolWithConfigKeys("update", "region"),
	}
	stored := map[string]any{
		"lookup": map[string]any{"region": "us", "api_key": "<REDACTED>", "retries": 3, "dropped_from_schema": "x"},
		"update": map[string]any{"region": "us"},
		"gone":   map[string]any{"region": "us"},
		"hil":    map[string]any{"lookup": true, "update": true},
	}
	for _, tc := range []struct {
		name    string
		desired map[string]any
		want    map[string]any
	}{
		{
			name:    "a removed setting is cleared and a resent secret rides along",
			desired: map[string]any{"lookup": map[string]any{"region": "us", "api_key": "sk-1"}, "update": map[string]any{"region": "us"}, "hil": map[string]any{"lookup": true, "update": true}},
			want:    map[string]any{"lookup": map[string]any{"api_key": "sk-1", "retries": nil}},
		},
		{
			name:    "a removed tool has each declared setting cleared",
			desired: map[string]any{"lookup": map[string]any{"region": "us", "api_key": "sk-1", "retries": 3}, "hil": map[string]any{"lookup": true, "update": true}},
			want:    map[string]any{"lookup": map[string]any{"api_key": "sk-1"}, "update": map[string]any{"region": nil}},
		},
		{
			name:    "a changed reserved block is sent whole, since the server replaces it",
			desired: map[string]any{"lookup": map[string]any{"region": "us", "api_key": "sk-1", "retries": 3}, "update": map[string]any{"region": "us"}, "hil": map[string]any{"lookup": false, "update": true}},
			want:    map[string]any{"lookup": map[string]any{"api_key": "sk-1"}, "hil": map[string]any{"lookup": false, "update": true}},
		},
		{
			name:    "a removed reserved block is kept",
			desired: map[string]any{"lookup": map[string]any{"region": "us", "api_key": "sk-1", "retries": 3}, "update": map[string]any{"region": "us"}},
			want:    map[string]any{"lookup": map[string]any{"api_key": "sk-1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := toolsetConfigPatch(tools, tc.desired, stored)
			if err != nil {
				t.Fatal(err)
			}
			if !body.Values.Set || !reflect.DeepEqual(normalizeJSON(t, body.Values.Value), normalizeJSON(t, tc.want)) {
				t.Errorf("values = %#v (set=%t), want %#v", body.Values.Value, body.Values.Set, tc.want)
			}
		})
	}

	t.Run("settings no tool declares still send a PATCH, so the server drops them", func(t *testing.T) {
		desired := map[string]any{"update": map[string]any{"region": "us"}}
		for name, orphaned := range map[string]map[string]any{
			"a setting":     {"update": map[string]any{"region": "us", "dropped_from_schema": "x"}},
			"a whole tool":  {"update": map[string]any{"region": "us"}, "gone": map[string]any{"region": "us"}},
			"a tool's keys": {"update": map[string]any{"region": "us"}, "update_v1": "x"},
		} {
			body, err := toolsetConfigPatch(tools, desired, orphaned)
			if err != nil {
				t.Fatal(err)
			}
			if !body.Values.Set || len(body.Values.Value) != 0 {
				t.Errorf("%s: values = %#v (set=%t), want an empty object sent", name, body.Values.Value, body.Values.Set)
			}
		}
	})

	t.Run("a reserved block holding numbers is not resent when unchanged", func(t *testing.T) {
		desired := map[string]any{"update": map[string]any{"region": "us"}, "hil": map[string]any{"timeout_seconds": 30}}
		decoded := map[string]any{"update": map[string]any{"region": "us"}, "hil": map[string]any{"timeout_seconds": float64(30)}}
		body, err := toolsetConfigPatch(tools, desired, decoded)
		if err != nil {
			t.Fatal(err)
		}
		if body.Values.Set {
			t.Errorf("values = %#v, want the field left out", body.Values.Value)
		}
	})

	t.Run("a changed reserved block alone is the whole body", func(t *testing.T) {
		desired := map[string]any{"update": map[string]any{"region": "us"}, "hil": map[string]any{"lookup": false}}
		storedHIL := map[string]any{"update": map[string]any{"region": "us"}, "hil": map[string]any{"lookup": true}}
		body, err := toolsetConfigPatch(tools, desired, storedHIL)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"hil": map[string]any{"lookup": false}}
		if !body.Values.Set || !reflect.DeepEqual(normalizeJSON(t, body.Values.Value), normalizeJSON(t, want)) {
			t.Errorf("values = %#v (set=%t), want %#v", body.Values.Value, body.Values.Set, want)
		}
	})

	t.Run("a removed secret is kept, since it reads back redacted", func(t *testing.T) {
		desired := map[string]any{"lookup": map[string]any{"region": "us"}}
		withSecret := map[string]any{"lookup": map[string]any{"region": "us", "api_key": "<REDACTED>"}}
		body, err := toolsetConfigPatch(tools, desired, withSecret)
		if err != nil {
			t.Fatal(err)
		}
		if body.Values.Set {
			t.Errorf("values = %#v, want the field left out", body.Values.Value)
		}
	})

	t.Run("nothing changed sends nothing", func(t *testing.T) {
		unchanged := map[string]any{"update": map[string]any{"region": "us"}}
		body, err := toolsetConfigPatch(tools, unchanged, map[string]any{"update": map[string]any{"region": "us"}})
		if err != nil {
			t.Fatal(err)
		}
		if body.Values.Set {
			t.Errorf("values = %#v, want the field left out", body.Values.Value)
		}
	})
}

func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func v2Platform(t *testing.T, h http.HandlerFunc) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == APIV2ProbePath {
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

func TestGetToolBuildTarget_V2(t *testing.T) {
	t.Run("keeps the v1 spelling of str-init", func(t *testing.T) {
		c, _ := v2Platform(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v2/platform/toolsetBuildTarget" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"os":"linux","arch":"amd64","source":"str_init","pythonVersion":null}}`))
		})
		got, err := c.GetToolBuildTarget(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := BuildTargetResponse{OS: "linux", Arch: "amd64", Source: "str-init"}
		if *got != want {
			t.Errorf("build target = %+v, want %+v", *got, want)
		}
	})
	t.Run("a mixed fleet is a MixedTargetError", func(t *testing.T) {
		c, _ := v2Platform(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"mixed","errorId":"e","validationDetails":{"targets":["linux/amd64","linux/arm64"]}}`))
		})
		_, err := c.GetToolBuildTarget(t.Context())
		var mixed *MixedTargetError
		if !errors.As(err, &mixed) || len(mixed.Targets) != 2 {
			t.Errorf("err = %v, want a MixedTargetError naming both targets", err)
		}
	})
}

func TestUploadToolsetChunked_UsesTheServersChunkSize(t *testing.T) {
	const chunkSize = chunkedUploadThreshold + 1
	data := make([]byte, chunkedUploadThreshold+chunkSize)
	var mu sync.Mutex
	var chunkLens []int
	c, requests := v2Platform(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == toolsetUploadsPath:
			var body initUploadRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ToolsetID != "ts-1" || body.TotalSize != int64(len(data)) {
				t.Errorf("create upload body = %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "up-1", "chunkSize": chunkSize}})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, toolsetUploadsPath+"/up-1/chunks/"):
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			chunkLens = append(chunkLens, len(raw))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{}}`))
		case r.Method == http.MethodPut && r.URL.Path == toolsetUploadsPath+"/up-1/complete":
			_, _ = w.Write([]byte(`{"data":{"id":"ts-1"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	if err := c.UploadToolset(t.Context(), "ts-1", "big.zip", data); err != nil {
		t.Fatal(err)
	}
	wantLens := []int{chunkSize, len(data) - chunkSize}
	if !reflect.DeepEqual(chunkLens, wantLens) {
		t.Errorf("chunk sizes = %v, want %v", chunkLens, wantLens)
	}
	if got := requests(); got[len(got)-1] != "PUT "+toolsetUploadsPath+"/up-1/complete" {
		t.Errorf("requests = %v, want the upload completed last", got)
	}
}

func TestToolsetNotReady(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&HTTPError{StatusCode: http.StatusUnprocessableEntity, Body: `{"message":"toolset is not ready for configuration"}`}, false},
		{&HTTPError{StatusCode: http.StatusConflict, Body: refusal(entplatform.ToolsetNotDiscoveredMessage + "; wait until its discoveryStatus is ready")}, true},
		{&HTTPError{StatusCode: http.StatusConflict, Body: `{"message":"Built-in toolsets cannot be changed"}`}, false},
		{&HTTPError{StatusCode: http.StatusInternalServerError, Body: refusal(entplatform.ToolsetNotDiscoveredMessage)}, false},
		{&HTTPError{StatusCode: http.StatusConflict, Body: "not json"}, false},
	} {
		if got := toolsetNotReady(tc.err); got != tc.want {
			t.Errorf("toolsetNotReady(%v) = %t, want %t", tc.err, got, tc.want)
		}
	}
}

func refusal(message string) string {
	raw, _ := json.Marshal(map[string]string{"message": message})
	return string(raw)
}

func TestPatchToolsetConfig_V2SendsAPatchToDropUndeclaredSettings(t *testing.T) {
	tools := []apiv2.ToolsetTool{toolWithConfigKeys("lookup", "region")}
	var patched string
	c, _ := v2Platform(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"values":{"lookup":{"region":"eu","retired":"x"}},"updatedBy":null,"updatedTime":null}}`))
		case http.MethodPatch:
			raw, _ := io.ReadAll(r.Body)
			patched = string(raw)
			_, _ = w.Write([]byte(`{"data":{"values":{"lookup":{"region":"eu"}},"updatedBy":null,"updatedTime":null}}`))
		}
	})
	if err := c.PatchToolsetConfig(t.Context(), "ts-1", tools, map[string]any{"lookup": map[string]any{"region": "eu"}}); err != nil {
		t.Fatal(err)
	}
	if patched != `{"values":{}}` {
		t.Errorf("PATCH body = %q, want an empty values object", patched)
	}
}

func TestPlanToolsets_UpdateClearsARemovedDescription(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{Name: "demo", SourceFile: filepath.Join(dir, "manifest.yaml"), SourceDir: dir}}
	actual := []wire.ToolsetResponse{{ID: "id-1", Name: "demo", Description: new("Old description")}}
	plan, err := planToolsets(t.Context(), nil, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("updates = %+v, want one", plan.Updates)
	}
	update := plan.Updates[0].Update
	if !update.Description.Set || !update.Description.Null || update.Name.Set {
		t.Errorf("update = %+v, want description sent as null and the name left out", update)
	}
}

func TestPlanSkills_Update(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "SKILL.md", "---\nname: x\n---\n# x\n")
	stored := []wire.SkillResponse{{ID: "id-1", Name: "x", Description: new("Old description")}}

	plan := planOneDescribedSkill(t, dir, "x", "New description", stored)
	update := plan.Updates[0].Update
	if !update.Description.Set || update.Description.Value != "New description" || update.Name.Set {
		t.Errorf("update = %+v, want only the new description", update)
	}
}

func TestPlanSkills_RemovedDescriptionReuploads(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "SKILL.md", "---\nname: x\n---\n# x\n")
	bundleHash := planOneDescribedSkill(t, dir, "x", "", nil).Creates[0].Desired.bundleHash

	for name, bundle := range map[string]*apiv2.SkillBundle{
		"the bundle changed":   nil,
		"the bundle is stored": {ContentHash: new(bundleHash)},
	} {
		t.Run(name, func(t *testing.T) {
			stored := []wire.SkillResponse{{ID: "id-1", Name: "x", Description: new("Old description"), Bundle: bundle}}
			plan := planOneDescribedSkill(t, dir, "x", "", stored)
			if len(plan.Updates) != 1 {
				t.Fatalf("plan = %+v, want one update", plan)
			}
			if op := plan.Updates[0]; op.Update != (wire.UpdateSkillRequest{}) || !op.UploadNeeded {
				t.Errorf("update = %+v, upload = %t; want no PATCH body and the bundle uploaded", op.Update, op.UploadNeeded)
			}
		})
	}

	t.Run("a description never set plans nothing", func(t *testing.T) {
		stored := []wire.SkillResponse{{ID: "id-1", Name: "x", Bundle: &apiv2.SkillBundle{ContentHash: new(bundleHash)}}}
		if plan := planOneDescribedSkill(t, dir, "x", "", stored); len(plan.Unchanged) != 1 {
			t.Errorf("plan = %+v, want the skill unchanged", plan)
		}
	})
}

func planOneDescribedSkill(t *testing.T, dir, name, desc string, actual []wire.SkillResponse) *skillPlan {
	t.Helper()
	desired := []resolve.ResolvedSkill{{Name: name, Description: desc, SourceFile: filepath.Join(dir, "SKILL.md"), BundleDir: dir}}
	plan, err := planSkills(t.Context(), desired, actual, t.TempDir(), BuildOptions{})
	if err != nil {
		t.Fatalf("planSkills: %v", err)
	}
	return plan
}
