// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { useIsBuilderServerApplyEnabled } from "./useIsBuilderServerApplyEnabled";

describe("useIsBuilderServerApplyEnabled", () => {
    function renderWithFlags(flags: Record<string, boolean>) {
        return renderHook(() => useIsBuilderServerApplyEnabled(), {
            wrapper: ({ children }) => <OpenFeatureTestProvider flagValueMap={flags}>{children}</OpenFeatureTestProvider>,
        });
    }

    test("falls back to off until the gateway serves the flag", async () => {
        const { result } = renderWithFlags({});
        await waitFor(() => expect(result.current).toBe(false));
    });

    test("turns on for the key the backend gates the route on", async () => {
        const { result } = renderWithFlags({ builder_server_apply: true });
        await waitFor(() => expect(result.current).toBe(true));
    });

    test("stays off for a near-miss key", async () => {
        const { result } = renderWithFlags({ builderServerApply: true });
        await waitFor(() => expect(result.current).toBe(false));
    });
});
