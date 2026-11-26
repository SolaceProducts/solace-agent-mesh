// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSkillInfoFromInit(t *testing.T) {
	params := &remotetool.SkillInitParams{
		SkillName:   "api-ref",
		Description: "API reference docs",
		Tags:        []string{"docs", "api"},
		Examples:    []string{"How do I authenticate?"},
		BundledTools: []remotetool.SkillBundledTool{
			{
				ToolName:    "api-ref:search_docs",
				Description: "Search API docs",
			},
		},
		HasReferences: true,
		HasAssets:     false,
		ResourceCount: 5,
	}

	info := SkillInfoFromInit(params)

	if info.Name != "api-ref" {
		t.Errorf("Name = %q, want %q", info.Name, "api-ref")
	}
	if info.Description != "API reference docs" {
		t.Errorf("Description = %q, want %q", info.Description, "API reference docs")
	}
	if len(info.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(info.Tags))
	}
	if len(info.Examples) != 1 {
		t.Errorf("Examples count = %d, want 1", len(info.Examples))
	}
	if len(info.BundledTools) != 1 {
		t.Errorf("BundledTools count = %d, want 1", len(info.BundledTools))
	}
	if !info.HasReferences {
		t.Error("HasReferences = false, want true")
	}
	if info.HasAssets {
		t.Error("HasAssets = true, want false")
	}
	if info.ResourceCount != 5 {
		t.Errorf("ResourceCount = %d, want 5", info.ResourceCount)
	}
}

func TestLoadedSkill_ToLoadedSkillInfo(t *testing.T) {
	ls := &LoadedSkill{
		Name:               "my-skill",
		InstructionContent: "Do this and that.",
		HasReferences:      true,
		ResourceCount:      3,
	}

	info := ls.ToLoadedSkillInfo()
	if info.Name != "my-skill" {
		t.Errorf("Name = %q", info.Name)
	}
	if info.InstructionContent != "Do this and that." {
		t.Errorf("InstructionContent = %q", info.InstructionContent)
	}
	if info.ReferenceFileCount != 3 {
		t.Errorf("ReferenceFileCount = %d, want 3", info.ReferenceFileCount)
	}
}

type mockSkillState struct {
	loaded map[string]bool
}

func (m *mockSkillState) IsLoaded(name string) bool { return m.loaded[name] }
func (m *mockSkillState) LoadedNames() []string     { return nil }
func (m *mockSkillState) Load(name string)          { m.loaded[name] = true }
func (m *mockSkillState) Unload(name string)        { delete(m.loaded, name) }
func (m *mockSkillState) Version() int64            { return 0 }

func TestManager_RegisterAndQuerySkillTools(t *testing.T) {
	m := NewManager()

	m.RegisterSkillTool("my-skill", "tool-a")
	m.RegisterSkillTool("my-skill", "tool-b")

	if owners := m.SkillToolOwners("tool-a"); len(owners) != 1 || owners[0] != "my-skill" {
		t.Errorf("SkillToolOwners(tool-a) = %v, want [my-skill]", owners)
	}
	if owners := m.SkillToolOwners("unknown-tool"); owners != nil {
		t.Errorf("SkillToolOwners(unknown-tool) = %v, want nil", owners)
	}
}

func TestManager_GetInstructions(t *testing.T) {
	m := NewManager()
	m.SetLoaded("skill-with-instructions", &LoadedSkill{
		Name:               "skill-with-instructions",
		InstructionContent: "Do this and that.",
	})
	m.SetLoaded("skill-no-instructions", &LoadedSkill{
		Name:               "skill-no-instructions",
		InstructionContent: "",
	})

	infos := m.GetInstructions([]string{"skill-with-instructions", "skill-no-instructions", "nonexistent"})
	if len(infos) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(infos))
	}
	if infos[0].Name != "skill-with-instructions" {
		t.Errorf("instruction name = %q, want skill-with-instructions", infos[0].Name)
	}
}

