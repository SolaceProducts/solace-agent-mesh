// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useAuthContext, useConfigContext } from "@/providers/contexts";

import { isProjectOwnedByCurrentUser } from "@/features/projects/ownership";

import { useIsProjectSharingEnabled } from "./useIsProjectSharingEnabled";

export function useIsProjectOwner(projectUserId: string) {
    const { userInfo } = useAuthContext();
    const { configUseAuthorization } = useConfigContext();
    const isSharingEnabled = useIsProjectSharingEnabled();

    return isProjectOwnedByCurrentUser(projectUserId, {
        username: userInfo?.username as string | undefined,
        configUseAuthorization,
        isSharingEnabled,
    });
}
