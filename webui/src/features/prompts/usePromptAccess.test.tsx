// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { PromptGroup } from "@/types/prompts";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { usePromptAccess } from "./usePromptAccess";

function makeWrapper(opts: { sharing: boolean }) {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <OpenFeatureTestProvider flagValueMap={{ prompt_sharing: opts.sharing }}>
                <MockConfigProvider mockValues={{ identityServiceType: opts.sharing ? "okta" : null }}>{children}</MockConfigProvider>
            </OpenFeatureTestProvider>
        );
    };
}

const promptWithLevel = (accessLevel?: PromptGroup["accessLevel"]): Pick<PromptGroup, "accessLevel"> => ({ accessLevel });

describe("usePromptAccess — sharing off (no identity service)", () => {
    test("treats user as owner regardless of accessLevel (legacy parity)", () => {
        const wrapper = makeWrapper({ sharing: false });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel("resource_viewer")), { wrapper });

        expect(result.current.sharingEnabled).toBe(false);
        expect(result.current.isViewer).toBe(false);
        expect(result.current.isEditor).toBe(false);
        expect(result.current.isOwner).toBe(true);
        expect(result.current.canShare).toBe(false);
    });

    test("null prompt still resolves to owner (legacy parity for create flows)", () => {
        const wrapper = makeWrapper({ sharing: false });

        const { result } = renderHook(() => usePromptAccess(null), { wrapper });

        expect(result.current.isOwner).toBe(true);
        expect(result.current.canShare).toBe(false);
    });

    test("canShare is false even for an owner (sharing gates everything)", () => {
        const wrapper = makeWrapper({ sharing: false });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel("owner")), { wrapper });

        expect(result.current.canShare).toBe(false);
    });
});

describe("usePromptAccess — sharing on (identity service wired)", () => {
    test("owner accessLevel: isOwner true, canShare true (no scope required)", () => {
        const wrapper = makeWrapper({ sharing: true });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel("owner")), { wrapper });

        expect(result.current.isOwner).toBe(true);
        expect(result.current.isEditor).toBe(false);
        expect(result.current.isViewer).toBe(false);
        expect(result.current.canShare).toBe(true);
    });

    test("editor accessLevel: isEditor true, isOwner false, canShare false", () => {
        const wrapper = makeWrapper({ sharing: true });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel("resource_editor")), { wrapper });

        expect(result.current.isEditor).toBe(true);
        expect(result.current.isOwner).toBe(false);
        expect(result.current.isViewer).toBe(false);
        expect(result.current.canShare).toBe(false);
    });

    test("viewer accessLevel: isViewer true, isOwner false, canShare false", () => {
        const wrapper = makeWrapper({ sharing: true });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel("resource_viewer")), { wrapper });

        expect(result.current.isViewer).toBe(true);
        expect(result.current.isOwner).toBe(false);
        expect(result.current.isEditor).toBe(false);
        expect(result.current.canShare).toBe(false);
    });

    test("undefined accessLevel: no role assigned, all gates default to false", () => {
        const wrapper = makeWrapper({ sharing: true });

        const { result } = renderHook(() => usePromptAccess(promptWithLevel(undefined)), { wrapper });

        expect(result.current.isOwner).toBe(false);
        expect(result.current.isEditor).toBe(false);
        expect(result.current.isViewer).toBe(false);
        expect(result.current.canShare).toBe(false);
    });

    test("null prompt: isOwner false (no implicit ownership when sharing is on)", () => {
        const wrapper = makeWrapper({ sharing: true });

        const { result } = renderHook(() => usePromptAccess(null), { wrapper });

        expect(result.current.isOwner).toBe(false);
        expect(result.current.canShare).toBe(false);
    });
});
