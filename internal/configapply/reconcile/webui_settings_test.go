// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

type webuiSettingsServer struct {
	current wire.WebuiSettingsResponse
	puts    atomic.Int32
	lastPut wire.ReplaceWebuiSettingsRequest
}

func newWebuiSettingsServer(t *testing.T) (*webuiSettingsServer, *Client) {
	t.Helper()
	srv := &webuiSettingsServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/webuiSettings", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, srv.current)
	})
	mux.HandleFunc("PUT /api/v2/platform/webuiSettings", func(w http.ResponseWriter, r *http.Request) {
		var body wire.ReplaceWebuiSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		srv.puts.Add(1)
		srv.lastPut = body
		srv.current = wire.WebuiSettingsResponse{
			AppName: body.AppName, WelcomeMessage: body.WelcomeMessage, CollectFeedback: body.CollectFeedback,
			PublishFeedback: body.PublishFeedback, FeedbackTaskInfoLevel: body.FeedbackTaskInfoLevel,
			DisclaimerText: body.DisclaimerText, LogoURL: body.LogoURL, SmallLogoURL: body.SmallLogoURL,
			SystemPurpose: body.SystemPurpose, ResponseFormat: body.ResponseFormat,
		}
		writeV2Single(w, http.StatusOK, srv.current)
	})
	return srv, newV2PlatformClient(t, mux)
}

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

func TestWebuiSettingsDiff_CoversEveryField(t *testing.T) {
	specType := reflect.TypeFor[manifest.WebuiSettingsSpec]()
	for i := range specType.NumField() {
		field := specType.Field(i)
		yamlName, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if yamlName == "" || yamlName == "-" {
			continue
		}
		t.Run(yamlName, func(t *testing.T) {
			desired := &manifest.WebuiSettingsSpec{}
			set := reflect.ValueOf(desired).Elem().Field(i)
			set.Set(reflect.New(field.Type.Elem()))
			switch field.Type.Elem().Kind() {
			case reflect.Bool:
				set.Elem().SetBool(true)
			case reflect.Int, reflect.Int32, reflect.Int64:
				set.Elem().SetInt(42)
			default:
				set.Elem().SetString("drifted")
			}

			changed := webuiSettingsDiff(&wire.WebuiSettingsResponse{}, desired)
			if len(changed) != 1 || changed[0] != yamlName+" (set)" {
				t.Fatalf("diff = %v, want exactly [%q] — the diff does not cover this field", changed, yamlName+" (set)")
			}
			if webuiSettingsUnchanged(&wire.WebuiSettingsResponse{}, desired) {
				t.Errorf("drift on %s reads as unchanged, so apply would never PUT it", yamlName)
			}
		})
	}
}

