// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

func (a *Adapter) resolveIngressIdentity(value *webhookChangeValue, msg *inboundMessage) ingressIdentity {
	id := ingressIdentity{ContinuityToken: continuityTokenFor(value, msg)}
	if a.cfg == nil {
		return id
	}
	switch a.cfg.IdentityLookupKey {
	case IdentityLookupPhone:
		id.LookupValue, id.LookupValueExact = senderPhone(value, msg)
	case IdentityLookupWAUsername:
		id.LookupValue, id.LookupValueExact = senderUsername(value, msg)
	}
	return id
}

func senderPhone(value *webhookChangeValue, msg *inboundMessage) (phone string, exact bool) {
	if c, ok := contactFor(value, msg); c != nil && c.WaID != "" {
		return c.WaID, ok
	}
	return "", false
}

func senderUsername(value *webhookChangeValue, msg *inboundMessage) (username string, exact bool) {
	c, exact := contactFor(value, msg)
	if c == nil {
		return "", false
	}
	return c.Profile.Username, exact
}
