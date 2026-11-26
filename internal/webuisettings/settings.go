// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webuisettings

import (
	"fmt"
	"reflect"
	"strings"
)

type Settings struct {
	AppName               *string `json:"appName"`
	WelcomeMessage        *string `json:"welcomeMessage"`
	CollectFeedback       *bool   `json:"collectFeedback"`
	PublishFeedback       *bool   `json:"publishFeedback"`
	FeedbackTaskInfoLevel *string `json:"feedbackTaskInfoLevel"`
	DisclaimerText        *string `json:"disclaimerText"`
	LogoURL               *string `json:"logoUrl"`
	SmallLogoURL          *string `json:"smallLogoUrl"`
	SystemPurpose         *string `json:"systemPurpose"`
	ResponseFormat        *string `json:"responseFormat"`
}

func (s *Settings) IsEmpty() bool { return s == nil || *s == Settings{} }

type Fallback struct {
	AppName               *string
	WelcomeMessage        *string
	CollectFeedback       *bool
	PublishFeedback       *bool
	FeedbackTaskInfoLevel *string
	DisclaimerText        *string
	LogoURL               *string
	SmallLogoURL          *string
	SystemPurpose         *string
	ResponseFormat        *string
}

const (
	DefaultAppName                     = "Solace Agent Mesh"
	DefaultFeedbackTaskInfoLevel       = FeedbackTaskInfoLevelNone
	DefaultFeedbackMaxPayloadSizeBytes = int64(9_000_000)
)

const (
	FeedbackTaskInfoLevelNone    = "none"
	FeedbackTaskInfoLevelSummary = "summary"
	FeedbackTaskInfoLevelStim    = "stim"
)

func ValidFeedbackTaskInfoLevels() []string {
	return []string{FeedbackTaskInfoLevelNone, FeedbackTaskInfoLevelSummary, FeedbackTaskInfoLevelStim}
}

func IsValidFeedbackTaskInfoLevel(v string) bool {
	switch v {
	case FeedbackTaskInfoLevelNone, FeedbackTaskInfoLevelSummary, FeedbackTaskInfoLevelStim:
		return true
	}
	return false
}

type Source string

const (
	SourceOverride Source = "override"
	SourceConfig   Source = "config"
	SourceDefault  Source = "default"
)

type FieldValue struct {
	Value  any    `json:"value"`
	Source Source `json:"source"`
}

type Effective struct {
	AppName               string
	WelcomeMessage        string
	CollectFeedback       bool
	PublishFeedback       bool
	FeedbackTaskInfoLevel string
	DisclaimerText        string
	LogoURL               string
	SmallLogoURL          string
	SystemPurpose         string
	ResponseFormat        string

	Sources map[string]Source
}

func Resolve(row *Settings, fallback Fallback) Effective {
	eff := Effective{
		AppName:               DefaultAppName,
		FeedbackTaskInfoLevel: DefaultFeedbackTaskInfoLevel,
		Sources:               make(map[string]Source, 10),
	}

	effValue := reflect.ValueOf(&eff).Elem()
	fallbackValue := reflect.ValueOf(fallback)
	var rowValue reflect.Value
	if row != nil {
		rowValue = reflect.ValueOf(*row)
	}

	settingsType := reflect.TypeFor[Settings]()
	for i := range settingsType.NumField() {
		name := settingsType.Field(i).Name
		key, _, _ := strings.Cut(settingsType.Field(i).Tag.Get("json"), ",")
		if key == "" || key == "-" {
			continue
		}
		target := effValue.FieldByName(name)
		configured := fallbackValue.FieldByName(name)
		if !target.IsValid() || !configured.IsValid() {
			panic(fmt.Sprintf("webuisettings.Resolve: Settings.%s has no counterpart on Effective and Fallback", name))
		}

		switch override := rowValue; {
		case override.IsValid() && !override.Field(i).IsNil():
			target.Set(override.Field(i).Elem())
			eff.Sources[key] = SourceOverride
		case !configured.IsNil():
			target.Set(configured.Elem())
			eff.Sources[key] = SourceConfig
		default:
			eff.Sources[key] = SourceDefault
		}
	}
	return eff
}
