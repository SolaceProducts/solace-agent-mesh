// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import "regexp"

func newNetQueueForTest(name, pattern string, buf int) *netQueue {
	q := &netQueue{
		name:  name,
		subs:  []string{pattern},
		msgCh: make(chan *Message, buf),
	}
	q.regexes = []*regexp.Regexp{subscriptionToRegex(pattern)}
	return q
}
