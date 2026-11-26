// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import "errors"

var ErrApplyHadFailures = errors.New("configapply: one or more operations failed")