func TestManager_HasResources(t *testing.T) {
	m := NewManager()
	m.AddAvailable(&SkillInfo{Name: "with-refs", HasReferences: true})
	m.AddAvailable(&SkillInfo{Name: "with-assets", HasAssets: true})
	m.AddAvailable(&SkillInfo{Name: "bare"})

	if !m.HasResources("with-refs") {
		t.Error("with-refs should have resources")
	}
	if !m.HasResources("with-assets") {
		t.Error("with-assets should have resources")
	}
	if m.HasResources("bare") {
		t.Error("bare should not have resources")
	}
	if m.HasResources("nonexistent") {
		t.Error("nonexistent should not have resources")
	}
}

func TestManager_HasAnyAssets(t *testing.T) {
	m := NewManager()
	m.AddAvailable(&SkillInfo{Name: "with-refs", HasReferences: true})
	if m.HasAnyAssets() {
		t.Error("references alone are not assets")
	}
	m.AddAvailable(&SkillInfo{Name: "with-assets", HasAssets: true})
	if !m.HasAnyAssets() {
		t.Error("should return true after adding a skill with assets")
	}
}

func TestManager_HasAnyResources(t *testing.T) {
	m := NewManager()
	if m.HasAnyResources() {
		t.Error("empty manager should return false")
	}
	m.AddAvailable(&SkillInfo{Name: "with-refs", HasReferences: true})
	if !m.HasAnyResources() {
		t.Error("should return true after adding skill with references")
	}
}

func TestManager_ListAvailable(t *testing.T) {
	m := NewManager()
	m.AddAvailable(&SkillInfo{Name: "s1", Description: "Skill one"})
	m.AddAvailable(&SkillInfo{Name: "s2", Description: "Skill two"})

	summaries := m.ListAvailable()
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
}

func TestNewSkillState_EmptySession(t *testing.T) {
	sess := &session.Session{}
	s := NewSkillState(sess)
	if len(s.LoadedNames()) != 0 {
		t.Error("expected empty loaded names for empty session")
	}
	if s.Version() != 0 {
		t.Errorf("version = %d, want 0", s.Version())
	}
}

func TestNewSkillState_NilMetadata(t *testing.T) {
	sess := &session.Session{Metadata: nil}
	s := NewSkillState(sess)
	if len(s.LoadedNames()) != 0 {
		t.Error("expected empty loaded names for nil metadata")
	}
}

func TestNewSkillState_FromAnySlice(t *testing.T) {
	sess := &session.Session{
		Metadata: map[string]any{
			"loaded_skills": []any{"alpha", "beta", ""},
		},
	}
	s := NewSkillState(sess)
	names := s.LoadedNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 loaded names, got %d: %v", len(names), names)
	}
	if !s.IsLoaded("alpha") || !s.IsLoaded("beta") {
		t.Error("expected alpha and beta to be loaded")
	}
	if s.Version() != 1 {
		t.Errorf("version = %d, want 1 (pre-loaded skills)", s.Version())
	}
}

func TestNewSkillState_FromStringSlice(t *testing.T) {
	sess := &session.Session{
		Metadata: map[string]any{
			"loaded_skills": []string{"gamma"},
		},
	}
	s := NewSkillState(sess)
	if !s.IsLoaded("gamma") {
		t.Error("expected gamma to be loaded")
	}
	if s.Version() != 1 {
		t.Errorf("version = %d, want 1", s.Version())
	}
}

func TestNewSkillState_UnrecognizedType(t *testing.T) {
	sess := &session.Session{
		Metadata: map[string]any{
			"loaded_skills": 42,
		},
	}
	s := NewSkillState(sess)
	if len(s.LoadedNames()) != 0 {
		t.Error("expected empty loaded names for unrecognized type")
	}
	if s.Version() != 0 {
		t.Errorf("version = %d, want 0", s.Version())
	}
}

func TestSkillState_LoadAndUnload(t *testing.T) {
	sess := &session.Session{}
	s := NewSkillState(sess)

	s.Load("skill-a")
	if !s.IsLoaded("skill-a") {
		t.Error("skill-a should be loaded")
	}
	if s.Version() != 1 {
		t.Errorf("version = %d, want 1 after first load", s.Version())
	}

	s.Load("skill-a")
	if s.Version() != 1 {
		t.Errorf("version = %d, want 1 after duplicate load", s.Version())
	}

	s.Load("skill-b")
	if s.Version() != 2 {
		t.Errorf("version = %d, want 2 after second load", s.Version())
	}
	names := s.LoadedNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 loaded names, got %d", len(names))
	}

	s.Unload("skill-a")
	if s.IsLoaded("skill-a") {
		t.Error("skill-a should be unloaded")
	}
	if s.Version() != 3 {
		t.Errorf("version = %d, want 3 after unload", s.Version())
	}

	s.Unload("skill-a")
	if s.Version() != 3 {
		t.Errorf("version = %d, want 3 after duplicate unload", s.Version())
	}
}

