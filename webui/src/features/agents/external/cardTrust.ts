// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RemoteAgentProtocolPin } from "@/api/remote-agents";

import type { RemoteAgentInterface } from "@/types";

import type { SelectOption } from "./FormComponents";

export const MAX_TRUSTED_KEY_SET_URLS = 10;

export const TRUSTED_JWKS_URLS_LABEL = "Trusted JWKS URLs";
export const REQUIRE_SIGNED_CARD_LABEL = "Require Signed Card";
const MAX_TRUSTED_KEY_SET_URL_BYTES = 2048;

export const PROTOCOL_PINS: readonly RemoteAgentProtocolPin[] = ["auto", "v1_0", "v0_3"];

export const PROTOCOL_VERSION_OPTIONS: SelectOption[] = [
    { value: "auto", label: "Automatic", description: "Use the highest A2A version that both the agent card and Agent Mesh support." },
    { value: "v1_0", label: "A2A 1.0", description: "Always use A2A 1.0. The agent does not start if its card offers no A2A 1.0 interface." },
    { value: "v0_3", label: "A2A 0.3", description: "Always use A2A 0.3, the older version, for an agent whose A2A 1.0 interface does not work yet." },
];

export const CARDLESS_PROTOCOL_VERSION_OPTIONS: SelectOption[] = [
    { value: "auto", label: "Automatic", description: "Use A2A 0.3. Without an agent card, the agent mesh cannot tell which versions the agent supports." },
    { value: "v1_0", label: "A2A 1.0", description: "Always use A2A 1.0. Select this when the agent supports A2A 1.0." },
    { value: "v0_3", label: "A2A 0.3", description: "Always use A2A 0.3, the older version." },
];

export const VERSION_LABELS: Record<RemoteAgentInterface["version"], string> = { v1_0: "A2A 1.0", v0_3: "A2A 0.3" };
const BINDING_LABELS: Record<RemoteAgentInterface["binding"], string> = { jsonrpc: "JSON-RPC", http_json: "HTTP+JSON" };

export function interfaceLabel({ version, binding }: Pick<RemoteAgentInterface, "version" | "binding">): string {
    return `${VERSION_LABELS[version]} over ${BINDING_LABELS[binding]}`;
}

export function parseTrustedKeySetUrls(text: string | undefined): string[] {
    return typedLines(text).map(line => line.url);
}

interface TypedLine {
    url: string;
    lineNumber: number;
}

function typedLines(text: string | undefined): TypedLine[] {
    return (text ?? "")
        .split("\n")
        .map((line, index) => ({ url: line.trim(), lineNumber: index + 1 }))
        .filter(line => line.url !== "");
}

function byteLength(text: string): number {
    return new TextEncoder().encode(text).length;
}

