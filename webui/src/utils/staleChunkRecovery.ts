// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const RELOAD_COUNT_KEY = "sam_stale_chunk_reloads";
const MAX_RELOADS = 3;

export const BEFORE_RECOVERY_RELOAD_EVENT = "sam:before-recovery-reload";

export function isChunkLoadError(err: unknown): boolean {
    if (!(err instanceof Error)) return false;
    return err.name === "ChunkLoadError" || /dynamically imported module|module script failed/i.test(err.message);
}

function sameOriginURL(candidate: string | undefined): string | undefined {
    if (!candidate) return undefined;
    try {
        const url = new URL(candidate, globalThis.location.href);
        return url.origin === globalThis.location.origin ? url.href : undefined;
    } catch {
        return undefined;
    }
}

function failedURL(err: unknown, target?: EventTarget | null): string | undefined {
    const element = target as { src?: unknown; href?: unknown } | null | undefined;
    if (typeof element?.src === "string" && element.src) return sameOriginURL(element.src);
    if (typeof element?.href === "string" && element.href) return sameOriginURL(element.href);
    if (!(err instanceof Error)) return undefined;
    return sameOriginURL(/(https?:\/\/[^\s)"']+)/.exec(err.message)?.[1]);
}

async function chunkIsGone(url: string | undefined): Promise<boolean> {
    if (!url) {
        console.debug("[app] a chunk failed but named no file this app serves, so there is nothing to check; not reloading.");
        return false;
    }
    try {
        const response = await fetch(url, { method: "HEAD", cache: "no-store" });
        return response.status === 404;
    } catch {
        return false;
    }
}

function reloadsSoFar(): number {
    try {
        return Number(sessionStorage.getItem(RELOAD_COUNT_KEY)) || 0;
    } catch {
        return MAX_RELOADS;
    }
}

function recordReload(count: number): boolean {
    try {
        sessionStorage.setItem(RELOAD_COUNT_KEY, String(count));
        return true;
    } catch {
        return false;
    }
}

export function installStaleChunkRecovery(): void {
    let probing = false;
    let reloading = false;

    const recover = async (err: unknown, target?: EventTarget | null) => {
        if (reloading || probing) return;
        const count = reloadsSoFar();
        if (count >= MAX_RELOADS) return;

        probing = true;
        try {
            if (!(await chunkIsGone(failedURL(err, target)))) return;
            if (!recordReload(count + 1)) return;
            reloading = true;
            console.warn("[app] a code-split chunk is missing, which happens when this tab outlived a deploy; reloading to pick up the current build.");
            globalThis.dispatchEvent(new Event(BEFORE_RECOVERY_RELOAD_EVENT));
            globalThis.location.reload();
        } finally {
            probing = false;
        }
    };

    globalThis.addEventListener("vite:preloadError", event => {
        void recover((event as Event & { payload?: unknown }).payload);
    });
    globalThis.addEventListener("unhandledrejection", event => {
        if (isChunkLoadError(event.reason)) void recover(event.reason);
    });
    globalThis.addEventListener(
        "error",
        event => {
            const target = event.target;
            if (target instanceof HTMLScriptElement || target instanceof HTMLLinkElement) {
                void recover(undefined, target);
                return;
            }
            if (isChunkLoadError(event.error)) void recover(event.error);
        },
        { capture: true }
    );
}
