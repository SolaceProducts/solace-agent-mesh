// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "time"

type PolicyMode string

const (
	ModeLogOnly      PolicyMode = "log_only"
	ModeRequireDMARC PolicyMode = "require_dmarc"
	ModeStrict       PolicyMode = "strict"
)

type EmailAddress struct {
	Address     string
	DisplayName string
}

type AuthOutcome string

const (
	AuthPass      AuthOutcome = "pass"
	AuthFail      AuthOutcome = "fail"
	AuthNone      AuthOutcome = "none"
	AuthSoftfail  AuthOutcome = "softfail"
	AuthNeutral   AuthOutcome = "neutral"
	AuthTempError AuthOutcome = "temperror"
	AuthPermError AuthOutcome = "permerror"
)

type AuthVerdict struct {
	SPF               AuthOutcome
	DKIM              AuthOutcome
	DMARC             AuthOutcome
	AlignedFromDomain string
	RawHeader         string
}

type Attachment struct {
	Filename         string
	DeclaredMIMEType string
	DetectedMIMEType string
	SizeBytes        int64
	Content          AttachmentContent
}

type AttachmentContent struct {
	InlineBytes []byte
	ArtifactURI string
}

type ProviderUID string

type RawMessage struct {
	UID          ProviderUID
	RFC822       []byte
	FetchedAt    time.Time
	ProviderMeta map[string]string
}

type NormalizedEmail struct {
	MessageID  string
	InReplyTo  string
	References []string
	ThreadID   string

	From    EmailAddress
	ReplyTo *EmailAddress
	To      []EmailAddress
	CC      []EmailAddress

	Subject     string
	BodyText    string
	BodyHTML    string
	Attachments []Attachment

	AuthResults AuthVerdict

	ReceivedAt  time.Time
	ProviderUID ProviderUID

	RawHeaders map[string][]string
}
