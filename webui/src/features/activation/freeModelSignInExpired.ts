// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useFreeModelStatus } from "@/api/activation";

import { useFreeModel } from "./useFreeModel";

export function useFreeModelSignInExpired(): boolean {
    const { inUse } = useFreeModel();
    const { data: status } = useFreeModelStatus(inUse);
    return inUse && status?.status === "reauthorization_required";
}
