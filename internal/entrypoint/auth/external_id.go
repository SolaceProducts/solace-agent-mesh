// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

type IDType string

const (
	IDSub   IDType = "sub"
	IDEmail IDType = "email"
	IDPhone IDType = "phone_number"
)

type ExternalID struct {
	Type  IDType
	Value string
}

func (e ExternalID) valid() bool {
	switch e.Type {
	case IDSub, IDEmail, IDPhone:
		return e.Value != ""
	default:
		return false
	}
}
