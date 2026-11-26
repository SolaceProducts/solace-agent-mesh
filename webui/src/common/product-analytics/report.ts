// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ProductAnalyticsEventName } from "./types";

export interface ProductAnalyticsSink {
    track(event: ProductAnalyticsEventName, properties?: Record<string, unknown>): void;
    trackOnce(event: ProductAnalyticsEventName, properties?: Record<string, unknown>): void;
    trackFirstEver(event: ProductAnalyticsEventName, properties?: Record<string, unknown>): void;
    sessionId(): string | undefined;
    flushNow(): Promise<void>;
}

let sink: ProductAnalyticsSink | null = null;

export function setProductAnalyticsSink(next: ProductAnalyticsSink | null): void {
    if (!__SAM_PRODUCT_ANALYTICS__) return;
    sink = next;
}

export function report(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}): void {
    deliver(s => s.track(event, properties));
}

export function reportOnce(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}): void {
    deliver(s => s.trackOnce(event, properties));
}

export function reportFirstEver(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}): void {
    deliver(s => s.trackFirstEver(event, properties));
}

export function reportingSessionId(): string | undefined {
    if (!__SAM_PRODUCT_ANALYTICS__ || !sink) return undefined;
    try {
        return sink.sessionId();
    } catch (error) {
        console.warn("[Product Analytics] could not read the session:", error);
        return undefined;
    }
}

const FLUSH_WAIT_MS = 2_000;

export async function flushReports(): Promise<void> {
    if (!__SAM_PRODUCT_ANALYTICS__ || !sink) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const gaveUp = new Promise<void>(resolve => (timer = setTimeout(resolve, FLUSH_WAIT_MS)));
    try {
        await Promise.race([sink.flushNow(), gaveUp]);
    } catch (error) {
        console.warn("[Product Analytics] could not send queued events:", error);
    } finally {
        clearTimeout(timer);
    }
}

function deliver(send: (sink: ProductAnalyticsSink) => void): void {
    if (!__SAM_PRODUCT_ANALYTICS__ || !sink) return;
    try {
        send(sink);
    } catch (error) {
        console.warn("[Product Analytics] could not report an event:", error);
    }
}
