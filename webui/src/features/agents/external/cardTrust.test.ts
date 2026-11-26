// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { isTrustedKeySetUrl, MAX_TRUSTED_KEY_SET_URLS, parseTrustedKeySetUrls, trustedKeySetUrlsProblem } from "./cardTrust";

describe("parseTrustedKeySetUrls", () => {
    it("reads one URL per line, trimmed, without blank lines", () => {
        expect(parseTrustedKeySetUrls("  https://a.example.com/jwks.json \n\n https://b.example.com/jwks.json\n")).toEqual(["https://a.example.com/jwks.json", "https://b.example.com/jwks.json"]);
    });

    it("reads nothing from an empty or missing value", () => {
        expect(parseTrustedKeySetUrls("")).toEqual([]);
        expect(parseTrustedKeySetUrls(undefined)).toEqual([]);
    });
});

describe("isTrustedKeySetUrl gives the verdict of the platform's Go rule: url.Parse, then https, or http on a net.ParseIP loopback host", () => {
    it.each([
        ["/jwks.json", false],
        ["HTTPS://keys.example.com/jwks.json", true],
        ["ftp://keys.example.com/jwks.json", false],
        ["http://127.0.0.01/jwks.json", false],
        ["http://127.0.0.1/jwks.json", true],
        ["http://127.0.0.256/jwks.json", false],
        ["http://127.0.0.1.evil.example.com/jwks.json", false],
        ["http://2130706433/jwks.json", false],
        ["http://0x7f.0.0.1/jwks.json", false],
        ["http://[x::ffff:127.0.0.1]/jwks.json", false],
        ["http://[::ffff:127.0.0.1x]/jwks.json", false],
        ["http://[::1:1]/jwks.json", false],
        ["http://127.0.0.2:9000/jwks.json", true],
        ["http://127.1/jwks.json", false],
        ["http://127.255.255.255/jwks.json", true],
        ["http://128.0.0.1/jwks.json", false],
        ["http://LOCALHOST:8080/jwks.json", true],
        ["http://[0:0:0:0:0:0:0:1]/jwks.json", true],
        ["http://[::1/jwks.json", false],
        ["http://[::1]/jwks.json", true],
        ["http://[::1]:8080/jwks.json", true],
        ["http://[::1]x/jwks.json", false],
        ["http://[::2]/jwks.json", false],
        ["http://[::ffff:10.0.0.1]/jwks.json", false],
        ["http://[::ffff:127.0.0.1]/jwks.json", true],
        ["http://[fe80::1%25en0]/jwks.json", false],
        ["http://keys.example.com/jwks.json", false],
        ["http://localhost./jwks.json", false],
        ["http://localhost.evil.example.com/jwks.json", false],
        ["http://localhost/jwks.json", true],
        ["http://user@localhost/jwks.json", true],
        ["https://", false],
        ["https:///jwks.json", false],
        ["https://bad host/jwks.json", false],
        ["https://keys.example.com/\u0001", false],
        ["https://keys.example.com/a b", true],
        ["https://keys.example.com/jwks.json", true],
        ["https://keys.example.com:8443/jwks.json", true],
        ["https://keys.example.com:abc/jwks.json", false],
        ["https://user:pw@keys.example.com/jwks.json", true],
        ["https:/keys.example.com/jwks.json", false],
        ["http://10.0.0.1/", false],
        ["http://126.0.0.1/", false],
        ["http://[1::1]/", false],
        ["http://[0:0:0:0:1:ffff:7f00:1]/", false],
        ["http://[1::ffff:127.0.0.1]/", false],
        ["http://[::1:7f00:1]/", false],
        ["httpx://localhost/", false],
        ["http://[::0.0.0.1]/", true],
        ["http://[::0.0.1.1]/", false],
        ["https://\u00fc@example.com/jwks.json", false],
        ["https://u[@example.com/", false],
        ["http://u]@127.0.0.1/x", false],
        ["http://[::1]@[::1]/", false],
        ["https://example.com[/x", false],
        ["https://a[b", false],
        ["https://[::1%25[]", false],
        ["https://k.example.com/%2f", true],
        ["https://k.example.com/%2F", true],
        ["https://k.example.com/%a0", true],
        ["https:keys.example.com/jwks.json", false],
        ["keys.example.com/jwks.json", false],
        [`https://keys.example.com/${"a".repeat(2048)}`, false],
        [`https://keys.example.com/${"é".repeat(1100)}`, false],
        [`https://keys.example.com/${"é".repeat(1000)}`, true],
    ])("%s is %s", (url, want) => {
        expect(isTrustedKeySetUrl(url)).toBe(want);
    });
});

