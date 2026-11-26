// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { transformToCreateRemoteAgentRequest } from "./agentDataTransform";

describe("transformToCreateRemoteAgentRequest — agent card auth", () => {
    const leftoverBearer = {
        agentUrl: "https://example.com/agent",
        authenticationType: "bearer",
        authBearerToken: "",
    };
    const additionalInfo = { agentName: "NoCardAgent", solaceDisplayName: "NoCardAgent" };

    test("sends no card auth when there is no card to authenticate to", () => {
        const req = transformToCreateRemoteAgentRequest({ ...leftoverBearer, agentCardLocation: "none" }, additionalInfo);
        expect(req.agentCardAuthConfig).toBeUndefined();
    });

    test("still sends card auth when there is a card", () => {
        const req = transformToCreateRemoteAgentRequest(
            {
                ...leftoverBearer,
                authBearerToken: "a-real-token",
                agentCardLocation: "well_known",
                agentCardUrl: "https://example.com/agent/.well-known/agent-card.json",
            },
            additionalInfo
        );
        expect(req.agentCardAuthConfig).toMatchObject({ type: "bearer" });
    });
});
