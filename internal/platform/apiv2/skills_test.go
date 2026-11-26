// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

const skillAPIKeySecret = "sk-skill-secret-the-test-supplies"

type skillAPI struct {
	*toolsetAPI
}

func newSkillAPI(t *testing.T) *skillAPI {
	t.Helper()
	return &skillAPI{newToolsetAPI(t)}
}

func skillZip(t *testing.T, name string) []byte {
	t.Helper()
	return zipOf(t, map[string]string{
		"SKILL.md":            "---\nname: " + name + "\ndescription: Skill the test builds.\n---\n# " + name + "\n",
		"references/guide.md": "# Guide\n",
		"assets/logo.txt":     "logo",
	})
}

func uploadForm(a *entrypointAPI, method, path, filename string, bundle []byte, fields map[string]string, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := mw.WriteField(name, value); err != nil {
			a.t.Fatal(err)
		}
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		a.t.Fatal(err)
	}
	_, _ = part.Write(bundle)
	if err := mw.Close(); err != nil {
		a.t.Fatal(err)
	}
	req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if len(scopes) > 0 {
		req.Header.Set("X-SAM-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *skillAPI) create(name string) Skill {
	a.t.Helper()
	rec := uploadForm(a.entrypointAPI, http.MethodPost, "/skills", name+".zip", skillZip(a.t, name), map[string]string{"description": "Look up orders."})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[Skill](a.t, rec)
	expectLocation(a.t, rec, "/skills/"+created.ID)
	return created
}

func (a *skillAPI) createReady(name string) Skill {
	a.t.Helper()
	sk := a.create(name)
	a.deps.SkillSvc.UpsertFromInit(a.t.Context(), remotetool.SkillInitParams{
		SkillName:   name,
		Description: "Look up orders.",
		BundledTools: []remotetool.SkillBundledTool{{
			ToolName:       name + "__find_order",
			Description:    "Find an order.",
			Instructions:   "Call first.",
			Parameters:     map[string]any{"type": "object"},
			ArtifactParams: map[string]remotetool.ArtifactParamInfo{"report": {IsList: true, IsOptional: true, FilenameKey: "filename"}},
			ConfigSchema: []map[string]any{
				{"key": "api_key", "type": "string", "secret": true, "required": true},
				{"key": "region", "type": "string"},
			},
		}},
		ReferenceCount: 1,
		AssetCount:     1,
	})
	got := decodeData[Skill](a.t, a.call(http.MethodGet, "/skills/"+sk.ID, nil))
	if got.DiscoveryStatus != "ready" {
		a.t.Fatalf("discoveryStatus %q after the tool runtime reported, want ready", got.DiscoveryStatus)
	}
	return got
}

func TestSkills_Lifecycle(t *testing.T) {
	api := newSkillAPI(t)
	created := api.create("order-lookup")
	if created.Type != "skill" || created.Origin != originCustom || created.Name != "order-lookup" ||
		created.Description == nil || *created.Description != "Look up orders." {
		t.Errorf("created %+v", created)
	}
	if created.Bundle == nil || created.Bundle.ContentHash == nil || created.DiscoveryStatus != "pending" {
		t.Errorf("a created skill has its bundle and awaits discovery: bundle %+v, status %q", created.Bundle, created.DiscoveryStatus)
	}
	iso := regexp.MustCompile(isoMillisUTC)
	if b, _ := json.Marshal(created.CreatedTime); !iso.Match(bytes.Trim(b, `"`)) {
		t.Errorf("createdTime %s is not ISO 8601 ms UTC", b)
	}

	rec := api.call(http.MethodGet, "/skills/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	keys := rawKeys(t, rec)
	for _, dropped := range []string{"createdAt", "updatedAt", "contentHash"} {
		if _, ok := keys[dropped]; ok {
			t.Errorf("v1 field %q is on the v2 skill", dropped)
		}
	}
	if !strings.Contains(string(keys["bundle"]), `"contentHash":"`) {
		t.Errorf("bundle %s does not carry the uploaded ZIP's contentHash", keys["bundle"])
	}
	if string(keys["origin"]) == `"platform"` {
		t.Error(`origin is still v1's "platform"`)
	}
	for _, field := range []string{"tags", "bundledTools", "toolsets", "agents"} {
		if string(keys[field]) == "null" {
			t.Errorf("%s is null, want a list", field)
		}
	}

	rec = api.call(http.MethodPatch, "/skills/"+created.ID, map[string]any{"description": "Explain orders."})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Skill](t, rec); got.Description == nil || *got.Description != "Explain orders." || got.Name != "order-lookup" {
		t.Errorf("after patch: %+v", got)
	}
	for _, bad := range []any{nil, ""} {
		expectDetail(t, api.call(http.MethodPatch, "/skills/"+created.ID, map[string]any{"description": bad}), http.StatusUnprocessableEntity, "description")
	}
	rec = api.call(http.MethodPatch, "/skills/"+created.ID, map[string]any{"name": "renamed", "description": ""})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	if len(details["name"]) != 1 || details["name"][0] != "cannot be changed" || len(details["description"]) != 1 {
		t.Errorf("one 422 should list every problem, got %v", details)
	}

	expectStatus(t, api.call(http.MethodDelete, "/skills/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/skills/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/skills/"+created.ID, nil), http.StatusNotFound)
}

func TestSkills_CreateValidation(t *testing.T) {
	api := newSkillAPI(t)
	bundle := skillZip(t, "order-lookup")
	expectDetail(t, uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "skill.tar", bundle, nil), http.StatusUnprocessableEntity, "file")
	expectDetail(t, uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "skill.zip", []byte("not a zip"), nil), http.StatusUnprocessableEntity, "file")
	expectDetail(t, uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "skill.zip", bundle, map[string]string{"extra": "x"}), http.StatusBadRequest, "extra")
	expectStatus(t, api.call(http.MethodPost, "/skills", `{"description":"x"}`), http.StatusBadRequest)

	api.create("order-lookup")
	expectStatus(t, uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "skill.zip", bundle, nil), http.StatusConflict)
}

