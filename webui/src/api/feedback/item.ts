// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { CrossUserFeedback } from "./types";

export async function fetchFeedbackItem(id: string): Promise<CrossUserFeedback> {
    return api.webui.get<CrossUserFeedback>(`/api/v1/feedback/${encodeURIComponent(id)}`);
}
