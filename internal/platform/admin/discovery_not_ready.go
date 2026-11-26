// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "errors"

var ErrDiscoveryNotReady = errors.New("discovery is not ready")

type validationErrorWith struct {
	*ValidationError
	cause error
}

func (e validationErrorWith) Error() string {
	return e.ValidationError.Error() + ": " + e.cause.Error()
}

func (e validationErrorWith) Unwrap() []error {
	return []error{e.ValidationError, e.cause}
}

func errNotReady(message string) error {
	return validationErrorWith{&ValidationError{Message: message}, ErrDiscoveryNotReady}
}