func TestSkills_ListPutsBuiltinsFirstAndFiltersByOrigin(t *testing.T) {
	api := newSkillAPI(t)
	custom := api.create("order-lookup")

	all := decodeList[Skill](t, api.call(http.MethodGet, "/skills?pageSize=100", nil))
	if len(all.Data) < 2 || all.Data[0].Origin != originBuiltin || all.Data[len(all.Data)-1].ID != custom.ID {
		t.Fatalf("want built-ins first and the custom skill last, got %d items", len(all.Data))
	}
	if all.Meta.Pagination.Count != len(all.Data) {
		t.Errorf("count %d, want %d", all.Meta.Pagination.Count, len(all.Data))
	}
	customs := decodeList[Skill](t, api.call(http.MethodGet, "/skills?origin=custom", nil))
	if len(customs.Data) != 1 || customs.Data[0].ID != custom.ID {
		t.Errorf("origin=custom listed %+v", customs.Data)
	}
	builtins := decodeList[Skill](t, api.call(http.MethodGet, "/skills?origin=builtin&pageSize=100", nil))
	if len(builtins.Data) != len(all.Data)-1 {
		t.Errorf("origin=builtin listed %d, want %d", len(builtins.Data), len(all.Data)-1)
	}
	for _, b := range builtins.Data {
		if b.Origin != originBuiltin || b.CreatedBy != nil || b.UpdatedBy != nil || b.Bundle != nil || b.DiscoveryStatus != "ready" {
			t.Errorf("built-in %s: %+v", b.ID, b)
		}
	}
	expectDetail(t, api.call(http.MethodGet, "/skills?origin=platform", nil), http.StatusUnprocessableEntity, "origin")
}

