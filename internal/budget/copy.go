// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"fmt"
	"time"
)

const ContactAdminText = "Contact your administrator if you need it sooner."

func ResetTimeText(resetsAt time.Time) string {
	return resetsAt.UTC().Format("2 January 2006 15:04 MST")
}

func SpentText(e *ExhaustedError) string {
	switch {
	case e.Tier != TierUser:
		return "this deployment's usage budget is used up until " + ResetTimeText(e.ResetsAt)
	case e.SystemUser != "":
		return SpentTextFor(e, fmt.Sprintf("the service account %q this runs as", e.SystemUser))
	default:
		return "your usage budget is used up until " + ResetTimeText(e.ResetsAt)
	}
}

func SpentTextFor(e *ExhaustedError, whom string) string {
	return "the usage budget is used up until " + ResetTimeText(e.ResetsAt) + " for " + whom
}