func TestSkillState_SyncToSession(t *testing.T) {
	sess := &session.Session{}
	s := NewSkillState(sess)

	s.Load("x")
	s.Load("y")

	raw, ok := sess.Metadata["loaded_skills"]
	if !ok {
		t.Fatal("expected loaded_skills in session metadata")
	}
	names, ok := raw.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", raw)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 names in session metadata, got %d", len(names))
	}

	s.Unload("x")
	raw = sess.Metadata["loaded_skills"]
	names = raw.([]string)
	if len(names) != 1 || names[0] != "y" {
		t.Errorf("expected [y] in session metadata, got %v", names)
	}
}

func TestParseLoadSkillResult_FullResult(t *testing.T) {
	result := map[string]any{
		"instructions":   "Follow these steps.",
		"has_references": true,
		"has_assets":     false,
		"resource_count": float64(7),
		"bundled_tools": []map[string]any{
			{
				"tool_name":   "skill__tool1",
				"description": "First tool",
			},
		},
		"required_builtins": []any{"builtin_a", "builtin_b"},
	}

	ls := ParseLoadSkillResult("test-skill", result)

	if ls.Name != "test-skill" {
		t.Errorf("Name = %q, want test-skill", ls.Name)
	}
	if ls.InstructionContent != "Follow these steps." {
		t.Errorf("InstructionContent = %q", ls.InstructionContent)
	}
	if !ls.HasReferences {
		t.Error("HasReferences should be true")
	}
	if ls.HasAssets {
		t.Error("HasAssets should be false")
	}
	if ls.ResourceCount != 7 {
		t.Errorf("ResourceCount = %d, want 7", ls.ResourceCount)
	}
	if len(ls.BundledTools) != 1 {
		t.Fatalf("BundledTools count = %d, want 1", len(ls.BundledTools))
	}
	if ls.BundledTools[0].ToolName != "skill__tool1" {
		t.Errorf("BundledTools[0].ToolName = %q", ls.BundledTools[0].ToolName)
	}
	if len(ls.RequiredBuiltins) != 2 {
		t.Fatalf("RequiredBuiltins count = %d, want 2", len(ls.RequiredBuiltins))
	}
	if ls.RequiredBuiltins[0] != "builtin_a" || ls.RequiredBuiltins[1] != "builtin_b" {
		t.Errorf("RequiredBuiltins = %v", ls.RequiredBuiltins)
	}
}

func TestParseLoadSkillResult_Templates(t *testing.T) {
	result := map[string]any{
		"instructions":  "x",
		"has_assets":    true,
		"has_templates": true,
		"templates": []map[string]any{
			{"name": "report.html", "description": "Quarterly report", "output_filename": "out.html", "asset": "assets/report.html.samt"},
		},
	}
	ls := ParseLoadSkillResult("rpt", result)
	if len(ls.Templates) != 1 {
		t.Fatalf("templates = %+v", ls.Templates)
	}
	if ls.Templates[0].Name != "report.html" || ls.Templates[0].Asset != "assets/report.html.samt" {
		t.Errorf("template = %+v", ls.Templates[0])
	}
	if got := describeTemplates(ls.Templates); got != "report.html (Quarterly report) [asset: assets/report.html.samt]." {
		t.Errorf("describeTemplates = %q", got)
	}
}

