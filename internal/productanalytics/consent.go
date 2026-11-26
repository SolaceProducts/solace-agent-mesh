// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import "context"

type ConsentProvider interface {
	Enabled(ctx context.Context) bool
}

type deniedConsent struct{}

func (deniedConsent) Enabled(context.Context) bool { return false }

type ConsentFunc func(ctx context.Context) bool

func (f ConsentFunc) Enabled(ctx context.Context) bool { return f(ctx) }
