// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "@testing-library/jest-dom/vitest";

import { afterEach } from "vitest";

import { queryClient } from "@/providers/query/QueryClient";

afterEach(() => {
    queryClient.clear();
});

if (typeof Promise.withResolvers === "undefined") {
    (Promise as any).withResolvers = function <T>() {
        let resolve!: (value: T | PromiseLike<T>) => void;
        let reject!: (reason?: unknown) => void;
        const promise = new Promise<T>((res, rej) => {
            resolve = res;
            reject = rej;
        });
        return { promise, resolve, reject };
    };
}

const localStorageMock = {
    store: {} as Record<string, string>,
    getItem(key: string) {
        return this.store[key] ?? null;
    },
    setItem(key: string, value: string) {
        this.store[key] = value;
    },
    removeItem(key: string) {
        delete this.store[key];
    },
    clear() {
        this.store = {};
    },
    get length() {
        return Object.keys(this.store).length;
    },
    key(index: number) {
        return Object.keys(this.store)[index] ?? null;
    },
};

Object.defineProperty(globalThis, "localStorage", { value: localStorageMock });

Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
    }),
});

if (typeof Element !== "undefined" && typeof Element.prototype.scrollIntoView !== "function") {
    Element.prototype.scrollIntoView = function () {};
}

if (typeof Element !== "undefined" && typeof Element.prototype.hasPointerCapture !== "function") {
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
}
