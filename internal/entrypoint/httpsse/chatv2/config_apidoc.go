// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ChatLoginConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatLoginConfig is what a client needs before anyone has signed in: how to
start a login and how to brand the sign-in screen. The gateway keeps one
record, so it has no id.`,
		Fields: map[string]apidoc.Field{
			"AuthorizationEnabled": {Doc: `AuthorizationEnabled is true when the operator turned authorization on. It does not
by itself mean a sign-in is required: a client starts a login only when it is true
and loginUrl is not null. When it is false every request runs as one default user,
so a client does not start a login.`},
			"LoginURL": {Doc: `LoginURL is where a client sends the browser to start a login. It is null
when no login is configured. The gateway fills in its own login route only when it
runs the single sign-on login.`},
			"IdentityServiceType": {Doc: `IdentityServiceType names the kind of identity service behind the login, such as
"enterprise" for a single sign-on login or "noop" when sign-in is off.`},
			"AppName": {Doc: `AppName is the product name to show on the sign-in screen.`},
			"WelcomeMessage": {Doc: `WelcomeMessage is the greeting to show. It is null when the operator set none, and a client
then shows its own default; an empty string means the operator chose to show nothing.`},
			"LogoURL": {Doc: `LogoURL is the full-size logo. It is null when the operator set none, and an empty string
means the operator chose to show no logo.`},
			"SmallLogoURL": {Doc: `SmallLogoURL is the logo for narrow places such as a collapsed sidebar. It is null when the
operator set none, and an empty string means the operator chose to show no logo.`},
			"DisclaimerText": {Doc: `DisclaimerText is the notice to show under the chat input. It is null when there is none.`},
		},
	}
}

func (ChatValidationLimits) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatValidationLimits are the size limits the gateway enforces on what a user creates.`,
		Fields: map[string]apidoc.Field{
			"ProjectNameMax":            {Doc: `ProjectNameMax is the longest project name, in characters.`},
			"ProjectDescriptionMax":     {Doc: `ProjectDescriptionMax is the longest project description, in characters.`},
			"ProjectInstructionsMax":    {Doc: `ProjectInstructionsMax is the longest set of project instructions, in characters.`},
			"MaxPerFileUploadSizeBytes": {Doc: `MaxPerFileUploadSizeBytes is the largest single file a user may upload.`},
			"MaxBatchUploadSizeBytes":   {Doc: `MaxBatchUploadSizeBytes is the most a single upload request may carry in total.`},
			"MaxZipUploadSizeBytes":     {Doc: `MaxZipUploadSizeBytes is the largest zip archive a user may import.`},
			"MaxProjectSizeBytes":       {Doc: `MaxProjectSizeBytes is the most all the files of one project may add up to.`},
		},
	}
}

func (ChatDataRetention) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatDataRetention is how long the gateway keeps what it records.`,
		Fields: map[string]apidoc.Field{
			"TaskDays":     {Doc: `TaskDays is how many days a finished task's record is kept.`},
			"FeedbackDays": {Doc: `FeedbackDays is how many days feedback is kept.`},
		},
	}
}

func (ChatUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatUser is the caller, as the gateway sees them.`,
		Fields: map[string]apidoc.Field{
			"ID":         {Doc: `ID is the caller's user id. It is the id sessions and tasks are owned by.`},
			"Type":       {Doc: `Type is always "user".`},
			"Email":      {Doc: `Email is the caller's email address. It is null when the identity service holds none.`},
			"AuthMethod": {Doc: `AuthMethod says how the caller signed in, such as "oidc". It is "development" when sign-in is off and every request runs as one default user.`},
			"Scopes":     {Doc: `Scopes are every scope the caller holds. A client matches a scope it needs against these: a scope of "*" matches everything; "_" as the second segment reads as "*"; a trailing "*" matches any longer scope with the same leading segments; and any segment may hold a "*" glob, as in agent:*:invoke. The list comes from the caller's roles, so it may be empty.`},
			"Roles":      {Doc: `Roles are the roles the caller holds, which the scopes come from.`},
		},
	}
}

func (UserCapability) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserCapability is whether the caller holds one scope.`,
		Fields: map[string]apidoc.Field{
			"Scope":   {Doc: `Scope is the scope that was asked about.`},
			"Granted": {Doc: `Granted is true when the caller holds the scope, directly or through a wildcard.`},
		},
	}
}

func (ChatUserCapabilities) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatUserCapabilities answers which of the scopes asked about the caller holds.`,
		Fields: map[string]apidoc.Field{
			"Capabilities": {Doc: `Capabilities has one entry for each scope in the scopes query parameter, in the order asked, with duplicates removed.`},
			"ManageRbac":   {Doc: `ManageRbac is true when the caller may change roles and permissions. It is not a scope check: only a caller holding the universal scope "*:*:*" (or the legacy "*") may, and holding the scopes asked about does not make the caller one.`},
		},
	}
}