func TestSkills_Paging(t *testing.T) {
	api := newSkillAPI(t)
	first := decodeList[Skill](t, api.call(http.MethodGet, "/skills", nil))
	total := first.Meta.Pagination.Count
	if first.Meta.Pagination.PageSize != defaultPageSize || len(first.Data) != min(total, defaultPageSize) {
		t.Errorf("default page: size %d, %d items of %d", first.Meta.Pagination.PageSize, len(first.Data), total)
	}
	if past := decodeList[Skill](t, api.call(http.MethodGet, "/skills?pageNumber=1000", nil)); len(past.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(past.Data))
	}
	expectStatus(t, api.call(http.MethodGet, "/skills?pageSize=100", nil), http.StatusOK)
	expectDetail(t, api.call(http.MethodGet, "/skills?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestSkills_BuiltinsAreFetchableAndFixed(t *testing.T) {
	api := newSkillAPI(t)
	builtins := decodeList[Skill](t, api.call(http.MethodGet, "/skills?origin=builtin&pageSize=100", nil))
	if len(builtins.Data) == 0 {
		t.Fatal("no built-in skills listed")
	}
	for _, b := range builtins.Data {
		rec := api.call(http.MethodGet, "/skills/"+b.ID, nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeData[Skill](t, rec); got.ID != b.ID || got.Origin != originBuiltin {
			t.Errorf("GET %s = %+v", b.ID, got)
		}
	}

	id := builtins.Data[0].ID
	for _, write := range []*httptest.ResponseRecorder{
		api.call(http.MethodPatch, "/skills/"+id, map[string]any{"description": "mine"}),
		api.call(http.MethodDelete, "/skills/"+id, nil),
		api.call(http.MethodGet, "/skills/"+id+"/bundle", nil),
		api.upload(http.MethodPut, "/skills/"+id+"/bundle", "x.zip", skillZip(t, id)),
		api.call(http.MethodPatch, "/skills/"+id+"/config", map[string]any{"values": map[string]any{}}),
	} {
		expectStatus(t, write, http.StatusConflict)
		decodeError(t, write)
	}

	files := api.call(http.MethodGet, "/skills/"+id+"/files", nil)
	expectStatus(t, files, http.StatusOK)
	if got := decodeList[SkillFile](t, files); len(got.Data) != 0 {
		t.Errorf("a built-in has no files, got %+v", got.Data)
	}
	cfg := api.call(http.MethodGet, "/skills/"+id+"/config", nil)
	expectStatus(t, cfg, http.StatusOK)
	if got := decodeData[SkillConfiguration](t, cfg); got.Values == nil || len(got.Values) != 0 || got.UpdatedBy != nil {
		t.Errorf("a built-in has no configuration, got %+v", got)
	}
	diff := api.call(http.MethodPut, "/skills/"+id+"/secretsDiff", map[string]any{"values": map[string]any{"k": "v"}})
	expectStatus(t, diff, http.StatusOK)
	if got := decodeData[SecretsDiffResult](t, diff); got.Secrets == nil || len(got.Secrets) != 0 {
		t.Errorf("a built-in has no secrets, got %+v", got)
	}
	expectStatus(t, api.call(http.MethodGet, "/skills/"+id+"/fileContent?path=SKILL.md", nil), http.StatusNotFound)

	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodGet, "/skills/no-such-builtin", nil),
		api.call(http.MethodDelete, "/skills/no-such-builtin", nil),
		api.call(http.MethodGet, "/skills/no-such-builtin/files", nil),
		api.call(http.MethodGet, "/skills/no-such-builtin/fileContent?path=SKILL.md", nil),
		api.call(http.MethodGet, "/skills/no-such-builtin/config", nil),
	} {
		expectStatus(t, rec, http.StatusNotFound)
	}
	for _, bad := range []string{"Not-A-UUID", "a.b"} {
		expectDetail(t, api.call(http.MethodGet, "/skills/"+bad, nil), http.StatusBadRequest, "id")
		expectDetail(t, api.call(http.MethodDelete, "/skills/"+bad, nil), http.StatusBadRequest, "id")
	}
	expectStatus(t, api.call(http.MethodGet, "/skills/"+missingID, nil), http.StatusNotFound)
}

