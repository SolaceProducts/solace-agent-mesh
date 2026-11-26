// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { fetchFeedbackItem } from "./item";

describe("fetchFeedbackItem", () => {
    afterEach(() => vi.restoreAllMocks());

    it("requests the bare item route", async () => {
        const get = vi.spyOn(api.webui, "get").mockResolvedValue({ id: "f1" } as never);
        await expect(fetchFeedbackItem("f1")).resolves.toEqual({ id: "f1" });
        expect(get.mock.calls[0][0]).toBe("/api/v1/feedback/f1");
    });

    it("propagates a 404 so the caller can render not found", async () => {
        vi.spyOn(api.webui, "get").mockRejectedValue(Object.assign(new Error("feedback not found"), { status: 404 }));
        await expect(fetchFeedbackItem("f1")).rejects.toMatchObject({ status: 404 });
    });
});
