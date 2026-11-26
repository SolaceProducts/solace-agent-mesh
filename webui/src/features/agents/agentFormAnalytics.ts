// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FieldErrors, FieldValues } from "react-hook-form";

import { trackFormErrors } from "@/common/product-analytics/trackFormErrors";

export function trackAgentFormErrors<T extends FieldValues>(errors: FieldErrors<T>): void {
    trackFormErrors(errors, "agent");
}
