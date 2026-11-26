// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { BEFORE_RECOVERY_RELOAD_EVENT, installStaleChunkRecovery, isChunkLoadError } from "@/utils/staleChunkRecovery";

const RELOAD_COUNT_KEY = "sam_stale_chunk_reloads";
const ORIGIN = "http://localhost:3000";
const CHUNK_URL = `${ORIGIN}/assets/main-OLD.js`;

let reload: ReturnType<typeof vi.fn>;
let warnSpy: ReturnType<typeof vi.spyOn>;
let originalLocation: Location;

const cleanups: Array<() => void> = [];

const install = () => {
    for (const remove of cleanups.splice(0)) remove();

    const registered: Array<[string, EventListener]> = [];
    const realAdd = globalThis.addEventListener.bind(globalThis);
    const spy = vi.spyOn(globalThis, "addEventListener").mockImplementation(((type: string, listener: EventListener, opts?: unknown) => {
        registered.push([type, listener]);
        realAdd(type as keyof WindowEventMap, listener, opts as AddEventListenerOptions);
    }) as typeof globalThis.addEventListener);

    installStaleChunkRecovery();
    spy.mockRestore();

    cleanups.push(() => {
        for (const [type, listener] of registered) globalThis.removeEventListener(type, listener);
    });
};

const serverSays = (status: number) => {
    vi.stubGlobal(
        "fetch",
        vi.fn(() => Promise.resolve({ status } as Response))
    );
};

const serverUnreachable = () => {
    vi.stubGlobal(
        "fetch",
        vi.fn(() => Promise.reject(new TypeError("Failed to fetch")))
    );
};

const chunkError = () => new Error(`Failed to fetch dynamically imported module: ${CHUNK_URL}`);

const settle = () => new Promise(resolve => setTimeout(resolve, 0));

const dispatchRejectionSync = (reason: unknown) => {
    const event = new Event("unhandledrejection") as Event & { reason?: unknown };
    event.reason = reason;
    globalThis.dispatchEvent(event);
};

const dispatchRejection = async (reason: unknown) => {
    dispatchRejectionSync(reason);
    await settle();
};

const dispatchPreloadError = async () => {
    const event = new Event("vite:preloadError") as Event & { payload?: unknown };
    event.payload = chunkError();
    globalThis.dispatchEvent(event);
    await settle();
};

beforeEach(() => {
    originalLocation = globalThis.location;
    reload = vi.fn();
    Object.defineProperty(globalThis, "location", {
        value: { reload, href: `${ORIGIN}/chat`, origin: ORIGIN },
        writable: true,
        configurable: true,
    });
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    sessionStorage.clear();
    serverSays(404);
});

afterEach(() => {
    for (const remove of cleanups.splice(0)) remove();
    Object.defineProperty(globalThis, "location", { value: originalLocation, writable: true, configurable: true });
    vi.unstubAllGlobals();
    warnSpy.mockRestore();
    sessionStorage.clear();
});

describe("isChunkLoadError", () => {
    test.each(["Failed to fetch dynamically imported module: /assets/main-OLD.js", "error loading dynamically imported module", "Importing a module script failed."])("recognises the engine wording %#", message => {
        expect(isChunkLoadError(new Error(message))).toBe(true);
    });

    test("recognises the named error type", () => {
        const err = new Error("boom");
        err.name = "ChunkLoadError";
        expect(isChunkLoadError(err)).toBe(true);
    });

    test.each([new Error("some unrelated failure"), "a string", null, undefined, { message: "not an Error" }])("ignores %#", value => {
        expect(isChunkLoadError(value)).toBe(false);
    });
});

