// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { addressSegment, googleChatWebhookUrl } from "./endpoint";

describe("addressSegment", () => {
    test("prefers the slug and falls back to the id", () => {
        expect(addressSegment("google-chat", "abc")).toBe("google-chat");
        expect(addressSegment(undefined, "abc")).toBe("abc");
        expect(addressSegment("", undefined)).toBe("");
    });
});

describe("googleChatWebhookUrl", () => {
    test.each([
        { name: "the stored URL wins", gateway: { slug: "hr", values: { endpoint_url: "https://t.example.com/gw/hr/events" } }, base: "https://sam.example.com", want: "https://t.example.com/gw/hr/events" },
        { name: "an empty stored URL is derived from the base", gateway: { slug: "hr", values: { endpoint_url: "" } }, base: "https://sam.example.com/", want: "https://sam.example.com/gw/hr/events" },
        { name: "an http base is still shown", gateway: { slug: "hr", values: {} }, base: "http://localhost:3029", want: "http://localhost:3029/gw/hr/events" },
        { name: "the id stands in when there is no slug", gateway: { id: "gw-1", values: {} }, base: "https://sam.example.com", want: "https://sam.example.com/gw/gw-1/events" },
    ])("$name", ({ gateway, base, want }) => {
        expect(googleChatWebhookUrl(gateway, base)).toBe(want);
    });
});
