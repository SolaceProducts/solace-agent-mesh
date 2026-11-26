// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

import (
	"fmt"
	"strings"
)

const topicPrefix = "a2a/v1/sam_remote_tool"

func ns(namespace string) string {
	return strings.TrimRight(namespace, "/")
}

func InvokeTopic(namespace, toolName string) string {
	return fmt.Sprintf("%s/%s/invoke/%s", ns(namespace), topicPrefix, toolName)
}

func ResponseTopic(namespace, agentName, corrID string) string {
	return fmt.Sprintf("%s/%s/response/%s/%s", ns(namespace), topicPrefix, agentName, corrID)
}

func StatusTopic(namespace, agentName, corrID string) string {
	return fmt.Sprintf("%s/%s/status/%s/%s", ns(namespace), topicPrefix, agentName, corrID)
}

func InitTopic(namespace, toolName string) string {
	return fmt.Sprintf("%s/%s/init/%s", ns(namespace), topicPrefix, toolName)
}

func InitSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/init/>", ns(namespace), topicPrefix)
}

func InitRequestTopic(namespace, toolName string) string {
	return fmt.Sprintf("%s/%s/init_request/%s", ns(namespace), topicPrefix, toolName)
}

func InitRequestSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/init_request/>", ns(namespace), topicPrefix)
}

func ResponseSubscription(namespace, agentName string) string {
	return fmt.Sprintf("%s/%s/response/%s/>", ns(namespace), topicPrefix, agentName)
}

func StatusSubscription(namespace, agentName string) string {
	return fmt.Sprintf("%s/%s/status/%s/>", ns(namespace), topicPrefix, agentName)
}

func RemovedTopic(namespace, toolName string) string {
	return fmt.Sprintf("%s/%s/removed/%s", ns(namespace), topicPrefix, toolName)
}

func RemovedSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/removed/>", ns(namespace), topicPrefix)
}

func FailedDiscoveryTopic(namespace, toolName string) string {
	return fmt.Sprintf("%s/%s/failed/%s", ns(namespace), topicPrefix, toolName)
}

func FailedDiscoverySubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/failed/>", ns(namespace), topicPrefix)
}

func CancelTopic(namespace, corrID string) string {
	return fmt.Sprintf("%s/%s/cancel/%s", ns(namespace), topicPrefix, corrID)
}

func CancelSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/cancel/>", ns(namespace), topicPrefix)
}

func ExtractCancelCorrID(topic string) string {
	_, after, ok := strings.Cut(topic, "/"+topicPrefix+"/cancel/")
	if !ok {
		return ""
	}
	return after
}