func TestParseLoadSkillResult_EmptyResult(t *testing.T) {
	ls := ParseLoadSkillResult("empty", map[string]any{})
	if ls.Name != "empty" {
		t.Errorf("Name = %q, want empty", ls.Name)
	}
	if ls.InstructionContent != "" {
		t.Errorf("InstructionContent should be empty, got %q", ls.InstructionContent)
	}
	if ls.HasReferences || ls.HasAssets {
		t.Error("HasReferences and HasAssets should both be false")
	}
	if ls.ResourceCount != 0 {
		t.Errorf("ResourceCount = %d, want 0", ls.ResourceCount)
	}
	if len(ls.BundledTools) != 0 {
		t.Errorf("BundledTools should be empty, got %d", len(ls.BundledTools))
	}
	if len(ls.RequiredBuiltins) != 0 {
		t.Errorf("RequiredBuiltins should be empty, got %d", len(ls.RequiredBuiltins))
	}
}

func TestParseLoadSkillResult_RequiredBuiltinsStringSlice(t *testing.T) {
	result := map[string]any{
		"required_builtins": []string{"tool_x"},
	}
	ls := ParseLoadSkillResult("s", result)
	if len(ls.RequiredBuiltins) != 1 || ls.RequiredBuiltins[0] != "tool_x" {
		t.Errorf("RequiredBuiltins = %v, want [tool_x]", ls.RequiredBuiltins)
	}
}

func TestUnloadSkillTool_Metadata(t *testing.T) {
	ut := NewUnloadSkillTool()

	if ut.Name() != "unload_skill" {
		t.Errorf("Name() = %q, want unload_skill", ut.Name())
	}
	if ut.Description() == "" {
		t.Error("Description() should not be empty")
	}
	params := ut.Parameters()
	if params == nil || params.Type != "object" {
		t.Error("Parameters() should return an object schema")
	}
	if _, ok := params.Properties["name"]; !ok {
		t.Error("Parameters() should have a 'name' property")
	}
	if len(params.Required) != 1 || params.Required[0] != "name" {
		t.Errorf("Required = %v, want [name]", params.Required)
	}
}

func TestUnloadSkillTool_Execute_MissingName(t *testing.T) {
	ut := NewUnloadSkillTool()
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := ut.Execute(t.Context(), map[string]any{}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q: %s", result.Status, result.Message)
	}
}

