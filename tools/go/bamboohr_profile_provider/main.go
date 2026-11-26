// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func main() {
	sdk.Run(
		sdk.NewTool("enrich_authenticated_user",
			"Resolves the authenticated user's lookup value (typically email) to a canonical BambooHR Profile.",
			enrichAuthenticatedUser,
			sdk.WithInstructions(enrichAuthenticatedUserInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("search_users",
			"Searches the BambooHR directory for users matching the query substring.",
			searchUsers,
			sdk.WithInstructions(searchUsersInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("get_user_profile",
			"Returns the raw BambooHR directory record for a user keyed by lowercased work email.",
			getUserProfile,
			sdk.WithInstructions(getUserProfileInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("get_time_off",
			"Returns time-off entries for one or more users in the given date range (pass user_ids for several people in one call).",
			getTimeOff,
			sdk.WithInstructions(getTimeOffInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("get_user_profile_picture",
			"Returns the user's BambooHR profile photo as a viewable image artifact (and base64 + MIME type).",
			getUserProfilePicture,
			sdk.WithInstructions(getUserProfilePictureInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	)
}
