// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import type { AttachedArtifactRef } from "@/types";

import { buildSubmitMessageMetadata, buildUserMessage, processSubmissionFiles } from "./submitMessage";

const artifactRef = (overrides: Partial<AttachedArtifactRef> = {}): AttachedArtifactRef => ({
    uri: "artifact://a.txt",
    filename: "a.txt",
    mimeType: "text/plain",
    ...overrides,
});

describe("buildUserMessage", () => {
    it("carries attached artifact refs onto the bubble, trimmed to the display fields", () => {
        const msg = buildUserMessage({
            currentInput: "look at this",
            currentFiles: [],
            currentArtifactRefs: [artifactRef({ uri: "artifact://a.txt", filename: "a.txt", mimeType: "text/plain" })],
            sessionId: "s1",
        });

        expect(msg.attachedArtifacts).toEqual([{ uri: "artifact://a.txt", filename: "a.txt", mimeType: "text/plain" }]);
    });

    it("leaves attachedArtifacts undefined when nothing was attached", () => {
        const msg = buildUserMessage({ currentInput: "hi", currentFiles: [], currentArtifactRefs: [], sessionId: "s1" });
        expect(msg.attachedArtifacts).toBeUndefined();
    });
});

describe("buildSubmitMessageMetadata", () => {
    it("includes project_id only when an active project is set", () => {
        const withProject = buildSubmitMessageMetadata({ selectedAgentName: "Agent", activeProjectId: "proj-1" });
        expect(withProject.project_id).toBe("proj-1");

        const withoutProject = buildSubmitMessageMetadata({ selectedAgentName: "Agent" });
        expect(withoutProject.project_id).toBeUndefined();
    });

    it("lets caller-supplied metadata override the computed defaults", () => {
        const metadata = buildSubmitMessageMetadata({
            selectedAgentName: "Agent",
            backgroundTasksDefaultTimeoutMs: 1000,
            extraMetadata: { resume_build: true, maxExecutionTimeMs: 5000 },
        });

        expect(metadata.resume_build).toBe(true);
        expect(metadata.maxExecutionTimeMs).toBe(5000);
    });
});

describe("processSubmissionFiles", () => {
    const noopUpload = vi.fn();
    const noopCleanup = vi.fn(async () => {});

    it("turns artifact references into file parts by URI, without uploading anything", async () => {
        const result = await processSubmissionFiles({
            currentFiles: [],
            currentArtifactRefs: [artifactRef({ uri: "artifact://a.txt", filename: "a.txt", mimeType: "text/csv" })],
            effectiveSessionId: "s1",
            uploadArtifactFile: noopUpload,
            cleanupUploadedFiles: noopCleanup,
        });

        expect(result).toEqual({ ok: true, uploadedFileParts: [{ kind: "file", file: { uri: "artifact://a.txt", name: "a.txt", mimeType: "text/csv" } }], effectiveSessionId: "s1" });
        expect(noopUpload).not.toHaveBeenCalled();
    });

    it("sends a small file inline as base64 rather than uploading it", async () => {
        const smallFile = new File(["hello"], "small.txt", { type: "text/plain" });
        const uploadArtifactFile = vi.fn();

        const result = await processSubmissionFiles({
            currentFiles: [smallFile],
            currentArtifactRefs: [],
            effectiveSessionId: "s1",
            uploadArtifactFile,
            cleanupUploadedFiles: noopCleanup,
        });

        expect(uploadArtifactFile).not.toHaveBeenCalled();
        expect(result.ok).toBe(true);
        if (result.ok) {
            expect(result.uploadedFileParts).toHaveLength(1);
            const part = result.uploadedFileParts[0];
            expect(part.file).toMatchObject({ name: "small.txt", mimeType: "text/plain" });
            expect("bytes" in part.file).toBe(true);
        }
    });

    it("uploads a large file, adopts the session id it returns, and lists it as successfully uploaded", async () => {
        const bigFile = new File([new Uint8Array(2 * 1024 * 1024)], "big.bin", { type: "application/octet-stream" });
        const uploadArtifactFile = vi.fn(async () => ({ uri: "uri://big.bin", sessionId: "new-session" }));

        const result = await processSubmissionFiles({
            currentFiles: [bigFile],
            currentArtifactRefs: [],
            effectiveSessionId: "",
            uploadArtifactFile,
            cleanupUploadedFiles: noopCleanup,
        });

        expect(uploadArtifactFile).toHaveBeenCalledWith(bigFile, "");
        expect(result).toEqual({
            ok: true,
            uploadedFileParts: [{ kind: "file", file: { uri: "uri://big.bin", name: "big.bin", mimeType: "application/octet-stream" } }],
            effectiveSessionId: "new-session",
        });
    });

    it("cleans up every file already uploaded this submit when a later large upload fails", async () => {
        const first = new File([new Uint8Array(2 * 1024 * 1024)], "first.bin", { type: "application/octet-stream" });
        const second = new File([new Uint8Array(2 * 1024 * 1024)], "second.bin", { type: "application/octet-stream" });
        const uploadArtifactFile = vi.fn().mockResolvedValueOnce({ uri: "uri://first.bin", sessionId: "s1" }).mockResolvedValueOnce({ error: "disk full" });
        const cleanupUploadedFiles = vi.fn(async () => {});

        const result = await processSubmissionFiles({
            currentFiles: [first, second],
            currentArtifactRefs: [],
            effectiveSessionId: "s1",
            uploadArtifactFile,
            cleanupUploadedFiles,
        });

        expect(result).toEqual({ ok: false, errorMessage: 'Message not sent. File upload failed for "second.bin" (disk full). Previously uploaded files have been cleaned up..' });
        expect(cleanupUploadedFiles).toHaveBeenCalledWith([{ filename: "first.bin", sessionId: "s1" }]);
    });

    it("reports a plain failure message when nothing had been uploaded yet", async () => {
        const onlyFile = new File([new Uint8Array(2 * 1024 * 1024)], "only.bin", { type: "application/octet-stream" });
        const uploadArtifactFile = vi.fn(async () => null);
        const cleanupUploadedFiles = vi.fn(async () => {});

        const result = await processSubmissionFiles({
            currentFiles: [onlyFile],
            currentArtifactRefs: [],
            effectiveSessionId: "s1",
            uploadArtifactFile,
            cleanupUploadedFiles,
        });

        expect(result).toEqual({ ok: false, errorMessage: 'Message not sent. File upload failed for "only.bin"..' });
        expect(cleanupUploadedFiles).toHaveBeenCalledWith([]);
    });
});
