// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxSubscriptionBytes = 250

const (
	singleLevelWildcard = "*"
	multiLevelWildcard  = ">"
	reservedTopicPrefix = "#"
)

var (
	ErrSubscriptionEmpty          = errors.New("must not be empty")
	ErrSubscriptionTooLong        = errors.New("must be at most 250 bytes")
	ErrSubscriptionReserved       = errors.New("must not start with #, which the broker reserves for its own topics and shared subscriptions")
	ErrSubscriptionCharacters     = errors.New("must be UTF-8 text without spaces or control characters")
	ErrSubscriptionEmptyLevel     = errors.New("must not start or end with / or contain an empty level (//)")
	ErrSubscriptionMultiLevel     = errors.New("may use > only as the whole last level")
	ErrSubscriptionSingleLevelUse = errors.New("may use * only as a whole level or as the last character of a level")
)

func ValidateSubscription(sub string) error {
	switch {
	case sub == "":
		return ErrSubscriptionEmpty
	case len(sub) > MaxSubscriptionBytes:
		return ErrSubscriptionTooLong
	case strings.HasPrefix(sub, reservedTopicPrefix):
		return ErrSubscriptionReserved
	case !utf8.ValidString(sub) || strings.IndexFunc(sub, isForbiddenTopicRune) >= 0:
		return ErrSubscriptionCharacters
	}
	levels := strings.Split(sub, "/")
	for i, level := range levels {
		if err := validateSubscriptionLevel(level, i == len(levels)-1); err != nil {
			return err
		}
	}
	return nil
}

func validateSubscriptionLevel(level string, last bool) error {
	if level == "" {
		return ErrSubscriptionEmptyLevel
	}
	if strings.Contains(level, multiLevelWildcard) && (level != multiLevelWildcard || !last) {
		return ErrSubscriptionMultiLevel
	}
	if star := strings.Index(level, singleLevelWildcard); star >= 0 && star != len(level)-1 {
		return ErrSubscriptionSingleLevelUse
	}
	return nil
}

func isForbiddenTopicRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

func TopicMatch(subscription, topic string) bool {
	re := subscriptionToRegex(subscription)
	return re.MatchString(topic)
}

func subscriptionToRegex(subscription string) *regexp.Regexp {
	parts := strings.Split(subscription, "/")
	for i, part := range parts {
		switch part {
		case "*":
			parts[i] = `[^/]+`
		case ">":
			parts[i] = `.+`
		default:
			parts[i] = regexp.QuoteMeta(part)
		}
	}
	pattern := "^" + strings.Join(parts, "/") + "$"
	return regexp.MustCompile(pattern)
}