func (ReadUserCapabilitiesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReadUserCapabilitiesRequest names the scopes to check.`,
		Fields: map[string]apidoc.Field{
			"Scopes": {Doc: "Scopes are the scopes to check, as comma-separated values. Blank entries are ignored. Asking about more than 100 distinct scopes fails with 422. Leave it out to learn only manageRbac."},
		},
	}
}

func (ChatConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatConfig is what this gateway offers its chat users once they have signed
in. It repeats the fields of ChatLoginConfig so a client needs one read after
login. The gateway keeps one record, so it has no id.`,
		Fields: map[string]apidoc.Field{
			"AuthorizationEnabled": {Doc: `AuthorizationEnabled is true when the operator turned authorization on. It does not
by itself mean a sign-in is required: a client starts a login only when it is true
and loginUrl is not null. When it is false every request runs as one default user,
so a client does not start a login.`},
			"LoginURL": {Doc: `LoginURL is where a client sends the browser to start a login. It is null
when no login is configured. The gateway fills in its own login route only when it
runs the single sign-on login.`},
			"IdentityServiceType": {Doc: `IdentityServiceType names the kind of identity service behind the login, such as
"enterprise" for a single sign-on login or "noop" when sign-in is off.`},
			"AppName": {Doc: `AppName is the product name to show.`},
			"WelcomeMessage": {Doc: `WelcomeMessage is the greeting to show. It is null when the operator set none, and a client
then shows its own default; an empty string means the operator chose to show nothing.`},
			"LogoURL": {Doc: `LogoURL is the full-size logo. It is null when the operator set none, and an empty string
means the operator chose to show no logo.`},
			"SmallLogoURL": {Doc: `SmallLogoURL is the logo for narrow places such as a collapsed sidebar. It is null when the
operator set none, and an empty string means the operator chose to show no logo.`},
			"DisclaimerText": {Doc: `DisclaimerText is the notice to show under the chat input. It is null when there is none.`},
			"CollectFeedback": {Doc: `CollectFeedback is true when users may rate responses. It is false when the gateway has
nowhere to store the ratings, whatever the operator asked for.`},
			"PersistenceEnabled": {Doc: `PersistenceEnabled is true when the gateway has a database. Without one, sessions, saved
history and projects are not available.`},
			"SchedulerEnabled":      {Doc: `SchedulerEnabled is true when scheduled tasks are available.`},
			"ActivationGateEnabled": {Doc: `ActivationGateEnabled is true when this installation must be activated before anyone can use the gateway.`},
			"PeopleSearchAvailable": {Doc: `PeopleSearchAvailable is true when a search for other people could return
anyone. It is false when no identity provider is configured to search, so
a client can hide the people search instead of showing one that finds
nobody.`},
			"GoogleChatEndpointBase":       {Doc: `GoogleChatEndpointBase is the public address Google Chat gateways are reached at, without a trailing slash. A client needs it to create a Google Chat gateway, which must be served over HTTPS. It is null when the operator set none.`},
			"AzureWorkloadIdentityEnabled": {Doc: `AzureWorkloadIdentityEnabled is true when the deployment can authenticate a model through Azure workload identity.`},
			"ProductAnalyticsEnabled":      {Doc: `ProductAnalyticsEnabled is true when this build collects product analytics.`},
			"TextToSpeechEnabled":          {Doc: `TextToSpeechEnabled is true when the gateway can turn a response into speech.`},
			"SpeechToTextEnabled":          {Doc: `SpeechToTextEnabled is true when the gateway can turn speech into text.`},
			"BackgroundTaskTimeoutMs":      {Doc: `BackgroundTaskTimeoutMs is how long a task started in the background may run before the gateway stops it, in milliseconds.`},
			"ValidationLimits":             {Doc: `ValidationLimits are the size limits the gateway enforces on what a user creates.`},
			"DataRetention":                {Doc: `DataRetention is how long the gateway keeps what it records. It is null when cleanup is off and everything is kept.`},
			"RedirectURL":                  {Doc: `RedirectURL is where the gateway sends the browser after a login. It is null when none is configured.`},
			"ServerURL":                    {Doc: `ServerURL is the address the gateway is served from, as the operator configured it. It is null when none is configured.`},
			"PlatformServerURL":            {Doc: `PlatformServerURL is the address of the platform service when it is served from a different address than the gateway. It is null when the platform shares the gateway's address or is not present.`},
			"PlatformServiceEnabled":       {Doc: `PlatformServiceEnabled is true when the platform service is available to this gateway's users.`},
			"ClaimKey":                     {Doc: `ClaimKey is the token claim the gateway reads a user's groups from. It is null when none is configured.`},
			"SessionIdleTimeoutSeconds":    {Doc: `SessionIdleTimeoutSeconds is how long a signed-in session may sit idle before the user must sign in again. It is null when sessions do not expire for idleness.`},
		},
	}
}
