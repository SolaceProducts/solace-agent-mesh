// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const CUSTOM_ENDPOINT_TYPES = new Set(["mcp", "google_chat"]);

const GOOGLE_CHAT_PULL_DELIVERY = "pubsub";
const GOOGLE_CHAT_PUSH_DELIVERY = "https";

const trimmedString = (value: unknown): string => (typeof value === "string" ? value.trim() : "");

export function googleChatDelivery(values?: Record<string, unknown>): string {
    const delivery = trimmedString(values?.delivery).toLowerCase();
    if (delivery) return delivery;
    return trimmedString(values?.pubsub_subscription) ? GOOGLE_CHAT_PULL_DELIVERY : GOOGLE_CHAT_PUSH_DELIVERY;
}

export function googleChatPullsEvents(values?: Record<string, unknown>): boolean {
    return googleChatDelivery(values) === GOOGLE_CHAT_PULL_DELIVERY;
}

export function supportsCustomEndpoint(type: string | undefined, values?: Record<string, unknown>): boolean {
    if (type === undefined || !CUSTOM_ENDPOINT_TYPES.has(type)) return false;
    return !(type === "google_chat" && googleChatPullsEvents(values));
}