func TestUnloadSkillTool_Execute_NilSkillState(t *testing.T) {
	ut := NewUnloadSkillTool()
	ctx := &tool.Context{SkillState: nil}

	result, err := ut.Execute(t.Context(), map[string]any{"name": "some-skill"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
}

func TestUnloadSkillTool_Execute_NotLoaded(t *testing.T) {
	ut := NewUnloadSkillTool()
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := ut.Execute(t.Context(), map[string]any{"name": "nope"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
}

func TestUnloadSkillTool_Execute_Success(t *testing.T) {
	mgr := NewManager()
	mgr.SetLoaded("my-skill", &LoadedSkill{Name: "my-skill"})
	mgr.RegisterSkillTool("my-skill", "my-skill__tool-a")

	ut := NewUnloadSkillTool()
	state := &mockSkillState{loaded: map[string]bool{"my-skill": true}}
	ctx := &tool.Context{SkillState: state}

	result, err := ut.Execute(t.Context(), map[string]any{"name": "my-skill"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %q: %s", result.Status, result.Message)
	}
	if state.loaded["my-skill"] {
		t.Error("skill should have been unloaded from state")
	}
	if mgr.GetLoaded("my-skill") == nil {
		t.Error("the agent's copy should stay held for other sessions")
	}
	if mgr.SkillToolOwners("my-skill__tool-a") == nil {
		t.Error("the skill's tool should stay registered for other sessions")
	}
}

func TestLoadSkillTool_Metadata(t *testing.T) {
	mgr := NewManager()
	lt := NewLoadSkillTool(mgr, nil, nil)

	if lt.Name() != "load_skill" {
		t.Errorf("Name() = %q, want load_skill", lt.Name())
	}
	params := lt.Parameters()
	if params == nil || params.Type != "object" {
		t.Error("Parameters() should return an object schema")
	}
	if _, ok := params.Properties["name"]; !ok {
		t.Error("Parameters() should have a 'name' property")
	}
	if lt.Instructions() == "" {
		t.Error("Instructions() should not be empty")
	}
	if !lt.IsLongRunning() {
		t.Error("IsLongRunning() should be true")
	}
}

func TestLoadSkillTool_Description_NoSkills(t *testing.T) {
	mgr := NewManager()
	lt := NewLoadSkillTool(mgr, nil, nil)
	desc := lt.Description()
	if desc != "Load a skill to activate its instructions and tools." {
		t.Errorf("unexpected description: %q", desc)
	}
}

func TestLoadSkillTool_Description_WithSkills(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "coding", Description: "Code generation"})
	mgr.AddAvailable(&SkillInfo{Name: "bare"})

	lt := NewLoadSkillTool(mgr, nil, nil)
	desc := lt.Description()
	if desc == "" {
		t.Error("description should not be empty")
	}
	if !contains(desc, "coding") {
		t.Errorf("description should mention 'coding': %q", desc)
	}
}

func TestLoadSkillTool_Execute_NilSkillState(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "s"})
	lt := NewLoadSkillTool(mgr, nil, nil)
	ctx := &tool.Context{SkillState: nil}

	result, err := lt.Execute(t.Context(), map[string]any{"name": "s"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error for nil SkillState, got %q", result.Status)
	}
}

func TestLoadSkillTool_Execute_NilLoader(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "s"})
	lt := NewLoadSkillTool(mgr, nil, nil)
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := lt.Execute(t.Context(), map[string]any{"name": "s"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error for nil loader, got %q: %s", result.Status, result.Message)
	}
}

type mockSkillLoader struct {
	result map[string]any
	err    error
}

func (m *mockSkillLoader) LoadSkill(_ context.Context, _ string, _ *tool.Context) (map[string]any, error) {
	return m.result, m.err
}

type mockToolRegistrar struct {
	bundledTools  []remotetool.SkillBundledTool
	builtinNames  []string
	builtinErrors map[string]error
	skillTools    map[string][]map[string]any
}

func (m *mockToolRegistrar) RegisterSkillBundledTool(bt remotetool.SkillBundledTool) {
	m.bundledTools = append(m.bundledTools, bt)
}

func (m *mockToolRegistrar) ToolInstructions(toolName string) string {
	for _, bt := range m.bundledTools {
		if bt.ToolName == toolName {
			return bt.Instructions
		}
	}
	return ""
}

func (m *mockToolRegistrar) RegisterSkillTools(_ string, configs []map[string]any) (registered []string, retry []map[string]any) {
	if m.skillTools == nil {
		m.skillTools = make(map[string][]map[string]any)
	}
	for _, cfg := range configs {
		tt, _ := cfg["tool_type"].(string)
		tn, _ := cfg["tool_name"].(string)
		if tt != "builtin" || tn == "" {
			continue
		}
		if m.builtinErrors != nil {
			if _, ok := m.builtinErrors[tn]; ok {
				continue
			}
		}
		m.builtinNames = append(m.builtinNames, tn)
		registered = append(registered, tn)
	}
	return registered, nil
}

func TestLoadSkillTool_Execute_FullSuccess(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "coding"})

	loader := &mockSkillLoader{
		result: map[string]any{
			"instructions":   "Code carefully.",
			"resource_count": float64(3),
			"bundled_tools": []map[string]any{
				{"tool_name": "coding__lint", "description": "Lint code", "instructions": "Lint before committing."},
			},
			"required_builtins": []any{"web_request"},
		},
	}
	registrar := &mockToolRegistrar{}
	lt := NewLoadSkillTool(mgr, loader, registrar)
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := lt.Execute(t.Context(), map[string]any{"name": "coding"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q: %s", result.Status, result.Message)
	}
	if !state.loaded["coding"] {
		t.Error("skill should be loaded in state")
	}
	if len(registrar.bundledTools) != 1 {
		t.Fatalf("expected 1 bundled tool registered, got %d", len(registrar.bundledTools))
	}
	if registrar.bundledTools[0].ToolName != "coding__lint" {
		t.Errorf("bundled tool name = %q", registrar.bundledTools[0].ToolName)
	}
	if len(registrar.builtinNames) != 1 || registrar.builtinNames[0] != "web_request" {
		t.Errorf("builtinNames = %v, want [web_request]", registrar.builtinNames)
	}
	ls := mgr.GetLoaded("coding")
	if ls == nil {
		t.Fatal("expected loaded skill in manager")
	}
	if ls.InstructionContent != "Code carefully." {
		t.Errorf("InstructionContent = %q", ls.InstructionContent)
	}
	if !contains(result.Message, "3 resource") {
		t.Errorf("message should mention resource count: %q", result.Message)
	}
	if result.LLMData[agent.LoadedSkillDataKey] != "coding" || result.LLMData[agent.SkillInstructionsDataKey] != "Code carefully." {
		t.Errorf("model-only data = %v, want the skill's name and instructions", result.LLMData)
	}
	if published := tool.PublishedResultData(result); published != result.Message {
		t.Errorf("published result = %v, want only the message: instructions are for the model, not the status stream", published)
	}
	if got, _ := result.LLMData[agent.ToolInstructionsDataKey].(string); got != "**coding__lint**: Lint before committing." {
		t.Errorf("tool_instructions = %q, want the bundled tool's instructions", got)
	}
}

