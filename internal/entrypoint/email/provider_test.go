// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

var _ EmailProvider = (*FakeProvider)(nil)

var _ IdleCapableProvider = (*FakeProvider)(nil)

var _ IdleCapableProvider = (*ImapProvider)(nil)