describe("installStaleChunkRecovery", () => {
    test("reloads when the chunk is confirmed gone", async () => {
        install();

        await dispatchRejection(chunkError());

        expect(reload).toHaveBeenCalledOnce();
        expect(sessionStorage.getItem(RELOAD_COUNT_KEY)).toBe("1");
    });

    test("recovers on Vite's preload-error event", async () => {
        install();

        await dispatchPreloadError();

        expect(reload).toHaveBeenCalledOnce();
    });

    test("recovers from a script that will not load, which names the url on the element", async () => {
        install();

        const script = document.createElement("script");
        script.src = CHUNK_URL;
        document.body.appendChild(script);
        script.dispatchEvent(new Event("error", { bubbles: false }));
        await settle();
        script.remove();

        expect(globalThis.fetch).toHaveBeenCalledWith(CHUNK_URL, expect.objectContaining({ method: "HEAD" }));
        expect(reload).toHaveBeenCalledOnce();
    });

    test("recovers from a stylesheet that will not load", async () => {
        install();

        const link = document.createElement("link");
        link.rel = "stylesheet";
        link.href = CHUNK_URL;
        document.body.appendChild(link);
        link.dispatchEvent(new Event("error", { bubbles: false }));
        await settle();
        link.remove();

        expect(globalThis.fetch).toHaveBeenCalledWith(CHUNK_URL, expect.objectContaining({ method: "HEAD" }));
        expect(reload).toHaveBeenCalledOnce();
    });

    test("ignores a failed script from another origin", async () => {
        install();

        const script = document.createElement("script");
        script.src = "https://cdn.example.com/analytics.js";
        document.body.appendChild(script);
        script.dispatchEvent(new Event("error", { bubbles: false }));
        await settle();
        script.remove();

        expect(globalThis.fetch).not.toHaveBeenCalled();
        expect(reload).not.toHaveBeenCalled();
    });

    test("does not reload when the chunk is still there", async () => {
        serverSays(200);
        install();

        await dispatchRejection(chunkError());

        expect(reload).not.toHaveBeenCalled();
    });

    test("does not reload when the server is erroring", async () => {
        serverSays(503);
        install();

        await dispatchRejection(chunkError());

        expect(reload).not.toHaveBeenCalled();
    });

    test("does not reload when the probe cannot reach the server, because a reload would fail the same way", async () => {
        serverUnreachable();
        install();

        await dispatchRejection(chunkError());

        expect(reload).not.toHaveBeenCalled();
    });

    test("leaves an unrelated failure alone", async () => {
        install();

        await dispatchRejection(new Error("network request failed"));

        expect(reload).not.toHaveBeenCalled();
        expect(globalThis.fetch).not.toHaveBeenCalled();
    });

    test("leaves a chunk failure that names no url alone, since it cannot be shown to be stale", async () => {
        install();

        await dispatchRejection(new Error("Importing a module script failed."));

        expect(globalThis.fetch).not.toHaveBeenCalled();
        expect(reload).not.toHaveBeenCalled();
    });

    test("probes once when two imports fail in the same tick", async () => {
        install();

        dispatchRejectionSync(chunkError());
        dispatchRejectionSync(chunkError());
        await settle();

        expect(globalThis.fetch).toHaveBeenCalledOnce();
        expect(reload).toHaveBeenCalledOnce();
    });

    test("spends one reload per document, however many imports fail", async () => {
        install();

        for (let failure = 0; failure < 6; failure++) {
            await dispatchRejection(chunkError());
        }

        expect(reload).toHaveBeenCalledOnce();
        expect(sessionStorage.getItem(RELOAD_COUNT_KEY)).toBe("1");
    });

    test("stops after the cap, however many documents it takes", async () => {
        for (let document = 0; document < 6; document++) {
            install();
            await dispatchRejection(chunkError());
        }

        expect(reload).toHaveBeenCalledTimes(3);
        expect(sessionStorage.getItem(RELOAD_COUNT_KEY)).toBe("3");
    });

    test("refuses outright when the count cannot be read", async () => {
        vi.stubGlobal("sessionStorage", {
            getItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            setItem: vi.fn(),
            removeItem: vi.fn(),
            clear: vi.fn(),
        });
        install();

        await dispatchRejection(chunkError());

        expect(reload).not.toHaveBeenCalled();
    });

    test("refuses when the count cannot be written, so it could never be spent", async () => {
        vi.stubGlobal("sessionStorage", {
            getItem: vi.fn(() => null),
            setItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            removeItem: vi.fn(),
            clear: vi.fn(),
        });
        install();

        await dispatchRejection(chunkError());

        expect(reload).not.toHaveBeenCalled();
    });

    test("announces the reload before navigating", async () => {
        const order: string[] = [];
        const listener = () => order.push("announced");
        globalThis.addEventListener(BEFORE_RECOVERY_RELOAD_EVENT, listener);
        reload.mockImplementation(() => order.push("reloaded"));
        install();

        await dispatchRejection(chunkError());
        globalThis.removeEventListener(BEFORE_RECOVERY_RELOAD_EVENT, listener);

        expect(order).toEqual(["announced", "reloaded"]);
    });
});
