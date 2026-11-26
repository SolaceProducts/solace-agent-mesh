// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { fetchFeedbackHistory, fetchFeedbackSummaries } from "./service";
import type { FeedbackHistoryResponse } from "./types";

describe("fetchFeedbackHistory", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    it("sends only the params that are set", async () => {
        const mockResponse: FeedbackHistoryResponse = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } };
        const get = vi.spyOn(api.webui, "get").mockResolvedValue(mockResponse as unknown as Response);

        await fetchFeedbackHistory({ pageNumber: 2, pageSize: 20, rating: "down" });

        const url = get.mock.calls[0][0] as string;
        expect(url).toContain("pageNumber=2");
        expect(url).toContain("pageSize=20");
        expect(url).toContain("rating=down");
        expect(url).not.toContain("agentIds");
        expect(url).not.toContain("startDate");
    });

    it("sends subject ids as a comma-delimited list", async () => {
        const mockResponse: FeedbackHistoryResponse = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } };
        const get = vi.spyOn(api.webui, "get").mockResolvedValue(mockResponse as unknown as Response);

        await fetchFeedbackHistory({ pageNumber: 1, pageSize: 20, agentIds: ["a-1", "a-2"] });

        expect(get.mock.calls[0][0] as string).toContain("agentIds=a-1%2Ca-2");
    });

    it("sends user ids, and omits them when the list is empty", async () => {
        const mockResponse: FeedbackHistoryResponse = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } };
        const get = vi.spyOn(api.webui, "get").mockResolvedValue(mockResponse as unknown as Response);

        await fetchFeedbackHistory({ pageNumber: 1, pageSize: 20, userIds: ["olive@example.com"] });
        expect(get.mock.calls[0][0] as string).toContain("userIds=olive%40example.com");

        await fetchFeedbackHistory({ pageNumber: 1, pageSize: 20, userIds: [] });
        expect(get.mock.calls[1][0] as string).not.toContain("userIds");
    });
});

describe("fetchFeedbackSummaries", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    it("sends only the params that are set, comma-joining id lists", async () => {
        const get = vi.spyOn(api.webui, "get").mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } } as unknown as Response);

        await fetchFeedbackSummaries({
            groupBy: "subject",
            trendGranularity: "none",
            agentIds: ["a1", "a2"],
            startDate: "2026-09-01T00:00:00.000Z",
            endDate: "2026-09-30T00:00:00.000Z",
            sortBy: "name",
            sortDir: "asc",
            pageSize: 100,
        });

        const url = get.mock.calls[0][0] as string;
        const query = new URLSearchParams(url.split("?")[1]);
        expect(url.startsWith("/api/v1/feedback/summaries?")).toBe(true);
        expect(query.get("groupBy")).toBe("subject");
        expect(query.get("trendGranularity")).toBe("none");
        expect(query.get("agentIds")).toBe("a1,a2");
        expect(query.get("startDate")).toBe("2026-09-01T00:00:00.000Z");
        expect(query.get("sort")).toBe("name:asc");
        expect(query.has("sortBy")).toBe(false);
        expect(query.has("sortDir")).toBe(false);
        expect(query.get("pageSize")).toBe("100");
        expect(query.has("workflowIds")).toBe(false);
        expect(query.has("subjectType")).toBe(false);
    });

    it("omits an empty id list rather than sending an empty value", async () => {
        const get = vi.spyOn(api.webui, "get").mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } } as unknown as Response);

        await fetchFeedbackSummaries({ groupBy: "tenant", trendGranularity: "none", agentIds: [] });

        const query = new URLSearchParams((get.mock.calls[0][0] as string).split("?")[1]);
        expect(query.has("agentIds")).toBe(false);
    });
});
