// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package registry

import (
	"context"
	"strings"
	"testing"
)

type fakeLister struct {
	skills     []NamedID
	connectors []NamedID
	toolsets   []NamedID
}

func (f fakeLister) ListSkills(ctx context.Context) ([]NamedID, error) {
	return f.skills, nil
}

func (f fakeLister) ListConnectors(ctx context.Context) ([]NamedID, error) {
	return f.connectors, nil
}

func (f fakeLister) ListToolsets(ctx context.Context) ([]NamedID, error) {
	return f.toolsets, nil
}

const danglingErrFragment = "not found on platform and not declared in the desired manifest"

func TestLookupID_AllKinds(t *testing.T) {
	cases := []struct {
		kind   string
		seed   func(*Registry)
		lookup func(*Registry, string) (string, error)
	}{
		{
			kind: "skill",
			seed: func(r *Registry) { r.SetDesiredSkills([]string{"new"}) },
			lookup: func(r *Registry, n string) (string, error) {
				return r.LookupSkillID(n)
			},
		},
		{
			kind: "connector",
			seed: func(r *Registry) { r.SetDesiredConnectors([]string{"new"}) },
			lookup: func(r *Registry, n string) (string, error) {
				return r.LookupConnectorID(n)
			},
		},
		{
			kind: "toolset",
			seed: func(r *Registry) { r.SetDesiredToolsets([]string{"new"}) },
			lookup: func(r *Registry, n string) (string, error) {
				return r.LookupToolsetID(n)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			lister := fakeLister{}
			row := []NamedID{{ID: "uuid", Name: "existing"}}
			switch tc.kind {
			case "skill":
				lister.skills = row
			case "connector":
				lister.connectors = row
			case "toolset":
				lister.toolsets = row
			}
			r := New(lister)
			if err := r.Load(context.Background()); err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.seed(r)

			if id, err := tc.lookup(r, "existing"); err != nil || id != "uuid" {
				t.Errorf("platform hit: id=%q err=%v, want uuid/nil", id, err)
			}
			if id, err := tc.lookup(r, "new"); err != nil || id != "new" {
				t.Errorf("sentinel: id=%q err=%v, want name/nil", id, err)
			}
			_, err := tc.lookup(r, "dangling")
			if err == nil {
				t.Fatalf("expected error for unknown %s", tc.kind)
			}
			if !strings.Contains(err.Error(), danglingErrFragment) {
				t.Errorf("error message changed: %v", err)
			}
			if !strings.Contains(err.Error(), tc.kind) {
				t.Errorf("error message should mention %q: %v", tc.kind, err)
			}
		})
	}
}

func TestValidateToolset(t *testing.T) {
	r := New(fakeLister{toolsets: []NamedID{{ID: "uuid", Name: "platform"}}})
	if err := r.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	r.SetBuiltinToolsets([]string{"builtin_one"})
	r.SetDesiredToolsets([]string{"new-toolset"})

	for _, name := range []string{"platform", "builtin_one", "new-toolset"} {
		if err := r.ValidateToolset(name); err != nil {
			t.Errorf("ValidateToolset(%q): %v, want nil", name, err)
		}
	}
	err := r.ValidateToolset("dangling")
	if err == nil {
		t.Fatalf("expected error for unknown toolset")
	}
	if !strings.Contains(err.Error(), danglingErrFragment) {
		t.Errorf("error message changed: %v", err)
	}
}

func TestLookupSkillID_BuiltinResolvesWithoutRow(t *testing.T) {
	r := New(fakeLister{})
	if err := r.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	r.SetBuiltinSkills([]string{"sam-knowledge", "sam-docs"})

	if id, err := r.LookupSkillID("sam-knowledge"); err != nil || id != "sam-knowledge" {
		t.Errorf("LookupSkillID(builtin) = %q, %v; want sentinel name, nil", id, err)
	}
	if _, err := r.LookupSkillID("not-a-builtin"); err == nil {
		t.Errorf("expected error for unknown skill, got nil")
	}
}

func TestLookupName_AllKinds(t *testing.T) {
	r := New(fakeLister{
		skills:     []NamedID{{ID: "uuid-skill", Name: "skill-a"}},
		connectors: []NamedID{{ID: "uuid-conn", Name: "conn-a"}},
		toolsets:   []NamedID{{ID: "uuid-ts", Name: "ts-a"}},
	})
	if err := r.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := []struct {
		kind   string
		id     string
		want   string
		lookup func(string) (string, error)
	}{
		{kind: "skill", id: "uuid-skill", want: "skill-a", lookup: r.LookupSkillName},
		{kind: "connector", id: "uuid-conn", want: "conn-a", lookup: r.LookupConnectorName},
		{kind: "toolset", id: "uuid-ts", want: "ts-a", lookup: r.LookupToolsetName},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := tc.lookup(tc.id)
			if err != nil || got != tc.want {
				t.Errorf("hit: got=%q err=%v, want %q/nil", got, err, tc.want)
			}
			_, err = tc.lookup("uuid-missing")
			if err == nil {
				t.Fatal("expected error for unknown id")
			}
			if !strings.Contains(err.Error(), tc.kind) {
				t.Errorf("error %v should mention %q", err, tc.kind)
			}
		})
	}
}

func TestIndexByName_SkipsBlankRows(t *testing.T) {
	r := New(fakeLister{
		skills: []NamedID{
			{ID: "", Name: "no-id"},
			{ID: "uuid-real", Name: ""},
			{ID: "uuid-keep", Name: "keep"},
		},
	})
	if err := r.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if id, err := r.LookupSkillID("keep"); err != nil || id != "uuid-keep" {
		t.Errorf("kept row missing: id=%q err=%v", id, err)
	}
	if _, err := r.LookupSkillID(""); err == nil {
		t.Errorf("blank name should not have been indexed")
	}
	if _, err := r.LookupSkillName(""); err == nil {
		t.Errorf("blank id should not have been indexed")
	}
}

func TestSetDesired_NilNamesIgnored(t *testing.T) {
	r := New(fakeLister{})
	r.SetDesiredSkills([]string{"", "ok"})
	r.SetDesiredConnectors([]string{"", "ok"})
	r.SetDesiredToolsets([]string{"", "ok"})
	for _, set := range []map[string]struct{}{r.desiredSkills, r.desiredConnectors, r.desiredToolsets} {
		if _, ok := set[""]; ok {
			t.Errorf("empty name leaked into desired set: %#v", set)
		}
		if _, ok := set["ok"]; !ok {
			t.Errorf("non-empty name missing from desired set: %#v", set)
		}
	}
}
