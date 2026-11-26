// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import { uuid } from "@/utils/uuid";

import type { ClientPlatform, PlatformSource } from "./platform";
import { currentPlatformSource, detectClientPlatform, resolveClientPlatform } from "./platform";
import { setProductAnalyticsSink } from "./report";
import type { ProductAnalyticsEventName } from "./types";

const BATCH_SIZE = 25;
const FLUSH_INTERVAL_MS = 5_000;
const INGEST_ENDPOINT = "/api/v1/productAnalytics/ingest";

const MAX_CONSECUTIVE_FAILURES = 20;

const KEEPALIVE_CHUNK_BYTES_UNDER_FETCH_QUOTA = 60_000;

const SESSION_STORAGE_KEY = "sam.productAnalytics.sessionId";

const MILESTONE_STORAGE_PREFIX = "sam.productAnalytics.milestone.";

function milestoneReached(event: ProductAnalyticsEventName): boolean {
    try {
        return localStorage.getItem(MILESTONE_STORAGE_PREFIX + event) !== null;
    } catch {
        return false;
    }
}

function recordMilestone(event: ProductAnalyticsEventName): void {
    try {
        localStorage.setItem(MILESTONE_STORAGE_PREFIX + event, "1");
    } catch {}
}

function forgetMilestones(): void {
    try {
        const stale: string[] = [];
        for (let i = 0; i < localStorage.length; i++) {
            const key = localStorage.key(i);
            if (key?.startsWith(MILESTONE_STORAGE_PREFIX)) stale.push(key);
        }
        stale.forEach(key => localStorage.removeItem(key));
    } catch {}
}

function readSessionId(): string {
    try {
        const existing = sessionStorage.getItem(SESSION_STORAGE_KEY);
        if (existing) return existing;
        const minted = uuid();
        sessionStorage.setItem(SESSION_STORAGE_KEY, minted);
        return minted;
    } catch {
        return uuid();
    }
}

interface QueuedEvent {
    eventId: string;
    sessionId: string;
    occurredAt: string;
    event: string;
    properties: Record<string, unknown>;
}

interface FlushOptions {
    keepalive: boolean;

    platform: ClientPlatform;
}

type Transport = (events: QueuedEvent[], options: FlushOptions) => void | Promise<void>;

const encoder = /*#__PURE__*/ new TextEncoder();

function chunkByByteSize(events: QueuedEvent[], maxBytes: number): QueuedEvent[][] {
    const chunks: QueuedEvent[][] = [];
    let current: QueuedEvent[] = [];
    let currentBytes = 0;
    for (const event of events) {
        const size = encoder.encode(JSON.stringify(event)).length;
        if (current.length > 0 && currentBytes + size > maxBytes) {
            chunks.push(current);
            current = [];
            currentBytes = 0;
        }
        current.push(event);
        currentBytes += size;
    }
    if (current.length > 0) chunks.push(current);
    return chunks;
}

export const fetchTransport: Transport = async (events, { keepalive, platform }) => {
    if (!keepalive) {
        await api.webui.post(INGEST_ENDPOINT, { platform, events });
        return;
    }
    await api.webui.post(INGEST_ENDPOINT, { platform, events }, { keepalive: true });
};

const consoleTransport: Transport = events => {
    console.groupCollapsed(`[Product Analytics] ${events.length} event(s)`);
    events.forEach(e => console.log(e.event, e.properties));
    console.groupEnd();
};

interface ProductAnalyticsClientOptions {
    transport?: Transport;
    platformSource?: PlatformSource;
}

export class ProductAnalyticsClient {
    private readonly queue: QueuedEvent[] = [];
    private intervalId?: ReturnType<typeof setInterval>;
    private abortController?: AbortController;
    private readonly transport: Transport;
    private listenersAttached = false;
    private consecutiveFailures = 0;
    private stopped = false;
    private cachedSessionId?: string;
    private readonly reportedThisLoad = new Set<ProductAnalyticsEventName>();

    private platform: ClientPlatform;

    private featureEnabled = false;

    private consentEnabled = false;

    private consentInitialized = false;

    constructor(options: ProductAnalyticsClientOptions = {}) {
        this.transport = options.transport ?? (import.meta.env.DEV ? consoleTransport : fetchTransport);
        const source = options.platformSource ?? currentPlatformSource();
        this.platform = detectClientPlatform(source);
        this.refinePlatform(source);
    }