func TestUnloadSkillTool_TellsTheModelToDisregardTheInstructions(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "coding"})
	mgr.SetLoaded("coding", &LoadedSkill{Name: "coding", InstructionContent: "Code carefully."})
	state := &mockSkillState{loaded: map[string]bool{"coding": true}}

	result, err := NewUnloadSkillTool().Execute(t.Context(), map[string]any{"name": "coding"}, &tool.Context{SkillState: state})
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("unload: %v %+v", err, result)
	}
	if !contains(result.Message, "Disregard the instructions") {
		t.Errorf("unload message = %q: the instructions stay in the conversation, so the model must be told to disregard them", result.Message)
	}
}

func TestLoadSkillTool_Execute_LoaderError(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "s"})

	loader := &mockSkillLoader{err: fmt.Errorf("connection refused")}
	lt := NewLoadSkillTool(mgr, loader, nil)
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := lt.Execute(t.Context(), map[string]any{"name": "s"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !contains(result.Message, "connection refused") {
		t.Errorf("message should contain error detail: %q", result.Message)
	}
}

func TestLoadSkillTool_Execute_BuiltinRegistrationError(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "s"})

	loader := &mockSkillLoader{
		result: map[string]any{
			"instructions":      "Hi",
			"required_builtins": []any{"bad_tool", "good_tool"},
		},
	}
	registrar := &mockToolRegistrar{
		builtinErrors: map[string]error{"bad_tool": fmt.Errorf("unknown builtin")},
	}
	lt := NewLoadSkillTool(mgr, loader, registrar)
	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}

	result, err := lt.Execute(t.Context(), map[string]any{"name": "s"}, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success (builtin errors are non-fatal), got %q: %s", result.Status, result.Message)
	}
	if len(registrar.builtinNames) != 1 || registrar.builtinNames[0] != "good_tool" {
		t.Errorf("builtinNames = %v, want [good_tool]", registrar.builtinNames)
	}
}

func TestLoadedSkill_ToLoadedSkillInfo_AssetsOnly(t *testing.T) {
	ls := &LoadedSkill{
		Name:          "asset-skill",
		HasAssets:     true,
		HasReferences: false,
		ResourceCount: 4,
	}
	info := ls.ToLoadedSkillInfo()
	if info.ReferenceFileCount != 0 {
		t.Errorf("ReferenceFileCount = %d, want 0", info.ReferenceFileCount)
	}
	if info.AssetFileCount != 4 {
		t.Errorf("AssetFileCount = %d, want 4", info.AssetFileCount)
	}
}

