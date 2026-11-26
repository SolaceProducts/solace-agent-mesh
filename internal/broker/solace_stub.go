// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosolace

package broker

import (
	"context"
	"log/slog"
	"time"
)

type SolaceBrokerConfig struct {
	BrokerURL        string
	VPN              string
	Username         string
	Password         string
	ReconnectRetries int
	ReconnectDelay   time.Duration
	ReconnectForever bool
	TLSSkipVerify    bool
	TrustStorePath   string
}

const errNoSolace = "no solace"

type SolaceBroker struct{}

const SolaceSupported = false

func NewSolaceBroker(_ SolaceBrokerConfig, _ *slog.Logger) *SolaceBroker {
	panic("solace broker not available: built with nosolace tag")
}

func (b *SolaceBroker) Connect(_ context.Context) error                     { panic(errNoSolace) }
func (b *SolaceBroker) Disconnect(_ context.Context) error                  { panic(errNoSolace) }
func (b *SolaceBroker) IsConnected() bool                                   { panic(errNoSolace) }
func (b *SolaceBroker) ConnectionState() int                                { panic(errNoSolace) }
func (b *SolaceBroker) CreateQueue(_ string, _ QueueOptions) (Queue, error) { panic(errNoSolace) }
func (b *SolaceBroker) CreateDirectSubscription() (Queue, error)            { panic(errNoSolace) }
func (b *SolaceBroker) ProvisionQueue(_ context.Context, _ string, _ QueueOptions, _ ...string) error {
	panic(errNoSolace)
}
func (b *SolaceBroker) DeleteQueue(_ string) error                            { panic(errNoSolace) }
func (b *SolaceBroker) DeprovisionQueue(_ string) error                       { panic(errNoSolace) }
func (b *SolaceBroker) Subscribe(_ Queue, _ string) error                     { panic(errNoSolace) }
func (b *SolaceBroker) Unsubscribe(_ Queue, _ string) error                   { panic(errNoSolace) }
func (b *SolaceBroker) Publish(_ context.Context, _ string, _ *Message) error { panic(errNoSolace) }
func (b *SolaceBroker) PublishGuaranteed(_ context.Context, _ string, _ *Message) error {
	panic(errNoSolace)
}
func (b *SolaceBroker) Receive(_ context.Context, _ Queue) (*Message, error) { panic(errNoSolace) }
func (b *SolaceBroker) Request(_ context.Context, _ string, _ *Message, _ time.Duration) (*Message, error) {
	panic(errNoSolace)
}
func (b *SolaceBroker) CreateTemporaryQueue() (Queue, error) { panic(errNoSolace) }
func (b *SolaceBroker) RequestReplyInbox(_ context.Context, _ string, _ *Message, _ time.Duration) (*Message, error) {
	panic(errNoSolace)
}

func (b *SolaceBroker) BindExisting(_ string) (BoundQueue, error) { panic(errNoSolace) }

func (b *SolaceBroker) ConsumeExisting(_ string) (Queue, error) { panic(errNoSolace) }

func (b *SolaceBroker) OnReconnect(_ func()) { panic(errNoSolace) }

func (b *SolaceBroker) OnConnectionLost(_ func()) { panic(errNoSolace) }

func IsPermanentConnectError(_ error) bool { return false }

var (
	_ Broker                 = (*SolaceBroker)(nil)
	_ ExistingQueueBinder    = (*SolaceBroker)(nil)
	_ ConnectionLossNotifier = (*SolaceBroker)(nil)
)
