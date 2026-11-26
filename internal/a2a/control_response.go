// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

func ControlResponseType(kind string, data any) (SignalType, bool) {
	if kind != "data" {
		return "", false
	}
	fields, ok := data.(map[string]any)
	if !ok {
		return "", false
	}
	switch fields["type"] {
	case string(SignalAuthResponse):
		return SignalAuthResponse, true
	case string(SignalUserInputResponse):
		return SignalUserInputResponse, true
	}
	return "", false
}
