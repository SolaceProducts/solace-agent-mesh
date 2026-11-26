// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types/chat";

import { decodeBase64Content, resolveArtifactContent } from "./artifactUtils";

function b64(input: string): string {
    const bytes = new TextEncoder().encode(input);
    let binary = "";
    for (const b of bytes) binary += String.fromCharCode(b);
    return btoa(binary);
}

describe("decodeBase64Content", () => {
    test("round-trips ASCII content", () => {
        expect(decodeBase64Content(b64("hello world"))).toBe("hello world");
    });

    test("decodes UTF-8 correctly (multibyte characters)", () => {
        const input = "héllo — 日本語 🚀";
        expect(decodeBase64Content(b64(input))).toBe(input);
    });

    test("returns empty string for empty input", () => {
        expect(decodeBase64Content("")).toBe("");
    });
});

function makeArtifact(overrides: Partial<ArtifactInfo> = {}): ArtifactInfo {
    return {
        filename: "config.yaml",
        mimeType: "application/vnd.sam-agent-config+yaml",
        size: 0,
        ...overrides,
    } as ArtifactInfo;
}

describe("resolveArtifactContent", () => {
    test("prefers accumulatedContent when present", async () => {
        const artifact = makeArtifact({ accumulatedContent: "apps: []" });
        const download = vi.fn();
        const result = await resolveArtifactContent(artifact, download);
        expect(result).toBe("apps: []");
        expect(download).not.toHaveBeenCalled();
    });

    test("falls back to downloadAndResolveArtifact and decodes base64", async () => {
        const artifact = makeArtifact();
        const payload = "hello: world";
        const download = vi.fn().mockResolvedValue({ content: b64(payload) });
        const result = await resolveArtifactContent(artifact, download);
        expect(download).toHaveBeenCalledWith("config.yaml");
        expect(result).toBe(payload);
    });

    test("returns raw content when the download result isn't valid base64", async () => {
        const artifact = makeArtifact();
        const download = vi.fn().mockResolvedValue({ content: "::not base64::" });
        const result = await resolveArtifactContent(artifact, download);
        expect(result).toBe("::not base64::");
    });

    test("returns null when no accumulated content, download resolves empty, and no sessionId", async () => {
        const artifact = makeArtifact();
        const download = vi.fn().mockResolvedValue(null);
        const result = await resolveArtifactContent(artifact, download);
        expect(result).toBeNull();
    });

    test("when download throws and no sessionId provided, returns null", async () => {
        const artifact = makeArtifact();
        const download = vi.fn().mockRejectedValue(new Error("boom"));
        const result = await resolveArtifactContent(artifact, download);
        expect(result).toBeNull();
    });
});
