// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, expect, test, vi } from "vitest";

vi.mock("@microsoft/teams-js", () => new Promise(() => {}));

beforeEach(() => {
    vi.useFakeTimers();
});

afterEach(() => {
    vi.useRealTimers();
});

test("gives up on an SDK that never loads instead of waiting forever", async () => {
    const { isInTeams, knownToBeInTeams } = await import("@/utils/teamsAuth");

    const pending = isInTeams();
    let settled = false;
    void pending.then(() => (settled = true));

    await vi.advanceTimersByTimeAsync(1_900);
    expect(settled).toBe(false);

    await vi.advanceTimersByTimeAsync(200);

    await expect(pending).resolves.toBe(false);
    expect(knownToBeInTeams()).toBe(false);
});
