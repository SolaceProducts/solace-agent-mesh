// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation } from "@tanstack/react-query";

import * as researchService from "./service";

export function useSubmitPlanResponse() {
    return useMutation({
        mutationFn: researchService.submitPlanResponse,
    });
}
