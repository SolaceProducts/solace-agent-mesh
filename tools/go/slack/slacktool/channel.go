// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

type SlackChannel string

type SlackChannelID string

type inputKind int

const (
	kindEmail inputKind = iota
	kindChannelName
	kindPassthrough
)

var emailRe = regexp.MustCompile(`.+@.+\..+`)

func classify(ch SlackChannel) inputKind {
	s := string(ch)
	if strings.HasPrefix(s, "#") {
		return kindChannelName
	}
	if emailRe.MatchString(s) {
		return kindEmail
	}
	return kindPassthrough
}

func mapSlackChannelToChannelID(ctx context.Context, ch SlackChannel, api slackapi.SlackAPIAdapter) (SlackChannelID, error) {
	switch classify(ch) {
	case kindEmail:
		user, err := api.GetUserByEmail(ctx, string(ch))
		if err != nil {
			return "", fmt.Errorf("resolve email %q: %w", string(ch), err)
		}
		return SlackChannelID(user.ID), nil

	case kindChannelName:
		return SlackChannelID(strings.TrimPrefix(string(ch), "#")), nil

	default:
		return SlackChannelID(ch), nil
	}
}
