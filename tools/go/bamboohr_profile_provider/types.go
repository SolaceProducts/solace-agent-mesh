// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

type Profile struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName,omitempty"`
	PreferredName string `json:"preferredName,omitempty"`
	WorkEmail     string `json:"workEmail,omitempty"`
	JobTitle      string `json:"jobTitle,omitempty"`
	Department    string `json:"department,omitempty"`
	Location      string `json:"location,omitempty"`
	SupervisorID  string `json:"supervisorId,omitempty"`
	HireDate      string `json:"hireDate,omitempty"`
	MobilePhone   string `json:"mobilePhone,omitempty"`
}

type TimeOffEntry struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Type   string `json:"type"`
	Amount string `json:"amount"`
}

type UserTimeOff struct {
	UserID  string         `json:"userId"`
	Entries []TimeOffEntry `json:"entries"`
}