    private refinePlatform(source: PlatformSource): void {
        void resolveClientPlatform(source).then(resolved => {
            this.platform = resolved;
        });
    }

    // global interval or click handler at all. Idempotent.
    private attachListeners() {
        if (this.listenersAttached) return;
        this.listenersAttached = true;
        this.abortController = new AbortController();
        const { signal } = this.abortController;

        this.intervalId = setInterval(() => this.flush(), FLUSH_INTERVAL_MS);
        document.addEventListener(
            "visibilitychange",
            () => {
                if (document.hidden) this.flush(true);
            },
            { signal }
        );
    }

    private canTrack(): boolean {
        return !this.stopped && this.featureEnabled && this.consentEnabled;
    }

    trackOnce(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}) {
        if (!this.canTrack() || this.reportedThisLoad.has(event)) return;
        this.reportedThisLoad.add(event);
        this.track(event, properties);
    }

    trackFirstEver(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}) {
        if (!this.canTrack() || milestoneReached(event)) return;
        recordMilestone(event);
        this.track(event, properties);
    }

    track(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}) {
        if (!this.canTrack()) return;
        this.queue.push({
            eventId: uuid(),
            sessionId: this.currentSessionId(),
            occurredAt: new Date().toISOString(),
            event,
            properties: {
                ...properties,
                interface: "ui",
            },
        });
        if (this.queue.length >= BATCH_SIZE) {
            this.flush();
        }
    }

    sessionId(): string | undefined {
        if (!this.canTrack()) return undefined;
        return this.currentSessionId();
    }

    private currentSessionId(): string {
        this.cachedSessionId ??= readSessionId();
        return this.cachedSessionId;
    }

    destroy() {
        if (this.intervalId !== undefined) {
            clearInterval(this.intervalId);
            this.intervalId = undefined;
        }
        this.abortController?.abort();
        this.listenersAttached = false;
        this.stopped = false;
        this.consecutiveFailures = 0;
        this.reportedThisLoad.clear();
    }

    setFeatureEnabled(enabled: boolean) {
        this.featureEnabled = enabled;
        if (enabled) this.attachListeners();
    }

    setConsent(collecting: boolean) {
        const isInitialMirror = !this.consentInitialized;
        this.consentInitialized = true;
        this.consentEnabled = collecting;
        if (!collecting) this.queue.length = 0;
        if (!collecting && !isInitialMirror) {
            this.forgetSession();
            forgetMilestones();
        }
    }

    private forgetSession() {
        this.cachedSessionId = undefined;
        try {
            sessionStorage.removeItem(SESSION_STORAGE_KEY);
        } catch {}
    }

    async flushNow(): Promise<void> {
        if (!this.canTrack() || this.queue.length === 0) return;
        await this.send(this.queue.splice(0), { keepalive: false, platform: this.platform });
    }

    private flush(keepalive = false) {
        if (!this.canTrack() || this.queue.length === 0) return;
        void this.send(this.queue.splice(0), { keepalive, platform: this.platform });
    }

    private async send(batch: QueuedEvent[], options: FlushOptions) {
        try {
            for (const chunk of options.keepalive ? chunkByByteSize(batch, KEEPALIVE_CHUNK_BYTES_UNDER_FETCH_QUOTA) : [batch]) {
                if (!this.canTrack()) return;
                await this.transport(chunk, options);
                this.consecutiveFailures = 0;
            }
        } catch (error) {
            this.consecutiveFailures += 1;
            if (this.consecutiveFailures === 1) {
                console.warn(`[Product Analytics] flush failed, dropping up to ${batch.length} event(s):`, error);
            } else if (import.meta.env.DEV) {
                console.debug(`[Product Analytics] flush failed (attempt ${this.consecutiveFailures})`, error);
            }
            if (this.consecutiveFailures >= MAX_CONSECUTIVE_FAILURES) this.stop();
        }
    }

    private stop() {
        if (this.stopped) return;
        this.stopped = true;
        console.error(`[Product Analytics] giving up after ${MAX_CONSECUTIVE_FAILURES} consecutive flush failures; no further events will be sent this session.`);
        if (this.intervalId !== undefined) {
            clearInterval(this.intervalId);
            this.intervalId = undefined;
        }
    }
}

export const productAnalyticsClient = /*#__PURE__*/ new ProductAnalyticsClient();

if (__SAM_PRODUCT_ANALYTICS__) setProductAnalyticsSink(productAnalyticsClient);
