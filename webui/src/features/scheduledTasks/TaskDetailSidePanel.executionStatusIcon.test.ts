// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BellOff, CircleCheckBig, XCircle } from "lucide-react";
import { describe, expect, test } from "vitest";

import { executionStatusIcon } from "./TaskDetailSidePanel";

describe("executionStatusIcon", () => {
    test("completed → success check labelled 'Succeeded'", () => {
        const r = executionStatusIcon("completed");
        expect(r.Icon).toBe(CircleCheckBig);
        expect(r.label).toBe("Succeeded");
        expect(r.iconClassName).toContain("success");
    });

    test("failed → error icon labelled 'Failed'", () => {
        const r = executionStatusIcon("failed");
        expect(r.Icon).toBe(XCircle);
        expect(r.label).toBe("Failed");
    });

    test("quiet → muted bell labelled 'Nothing to report'", () => {
        const r = executionStatusIcon("quiet");
        expect(r.Icon).toBe(BellOff);
        expect(r.label).toBe("Nothing to report");
        expect(r.iconClassName).toContain("secondary");
    });

    test("running → labelled 'Running'", () => {
        expect(executionStatusIcon("running").label).toBe("Running");
    });
});
