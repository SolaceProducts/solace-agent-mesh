// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CrossUserFeedback } from "@/api/feedback";

export function buildFeedbackTracePrefill(feedback: Pick<CrossUserFeedback, "id" | "rating">): string {
    const ratingWord = feedback.rating === "up" ? "positive" : "negative";
    return `Why was this response rated ${ratingWord}? Feedback ID ${feedback.id}`;
}
