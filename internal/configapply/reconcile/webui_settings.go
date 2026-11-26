// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

var webuiSettingsResource = platformResource{path: "/webuiSettings"}

type webuiSettingsVerb string

const (
	webuiSettingsVerbNone      webuiSettingsVerb = "none"
	webuiSettingsVerbPut       webuiSettingsVerb = "put"
	webuiSettingsVerbDelete    webuiSettingsVerb = "delete"
	webuiSettingsVerbUnchanged webuiSettingsVerb = "unchanged"
)

type webuiSettingsPlan struct {
	Verb    webuiSettingsVerb
	Desired *manifest.WebuiSettingsSpec
	Current *wire.WebuiSettingsResponse
}

type webuiSettingsOpResult struct {
	Verb webuiSettingsVerb
	Err  error
}

type webuiSettingsOutcome struct {
	Applied  *webuiSettingsOpResult
	Skipped  *webuiSettingsOpResult
	Failures []webuiSettingsOpResult
}

func (c *Client) GetWebuiSettings(ctx context.Context) (*wire.WebuiSettingsResponse, error) {
	var resp wire.WebuiSettingsResponse
	if err := c.callResource(ctx, webuiSettingsResource, http.MethodGet, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("get webui settings: %w", err)
	}
	return &resp, nil
}

func planWebuiSettings(ctx context.Context, c *Client, desired *manifest.WebuiSettingsSpec) (*webuiSettingsPlan, error) {
	storedOverrides, err := c.GetWebuiSettings(ctx)
	if err != nil {
		return nil, err
	}
	if desired == nil {
		if !webuiSettingsHasOverrides(storedOverrides) {
			return &webuiSettingsPlan{Verb: webuiSettingsVerbNone, Current: storedOverrides}, nil
		}
		return &webuiSettingsPlan{Verb: webuiSettingsVerbDelete, Current: storedOverrides}, nil
	}
	if webuiSettingsUnchanged(storedOverrides, desired) {
		return &webuiSettingsPlan{Verb: webuiSettingsVerbUnchanged, Desired: desired, Current: storedOverrides}, nil
	}
	return &webuiSettingsPlan{Verb: webuiSettingsVerbPut, Desired: desired, Current: storedOverrides}, nil
}

func webuiSettingsHasOverrides(current *wire.WebuiSettingsResponse) bool {
	return len(webuiSettingsOverrideNames(current)) > 0
}

func webuiSettingsOverrideNames(current *wire.WebuiSettingsResponse) []string {
	if current == nil {
		return nil
	}
	value := reflect.ValueOf(*current)
	specType := reflect.TypeFor[manifest.WebuiSettingsSpec]()

	var names []string
	for i := range specType.NumField() {
		field := specType.Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		if stored := value.FieldByName(field.Name); stored.IsValid() && !stored.IsNil() {
			names = append(names, key)
		}
	}
	return names
}

func webuiSettingsUnchanged(current *wire.WebuiSettingsResponse, desired *manifest.WebuiSettingsSpec) bool {
	return len(webuiSettingsDiff(current, desired)) == 0
}

func webuiSettingsPruneOnly(p *webuiSettingsPlan) bool {
	return len(webuiSettingsDiff(p.Current, webuiSettingsEffective(p.Current, p.Desired, false))) == 0
}

