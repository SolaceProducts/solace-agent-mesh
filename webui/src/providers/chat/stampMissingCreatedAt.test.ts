// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { MessageFE } from "@/types";

import { stampMissingCreatedAt } from "./stampMissingCreatedAt";

const msg = (createdAt?: number): MessageFE => ({ role: "agent", parts: [], isUser: false, ...(createdAt != null ? { createdAt } : {}) }) as MessageFE;

describe("stampMissingCreatedAt", () => {
    it("returns the same array reference when no message is missing createdAt", () => {
        const messages = [msg(100), msg(200)];
        expect(stampMissingCreatedAt(messages, 999)).toBe(messages);
    });

    it("stamps only messages missing createdAt and preserves existing values", () => {
        const messages = [msg(100), msg(), msg(300)];
        const result = stampMissingCreatedAt(messages, 999);
        expect(result).not.toBe(messages);
        expect(result.map(m => m.createdAt)).toEqual([100, 999, 300]);
    });

    it("stamps all messages when all are missing createdAt", () => {
        const result = stampMissingCreatedAt([msg(), msg()], 999);
        expect(result.map(m => m.createdAt)).toEqual([999, 999]);
    });

    it("treats createdAt of 0 as present (not missing)", () => {
        const messages = [msg(0)];
        expect(stampMissingCreatedAt(messages, 999)).toBe(messages);
    });
});
