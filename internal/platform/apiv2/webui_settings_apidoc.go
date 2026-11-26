// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (WebuiSettings) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WebuiSettings holds the web UI settings an administrator has overridden for the whole
instance. Agent Mesh keeps one record, so it has no id. A null field has no
override, so the web UI entrypoint's own value applies. An empty string is a
real override, such as an empty logo to show none.`,
		Fields: map[string]apidoc.Field{
			"AppName": {Doc: `AppName overrides the application name shown in the browser tab and header. Null means no
override, so the web UI entrypoint's own value applies.`},
			"WelcomeMessage": {Doc: `WelcomeMessage overrides the greeting shown on a new chat. Null means no override, so the
web UI entrypoint's own value applies.`},
			"CollectFeedback": {Doc: `CollectFeedback overrides whether users can rate responses. Null means no override, so the
web UI entrypoint's own value applies.`},
			"PublishFeedback": {Doc: `PublishFeedback overrides whether feedback is published to the event broker for other systems to
consume. Null means no override, so the web UI entrypoint's own value applies.`},
			"FeedbackTaskInfoLevel": {Doc: `FeedbackTaskInfoLevel overrides how much task content leaves the mesh with each published
feedback event: none, summary, or stim. Null means no override, so the web UI entrypoint's
own value applies.`},
			"DisclaimerText": {Doc: `DisclaimerText overrides the disclaimer shown beneath the chat input. Null means no
override, so the web UI entrypoint's own value applies.`},
			"LogoURL": {Doc: `LogoURL overrides the full logo, as an http(s) URL, a site-relative path, or a data:image
URI. Null means no override, so the web UI entrypoint's own value applies.`},
			"SmallLogoURL": {Doc: `SmallLogoURL overrides the compact logo, in the same forms as logoUrl. Null means no
override, so the web UI entrypoint's own value applies.`},
			"SystemPurpose": {Doc: `SystemPurpose overrides the purpose text added to every prompt. Null means no override, so
the web UI entrypoint's own value applies.`},
			"ResponseFormat": {Doc: `ResponseFormat overrides the response-format guidance added to every prompt. Null means no
override, so the web UI entrypoint's own value applies.`},
			"CreatedBy": {Doc: `CreatedBy identifies who first saved an override: their email, or their user ID when no
email is known. Null while no override is stored.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies who last changed the settings, in the same form as createdBy. Null
while no override is stored.`},
			"CreatedTime": {Doc: "CreatedTime is when an override was first saved. Null while no override is stored."},
			"UpdatedTime": {Doc: "UpdatedTime is when the settings last changed. Null while no override is stored."},
		},
	}
}

func (ReplaceWebuiSettingsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceWebuiSettingsRequest is the body of a PUT, which replaces the whole
override set. A field left out or sent as null stores no override for it, so
a body with every field null clears all overrides.`,
		Fields: map[string]apidoc.Field{
			"AppName": {Doc: `AppName is the application name. Up to 64 characters, with no control characters. Null or
left out means no override, so the web UI entrypoint's own value applies.`},
			"WelcomeMessage": {Doc: `WelcomeMessage is the greeting on a new chat. Up to 2,000 characters. Null or left out
means no override, so the web UI entrypoint's own value applies.`},
			"CollectFeedback": {Doc: `CollectFeedback sets whether users can rate responses. Null or left out means no override,
so the web UI entrypoint's own value applies.`},
			"PublishFeedback": {Doc: `PublishFeedback sets whether feedback is published to the event broker. Null or left out means no
override, so the web UI entrypoint's own value applies.`},
			"FeedbackTaskInfoLevel": {Doc: `FeedbackTaskInfoLevel is how much task content leaves the mesh with each published feedback
event: none, summary, or stim. Null or left out means no override, so the web UI
entrypoint's own value applies.`},
			"DisclaimerText": {Doc: `DisclaimerText is the disclaimer shown beneath the chat input. Up to 800 characters. Null or
left out means no override, so the web UI entrypoint's own value applies.`},
			"LogoURL": {Doc: `LogoURL is the full logo, as an http(s) URL, a site-relative path, or a data:image URI. An
empty string shows no logo. Null or left out means no override, so the web UI entrypoint's
own value applies.`},
			"SmallLogoURL": {Doc: `SmallLogoURL is the compact logo, in the same forms as logoUrl. Null or left out means no
override, so the web UI entrypoint's own value applies.`},
			"SystemPurpose": {Doc: `SystemPurpose is the purpose text added to every prompt, so its length costs tokens on every
task. Up to 4,000 characters. Null or left out means no override, so the web UI entrypoint's
own value applies.`},
			"ResponseFormat": {Doc: `ResponseFormat is the response-format guidance added to every prompt. Up to 4,000
characters. Null or left out means no override, so the web UI entrypoint's own value
applies.`},
		},
	}
}
