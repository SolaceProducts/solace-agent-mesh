// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useUserContext } from "@/providers/contexts";

import { useIsFeedbackManagementEnabled } from "./useIsFeedbackManagementEnabled";

export function useFeedbackManagementEnabled(): boolean {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const flagEnabled = useIsFeedbackManagementEnabled();
    return flagEnabled && userHasCapability(CAPABILITIES.readFeedback);
}
