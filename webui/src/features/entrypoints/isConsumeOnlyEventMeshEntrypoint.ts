// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EventRule, Gateway } from "@/types/models";

export const isConsumeOnlyEventMeshEntrypoint = (gateway: Gateway | null | undefined): boolean => {
    const rules = gateway?.values?.event_rules;
    if (!Array.isArray(rules)) return false;
    return rules.some((rule: EventRule) => typeof rule?.existingQueueName === "string" && rule.existingQueueName.trim() !== "");
};
