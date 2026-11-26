// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func (p *PlatformCore) productAnalytics() *productanalytics.Service {
	if p.svc == nil {
		return nil
	}
	svc, _ := p.svc.UsageReporter().(*productanalytics.Service)
	return svc
}

func (p *PlatformCore) platformBroker() broker.Broker {
	if p.svc == nil {
		return nil
	}
	return p.svc.Broker()
}

func (p *PlatformCore) newResourceReporter() *ResourceReporter {
	return NewResourceReporter(p.productAnalytics(), store.NewDeployHistoryRepo(p.db), p.logger)
}

func (p *PlatformCore) registerCLIIngest() {
	relay := cliIngestRelayFor(p.productAnalytics(), p.platformBroker(), p.cfg.Namespace, p.logger)
	if relay == nil {
		return
	}
	p.cliIngester = relayIngester{relay: relay}
}

func (p *PlatformCore) appendInventoryReporter(services []BackgroundService) []BackgroundService {
	emitter := inventoryEmitterFor(p.productAnalytics(), p.platformBroker(), p.cfg.Namespace, p.logger)
	if reporter := newInventoryReporter(p.db, emitter, p.logger); reporter != nil {
		return append(services, reporter)
	}
	return services
}