func TestPlanAllWebuiSettings_NoPlatformBlockManagesNothing(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme")}

	m, err := manifest.Parse([]byte("kind: manifest\nname: prod\ntarget: { url: http://x }\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := &PlanResult{}
	if err := planAllWebuiSettings(t.Context(), c, m, out); err != nil {
		t.Fatalf("planAllWebuiSettings: %v", err)
	}
	if out.WebuiSettings != nil {
		t.Errorf("plan = %+v; want none — a manifest without a platform: block manages nothing", out.WebuiSettings)
	}
}

func TestPlanAllWebuiSettings_DroppedFromPresentPlatformBlockDeletes(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme")}

	m, err := manifest.Parse([]byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  defaultRoles:
    roles: [sam_user]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := &PlanResult{}
	if err := planAllWebuiSettings(t.Context(), c, m, out); err != nil {
		t.Fatalf("planAllWebuiSettings: %v", err)
	}
	if out.WebuiSettings == nil || out.WebuiSettings.Verb != webuiSettingsVerbDelete {
		t.Errorf("plan = %+v; want a delete", out.WebuiSettings)
	}
}

func TestPlanWebuiSettings_DroppedBlockOnUnconfiguredIsNone(t *testing.T) {
	_, c := newWebuiSettingsServer(t)
	plan, err := planWebuiSettings(t.Context(), c, nil)
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbNone {
		t.Errorf("Verb = %q; want %q", plan.Verb, webuiSettingsVerbNone)
	}
}

func TestPlanWebuiSettings_DroppedBlockPlansDelete(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), CollectFeedback: bp(true)}

	plan, err := planWebuiSettings(t.Context(), c, nil)
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbDelete {
		t.Fatalf("Verb = %q; want %q — a dropped block must remove the stored overrides", plan.Verb, webuiSettingsVerbDelete)
	}

	row, ok := webuiSettingsRow(plan)
	if !ok {
		t.Fatal("expected a plan row for the delete")
	}
	if row.change != platformDeleted {
		t.Errorf("row.change = %v; want platformDeleted so it renders as a removal", row.change)
	}
	for _, want := range []string{"appName", "collectFeedback"} {
		if !strings.Contains(row.label(), want) {
			t.Errorf("row label %q is missing %q", row.label(), want)
		}
	}
	if strings.Contains(row.label(), skipPruneLabel) {
		t.Errorf("row label %q asserts an outcome the plan cannot know", row.label())
	}
}

func TestRenderPlatformApply_SkippedDeleteNamesPrune(t *testing.T) {
	var buf bytes.Buffer
	renderPlatformApply(&buf, nil, nil, &webuiSettingsOutcome{
		Skipped: &webuiSettingsOpResult{Verb: webuiSettingsVerbDelete},
	}, FormatOptions{NoColor: true})

	if !strings.Contains(buf.String(), skipPruneLabel) {
		t.Errorf("apply output %q does not tell the operator the removal needs --prune", buf.String())
	}
}

func TestApplyWebuiSettings_DeleteIsPruneGated(t *testing.T) {
	t.Run("without prune", func(t *testing.T) {
		srv, c := newWebuiSettingsServer(t)
		srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme")}
		plan := &webuiSettingsPlan{Verb: webuiSettingsVerbDelete, Current: &srv.current}

		out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{Prune: false})
		if out == nil || out.Skipped == nil || out.Skipped.Verb != webuiSettingsVerbDelete {
			t.Fatalf("expected a skipped delete; out = %+v", out)
		}
		if srv.puts.Load() != 0 {
			t.Errorf("puts = %d; want 0 — a removal must not happen without --prune", srv.puts.Load())
		}
	})

	t.Run("with prune", func(t *testing.T) {
		srv, c := newWebuiSettingsServer(t)
		srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), CollectFeedback: bp(true)}
		plan := &webuiSettingsPlan{Verb: webuiSettingsVerbDelete, Current: &srv.current}

		out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{Prune: true})
		if out == nil || out.Applied == nil {
			t.Fatalf("expected the delete to be applied; out = %+v", out)
		}
		if srv.puts.Load() != 1 {
			t.Fatalf("puts = %d; want 1", srv.puts.Load())
		}
		if srv.lastPut.AppName != nil || srv.lastPut.CollectFeedback != nil {
			t.Errorf("PUT must send every field null to clear the row; got appName=%v collectFeedback=%v",
				srv.lastPut.AppName, srv.lastPut.CollectFeedback)
		}
	})
}

func TestPlanWebuiSettings_EmptyBlockAgainstUnconfiguredIsUnchanged(t *testing.T) {
	_, c := newWebuiSettingsServer(t)
	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbUnchanged {
		t.Errorf("Verb = %q; want %q", plan.Verb, webuiSettingsVerbUnchanged)
	}
}

func TestPlanWebuiSettings_DriftProducesPut(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Old Name")}

	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{AppName: sp("New Name")})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbPut {
		t.Errorf("Verb = %q; want %q", plan.Verb, webuiSettingsVerbPut)
	}
}

func TestPlanWebuiSettings_Unchanged(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), CollectFeedback: bp(true)}

	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{AppName: sp("Acme"), CollectFeedback: bp(true)})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbUnchanged {
		t.Errorf("Verb = %q; want %q", plan.Verb, webuiSettingsVerbUnchanged)
	}
}

