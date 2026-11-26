// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

import "fmt"

const skillTopicPrefix = "a2a/v1/sam_skill"

func SkillInitTopic(namespace, skillName string) string {
	return fmt.Sprintf("%s/%s/init/%s", ns(namespace), skillTopicPrefix, skillName)
}

func SkillInitSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/init/>", ns(namespace), skillTopicPrefix)
}

func SkillRemovedTopic(namespace, skillName string) string {
	return fmt.Sprintf("%s/%s/removed/%s", ns(namespace), skillTopicPrefix, skillName)
}

func SkillRemovedSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/removed/>", ns(namespace), skillTopicPrefix)
}

func SkillInitRequestTopic(namespace string) string {
	return fmt.Sprintf("%s/%s/init_request", ns(namespace), skillTopicPrefix)
}

func SkillInitRequestSubscription(namespace string) string {
	return fmt.Sprintf("%s/%s/init_request", ns(namespace), skillTopicPrefix)
}
