// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { googleChatDelivery, supportsCustomEndpoint } from "./customEndpoint";

const SUBSCRIPTION = "projects/p/subscriptions/chat-events";

describe("googleChatDelivery", () => {
    it.each([
        ["nothing set", {}, "https"],
        ["no values", undefined, "https"],
        ["explicit https", { delivery: "https" }, "https"],
        ["padded and capitalised", { delivery: " PubSub " }, "pubsub"],
        ["a row stored before HTTPS existed", { pubsub_subscription: SUBSCRIPTION }, "pubsub"],
        ["blank delivery with a subscription", { delivery: "  ", pubsub_subscription: SUBSCRIPTION }, "pubsub"],
        ["explicit https beats a subscription", { delivery: "https", pubsub_subscription: SUBSCRIPTION }, "https"],
    ])("resolves %s like the backend", (_name, values, want) => {
        expect(googleChatDelivery(values)).toBe(want);
    });
});

describe("supportsCustomEndpoint", () => {
    it("offers a custom endpoint to MCP and to Google Chat over HTTPS", () => {
        expect(supportsCustomEndpoint("mcp")).toBe(true);
        expect(supportsCustomEndpoint("google_chat", { delivery: "https" })).toBe(true);
        expect(supportsCustomEndpoint("google_chat")).toBe(true);
    });

    it.each([
        ["explicit pubsub", { delivery: "pubsub" }],
        ["pubsub in another case", { delivery: "PubSub" }],
        ["a subscription-only row", { pubsub_subscription: SUBSCRIPTION }],
    ])("withholds it from Google Chat on Pub/Sub: %s", (_name, values) => {
        expect(supportsCustomEndpoint("google_chat", values)).toBe(false);
    });

    it("withholds it from types the operator does not register elsewhere", () => {
        expect(supportsCustomEndpoint("slack")).toBe(false);
        expect(supportsCustomEndpoint(undefined)).toBe(false);
    });
});
