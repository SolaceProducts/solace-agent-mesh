// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { QueryClient } from "@tanstack/react-query";

import { activationKeys } from "./keys";

const LITELLM_SPEND_BATCH_MS = 10_000;
const LITELLM_SPEND_SETTLED_MS = 60_000;

export function rereadFreeModelAfterReply(queryClient: QueryClient) {
    const reread = () => queryClient.invalidateQueries({ queryKey: activationKeys.freeModel() });
    reread();
    setTimeout(reread, LITELLM_SPEND_BATCH_MS);
    setTimeout(reread, LITELLM_SPEND_SETTLED_MS);
}
