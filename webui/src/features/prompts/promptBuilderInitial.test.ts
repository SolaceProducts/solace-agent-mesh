// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it } from "vitest";

import { consumeNewPromptSeed, resolvePromptBuilderInitial } from "./promptBuilderInitial";

describe("resolvePromptBuilderInitial", () => {
    it("prefers an explicit pill/dialog taskDescription over a stale redirect context", () => {
        const result = resolvePromptBuilderInitial("build a financial advisor agent", "Summarize a document");
        expect(result).toEqual({ initialMessage: "Summarize a document", prefillOnly: false });
    });

    it("uses the redirect context (prefill-only) when there is no explicit taskDescription", () => {
        const result = resolvePromptBuilderInitial("build a financial advisor agent", undefined);
        expect(result).toEqual({ initialMessage: "build a financial advisor agent", prefillOnly: true });
    });

    it("uses taskDescription when there is no redirect context", () => {
        const result = resolvePromptBuilderInitial(null, "Write me an email");
        expect(result).toEqual({ initialMessage: "Write me an email", prefillOnly: false });
    });

    it("returns an empty seed when neither source is present", () => {
        expect(resolvePromptBuilderInitial(null, undefined)).toEqual({ initialMessage: null, prefillOnly: false });
        expect(resolvePromptBuilderInitial(null, null)).toEqual({ initialMessage: null, prefillOnly: false });
    });

    it("treats an empty-string taskDescription as absent (falls back to redirect context)", () => {
        const result = resolvePromptBuilderInitial("redirect ctx", "");
        expect(result).toEqual({ initialMessage: "redirect ctx", prefillOnly: true });
    });
});

describe("consumeNewPromptSeed", () => {
    afterEach(() => {
        sessionStorage.clear();
    });

    it("lets an explicit pill taskDescription win over a stale redirect context AND clears the context", () => {
        sessionStorage.setItem("sam_redirect_context", "build a financial advisor agent");
        const result = consumeNewPromptSeed("ai-assisted", "Summarize a document");
        expect(result).toEqual({ initialMessage: "Summarize a document", prefillOnly: false });
        expect(sessionStorage.getItem("sam_redirect_context")).toBeNull();
    });

    it("uses the redirect context (prefill-only) for a genuine builder→prompts redirect (no router state)", () => {
        sessionStorage.setItem("sam_redirect_context", "build a financial advisor agent");
        const result = consumeNewPromptSeed("ai-assisted", undefined);
        expect(result).toEqual({ initialMessage: "build a financial advisor agent", prefillOnly: true });
        expect(sessionStorage.getItem("sam_redirect_context")).toBeNull();
    });

    it("clears a stale context even when nothing seeds the build (no pill, no consume)", () => {
        sessionStorage.setItem("sam_redirect_context", "stale");
        const result = consumeNewPromptSeed("ai-assisted", undefined);
        expect(result.initialMessage).toBe("stale");
        expect(sessionStorage.getItem("sam_redirect_context")).toBeNull();
    });

    it("returns an empty seed in manual mode and does not touch a stored context", () => {
        sessionStorage.setItem("sam_redirect_context", "keep me");
        const result = consumeNewPromptSeed("manual", "ignored");
        expect(result).toEqual({ initialMessage: null, prefillOnly: false });
        expect(sessionStorage.getItem("sam_redirect_context")).toBe("keep me");
    });

    it("clears a leftover pending-artifacts blob even in manual mode (so it can't hijack a later /builder redirect)", () => {
        sessionStorage.setItem("sam_redirect_pending_artifacts", JSON.stringify({ destSessionId: "d", sourceSessionId: "s", filenames: ["a.txt"] }));
        consumeNewPromptSeed("manual", "ignored");
        expect(sessionStorage.getItem("sam_redirect_pending_artifacts")).toBeNull();
    });

    it("clears the pending-artifacts blob on the ai-assisted path too", () => {
        sessionStorage.setItem("sam_redirect_pending_artifacts", JSON.stringify({ destSessionId: "d", sourceSessionId: "s", filenames: ["a.txt"] }));
        consumeNewPromptSeed("ai-assisted", undefined);
        expect(sessionStorage.getItem("sam_redirect_pending_artifacts")).toBeNull();
    });
});