func TestPlanWebuiSettings_EmptyStringDiffersFromNil(t *testing.T) {
	_, c := newWebuiSettingsServer(t)

	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{WelcomeMessage: sp("")})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbPut {
		t.Errorf("Verb = %q; want %q (nil != explicit empty string)", plan.Verb, webuiSettingsVerbPut)
	}
}

func TestApplyWebuiSettings_PutSendsFullReplacement(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	plan := &webuiSettingsPlan{Verb: webuiSettingsVerbPut, Desired: &manifest.WebuiSettingsSpec{
		AppName: sp("Acme Assistant"), DisclaimerText: sp(""), CollectFeedback: bp(true),
	}}

	out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{})
	if out == nil || out.Applied == nil {
		t.Fatalf("expected an applied PUT; out = %+v", out)
	}
	if srv.puts.Load() != 1 {
		t.Errorf("puts = %d; want 1", srv.puts.Load())
	}
	if srv.lastPut.AppName == nil || *srv.lastPut.AppName != "Acme Assistant" {
		t.Errorf("PUT appName = %v; want Acme Assistant", srv.lastPut.AppName)
	}
	if srv.lastPut.DisclaimerText == nil || *srv.lastPut.DisclaimerText != "" {
		t.Errorf("PUT disclaimerText = %v; want explicit empty string, not nil", srv.lastPut.DisclaimerText)
	}
	if srv.lastPut.WelcomeMessage != nil {
		t.Errorf("PUT welcomeMessage = %v; want nil (omitted from Desired)", srv.lastPut.WelcomeMessage)
	}
}

func TestApplyWebuiSettings_EmptyBlockNeedsPrune(t *testing.T) {
	t.Run("without prune leaves overrides standing", func(t *testing.T) {
		srv, c := newWebuiSettingsServer(t)
		srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme")}
		plan := &webuiSettingsPlan{Verb: webuiSettingsVerbPut, Desired: &manifest.WebuiSettingsSpec{}, Current: &srv.current}

		out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{})
		if out == nil || out.Skipped == nil {
			t.Fatalf("expected the apply to be a no-op; out = %+v", out)
		}
		if srv.puts.Load() != 0 {
			t.Errorf("puts = %d; want 0 — an undeclared field must survive a plain apply", srv.puts.Load())
		}
	})

	t.Run("with prune clears every override", func(t *testing.T) {
		srv, c := newWebuiSettingsServer(t)
		srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme")}
		plan := &webuiSettingsPlan{Verb: webuiSettingsVerbPut, Desired: &manifest.WebuiSettingsSpec{}, Current: &srv.current}

		out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{Prune: true})
		if out == nil || out.Applied == nil {
			t.Fatalf("expected an applied PUT; out = %+v", out)
		}
		if srv.lastPut.AppName != nil {
			t.Errorf("PUT appName = %v; want nil (clearing every override)", srv.lastPut.AppName)
		}
	})
}

func TestApplyWebuiSettings_ExplicitNullClearsWithoutPrune(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), LogoURL: sp("https://cdn/logo.svg")}

	m, err := manifest.Parse([]byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    appName: "Acme"
    logoUrl: null
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	plan, err := planWebuiSettings(t.Context(), c, m.Platform.WebuiSettings)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{Prune: false})
	if out == nil || out.Applied == nil {
		t.Fatalf("expected an applied PUT; out = %+v", out)
	}
	if srv.lastPut.LogoURL != nil {
		t.Errorf("logoUrl = %v; want null — an explicit null clears without --prune", *srv.lastPut.LogoURL)
	}
	if srv.lastPut.AppName == nil || *srv.lastPut.AppName != "Acme" {
		t.Errorf("appName = %v; want Acme", srv.lastPut.AppName)
	}
}

