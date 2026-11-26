// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstantiate_BindsAndPinsDataInput(t *testing.T) {
	sc := buildTemplateSkill(t)
	store := newFakeStore()
	store.put("upstream_rows.json", "application/json", []byte(`[{"month":"Jan","revenue":100}]`))

	res, findings, err := InstantiateSkillAsset(context.Background(), sc, "report.html.samt", "",
		map[string]string{"prepared_by": "Ed"},
		InstantiateDeps{Store: store, Resolve: cleanResolve, DataInputs: map[string]string{"sales_rows": "upstream_rows.json"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if res.Filename != "quarterly_report.html" {
		t.Fatalf("res = %+v", res)
	}
	if _, staged := store.history["sales_data.json"]; staged {
		t.Error("binding must not create the declared filename anymore")
	}
	if len(store.saved) != 1 {
		t.Errorf("only the output document should be written, saved = %v", store.saved)
	}
	body := string(store.saved["quarterly_report.html"].content)
	if !strings.Contains(body, `data="upstream_rows.json:1"`) {
		t.Errorf("data reference not rewritten to the pinned bound source: %s", body)
	}
	if strings.Contains(body, "sales_data.json") {
		t.Errorf("declared filename should be fully rewritten out of the document: %s", body)
	}
}

func TestInstantiate_BindingGuards(t *testing.T) {
	cases := []struct {
		name      string
		bindings  map[string]string
		setup     func(*fakeStore)
		wantInDet string
	}{
		{
			name:      "undeclared binding",
			bindings:  map[string]string{"not_a_binding": "upstream.json"},
			setup:     func(s *fakeStore) { s.put("upstream.json", "application/json", []byte(`[]`)) },
			wantInDet: "not declared in the template's sidecar",
		},
		{
			name:      "source artifact missing",
			bindings:  map[string]string{"sales_rows": "nope.json"},
			setup:     func(s *fakeStore) {},
			wantInDet: `"nope.json" not found in scope`,
		},
		{
			name:      "bound version does not exist",
			bindings:  map[string]string{"sales_rows": "upstream.json:9"},
			setup:     func(s *fakeStore) { s.put("upstream.json", "application/json", []byte(`[]`)) },
			wantInDet: `"upstream.json:9"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := buildTemplateSkill(t)
			store := newFakeStore()
			tc.setup(store)
			res, findings, err := InstantiateSkillAsset(context.Background(), sc, "report.html.samt", "",
				map[string]string{"prepared_by": "Ed"},
				InstantiateDeps{Store: store, Resolve: cleanResolve, DataInputs: tc.bindings})
			if err != nil {
				t.Fatalf("unexpected hard error: %v", err)
			}
			if res != nil {
				t.Fatalf("expected no result, got %+v", res)
			}
			if !hasFinding(findings, errKindStaging, tc.wantInDet) {
				t.Fatalf("want staging finding ~=%q, got %+v", tc.wantInDet, findings)
			}
			if len(store.saved) != 0 {
				t.Errorf("fail-closed violated: %d artifacts saved", len(store.saved))
			}
		})
	}
}

func TestInstantiate_BindingToInputWithoutDeclaredArtifact(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	asset := writeTemplateAsset(t, skillDir, "doc.md", "body", `
template:
  file: doc.md
  mime_type: text/markdown
data_inputs:
  payload:
    description: no artifact filename declared
`)
	sc := SkillContext{Name: "s", LocalPath: skillDir}

	store := newFakeStore()
	store.put("source.json", "application/json", []byte(`{}`))
	_, findings, err := InstantiateSkillAsset(context.Background(), sc, asset, "", nil,
		InstantiateDeps{Store: store, Resolve: cleanResolve, DataInputs: map[string]string{"payload": "source.json"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !hasFinding(findings, errKindStaging, "declares no `artifact` filename") {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestInstantiate_PlainCopyRejectsDataInputs(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	writeFile(t, filepath.Join(skillDir, "assets", "logo.txt"), "x")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	_, _, err := InstantiateSkillAsset(context.Background(), sc, "logo.txt", "", nil,
		InstantiateDeps{Store: newFakeStore(), Resolve: cleanResolve, DataInputs: map[string]string{"k": "v"}})
	if err == nil || !strings.Contains(err.Error(), "data_inputs are only valid for templates") {
		t.Fatalf("expected data_inputs-rejected error, got %v", err)
	}
}

type errStore struct {
	*fakeStore
	loadErrOn string
	saveErrOn string
}

func (e *errStore) Load(ctx context.Context, filename string, version *int) ([]byte, string, int, error) {
	if filename == e.loadErrOn {
		return nil, "", 0, context.DeadlineExceeded
	}
	return e.fakeStore.Load(ctx, filename, version)
}

func (e *errStore) Save(ctx context.Context, filename string, content []byte, mimeType, description string) (int, error) {
	if filename == e.saveErrOn {
		return 0, context.DeadlineExceeded
	}
	return e.fakeStore.Save(ctx, filename, content, mimeType, description)
}

func TestInstantiate_StoreErrors(t *testing.T) {
	t.Run("bound source load error is a finding", func(t *testing.T) {
		sc := buildTemplateSkill(t)
		store := &errStore{fakeStore: newFakeStore(), loadErrOn: "upstream.json"}
		store.put("upstream.json", "application/json", []byte(`[]`))
		_, findings, err := InstantiateSkillAsset(context.Background(), sc, "report.html.samt", "",
			map[string]string{"prepared_by": "Ed"},
			InstantiateDeps{Store: store, Resolve: cleanResolve, DataInputs: map[string]string{"sales_rows": "upstream.json"}})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !hasFinding(findings, errKindStaging, "failed to load source artifact") {
			t.Fatalf("findings = %+v", findings)
		}
	})

	t.Run("output save error is a hard error", func(t *testing.T) {
		sc := buildTemplateSkill(t)
		store := &errStore{fakeStore: newFakeStore(), saveErrOn: "quarterly_report.html"}
		store.put("sales_data.json", "application/json", []byte(`[{"month":"Jan","revenue":100}]`))
		_, _, err := InstantiateSkillAsset(context.Background(), sc, "report.html.samt", "",
			map[string]string{"prepared_by": "Ed"},
			InstantiateDeps{Store: store, Resolve: cleanResolve})
		if err == nil || !strings.Contains(err.Error(), "saving instantiated template") {
			t.Fatalf("expected save error, got %v", err)
		}
	})
}

func TestPrepareCandidate_BindsAndPins(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.put("upstream_rows.json", "application/json", []byte(`[1,2,3]`))

	cand, tmpl, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
		map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream_rows.json"}, store)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if tmpl == nil {
		t.Fatal("tmpl is nil")
	}
	if !strings.Contains(cand, "«artifact_content:upstream_rows.json:1»") {
		t.Errorf("reference not rewritten to pinned bound source: %q", cand)
	}
	if len(store.saved) != 0 {
		t.Errorf("PrepareCandidate must not write artifacts, saved = %v", store.saved)
	}
}

func TestPrepareCandidate_VersionPinning(t *testing.T) {
	ctx := context.Background()

	t.Run("explicit version pins and validates that version", func(t *testing.T) {
		store := newFakeStore()
		store.put("upstream.json", "application/json", []byte(`[1]`))
		store.put("upstream.json", "application/json", []byte(`{"not":"an array"}`))

		cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
			map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream.json:1"}, store)
		if err != nil || len(findings) > 0 {
			t.Fatalf("binding v1 should validate v1's bytes: err=%v findings=%+v", err, findings)
		}
		if !strings.Contains(cand, "«artifact_content:upstream.json:1»") {
			t.Errorf("explicit version not pinned: %q", cand)
		}

		_, _, findings, err = PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
			map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream.json:2"}, store)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !hasFinding(findings, errKindSchema, "not valid JSON") && !hasFinding(findings, errKindSchema, "array") {
			t.Fatalf("binding v2 should fail v2's schema validation, got %+v", findings)
		}
	})

	t.Run("omitted version resolves latest and pins the resolved number", func(t *testing.T) {
		store := newFakeStore()
		store.put("upstream.json", "application/json", []byte(`[1]`))
		store.put("upstream.json", "application/json", []byte(`[1,2]`))

		cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
			map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream.json"}, store)
		if err != nil || len(findings) > 0 {
			t.Fatalf("err=%v findings=%+v", err, findings)
		}
		if !strings.Contains(cand, "«artifact_content:upstream.json:2»") {
			t.Errorf("latest version not pinned: %q", cand)
		}
	})

	t.Run("unbound declared input pins the declared filename's latest", func(t *testing.T) {
		store := newFakeStore()
		store.put("data.json", "application/json", []byte(`[1]`))
		store.put("data.json", "application/json", []byte(`[1,2]`))
		store.put("data.json", "application/json", []byte(`[1,2,3]`))

		cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
			map[string]string{"title": "Q3"}, nil, store)
		if err != nil || len(findings) > 0 {
			t.Fatalf("err=%v findings=%+v", err, findings)
		}
		if !strings.Contains(cand, "«artifact_content:data.json:3»") {
			t.Errorf("unbound declared input not pinned at latest: %q", cand)
		}
	})
}

func TestPrepareCandidate_NoAliasingAcrossInstantiations(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.put("daily.json", "application/json", []byte(`["daily"]`))
	store.put("monthly.json", "application/json", []byte(`["monthly"]`))

	candDaily, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
		map[string]string{"title": "Daily"}, map[string]string{"rows": "daily.json"}, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("daily: err=%v findings=%+v", err, findings)
	}
	candMonthly, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
		map[string]string{"title": "Monthly"}, map[string]string{"rows": "monthly.json"}, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("monthly: err=%v findings=%+v", err, findings)
	}

	if !strings.Contains(candDaily, "«artifact_content:daily.json:1»") {
		t.Errorf("daily document reference = %q", candDaily)
	}
	if !strings.Contains(candMonthly, "«artifact_content:monthly.json:1»") {
		t.Errorf("monthly document reference = %q", candMonthly)
	}
	if _, staged := store.history["data.json"]; staged {
		t.Error("no shared declared-filename artifact may be created")
	}

	store.put("daily.json", "application/json", []byte(`["daily-v2"]`))
	if !strings.Contains(candDaily, "«artifact_content:daily.json:1»") {
		t.Errorf("earlier document lost its version pin: %q", candDaily)
	}
}

func TestPrepareCandidate_ChainedRenameDoesNotCascade(t *testing.T) {
	ctx := context.Background()
	sidecar := `
template:
  file: doc.md
  mime_type: text/markdown
data_inputs:
  a:
    artifact: a.json
  b:
    artifact: b.json
`
	body := "«artifact_content:a.json» and «artifact_content:b.json»"
	store := newFakeStore()
	store.put("b.json", "application/json", []byte(`[1]`))
	store.put("c.json", "application/json", []byte(`[2]`))

	cand, _, findings, err := PrepareCandidate(ctx, []byte(sidecar), body, nil,
		map[string]string{"a": "b.json", "b": "c.json"}, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	want := "«artifact_content:b.json:1» and «artifact_content:c.json:1»"
	if cand != want {
		t.Errorf("chained rename corrupted the rewrite:\n got %q\nwant %q", cand, want)
	}
}

func TestPrepareCandidate_ModifiersPreservedThroughRewrite(t *testing.T) {
	ctx := context.Background()
	body := `<img src="«artifact_content:data.json >>> jsonpath:$.rows >>> format:datauri»">`
	store := newFakeStore()
	store.put("upstream.json", "application/json", []byte(`[1]`))

	cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), addTitle(body),
		map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream.json"}, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	if !strings.Contains(cand, "«artifact_content:upstream.json:1 >>> jsonpath:$.rows >>> format:datauri»") {
		t.Errorf("modifiers not preserved through rewrite: %q", cand)
	}
}

func TestPrepareCandidate_ProseAndSubstringNamesUntouched(t *testing.T) {
	ctx := context.Background()
	body := "see data.json for details «artifact_content:data.json» «artifact_content:mydata.json»"
	store := newFakeStore()
	store.put("data.json", "application/json", []byte(`[1]`))
	store.put("mydata.json", "application/json", []byte(`[2]`))

	cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), addTitle(body),
		map[string]string{"title": "Q3"}, nil, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	if !strings.Contains(cand, "see data.json for details") {
		t.Errorf("prose mention rewritten: %q", cand)
	}
	if !strings.Contains(cand, "«artifact_content:data.json:1»") {
		t.Errorf("declared reference not pinned: %q", cand)
	}
	if !strings.Contains(cand, "«artifact_content:mydata.json»") {
		t.Errorf("substring-named artifact must not be confused with the declared one: %q", cand)
	}
}

func TestPrepareCandidate_PinsReferenceNestedInTemplateBlock(t *testing.T) {
	ctx := context.Background()
	body := "«««template_liquid: data=\"data.json\"\n{% for r in rows %}«artifact_content:data.json»{% endfor %}»»»"
	store := newFakeStore()
	store.put("upstream.json", "application/json", []byte(`[1]`))

	cand, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), addTitle(body),
		map[string]string{"title": "Q3"}, map[string]string{"rows": "upstream.json"}, store)
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	if !strings.Contains(cand, `data="upstream.json:1"`) {
		t.Errorf("block data param not pinned: %q", cand)
	}
	if !strings.Contains(cand, "«artifact_content:upstream.json:1»") {
		t.Errorf("directive nested in block content not pinned: %q", cand)
	}
}

func TestPrepareCandidate_ResidualGuard(t *testing.T) {
	ctx := context.Background()
	sidecar := `
template:
  file: doc.md
  mime_type: text/markdown
data_inputs:
  extra:
    artifact: opt.json
    required: false
`
	body := "«artifact_content:opt.json»"
	_, _, findings, err := PrepareCandidate(ctx, []byte(sidecar), body, nil, nil, newFakeStore())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !hasFinding(findings, errKindResidual, `"opt.json"`) {
		t.Fatalf("want residual_reference finding for opt.json, got %+v", findings)
	}
}

func TestPrepareCandidate_UndeclaredBindingGuard(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.put("upstream.json", "application/json", []byte(`[]`))
	_, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody,
		map[string]string{"title": "Q3"}, map[string]string{"ghost": "upstream.json"}, store)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !hasFinding(findings, errKindStaging, "not declared in the template's sidecar") {
		t.Fatalf("findings = %+v", findings)
	}
}

func addTitle(body string) string { return "<h1>@@title@@</h1>" + body }