func webuiSettingsDiff(current *wire.WebuiSettingsResponse, desired *manifest.WebuiSettingsSpec) []string {
	if desired == nil {
		return nil
	}
	if current == nil {
		current = &wire.WebuiSettingsResponse{}
	}
	currentValue := reflect.ValueOf(*current)
	desiredValue := reflect.ValueOf(*desired)
	desiredType := desiredValue.Type()

	var changed []string
	for i := range desiredType.NumField() {
		name, _, _ := strings.Cut(desiredType.Field(i).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		want := desiredValue.Field(i)
		got := currentValue.FieldByName(desiredType.Field(i).Name)
		if !got.IsValid() {
			panic(fmt.Sprintf("webuiSettingsDiff: %s has no field %s", currentValue.Type(), desiredType.Field(i).Name))
		}
		if reflect.DeepEqual(got.Interface(), want.Interface()) {
			continue
		}
		switch {
		case got.IsNil():
			changed = append(changed, name+" (set)")
		case want.IsNil():
			if slices.Contains(desired.ExplicitNulls, name) {
				changed = append(changed, name+" (cleared)")
			} else {
				changed = append(changed, name+" (cleared with --prune)")
			}
		default:
			changed = append(changed, name)
		}
	}
	return changed
}

func webuiSettingsEffective(current *wire.WebuiSettingsResponse, desired *manifest.WebuiSettingsSpec, prune bool) *manifest.WebuiSettingsSpec {
	if desired == nil {
		desired = &manifest.WebuiSettingsSpec{}
	}
	if current == nil {
		current = &wire.WebuiSettingsResponse{}
	}
	out := &manifest.WebuiSettingsSpec{}
	outValue := reflect.ValueOf(out).Elem()
	desiredValue := reflect.ValueOf(*desired)
	currentValue := reflect.ValueOf(*current)
	specType := desiredValue.Type()

	for i := range specType.NumField() {
		field := specType.Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		declared := desiredValue.Field(i)
		if !declared.IsNil() {
			outValue.Field(i).Set(declared)
			continue
		}
		if prune || slices.Contains(desired.ExplicitNulls, key) {
			continue
		}
		outValue.Field(i).Set(currentValue.FieldByName(field.Name))
	}
	return out
}

func applyWebuiSettings(ctx context.Context, c *Client, plan *webuiSettingsPlan, opts ApplyOptions) *webuiSettingsOutcome {
	if plan == nil || plan.Verb == webuiSettingsVerbNone {
		return nil
	}
	if plan.Verb == webuiSettingsVerbUnchanged {
		return &webuiSettingsOutcome{Skipped: &webuiSettingsOpResult{Verb: webuiSettingsVerbUnchanged}}
	}
	if plan.Verb == webuiSettingsVerbDelete && !opts.Prune {
		return &webuiSettingsOutcome{Skipped: &webuiSettingsOpResult{Verb: webuiSettingsVerbDelete}}
	}

	current := plan.Current
	if fresh, err := c.GetWebuiSettings(ctx); err == nil {
		current = fresh
	} else {
		reconcileLogger.Warn("webui settings re-read failed; undeclared fields will be written from the plan-time snapshot and may revert a concurrent change",
			"error", err)
	}

	effective := webuiSettingsEffective(current, plan.Desired, opts.Prune)
	if len(webuiSettingsDiff(current, effective)) == 0 {
		return &webuiSettingsOutcome{Skipped: &webuiSettingsOpResult{Verb: webuiSettingsVerbUnchanged}}
	}
	body := wire.ReplaceWebuiSettingsRequest{
		AppName:               effective.AppName,
		WelcomeMessage:        effective.WelcomeMessage,
		CollectFeedback:       effective.CollectFeedback,
		PublishFeedback:       effective.PublishFeedback,
		FeedbackTaskInfoLevel: effective.FeedbackTaskInfoLevel,
		DisclaimerText:        effective.DisclaimerText,
		LogoURL:               effective.LogoURL,
		SmallLogoURL:          effective.SmallLogoURL,
		SystemPurpose:         effective.SystemPurpose,
		ResponseFormat:        effective.ResponseFormat,
	}
	err := c.callResource(ctx, webuiSettingsResource, http.MethodPut, "", body, nil)
	res := webuiSettingsOpResult{Verb: plan.Verb, Err: err}
	if err != nil {
		return &webuiSettingsOutcome{Failures: []webuiSettingsOpResult{res}}
	}
	return &webuiSettingsOutcome{Applied: &res}
}
