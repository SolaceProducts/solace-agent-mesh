// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { PlatformSource } from "./platform";
import { currentPlatformSource, detectClientPlatform, resolveClientPlatform } from "./platform";

const GOOS = ["aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows"];
const GOARCH = ["386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm"];

function highEntropy(architecture: string, bitness: string) {
    return {
        getHighEntropyValues: () => Promise.resolve({ architecture, bitness }),
    };
}

describe("detectClientPlatform — the synchronous reading", () => {
    test("maps every userAgentData platform onto a GOOS value", () => {
        const cases: Record<string, string> = {
            Android: "android",
            "Chrome OS": "linux",
            "Chromium OS": "linux",
            iOS: "ios",
            Linux: "linux",
            Windows: "windows",
            macOS: "darwin",
        };
        for (const [platform, want] of Object.entries(cases)) {
            expect(detectClientPlatform({ userAgentData: { platform } }).os).toBe(want);
            expect(GOOS).toContain(want);
        }
    });

    test("falls back to navigator.platform for the OS family", () => {
        const cases: Record<string, string> = {
            MacIntel: "darwin",
            "Linux x86_64": "linux",
            "Linux aarch64": "linux",
            Win32: "windows",
            iPhone: "ios",
        };
        for (const [navigatorPlatform, want] of Object.entries(cases)) {
            expect(detectClientPlatform({ navigatorPlatform }).os).toBe(want);
        }
    });

    test("reads the architecture from navigator.platform only where the value names the machine", () => {
        expect(detectClientPlatform({ navigatorPlatform: "Linux x86_64" }).arch).toBe("amd64");
        expect(detectClientPlatform({ navigatorPlatform: "Linux aarch64" }).arch).toBe("arm64");
    });

    test("reports no architecture for the values that do not name one", () => {
        expect(detectClientPlatform({ navigatorPlatform: "MacIntel" }).arch).toBeUndefined();
        expect(detectClientPlatform({ navigatorPlatform: "Win32" }).arch).toBeUndefined();
    });

    test("reports nothing at all when the browser offers neither read", () => {
        expect(detectClientPlatform({})).toEqual({ os: undefined, arch: undefined });
    });

    test("passes no unrecognised platform string through", () => {
        const forged = ["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "Ubuntu 22.04", "SunOS", ""];
        for (const value of forged) {
            expect(detectClientPlatform({ userAgentData: { platform: value } }).os).toBeUndefined();
            expect(detectClientPlatform({ navigatorPlatform: value }).os).toBeUndefined();
        }
    });
});

describe("resolveClientPlatform — the asynchronous refinement", () => {
    test("maps the userAgentData architecture hints onto GOARCH values", async () => {
        const cases: [string, string, string][] = [
            ["x86", "64", "amd64"],
            ["x86", "32", "386"],
            ["arm", "64", "arm64"],
            ["arm", "32", "arm"],
            ["arm64", "64", "arm64"],
        ];
        for (const [architecture, bitness, want] of cases) {
            const source: PlatformSource = { userAgentData: { platform: "Windows", ...highEntropy(architecture, bitness) } };
            expect((await resolveClientPlatform(source)).arch).toBe(want);
            expect(GOARCH).toContain(want);
        }
    });

    test("reports no architecture for a hint pair outside the vocabulary", async () => {
        const source: PlatformSource = { userAgentData: { platform: "Windows", ...highEntropy("sparc", "64") } };
        expect((await resolveClientPlatform(source)).arch).toBeUndefined();
    });

    test("prefers the desktop host environment, which reports the machine exactly", async () => {
        const source: PlatformSource = {
            navigatorPlatform: "MacIntel",
            hostEnvironment: () => Promise.resolve({ platform: "darwin", arch: "arm64" }),
        };
        expect(await resolveClientPlatform(source)).toEqual({ os: "darwin", arch: "arm64" });
    });

    test("falls back to the browser reads when the desktop bridge throws", async () => {
        const source: PlatformSource = {
            navigatorPlatform: "Linux x86_64",
            hostEnvironment: () => Promise.reject(new Error("bridge closed")),
        };
        expect(await resolveClientPlatform(source)).toEqual({ os: "linux", arch: "amd64" });
    });

    test("falls back to the browser reads when the desktop bridge answers nothing", async () => {
        const source: PlatformSource = {
            navigatorPlatform: "Linux x86_64",
            hostEnvironment: () => Promise.resolve({}),
        };
        expect(await resolveClientPlatform(source)).toEqual({ os: "linux", arch: "amd64" });
    });

    test("does not overwrite an architecture navigator.platform already named", async () => {
        const source: PlatformSource = { navigatorPlatform: "Linux aarch64", userAgentData: highEntropy("x86", "64") };
        expect((await resolveClientPlatform(source)).arch).toBe("arm64");
    });

    test("reports nothing rather than throwing or the server's platform when every read fails", async () => {
        const source: PlatformSource = {
            userAgentData: { getHighEntropyValues: () => Promise.reject(new Error("blocked")) },
        };
        await expect(resolveClientPlatform(source)).resolves.toEqual({ os: undefined, arch: undefined });
    });
});

describe("currentPlatformSource", () => {
    test("collects navigator.platform from the running page", () => {
        expect(currentPlatformSource().navigatorPlatform).toBe(globalThis.navigator.platform);
    });

    test("answers nothing from the host bridge outside the desktop WebView", async () => {
        await expect(currentPlatformSource().hostEnvironment!()).resolves.toEqual({});
    });

    test("finds a desktop bridge injected after the source was built", async () => {
        const source = currentPlatformSource();
        vi.stubGlobal("runtime", { Environment: () => Promise.resolve({ platform: "darwin", arch: "arm64" }) });

        await expect(source.hostEnvironment!()).resolves.toEqual({ platform: "darwin", arch: "arm64" });
        vi.unstubAllGlobals();
    });
});