func TestWebuiSettings_PruneOnlyClearIsPendingOnlyUnderPrune(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), WelcomeMessage: sp("Hi")}

	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{AppName: sp("Acme")})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	summary := Summarize((&PlanResult{WebuiSettings: plan}).Ops())
	if summary.Pending(false) {
		t.Errorf("summary = %+v; a prune-only clear must not be pending without --prune", summary)
	}
	if !summary.Pending(true) {
		t.Errorf("summary = %+v; a prune-only clear must be pending under --prune", summary)
	}
	if out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{}); out == nil || out.Skipped == nil {
		t.Errorf("plain apply must skip the prune-only clear; out = %+v", out)
	}
}

func TestWebuiSettings_PartialBlockClearsOmittedFieldsOnlyUnderPrune(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{
		AppName:         sp("Old Name"),
		WelcomeMessage:  sp("Old welcome"),
		CollectFeedback: bp(true),
		DisclaimerText:  sp("Old disclaimer"),
		LogoURL:         sp("https://cdn.example.com/old.svg"),
		SystemPurpose:   sp("Old purpose"),
	}

	m, err := manifest.Parse([]byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    appName: "New Name"
    collectFeedback: false
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	plan, err := planWebuiSettings(t.Context(), c, m.Platform.WebuiSettings)
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}
	if plan.Verb != webuiSettingsVerbPut {
		t.Fatalf("Verb = %q; want %q", plan.Verb, webuiSettingsVerbPut)
	}

	row, ok := webuiSettingsRow(plan)
	if !ok {
		t.Fatal("expected a plan row for the PUT")
	}
	for _, want := range []string{
		"welcomeMessage (cleared with --prune)",
		"disclaimerText (cleared with --prune)",
		"logoUrl (cleared with --prune)",
		"systemPurpose (cleared with --prune)",
	} {
		if !strings.Contains(row.label(), want) {
			t.Errorf("plan detail %q is missing %q", row.label(), want)
		}
	}

	t.Run("plain apply writes the declared fields and preserves the rest", func(t *testing.T) {
		if out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{}); out == nil || out.Applied == nil {
			t.Fatalf("expected an applied PUT; out = %+v", out)
		}
		if srv.lastPut.AppName == nil || *srv.lastPut.AppName != "New Name" {
			t.Errorf("appName = %v; want New Name", srv.lastPut.AppName)
		}
		if srv.lastPut.CollectFeedback == nil || *srv.lastPut.CollectFeedback {
			t.Errorf("collectFeedback = %v; want explicit false", srv.lastPut.CollectFeedback)
		}
		for name, want := range map[string]string{
			"welcomeMessage": "Old welcome",
			"disclaimerText": "Old disclaimer",
			"logoUrl":        "https://cdn.example.com/old.svg",
			"systemPurpose":  "Old purpose",
		} {
			got := map[string]*string{
				"welcomeMessage": srv.lastPut.WelcomeMessage,
				"disclaimerText": srv.lastPut.DisclaimerText,
				"logoUrl":        srv.lastPut.LogoURL,
				"systemPurpose":  srv.lastPut.SystemPurpose,
			}[name]
			if got == nil || *got != want {
				t.Errorf("%s = %v; want %q preserved — an omitted field is not a removal without --prune", name, got, want)
			}
		}
	})

	t.Run("prune clears the undeclared fields and keeps the declared ones", func(t *testing.T) {
		out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{Prune: true})
		if out == nil || out.Applied == nil {
			t.Fatalf("expected an applied PUT; out = %+v", out)
		}
		if srv.lastPut.AppName == nil || *srv.lastPut.AppName != "New Name" {
			t.Errorf("appName = %v; want the declared value preserved under --prune", srv.lastPut.AppName)
		}
		if srv.lastPut.CollectFeedback == nil || *srv.lastPut.CollectFeedback {
			t.Errorf("collectFeedback = %v; want the declared explicit false preserved under --prune", srv.lastPut.CollectFeedback)
		}
		for name, got := range map[string]*string{
			"welcomeMessage": srv.lastPut.WelcomeMessage,
			"disclaimerText": srv.lastPut.DisclaimerText,
			"logoUrl":        srv.lastPut.LogoURL,
			"smallLogoUrl":   srv.lastPut.SmallLogoURL,
			"systemPurpose":  srv.lastPut.SystemPurpose,
			"responseFormat": srv.lastPut.ResponseFormat,
		} {
			if got != nil {
				t.Errorf("%s = %q; want null under --prune", name, *got)
			}
		}
	})
}