describe("isTrustedKeySetUrl matches the platform's Go rule on authority, IPv6, escape and length edge cases", () => {
    it.each([
        ["http://keys.example.com?@localhost/", false],
        ["http://keys.example.com#@localhost/", false],
        ["http://a@b@localhost/", true],
        ["ftp://localhost/", false],
        ["ws://127.0.0.1/", false],
        ["/x?u=https://k.example.com", false],
        ["https://keys.example.com:80:80/", false],
        ["http://[::1::]/", false],
        ["http://[0:0:0:0:0:0:0::1]/", false],
        ["http://[1:2:3:4:5:6:7:8:9::]/", false],
        ["http://[::00001]/", false],
        ["http://127.0.0.1.1/", false],
        ["http://[::FFFF:127.0.0.1]/", true],
        ["https://keys.example.com/\u007f", false],
        ["https://keys.example.com/\u001f", false],
        ["https://keys.example.com/\r", false],
        ["https://keys.example.com/%zz", false],
        ["http://localhost/%", false],
        ["https://keys.example.com/x?q=%zz", true],
        ["https://keys.example.com/x#%zz", false],
        ["http://[::1%25eth0]/x", false],
        ["https://[::g]/", false],
        ["https://[127.0.0.1]/", false],
        ["https://[::1::]/", false],
        ["https://a b@keys.example.com/", false],
        ["http://evil.example.com\\@localhost/", false],
        ["http://[::ffff:7f00:1]/", true],
        ["http://[::127.0.0.1]/", false],
        ["http://[0::1]/", true],
        ["http://[::0:1]/", true],
        ["http://[0000:0000:0000:0000:0000:0000:0000:0001]/", true],
        ["http://[::ffff:127.255.0.1]/", true],
        ["http://[::ffff:128.0.0.1]/", false],
        ["https://[2001:db8::1]/jwks.json", true],
        ["https://[2001:db8::1]:8443/jwks.json", true],
        ["https://[fe80::1%25en0]/jwks.json", true],
        ["http://[1:2:3:4:5:6:7:8]/", false],
        ["http://[1:2:3:4:5:6:7]/", false],
        ["http://[::]/", false],
        ["http://LocalHost/jwks.json", true],
        ["http://localhost:/jwks.json", true],
        ["http://localhost:99999/jwks.json", true],
        ["https://keys.example.com:/jwks.json", true],
        ["https://user@/jwks.json", false],
        ["https://@keys.example.com/jwks.json", true],
        ["https://keys.example.com/a%20b", true],
        ["https://keys.example.com/a%2", false],
        ["https://k\u00e9ys.example.com/jwks.json", true],
        ["https://keys.example.com/\u00e9", true],
        [`https://k.example.com/${"a".repeat(2048 - "https://k.example.com/".length)}`, true],
        [`https://k.example.com/${"a".repeat(2049 - "https://k.example.com/".length)}`, false],
    ])("%j is %s", (url, want) => {
        expect(isTrustedKeySetUrl(url)).toBe(want);
    });
});

describe("isTrustedKeySetUrl is stricter than the platform only on URLs no operator means to write", () => {
    it.each(["https://:443/k", "https://ex%C3%A9.com/k", "https://keys.example.com/x#\u0001", 'https://exam"ple.com/', "https://exa]mple.com/x"])("refuses %j", url => {
        expect(isTrustedKeySetUrl(url)).toBe(false);
    });
});

describe("trustedKeySetUrlsProblem", () => {
    const lines = (count: number) => Array.from({ length: count }, (_, i) => `https://k${i}.example.com/jwks.json`).join("\n");

    it("accepts up to the limit of valid URLs", () => {
        expect(trustedKeySetUrlsProblem(lines(MAX_TRUSTED_KEY_SET_URLS))).toBeUndefined();
    });

    it("refuses more than the limit", () => {
        expect(trustedKeySetUrlsProblem(lines(MAX_TRUSTED_KEY_SET_URLS + 1))).toBe("Exceeds limit. Enter no more than 10 URLs.");
    });

    it("names the first line that is not a trusted key-set URL", () => {
        expect(trustedKeySetUrlsProblem("https://ok.example.com/jwks.json\nhttp://keys.example.com/jwks.json\nftp://keys.example.com/jwks.json")).toBe("Invalid format on line 2. Enter an https URL (http only on a loopback host such as localhost).");
    });

    it("names a bad first line", () => {
        expect(trustedKeySetUrlsProblem("ftp://keys.example.com/jwks.json")).toBe("Invalid format on line 1. Enter an https URL (http only on a loopback host such as localhost).");
    });

    it("counts the blank lines the user typed when naming a line", () => {
        expect(trustedKeySetUrlsProblem("https://ok.example.com/jwks.json\n\n\nhttp://keys.example.com/jwks.json")).toBe("Invalid format on line 4. Enter an https URL (http only on a loopback host such as localhost).");
    });

    it("says when an https URL is too long", () => {
        const long = `https://keys.example.com/${"a".repeat(2048)}`;
        expect(trustedKeySetUrlsProblem(`https://ok.example.com/jwks.json\n${long}`)).toBe("Exceeds limit on line 2. Enter a URL no more than 2,048 bytes.");
    });

    it("accepts a repeated URL, as the platform does", () => {
        expect(trustedKeySetUrlsProblem("https://a.example.com/jwks.json\nhttps://b.example.com/jwks.json\n https://a.example.com/jwks.json")).toBeUndefined();
    });

    it("names the line of a bad URL after blank lines and a repeat", () => {
        expect(trustedKeySetUrlsProblem("https://a.example.com/jwks.json\n\nhttps://a.example.com/jwks.json\n\nftp://k.example.com/")).toBe("Invalid format on line 5. Enter an https URL (http only on a loopback host such as localhost).");
    });

    it("accepts nothing typed", () => {
        expect(trustedKeySetUrlsProblem("")).toBeUndefined();
        expect(trustedKeySetUrlsProblem(" \n\n")).toBeUndefined();
        expect(trustedKeySetUrlsProblem(undefined)).toBeUndefined();
    });
});