const SCHEME_AND_AUTHORITY = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)/;
const INVALID_AUTHORITY_CHARACTER = /[\s<>"{}|\\^`]/;
const INVALID_ESCAPE = /%(?![0-9A-Fa-f]{2})/;
const VALID_USERINFO = /^[A-Za-z0-9\-._:~!$&'()*+,;=%@]*$/;

export function isTrustedKeySetUrl(raw: string): boolean {
    if (!raw || byteLength(raw) > MAX_TRUSTED_KEY_SET_URL_BYTES || hasControlCharacter(raw)) return false;
    const match = SCHEME_AND_AUTHORITY.exec(raw);
    if (!match || INVALID_ESCAPE.test(withoutQuery(raw))) return false;
    const host = hostOf(match[2]);
    if (!host) return false;
    const scheme = match[1].toLowerCase();
    return scheme === "https" || (scheme === "http" && isLoopbackHost(host));
}

function hasControlCharacter(text: string): boolean {
    return [...text].some(character => {
        const code = character.charCodeAt(0);
        return code < 0x20 || code === 0x7f;
    });
}

function withoutQuery(raw: string): string {
    const hash = raw.indexOf("#");
    const beforeFragment = hash < 0 ? raw : raw.slice(0, hash);
    const fragment = hash < 0 ? "" : raw.slice(hash);
    const question = beforeFragment.indexOf("?");
    return (question < 0 ? beforeFragment : beforeFragment.slice(0, question)) + fragment;
}

interface UrlHost {
    name: string;
    bracketed: boolean;
    zone?: string;
}

function hostOf(authority: string): UrlHost | undefined {
    if (INVALID_AUTHORITY_CHARACTER.test(authority)) return undefined;
    const at = authority.lastIndexOf("@");
    if (at >= 0 && !VALID_USERINFO.test(authority.slice(0, at))) return undefined;
    const hostAndPort = authority.slice(at + 1);
    if (hostAndPort.startsWith("[")) {
        const end = hostAndPort.indexOf("]");
        if (end < 0 || !/^(:\d*)?$/.test(hostAndPort.slice(end + 1))) return undefined;
        const [name, zone] = hostAndPort.slice(1, end).split(/%25(.*)/s);
        if (name.includes("%") || zone === "" || zone?.includes("[") || !ipv6Groups(name)) return undefined;
        return { name, bracketed: true, zone };
    }
    const colon = hostAndPort.indexOf(":");
    const name = colon < 0 ? hostAndPort : hostAndPort.slice(0, colon);
    const port = colon < 0 ? "" : hostAndPort.slice(colon + 1);
    if (name === "" || /[%[\]]/.test(name) || !/^\d*$/.test(port)) return undefined;
    return { name, bracketed: false };
}

function isLoopbackHost(host: UrlHost): boolean {
    if (host.zone !== undefined) return false;
    if (!host.bracketed) return host.name.toLowerCase() === "localhost" || ipv4Octets(host.name)?.[0] === 127;
    const groups = ipv6Groups(host.name);
    if (!groups) return false;
    const leadingZeros = (count: number) => groups.slice(0, count).every(group => group === 0);
    const isOne = leadingZeros(7) && groups[7] === 1;
    const isMappedLoopback = leadingZeros(5) && groups[5] === 0xffff && groups[6] >> 8 === 127;
    return isOne || isMappedLoopback;
}

function ipv4Octets(text: string): number[] | undefined {
    const parts = text.split(".");
    if (parts.length !== 4 || !parts.every(part => /^(0|[1-9]\d{0,2})$/.test(part))) return undefined;
    const octets = parts.map(Number);
    return octets.every(octet => octet <= 255) ? octets : undefined;
}

function ipv6Groups(text: string): number[] | undefined {
    const lastColon = text.lastIndexOf(":");
    const last = text.slice(lastColon + 1);
    const v4Tail = last.includes(".") ? ipv4Octets(last) : undefined;
    if (last.includes(".") && !v4Tail) return undefined;
    const body = v4Tail ? `${text.slice(0, lastColon + 1)}0:0` : text;
    const halves = body.split("::");
    if (halves.length > 2) return undefined;
    const groupsOf = (part: string) => (part === "" ? [] : part.split(":"));
    const head = groupsOf(halves[0]);
    const tail = halves.length === 2 ? groupsOf(halves[1]) : [];
    const missing = 8 - head.length - tail.length;
    if (halves.length === 1 ? missing !== 0 : missing < 1) return undefined;
    const written = [...head, ...Array<string>(halves.length === 2 ? missing : 0).fill("0"), ...tail];
    if (!written.every(group => /^[0-9a-f]{1,4}$/i.test(group))) return undefined;
    const groups = written.map(group => parseInt(group, 16));
    if (v4Tail) {
        groups[6] = (v4Tail[0] << 8) | v4Tail[1];
        groups[7] = (v4Tail[2] << 8) | v4Tail[3];
    }
    return groups;
}

export function trustedKeySetUrlsProblem(text: string | undefined): string | undefined {
    const lines = typedLines(text);
    if (lines.length > MAX_TRUSTED_KEY_SET_URLS) {
        return `Exceeds limit. Enter no more than ${MAX_TRUSTED_KEY_SET_URLS} URLs.`;
    }
    for (const { url, lineNumber } of lines) {
        if (byteLength(url) > MAX_TRUSTED_KEY_SET_URL_BYTES) {
            return `Exceeds limit on line ${lineNumber}. Enter a URL no more than ${MAX_TRUSTED_KEY_SET_URL_BYTES.toLocaleString("en-US")} bytes.`;
        }
        if (!isTrustedKeySetUrl(url)) {
            return `Invalid format on line ${lineNumber}. Enter an https URL (http only on a loopback host such as localhost).`;
        }
    }
    return undefined;
}
