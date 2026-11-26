// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BellOff, CircleCheckBig, XCircle } from "lucide-react";
import { describe, expect, test } from "vitest";

import type { LastExecutionSummary } from "@/types/scheduled-tasks";

import { lastRunIconConfig } from "./TaskCard";

const summary = (status: LastExecutionSummary["status"]): LastExecutionSummary => ({
    id: "e1",
    status,
    scheduledFor: "2026-01-01T00:00:00.000Z",
});

describe("lastRunIconConfig", () => {
    test("completed run uses the success check", () => {
        const { Icon, colorClass } = lastRunIconConfig(summary("completed"));
        expect(Icon).toBe(CircleCheckBig);
        expect(colorClass).toContain("success");
    });

    test("quiet run uses the muted bell, not the success or error icon", () => {
        const { Icon, colorClass } = lastRunIconConfig(summary("quiet"));
        expect(Icon).toBe(BellOff);
        expect(colorClass).toContain("secondary");
    });

    test("failed run uses the error icon", () => {
        expect(lastRunIconConfig(summary("failed")).Icon).toBe(XCircle);
    });
});
