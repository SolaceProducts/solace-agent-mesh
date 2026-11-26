// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webuisettings

import (
	"reflect"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

func TestResolve_CoversEverySetting(t *testing.T) {
	settingsType := reflect.TypeFor[Settings]()
	for i := range settingsType.NumField() {
		field := settingsType.Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		t.Run(key, func(t *testing.T) {
			row := &Settings{}
			set := reflect.ValueOf(row).Elem().Field(i)
			set.Set(reflect.New(field.Type.Elem()))
			kind := field.Type.Elem().Kind()
			switch kind {
			case reflect.Bool:
				set.Elem().SetBool(true)
			case reflect.Int, reflect.Int32, reflect.Int64:
				set.Elem().SetInt(42)
			default:
				set.Elem().SetString("resolved")
			}

			eff := Resolve(row, Fallback{})
			if got := eff.Sources[key]; got != SourceOverride {
				t.Fatalf("source = %q, want %q — Resolve does not cover this field", got, SourceOverride)
			}
			got := reflect.ValueOf(eff).FieldByName(field.Name)
			switch kind {
			case reflect.Bool:
				if !got.Bool() {
					t.Errorf("Effective.%s = false, want the override value", field.Name)
				}
			case reflect.Int, reflect.Int32, reflect.Int64:
				if got.Int() != 42 {
					t.Errorf("Effective.%s = %d, want 42", field.Name, got.Int())
				}
			default:
				if got.String() != "resolved" {
					t.Errorf("Effective.%s = %q, want %q", field.Name, got.String(), "resolved")
				}
			}

			if bottom := Resolve(nil, Fallback{}).Sources[key]; bottom != SourceDefault {
				t.Errorf("source with nothing set = %q, want %q", bottom, SourceDefault)
			}
		})
	}
}

func TestResolve_PrecedenceTable(t *testing.T) {
	cases := []struct {
		name       string
		row        *Settings
		fallback   Fallback
		wantValue  string
		wantSource Source
	}{
		{
			name:       "nil row falls through to yaml fallback",
			row:        nil,
			fallback:   Fallback{AppName: sp("From YAML")},
			wantValue:  "From YAML",
			wantSource: SourceConfig,
		},
		{
			name:       "nil row and nil fallback falls through to hardcoded default",
			row:        nil,
			fallback:   Fallback{},
			wantValue:  DefaultAppName,
			wantSource: SourceDefault,
		},
		{
			name:       "nil field on a non-nil row falls through to yaml",
			row:        &Settings{AppName: nil},
			fallback:   Fallback{AppName: sp("From YAML")},
			wantValue:  "From YAML",
			wantSource: SourceConfig,
		},
		{
			name:       "override wins over yaml",
			row:        &Settings{AppName: sp("From DB")},
			fallback:   Fallback{AppName: sp("From YAML")},
			wantValue:  "From DB",
			wantSource: SourceOverride,
		},
		{
			name:       "empty-string override beats yaml — the sharp edge",
			row:        &Settings{AppName: sp("")},
			fallback:   Fallback{AppName: sp("From YAML")},
			wantValue:  "",
			wantSource: SourceOverride,
		},
		{
			name:       "empty-string yaml beats hardcoded default",
			row:        nil,
			fallback:   Fallback{AppName: sp("")},
			wantValue:  "",
			wantSource: SourceConfig,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := Resolve(tc.row, tc.fallback)
			if eff.AppName != tc.wantValue {
				t.Errorf("AppName = %q, want %q", eff.AppName, tc.wantValue)
			}
			if eff.Sources["appName"] != tc.wantSource {
				t.Errorf("source = %q, want %q", eff.Sources["appName"], tc.wantSource)
			}
		})
	}
}

func TestResolve_AllFieldsIndependent(t *testing.T) {
	row := &Settings{
		WelcomeMessage: sp("Hi from DB"),
		LogoURL:        sp("https://cdn.acme.com/logo.svg"),
		SystemPurpose:  sp("You are a support assistant"),
	}
	fallback := Fallback{
		AppName:        sp("Configured App"),
		DisclaimerText: sp("May be inaccurate."),
		ResponseFormat: sp("Markdown"),
	}
	eff := Resolve(row, fallback)

	if eff.AppName != "Configured App" || eff.Sources["appName"] != SourceConfig {
		t.Errorf("appName = %q/%q", eff.AppName, eff.Sources["appName"])
	}
	if eff.WelcomeMessage != "Hi from DB" || eff.Sources["welcomeMessage"] != SourceOverride {
		t.Errorf("welcomeMessage = %q/%q", eff.WelcomeMessage, eff.Sources["welcomeMessage"])
	}
	if eff.DisclaimerText != "May be inaccurate." || eff.Sources["disclaimerText"] != SourceConfig {
		t.Errorf("disclaimerText = %q/%q", eff.DisclaimerText, eff.Sources["disclaimerText"])
	}
	if eff.LogoURL != "https://cdn.acme.com/logo.svg" || eff.Sources["logoUrl"] != SourceOverride {
		t.Errorf("logoUrl = %q/%q", eff.LogoURL, eff.Sources["logoUrl"])
	}
	if eff.SmallLogoURL != "" || eff.Sources["smallLogoUrl"] != SourceDefault {
		t.Errorf("smallLogoUrl = %q/%q", eff.SmallLogoURL, eff.Sources["smallLogoUrl"])
	}
	if eff.SystemPurpose != "You are a support assistant" || eff.Sources["systemPurpose"] != SourceOverride {
		t.Errorf("systemPurpose = %q/%q", eff.SystemPurpose, eff.Sources["systemPurpose"])
	}
	if eff.ResponseFormat != "Markdown" || eff.Sources["responseFormat"] != SourceConfig {
		t.Errorf("responseFormat = %q/%q", eff.ResponseFormat, eff.Sources["responseFormat"])
	}
}

func TestResolve_CollectFeedbackBoolPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		row        *Settings
		fallback   Fallback
		want       bool
		wantSource Source
	}{
		{"override false wins over yaml true", &Settings{CollectFeedback: bp(false)}, Fallback{CollectFeedback: bp(true)}, false, SourceOverride},
		{"override true wins over yaml false", &Settings{CollectFeedback: bp(true)}, Fallback{CollectFeedback: bp(false)}, true, SourceOverride},
		{"no override falls to yaml", nil, Fallback{CollectFeedback: bp(true)}, true, SourceConfig},
		{"no override no yaml falls to default false", nil, Fallback{}, false, SourceDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := Resolve(tc.row, tc.fallback)
			if eff.CollectFeedback != tc.want {
				t.Errorf("collectFeedback = %v, want %v", eff.CollectFeedback, tc.want)
			}
			if eff.Sources["collectFeedback"] != tc.wantSource {
				t.Errorf("source = %q, want %q", eff.Sources["collectFeedback"], tc.wantSource)
			}
		})
	}
}
