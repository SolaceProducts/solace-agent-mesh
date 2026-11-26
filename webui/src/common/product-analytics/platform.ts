// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface ClientPlatform {
    os?: string;
    arch?: string;
}

interface HighEntropyHints {
    architecture?: string;
    bitness?: string;
}

interface UserAgentData {
    platform?: string;
    getHighEntropyValues?: (hints: string[]) => Promise<HighEntropyHints>;
}

interface HostEnvironment {
    platform?: string;
    arch?: string;
}

export interface PlatformSource {
    navigatorPlatform?: string;
    userAgentData?: UserAgentData;
    hostEnvironment?: () => Promise<HostEnvironment>;
}

const GOOS_BY_UA_DATA_PLATFORM: Record<string, string> = {
    Android: "android",
    "Chrome OS": "linux",
    "Chromium OS": "linux",
    iOS: "ios",
    Linux: "linux",
    Windows: "windows",
    macOS: "darwin",
};

const GOARCH_BY_UA_DATA_HINTS: Record<string, string> = {
    "arm/32": "arm",
    "arm/64": "arm64",
    "arm64/64": "arm64",
    "x86/32": "386",
    "x86/64": "amd64",
};

function goosFromUserAgentData(platform: string | undefined): string | undefined {
    return platform === undefined ? undefined : GOOS_BY_UA_DATA_PLATFORM[platform];
}

function goosFromNavigatorPlatform(platform: string | undefined): string | undefined {
    if (!platform) return undefined;
    if (platform.startsWith("Mac")) return "darwin";
    if (platform.startsWith("Win")) return "windows";
    if (platform.startsWith("iP")) return "ios";
    if (platform.startsWith("Android")) return "android";
    if (platform.startsWith("Linux")) return "linux";
    return undefined;
}

function goarchFromNavigatorPlatform(platform: string | undefined): string | undefined {
    if (!platform) return undefined;
    if (/x86_64|amd64|x64/.test(platform)) return "amd64";
    if (/aarch64|arm64|armv8/.test(platform)) return "arm64";
    return undefined;
}

export function detectClientPlatform(source: PlatformSource): ClientPlatform {
    return {
        os: goosFromUserAgentData(source.userAgentData?.platform) ?? goosFromNavigatorPlatform(source.navigatorPlatform),
        arch: goarchFromNavigatorPlatform(source.navigatorPlatform),
    };
}

async function hostPlatform(source: PlatformSource): Promise<ClientPlatform | undefined> {
    if (!source.hostEnvironment) return undefined;
    try {
        const env = await source.hostEnvironment();
        if (env?.platform || env?.arch) return { os: env.platform || undefined, arch: env.arch || undefined };
    } catch {}
    return undefined;
}

async function highEntropyArch(userAgentData: UserAgentData | undefined): Promise<string | undefined> {
    if (!userAgentData?.getHighEntropyValues) return undefined;
    try {
        const hints = await userAgentData.getHighEntropyValues(["architecture", "bitness"]);
        return GOARCH_BY_UA_DATA_HINTS[`${hints.architecture}/${hints.bitness}`];
    } catch {
        return undefined;
    }
}

export async function resolveClientPlatform(source: PlatformSource): Promise<ClientPlatform> {
    const host = await hostPlatform(source);
    if (host) return host;

    const detected = detectClientPlatform(source);
    if (detected.arch !== undefined) return detected;
    return { ...detected, arch: await highEntropyArch(source.userAgentData) };
}

interface LegacyNavigator {
    platform?: string;
    userAgentData?: UserAgentData;
}

export function currentPlatformSource(): PlatformSource {
    const nav = globalThis.navigator as unknown as LegacyNavigator | undefined;
    return {
        navigatorPlatform: nav?.platform,
        userAgentData: nav?.userAgentData,
        hostEnvironment: () => {
            const injected = (globalThis as { runtime?: { Environment?: () => Promise<HostEnvironment> } }).runtime;
            return injected?.Environment ? injected.Environment() : Promise.resolve({});
        },
    };
}