func TestSkills_Bundle(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.create("order-lookup")

	download := api.call(http.MethodGet, "/skills/"+sk.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	if download.Header().Get("Content-Type") != "application/zip" ||
		download.Header().Get("Content-Disposition") != "attachment; filename*=UTF-8''order-lookup.zip" || download.Body.Len() == 0 {
		t.Errorf("download: %d bytes, headers %v", download.Body.Len(), download.Header())
	}

	replacement := zipOf(t, map[string]string{"SKILL.md": "---\nname: order-lookup\ndescription: New.\n---\n", "references/new.md": "new"})
	rec := api.upload(http.MethodPut, "/skills/"+sk.ID+"/bundle", "order-lookup.zip", replacement)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[Skill](t, rec)
	if got.ID != sk.ID || got.DiscoveryStatus != "pending" || got.Bundle == nil || got.Bundle.ContentHash == nil || *got.Bundle.ContentHash == *sk.Bundle.ContentHash {
		t.Errorf("after replace: %+v", got)
	}

	expectDetail(t, api.upload(http.MethodPut, "/skills/"+sk.ID+"/bundle", "other.zip", skillZip(t, "other-skill")), http.StatusUnprocessableEntity, "file")
	expectDetail(t, api.upload(http.MethodPut, "/skills/"+sk.ID+"/bundle", "skill.tar", replacement), http.StatusUnprocessableEntity, "file")
	notZip := api.upload(http.MethodPut, "/skills/"+sk.ID+"/bundle", "skill.zip", []byte("not a zip"))
	expectStatus(t, notZip, http.StatusUnprocessableEntity)
	if problems := decodeError(t, notZip).ValidationDetails["file"]; len(problems) != 1 || problems[0] != "invalid ZIP file" {
		t.Errorf("file details %v, want SAM's sentence only", problems)
	}
	if strings.Contains(notZip.Body.String(), "zip:") {
		t.Errorf("the archive reader's own error reached the wire: %s", notZip.Body.String())
	}
	expectStatus(t, api.upload(http.MethodPut, "/skills/"+missingID+"/bundle", "x.zip", replacement), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/skills/"+missingID+"/bundle", nil), http.StatusNotFound)
}

func TestSkills_BundleMissingIs409(t *testing.T) {
	api := newSkillAPI(t)
	created, err := api.deps.SkillSvc.Create(t.Context(), entplatform.CreateSkillRequest{Name: "empty-skill"}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	rec := api.call(http.MethodGet, "/skills/"+created.ID+"/bundle", nil)
	expectStatus(t, rec, http.StatusConflict)
	if msg := decodeError(t, rec).Message; msg != "The skill has no bundle yet; upload one first" {
		t.Errorf("message %q", msg)
	}
}

func TestSkills_BundleObjectMissingIs409(t *testing.T) {
	api := newSkillAPI(t)
	created, err := api.deps.SkillSvc.Create(t.Context(), entplatform.CreateSkillRequest{Name: "lost-bundle"}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	api.deps.SkillSvc.UpsertFromInit(t.Context(), remotetool.SkillInitParams{SkillName: "lost-bundle", Description: "Lost its bundle."})
	got := decodeData[Skill](t, api.call(http.MethodGet, "/skills/"+created.ID, nil))
	if got.DiscoveryStatus == skillStatusCreated {
		t.Fatalf("discoveryStatus %q, want past created so the object store is consulted", got.DiscoveryStatus)
	}
	rec := api.call(http.MethodGet, "/skills/"+created.ID+"/bundle", nil)
	expectStatus(t, rec, http.StatusConflict)
	if msg := decodeError(t, rec).Message; msg != "The skill has no bundle yet; upload one first" {
		t.Errorf("message %q", msg)
	}
}

func TestSkills_Files(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.create("order-lookup")

	all := decodeList[SkillFile](t, api.call(http.MethodGet, "/skills/"+sk.ID+"/files", nil))
	if len(all.Data) != 2 || all.Data[0].Path != "references/guide.md" || all.Data[0].Category != "reference" ||
		all.Data[1].Path != "assets/logo.txt" || all.Data[1].Category != "asset" || all.Data[1].SizeBytes != 4 {
		t.Errorf("files %+v", all.Data)
	}
	assets := decodeList[SkillFile](t, api.call(http.MethodGet, "/skills/"+sk.ID+"/files?category=asset", nil))
	if len(assets.Data) != 1 || assets.Data[0].Category != "asset" {
		t.Errorf("category=asset listed %+v", assets.Data)
	}
	paged := decodeList[SkillFile](t, api.call(http.MethodGet, "/skills/"+sk.ID+"/files?pageSize=1&pageNumber=2", nil))
	if len(paged.Data) != 1 || paged.Data[0].Path != "assets/logo.txt" || paged.Meta.Pagination.Count != 2 {
		t.Errorf("page 2 of 1: %+v", paged)
	}
	expectDetail(t, api.call(http.MethodGet, "/skills/"+sk.ID+"/files?category=tool", nil), http.StatusUnprocessableEntity, "category")
	expectStatus(t, api.call(http.MethodGet, "/skills/"+missingID+"/files", nil), http.StatusNotFound)
}

func TestSkills_FileContent(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.create("order-lookup")

	rec := api.call(http.MethodGet, "/skills/"+sk.ID+"/fileContent?path=references/guide.md", nil)
	expectStatus(t, rec, http.StatusOK)
	h := rec.Header()
	if rec.Body.String() != "# Guide\n" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "inline; filename*=UTF-8''guide.md") {
		t.Errorf("body %q, headers %v", rec.Body.String(), h)
	}
	expectDetail(t, api.call(http.MethodGet, "/skills/"+sk.ID+"/fileContent?path=tools/x", nil), http.StatusUnprocessableEntity, "path")
	expectDetail(t, api.call(http.MethodGet, "/skills/"+sk.ID+"/fileContent", nil), http.StatusUnprocessableEntity, "path")
	expectStatus(t, api.call(http.MethodGet, "/skills/"+sk.ID+"/fileContent?path=references/missing.md", nil), http.StatusNotFound)
}

func TestSkills_ReadsRedactSkillMDCredentials(t *testing.T) {
	api := newSkillAPI(t)
	const searchKeySplitForScanners = "AIza" + "FAKEKEY0000000000000000000000000000"
	withSecret := zipOf(t, map[string]string{
		"SKILL.md": "---\nname: web-search\ndescription: Search the web.\ntools:\n" +
			"  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n" +
			"      google_search_api_key: " + searchKeySplitForScanners + "\n---\n# web-search\n",
		"references/guide.md": "# Guide\n",
	})
	rec := uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "web-search.zip", withSecret, nil)
	expectStatus(t, rec, http.StatusCreated)
	sk := decodeData[Skill](t, rec)

	fileContent := api.call(http.MethodGet, "/skills/"+sk.ID+"/fileContent?path=SKILL.md", nil)
	expectStatus(t, fileContent, http.StatusOK)
	expectRedacted(t, "fileContent SKILL.md", fileContent.Body.String(), searchKeySplitForScanners)

	download := api.call(http.MethodGet, "/skills/"+sk.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	zr, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if err != nil {
		t.Fatalf("bundle is not a ZIP: %v", err)
	}
	manifest, err := zr.Open("SKILL.md")
	if err != nil {
		t.Fatalf("bundle has no SKILL.md: %v", err)
	}
	defer func() { _ = manifest.Close() }()
	content, err := io.ReadAll(manifest)
	if err != nil {
		t.Fatal(err)
	}
	expectRedacted(t, "bundle SKILL.md", string(content), searchKeySplitForScanners)
}

func expectRedacted(t *testing.T, what, served, secret string) {
	t.Helper()
	if strings.Contains(served, secret) || !strings.Contains(served, skillsecret.RedactedSentinel) {
		t.Errorf("%s is not redacted:\n%s", what, served)
	}
}

func TestSkills_ConfigurationAndSecrets(t *testing.T) {
	api := newSkillAPI(t)
	pending := api.create("pending-skill")
	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodGet, "/skills/"+pending.ID+"/config", nil),
		api.call(http.MethodPatch, "/skills/"+pending.ID+"/config", map[string]any{"values": map[string]any{}}),
		api.call(http.MethodPut, "/skills/"+pending.ID+"/secretsDiff", map[string]any{"values": map[string]any{}}),
	} {
		expectStatus(t, rec, http.StatusConflict)
		if msg := decodeError(t, rec).Message; msg != msgSkillNotReady {
			t.Errorf("message %q, want %q", msg, msgSkillNotReady)
		}
	}

	ready := api.createReady("order-lookup")
	if ready.ConfigSummary == nil || !ready.ConfigSummary.RequiresAgentConfig {
		t.Fatalf("ready skill summary %+v", ready.ConfigSummary)
	}

	empty := api.call(http.MethodGet, "/skills/"+ready.ID+"/config", nil)
	expectStatus(t, empty, http.StatusOK)
	if keys := rawKeys(t, empty); string(keys["values"]) != "{}" || string(keys["updatedBy"]) != "null" || string(keys["updatedTime"]) != "null" {
		t.Errorf("unset configuration = %s", empty.Body.String())
	}

	rec := api.call(http.MethodPatch, "/skills/"+ready.ID+"/config", map[string]any{
		"values": map[string]any{"find_order": map[string]any{"api_key": skillAPIKeySecret, "region": "us-east-1"}},
	})
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), skillAPIKeySecret) {
		t.Fatalf("the secret came back: %s", rec.Body.String())
	}
	cfg := decodeData[SkillConfiguration](t, rec)
	if cfg.Values["find_order"]["api_key"] != redacted || cfg.Values["find_order"]["region"] != "us-east-1" || cfg.UpdatedBy == nil || cfg.UpdatedTime == 0 {
		t.Errorf("configuration after patch: %+v", cfg)
	}
	expectDetail(t, api.call(http.MethodPatch, "/skills/"+ready.ID+"/config", map[string]any{"values": nil}), http.StatusUnprocessableEntity, "values")
	unchanged := api.call(http.MethodPatch, "/skills/"+ready.ID+"/config", map[string]any{})
	expectStatus(t, unchanged, http.StatusOK)
	if strings.Contains(unchanged.Body.String(), skillAPIKeySecret) {
		t.Errorf("the secret came back: %s", unchanged.Body.String())
	}

	diff := func(secret string) SecretsDiffResult {
		rec := api.call(http.MethodPut, "/skills/"+ready.ID+"/secretsDiff", map[string]any{"values": map[string]any{"find_order": map[string]any{"api_key": secret}}})
		expectStatus(t, rec, http.StatusOK)
		return decodeData[SecretsDiffResult](t, rec)
	}
	if !diff(skillAPIKeySecret).Secrets["find_order.api_key"].Matches {
		t.Error("the stored secret should match")
	}
	if diff("sk-other").Secrets["find_order.api_key"].Matches {
		t.Error("a different secret should not match")
	}
	expectDetail(t, api.call(http.MethodPut, "/skills/"+ready.ID+"/secretsDiff", map[string]any{}), http.StatusUnprocessableEntity, "values")
	expectStatus(t, api.call(http.MethodPut, "/skills/"+missingID+"/secretsDiff", map[string]any{"values": map[string]any{}}), http.StatusNotFound)
}

