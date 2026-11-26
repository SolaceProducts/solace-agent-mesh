// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { VisualizedTask } from "@/types/activities";

export const TEST_RUNNER_LABEL = "Test Runner";

export function rootTestAgentName(task: VisualizedTask | null): string | undefined {
    const entry = task?.steps.find(s => s.nestingLevel === 0 && s.source === "User" && !!s.target && s.target !== "User");
    return entry?.target;
}

export function testRunnerNameOverrides(task: VisualizedTask | null): Record<string, string> | undefined {
    const rootName = rootTestAgentName(task);
    return rootName ? { [rootName]: TEST_RUNNER_LABEL } : undefined;
}
