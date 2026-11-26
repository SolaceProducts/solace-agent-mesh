// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { AuthConfig } from "@/api/remote-agents";

import { useTrackAuthStoredSecrets } from "./authSecretTracking";

describe("useTrackAuthStoredSecrets", () => {
    test("flags <REDACTED> secrets by camelCase field name across both scopes", () => {
        const cardAuth: AuthConfig = { type: "apikey", headerName: "X-API-Key", apiKey: "<REDACTED>" };
        const taskAuth: AuthConfig = { type: "oauth2", clientId: "id", clientSecret: "<REDACTED>", tokenEndpoint: "https://idp/token" };
        const { result } = renderHook(() => useTrackAuthStoredSecrets(cardAuth, taskAuth));
        expect(result.current.agentCardAuth.has("apiKey")).toBe(true);
        expect(result.current.taskAuth.has("clientSecret")).toBe(true);
    });

    test("detects bearer token and basic password sentinels", () => {
        const cardAuth: AuthConfig = { type: "bearer", token: "<REDACTED>" };
        const taskAuth: AuthConfig = { type: "basic", username: "u", password: "<REDACTED>" };
        const { result } = renderHook(() => useTrackAuthStoredSecrets(cardAuth, taskAuth));
        expect(result.current.agentCardAuth.has("token")).toBe(true);
        expect(result.current.taskAuth.has("password")).toBe(true);
    });

    test("does not flag real (non-redacted) or absent secrets", () => {
        const cardAuth: AuthConfig = { type: "apikey", headerName: "X-API-Key", apiKey: "real-key" };
        const { result } = renderHook(() => useTrackAuthStoredSecrets(cardAuth, undefined));
        expect(result.current.agentCardAuth.has("apiKey")).toBe(false);
        expect(result.current.taskAuth.size).toBe(0);
    });
});