func TestSkills_BundledToolsAreCamelCase(t *testing.T) {
	api := newSkillAPI(t)
	ready := api.createReady("order-lookup")
	rec := api.call(http.MethodGet, "/skills/"+ready.ID, nil)
	var body struct {
		Data struct {
			BundledTools []map[string]json.RawMessage `json:"bundledTools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Data.BundledTools) != 1 {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	tool := body.Data.BundledTools[0]
	for _, present := range []string{"toolName", "description", "instructions", "parameters", "artifactParams", "configSchema"} {
		if _, ok := tool[present]; !ok {
			t.Errorf("bundled tool lacks %q: %v", present, tool)
		}
	}
	for _, absent := range []string{"tool_name", "artifact_params", "config_schema"} {
		if _, ok := tool[absent]; ok {
			t.Errorf("v1 key %q is on the v2 bundled tool", absent)
		}
	}
	var params map[string]SkillArtifactParam
	if err := json.Unmarshal(tool["artifactParams"], &params); err != nil || !params["report"].IsList || !params["report"].IsOptional ||
		params["report"].FilenameKey == nil || *params["report"].FilenameKey != "filename" {
		t.Errorf("artifactParams %s: %v", tool["artifactParams"], err)
	}
}

func TestSkills_Scopes(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.create("order-lookup")
	read := rbac.ScopeSkillRead
	expectStatus(t, api.call(http.MethodGet, "/skills", nil, read), http.StatusOK)
	for _, sub := range []string{"", "/bundle", "/config", "/files", "/fileContent?path=SKILL.md"} {
		path := "/skills/" + sk.ID + sub
		if rec := api.call(http.MethodGet, path, nil, read); rec.Code == http.StatusForbidden {
			t.Errorf("GET %s with %s = 403, want it allowed", path, read)
		}
		if rec := api.call(http.MethodGet, path, nil, rbac.ScopeToolsetRead); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s with only %s = %d, want 403", path, rbac.ScopeToolsetRead, rec.Code)
		}
	}
	for _, rec := range []*httptest.ResponseRecorder{
		uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "x.zip", skillZip(t, "other-skill"), nil, read),
		api.call(http.MethodPatch, "/skills/"+sk.ID, map[string]any{"description": "x"}, read),
		api.call(http.MethodDelete, "/skills/"+sk.ID, nil, read),
		api.upload(http.MethodPut, "/skills/"+sk.ID+"/bundle", "x.zip", skillZip(t, "order-lookup"), read),
		api.call(http.MethodPatch, "/skills/"+sk.ID+"/config", map[string]any{"values": map[string]any{}}, read),
		api.call(http.MethodPut, "/skills/"+sk.ID+"/secretsDiff", map[string]any{"values": map[string]any{}}, read),
		api.call(http.MethodGet, "/skills", nil, rbac.ScopeToolsetRead),
	} {
		expectStatus(t, rec, http.StatusForbidden)
	}
}

func TestSkills_BundleRedactionRefusalKeepsTheSharedSentence(t *testing.T) {
	api := newSkillAPI(t)
	manifest := "---\nname: anchored\ndescription: Uses a YAML anchor.\ntools:\n  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n      google_cse_id: &k sk-not-a-real-key\n      google_search_api_key: *k\n---\nbody\n"
	created := api.upload(http.MethodPost, "/skills", "anchored.zip", zipOf(t, map[string]string{"SKILL.md": manifest}))
	expectStatus(t, created, http.StatusCreated)
	sk := decodeData[Skill](t, created)

	rec := api.call(http.MethodGet, "/skills/"+sk.ID+"/bundle", nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if msg := decodeError(t, rec).Message; msg != skillsecret.RedactionRefusedMessage {
		t.Errorf("message %q, want %q", msg, skillsecret.RedactionRefusedMessage)
	}
	if strings.Contains(rec.Body.String(), "sk-not-a-real-key") {
		t.Errorf("the refusal carried the secret: %s", rec.Body.String())
	}
}