func TestLoadedSkill_ToLoadedSkillInfo_BothRefsAndAssets(t *testing.T) {
	ls := &LoadedSkill{
		Name:          "mixed-skill",
		HasAssets:     true,
		HasReferences: true,
		ResourceCount: 10,
	}
	info := ls.ToLoadedSkillInfo()
	if info.ReferenceFileCount != 10 {
		t.Errorf("ReferenceFileCount = %d, want 10", info.ReferenceFileCount)
	}
	if info.AssetFileCount != 0 {
		t.Errorf("AssetFileCount = %d, want 0", info.AssetFileCount)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLoadSkillTool_ValidationErrors(t *testing.T) {
	m := NewManager()
	m.AddAvailable(&SkillInfo{Name: "allowed-skill", Description: "Allowed"})

	lt := NewLoadSkillTool(m, nil, nil)

	state := &mockSkillState{loaded: map[string]bool{}}
	ctx := &tool.Context{SkillState: state}
	result, _ := lt.Execute(t.Context(), map[string]any{}, ctx)
	if result.Status != "error" {
		t.Error("expected error for missing name")
	}

	result, _ = lt.Execute(t.Context(), map[string]any{"name": "unknown"}, ctx)
	if result.Status != "error" {
		t.Error("expected error for unknown skill")
	}

	state.loaded["allowed-skill"] = true
	m.SetLoaded("allowed-skill", &LoadedSkill{Name: "allowed-skill"})
	result, _ = lt.Execute(t.Context(), map[string]any{"name": "allowed-skill"}, ctx)
	if result.Status == "error" {
		t.Errorf("expected OK for already-loaded skill, got error: %s", result.Message)
	}
}

func TestLoadSkillTool_LoadsASkillTheAgentDoesNotHold(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "coding"})
	loader := &mockSkillLoader{result: map[string]any{
		"instructions":  "Code carefully.",
		"bundled_tools": []map[string]any{{"tool_name": "coding__lint", "description": "Lint code"}},
	}}
	registrar := &mockToolRegistrar{}
	state := NewSkillState(&session.Session{Metadata: map[string]any{"loaded_skills": []string{"coding"}}})
	versionBefore := state.Version()

	result, err := NewLoadSkillTool(mgr, loader, registrar).Execute(t.Context(), map[string]any{"name": "coding"}, &tool.Context{SkillState: state})
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("load: %v %+v", err, result)
	}
	if !mgr.IsHeld("coding") {
		t.Error("the skill is not held after the load")
	}
	if len(registrar.bundledTools) != 1 {
		t.Errorf("registered %d bundled tools, want the skill's tool registered", len(registrar.bundledTools))
	}
	if !state.IsLoaded("coding") || state.Version() == versionBefore {
		t.Errorf("loaded = %v, version %d -> %d; want it loaded with a new version so the tool list is rebuilt",
			state.IsLoaded("coding"), versionBefore, state.Version())
	}
	if contains(result.Message, "already loaded") {
		t.Errorf("message = %q, want a fresh load", result.Message)
	}
}

func TestListAvailable_SortedByNameForStableCache(t *testing.T) {
	m := NewManager()
	nonAlphabeticalInsertionOrder := []string{"delta", "alpha", "charlie", "bravo"}
	for _, name := range nonAlphabeticalInsertionOrder {
		m.AddAvailable(&SkillInfo{Name: name, Description: "d"})
	}

	var got []string
	for _, s := range m.ListAvailable() {
		got = append(got, s.Name)
	}
	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !slices.Equal(got, want) {
		t.Errorf("ListAvailable must be sorted — it embeds in load_skill's tool description (cache prefix); got %v want %v", got, want)
	}
}

func TestLoadedNames_SortedForStableCache(t *testing.T) {
	s := NewSkillState(&session.Session{})
	nonAlphabeticalInsertionOrder := []string{"delta", "alpha", "charlie", "bravo"}
	for _, name := range nonAlphabeticalInsertionOrder {
		s.Load(name)
	}
	got := s.LoadedNames()
	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !slices.Equal(got, want) {
		t.Errorf("LoadedNames must be sorted — it drives the system prompt's skill-instructions section (cache prefix); got %v want %v", got, want)
	}
}

func TestAvailableSkillsForCard_SortedByName(t *testing.T) {
	m := NewManager()
	for _, name := range []string{"delta", "alpha", "charlie", "bravo"} {
		m.AddAvailable(&SkillInfo{Name: name, Description: "d-" + name})
	}

	got := m.AvailableSkillsForCard()
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !slices.Equal(names, want) {
		t.Errorf("AvailableSkillsForCard order = %v, want %v", names, want)
	}
	for _, s := range got {
		if s.Description != "d-"+s.Name {
			t.Errorf("skill %q carries description %q", s.Name, s.Description)
		}
	}
}
