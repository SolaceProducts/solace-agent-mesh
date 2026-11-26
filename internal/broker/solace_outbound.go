// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import "solace.dev/go/messaging/pkg/solace/config"

func outboundProperties(msg *Message, guaranteed bool) config.MessagePropertyMap {
	props := make(config.MessagePropertyMap, len(msg.UserProperties)+4)
	for k, v := range msg.UserProperties {
		props[config.MessageProperty(k)] = v
	}
	if msg.ReplyTo != "" {
		props[messagePropertyReplyTo] = msg.ReplyTo
	}
	if msg.CorrelationID != "" {
		props[config.MessagePropertyCorrelationID] = msg.CorrelationID
	}
	if guaranteed && msg.TTL > 0 {
		props[config.MessagePropertyPersistentTimeToLive] = msg.TTL.Milliseconds()
	}
	addHeaderProperties(props, msg.Headers, guaranteed)
	return props
}

func addHeaderProperties(props config.MessagePropertyMap, h *PublishHeaders, guaranteed bool) {
	if h == nil {
		return
	}
	if h.Priority != nil {
		props[config.MessagePropertyPriority] = *h.Priority
	}
	if h.SequenceNumber != nil {
		props[config.MessagePropertySequenceNumber] = *h.SequenceNumber
	}
	setIfNotEmpty(props, config.MessagePropertyApplicationMessageID, h.ApplicationMessageID)
	setIfNotEmpty(props, config.MessagePropertyApplicationMessageType, h.ApplicationMessageType)
	setIfNotEmpty(props, config.MessagePropertySenderID, h.SenderID)
	setIfNotEmpty(props, config.MessagePropertyHTTPContentEncoding, h.HTTPContentEncoding)
	if !guaranteed {
		return
	}
	if h.DMQEligible != nil {
		props[config.MessagePropertyPersistentDMQEligible] = *h.DMQEligible
	}
	if !h.Expiration.IsZero() {
		props[config.MessagePropertyPersistentExpiration] = h.Expiration.UnixMilli()
	}
}

func setIfNotEmpty(props config.MessagePropertyMap, key config.MessageProperty, value string) {
	if value != "" {
		props[key] = value
	}
}