func TestApplyWebuiSettings_RereadsBeforePreservingUndeclaredFields(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)
	srv.current = wire.WebuiSettingsResponse{AppName: sp("Acme"), LogoURL: sp("https://cdn/old.svg")}

	plan, err := planWebuiSettings(t.Context(), c, &manifest.WebuiSettingsSpec{AppName: sp("Acme Renamed")})
	if err != nil {
		t.Fatalf("planWebuiSettings: %v", err)
	}

	srv.current.LogoURL = sp("https://cdn/new.svg")

	if out := applyWebuiSettings(t.Context(), c, plan, ApplyOptions{}); out == nil || out.Applied == nil {
		t.Fatalf("expected an applied PUT; out = %+v", out)
	}
	if srv.lastPut.LogoURL == nil || *srv.lastPut.LogoURL != "https://cdn/new.svg" {
		t.Errorf("logoUrl = %v; want the concurrent edit preserved — apply must not echo the plan-time snapshot back", srv.lastPut.LogoURL)
	}
	if srv.lastPut.AppName == nil || *srv.lastPut.AppName != "Acme Renamed" {
		t.Errorf("appName = %v; want the declared value applied", srv.lastPut.AppName)
	}
}

func TestApplyWebuiSettings_PutBodyCarriesEverySetting(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)

	desired := &manifest.WebuiSettingsSpec{}
	specValue := reflect.ValueOf(desired).Elem()
	specType := specValue.Type()
	for i := range specType.NumField() {
		field := specType.Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		specValue.Field(i).Set(reflect.New(field.Type.Elem()))
		switch field.Type.Elem().Kind() {
		case reflect.Bool:
			specValue.Field(i).Elem().SetBool(true)
		case reflect.Int, reflect.Int32, reflect.Int64:
			specValue.Field(i).Elem().SetInt(42)
		default:
			specValue.Field(i).Elem().SetString("carried")
		}
	}

	out := applyWebuiSettings(t.Context(), c, &webuiSettingsPlan{Verb: webuiSettingsVerbPut, Desired: desired}, ApplyOptions{})
	if out == nil || out.Applied == nil {
		t.Fatalf("expected an applied PUT; out = %+v", out)
	}

	sent := reflect.ValueOf(srv.lastPut)
	for i := range specType.NumField() {
		name := specType.Field(i).Name
		key, _, _ := strings.Cut(specType.Field(i).Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		got := sent.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("UpdateWebuiSettingsRequest has no field %s", name)
			continue
		}
		if got.IsNil() {
			t.Errorf("%s was declared but the PUT body left it nil — a replace-all PUT would clear it instead of applying it", key)
		}
	}
}

func TestApplyWebuiSettings_NoneAndUnchangedIssueNoPut(t *testing.T) {
	srv, c := newWebuiSettingsServer(t)

	if out := applyWebuiSettings(t.Context(), c, &webuiSettingsPlan{Verb: webuiSettingsVerbNone}, ApplyOptions{}); out != nil {
		t.Errorf("Verb none: out = %+v, want nil", out)
	}
	if out := applyWebuiSettings(t.Context(), c, &webuiSettingsPlan{Verb: webuiSettingsVerbUnchanged}, ApplyOptions{}); out == nil || out.Skipped == nil {
		t.Errorf("Verb unchanged: out = %+v, want a Skipped outcome", out)
	}
	if srv.puts.Load() != 0 {
		t.Errorf("puts = %d; want 0 (neither none nor unchanged issues a PUT)", srv.puts.Load())
	}
}
