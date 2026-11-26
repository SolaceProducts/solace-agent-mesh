// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import "time"

type Record struct {
	Account        *Account    `yaml:"account,omitempty"`
	EULA           *Acceptance `yaml:"eula,omitempty"`
	FreeModelTerms *Acceptance `yaml:"free_model_terms,omitempty"`
}

type Account struct {
	Subject    string    `yaml:"subject"`
	Email      string    `yaml:"email"`
	Name       string    `yaml:"name,omitempty"`
	SignedInAt time.Time `yaml:"signed_in_at"`
}

type Acceptance struct {
	Subject     string    `yaml:"subject"`
	AgreementID string    `yaml:"agreement_id,omitempty"`
	AcceptedAt  time.Time `yaml:"accepted_at"`
}

func (r Record) State() State {
	switch {
	case r.Account == nil:
		return StateSignedOut
	case r.acceptedByAccount(r.EULA):
		return StateActive
	default:
		return StateEULAPending
	}
}

func (r Record) choseOwnModel() bool {
	return r.EULA != nil && r.Account != nil && r.EULA.Subject == r.Account.Subject && r.FreeModelTerms == nil
}

func (r Record) acceptedByAccount(a *Acceptance) bool {
	return a != nil && r.Account != nil && a.Subject == r.Account.Subject
}
