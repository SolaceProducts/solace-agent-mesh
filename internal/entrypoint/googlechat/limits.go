// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import "time"

const (
	maxMessageRunes = 4000

	minSplitRunes = 1000

	perSpaceWriteInterval = 1100 * time.Millisecond
)

const (
	outboundRetryBase     = time.Second
	outboundRetryCap      = 32 * time.Second
	outboundRetryJitter   = time.Second
	outboundRetryAttempts = 5
)

const (
	textFlushInterval       = 1500 * time.Millisecond
	replyQueueLingerTimeout = 3*time.Minute + 30*time.Second
	replyQueueOpsCap        = 256
)

const (
	MaxThreadHistoryLimit     = 100
	threadHistoryFetchTimeout = 5 * time.Second
	threadHistoryWarnWindow   = 10 * time.Minute
)

const (
	taskIdleRelease        = 15 * time.Minute
	taskSweepInterval      = time.Minute
	statusAdoptWait        = 2 * time.Second
	statusAdoptPoll        = 200 * time.Millisecond
	terminalLoadAttempts   = 3
	terminalLoadRetryDelay = 500 * time.Millisecond
)

const (
	pullMaxMessages          = 10
	ackDeadline              = 60 * time.Second
	feedbackCardWriteTimeout = 15 * time.Second
	pullErrorBackoffMax      = 30 * time.Second
	ingressDownGrace         = 30 * time.Second
	maxNackDelay             = 600 * time.Second
)

const (
	minIngressWorkers     = 4
	maxIngressWorkers     = 16
	ingressWorkerQueueCap = 512
)

const (
	claimLease                 = 15 * time.Minute
	maxAuthzDenialAttempts     = 6
	maxClickAttempts           = 6
	maxTransientAttempts       = 50
	stalledRetryBudget         = 5
	defaultTransientRetryDelay = 2 * time.Second
	maxTransientRetryDelay     = 5 * time.Minute
	claimStoreTimeout          = 5 * time.Second
	senderAuthzTimeout         = 4 * time.Second
	adapterStopBudget          = 20 * time.Second
	taskCancelTimeout          = 5 * time.Second
)

const (
	attachmentDownloadTimeout = 30 * time.Second
	attachmentsDownloadBudget = 45 * time.Second
	attachmentStoreTimeout    = 60 * time.Second
)

const signerCertsFetchTimeout = 10 * time.Second

const (
	identityGateNoticeWindow = time.Hour
	refusalLogWindow         = time.Hour
)

const maxExternalKeyLen = 128
