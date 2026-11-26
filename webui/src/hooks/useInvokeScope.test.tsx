// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, test } from "vitest";

import { useInvokeScope } from "./useInvokeScope";

const ID = "1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b";

const wrapper = (rbac: boolean) =>
    function Wrapper({ children }: { children: ReactNode }) {
        return <OpenFeatureTestProvider flagValueMap={{ rbac }}>{children}</OpenFeatureTestProvider>;
    };

describe("useInvokeScope", () => {
    test("builds the invoke scope when rbac is enabled", () => {
        const { result } = renderHook(() => useInvokeScope("agent", ID), { wrapper: wrapper(true) });
        expect(result.current).toBe(`agent:${ID}:invoke`);
    });

    test("uses the resource type in the scope", () => {
        const { result } = renderHook(() => useInvokeScope("workflow", ID), { wrapper: wrapper(true) });
        expect(result.current).toBe(`workflow:${ID}:invoke`);
    });

    test("returns undefined when rbac is disabled", () => {
        const { result } = renderHook(() => useInvokeScope("agent", ID), { wrapper: wrapper(false) });
        expect(result.current).toBeUndefined();
    });

    test("returns undefined when the id is missing", () => {
        const { result } = renderHook(() => useInvokeScope("agent", undefined), { wrapper: wrapper(true) });
        expect(result.current).toBeUndefined();
    });
});
