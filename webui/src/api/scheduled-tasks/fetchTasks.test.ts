// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { beforeEach, describe, expect, test, vi } from "vitest";

describe("fetchTasks", () => {
    let fetchTasks: typeof import("@/api/scheduled-tasks/service").fetchTasks;
    const getMock = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        getMock.mockReset();

        vi.doMock("@/api/client", () => ({
            api: { webui: { get: getMock } },
        }));

        const mod = await import("@/api/scheduled-tasks/service");
        fetchTasks = mod.fetchTasks;
    });

    test("returns an empty page on 404 (scheduler routes unmounted), never an error banner", async () => {
        getMock.mockResolvedValue({ ok: false, status: 404 });

        const result = await fetchTasks();

        expect(result.data).toEqual([]);
        expect(result.meta.pagination.count).toBe(0);
        expect(getMock).toHaveBeenCalledWith(expect.stringContaining("/api/v1/scheduledTasks/"), { fullResponse: true });
    });

    test("returns an empty page on 501", async () => {
        getMock.mockResolvedValue({ ok: false, status: 501 });

        const result = await fetchTasks();

        expect(result.data).toEqual([]);
    });

    test("throws on other non-2xx (e.g. 500) so React Query flips isError", async () => {
        getMock.mockResolvedValue({ ok: false, status: 500 });

        await expect(fetchTasks()).rejects.toThrow(/500/);
    });

    test("parses and returns the list on success", async () => {
        getMock.mockResolvedValue({
            ok: true,
            status: 200,
            json: async () => ({
                data: [{ id: "t1", name: "task" }],
                meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } },
            }),
        });

        const result = await fetchTasks();

        expect(result.data).toHaveLength(1);
        expect(result.data[0].id).toBe("t1");
    });
});
