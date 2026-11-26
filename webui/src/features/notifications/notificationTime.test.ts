// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { formatNotificationTime } from "./notificationTime";

const local = (...parts: [number, number, number, number, number]) => new Date(...parts).toISOString();

describe("formatNotificationTime", () => {
    it.each([
        ["a morning time", local(2026, 9, 17, 8, 2), "17/10/2026, 8:02 am"],
        ["an afternoon time", local(2026, 9, 5, 17, 19), "5/10/2026, 5:19 pm"],
        ["just after midnight", local(2026, 0, 1, 0, 5), "1/1/2026, 12:05 am"],
        ["noon", local(2026, 11, 31, 12, 30), "31/12/2026, 12:30 pm"],
    ])("shows %s day first with a lowercase am or pm", (_name, iso, want) => {
        expect(formatNotificationTime(iso)).toBe(want);
    });

    it("shows nothing for a time it cannot read", () => {
        expect(formatNotificationTime("not a time")).toBe("");
    });
});
