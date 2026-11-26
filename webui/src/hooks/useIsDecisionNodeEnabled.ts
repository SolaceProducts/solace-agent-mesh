// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

export function useIsDecisionNodeEnabled(): boolean {
    return useBooleanFlagValue("workflow_decision_node", false);
}
