// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any -- fixtures intentionally elide optional fields and cast through any. */
import { describe, expect, it } from "vitest";

import type { RemoteAgentResponse } from "@/api/remote-agents";

import { transformRemoteAgentToFormData, transformToCreateRemoteAgentRequest, withFieldsTheFormKeeps } from "./agentDataTransform";

describe("transformToCreateRemoteAgentRequest", () => {
    it("sends the A2A version pin and the card trust the form holds, one key-set URL per line", () => {
        const request = transformToCreateRemoteAgentRequest(
            { agentUrl: "https://api.example.com", agentCardLocation: "well_known" },
            { protocolVersion: "v1_0", trustedCardJwksUrls: " https://a.example.com/jwks.json\n\nhttps://b.example.com/jwks.json ", requireSignedCard: true }
        );
        expect(request.protocolVersion).toBe("v1_0");
        expect(request.trustedCardJwksUrls).toEqual(["https://a.example.com/jwks.json", "https://b.example.com/jwks.json"]);
        expect(request.requireSignedCard).toBe(true);
    });

    it("sends trusted URLs the form holds without a signature requirement, as an agent stored that way keeps them", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com", agentCardLocation: "well_known" }, { trustedCardJwksUrls: "https://a.example.com/jwks.json", requireSignedCard: false });
        expect(request.trustedCardJwksUrls).toEqual(["https://a.example.com/jwks.json"]);
        expect(request.requireSignedCard).toBe(false);
    });

    it("sends no card trust for a card-less agent and the auto pin by default", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com", agentCardLocation: "none" }, { agentName: "MyAgent", trustedCardJwksUrls: "https://a.example.com/jwks.json", requireSignedCard: true });
        expect(request.protocolVersion).toBe("auto");
        expect(request.trustedCardJwksUrls).toEqual([]);
        expect(request.requireSignedCard).toBe(false);
    });

    it("uses agentName when there is no agent card", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com", agentCardLocation: "none" }, { agentName: "MyAgent" });
        expect(request.name).toBe("MyAgent");
        expect(request.agentCardLocation).toBe("none");
        expect(request.agentUrl).toBe("https://api.example.com");
    });

    it("uses solaceDisplayName when an agent card is configured", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com", agentCardLocation: "well_known" }, { solaceDisplayName: "Friendly Name", agentName: "ignored" });
        expect(request.name).toBe("Friendly Name");
        expect(request.agentCardLocation).toBe("well_known");
    });

    it("builds a bearer agentAuthConfig from form data", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com" }, { taskAuthenticationType: "bearer", taskAuthBearerToken: "secret-token" });
        expect(request.agentAuthConfig).toEqual({ type: "bearer", token: "secret-token" });
    });

    it("builds an apikey agentAuthConfig with the standard X-API-Key header", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com" }, { taskAuthenticationType: "apikey", taskAuthApikeyValue: "key-123" });
        expect(request.agentAuthConfig).toEqual({
            type: "apikey",
            headerName: "X-API-Key",
            apiKey: "key-123",
        });
    });

    it("splits oauth2 scopes from a space/comma-delimited string and drops empties", () => {
        const request = transformToCreateRemoteAgentRequest(
            { agentUrl: "https://api.example.com" },
            {
                taskAuthenticationType: "oauth2",
                taskAuthOauth2TokenUrl: "https://idp/token",
                taskAuthOauth2ClientId: "client-id",
                taskAuthOauth2ClientSecret: "client-secret",
                taskAuthOauth2Scope: "read  write,admin",
            }
        );
        expect(request.agentAuthConfig).toEqual({
            type: "oauth2",
            clientId: "client-id",
            clientSecret: "client-secret",
            tokenEndpoint: "https://idp/token",
            scopes: ["read", "write", "admin"],
        });
    });

    it("omits oauth2 scopes when the scope string is empty", () => {
        const request = transformToCreateRemoteAgentRequest({ agentUrl: "https://api.example.com" }, { taskAuthenticationType: "oauth2", taskAuthOauth2Scope: "" });
        const auth = request.agentAuthConfig as { type: string; scopes?: string[] } | undefined;
        expect(auth?.type).toBe("oauth2");
        expect(auth?.scopes).toBeUndefined();
    });

    it("converts non-empty header arrays into objects and drops blank entries", () => {
        const request = transformToCreateRemoteAgentRequest(
            {
                agentUrl: "https://api.example.com",
                agentCardHeaders: [
                    { key: "X-Trace", value: "abc" },
                    { key: "", value: "ignored" },
                ],
            },
            {
                taskHeaders: [{ key: "X-Tenant", value: "acme" }],
            }
        );
        expect(request.agentCardHeaders).toEqual({ "X-Trace": "abc" });
        expect(request.taskHeaders).toEqual({ "X-Tenant": "acme" });
    });

    it("omits header fields entirely when every entry is blank", () => {
        const request = transformToCreateRemoteAgentRequest(
            {
                agentUrl: "https://api.example.com",
                agentCardHeaders: [{ key: "", value: "" }],
            },
            {}
        );
        expect(request.agentCardHeaders).toBeUndefined();
    });

    it("prefers the fetched agent card's skills/inputModes/outputModes/description over form values", () => {
        const fetchedCard = {
            description: "from-card",
            skills: [{ name: "card-skill", description: "from card", id: "ignored", tags: ["ignored"] }],
            inputModes: ["text/plain"],
            outputModes: ["text/markdown"],
        };
        const request = transformToCreateRemoteAgentRequest(
            { agentUrl: "https://api.example.com", agentCardLocation: "well_known" },
            {
                agentDescription: "from-form",
                skills: [{ name: "form-skill", description: "from form" }],
                inputModes: ["form/in"],
                outputModes: ["form/out"],
            },
            fetchedCard as any
        );
        expect(request.description).toBe("from-card");
        expect(request.skills).toEqual([{ name: "card-skill", description: "from card" }]);
        expect(request.inputModes).toEqual(["text/plain"]);
        expect(request.outputModes).toEqual(["text/markdown"]);
    });

    it("falls back to form skills/modes when there is no fetched card", () => {
        const request = transformToCreateRemoteAgentRequest(
            { agentUrl: "https://api.example.com", agentCardLocation: "none" },
            {
                agentName: "Agent",
                skills: [{ name: "form-skill", description: "from form" }],
                inputModes: ["form/in"],
                outputModes: ["form/out"],
            }
        );
        expect(request.skills).toEqual([{ name: "form-skill", description: "from form" }]);
        expect(request.inputModes).toEqual(["form/in"]);
        expect(request.outputModes).toEqual(["form/out"]);
    });
});

describe("transformRemoteAgentToFormData", () => {
    it("loads the stored pin and card trust into the form, one key-set URL per line", () => {
        const { additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "Agent",
            agentCardLocation: "well_known",
            protocolVersion: "v0_3",
            trustedCardJwksUrls: ["https://a.example.com/jwks.json", "https://b.example.com/jwks.json"],
            requireSignedCard: true,
        } as any);
        expect(additionalInfoFormData.protocolVersion).toBe("v0_3");
        expect(additionalInfoFormData.trustedCardJwksUrls).toBe("https://a.example.com/jwks.json\nhttps://b.example.com/jwks.json");
        expect(additionalInfoFormData.requireSignedCard).toBe(true);
    });

    it("reads a card agent stored without a card auth type as no authentication, so step 1 can continue", () => {
        const { connectFormData } = transformRemoteAgentToFormData({ agentUrl: "https://api.example.com", name: "Agent", agentCardLocation: "well_known" } as any);
        expect(connectFormData.authenticationType).toBe("none");
    });

    it("reads the card auth type from the stored config when only the config names it", () => {
        const { connectFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "Agent",
            agentCardLocation: "well_known",
            agentCardAuthConfig: { type: "bearer" },
        } as any);
        expect(connectFormData.authenticationType).toBe("bearer");
    });

    it("reads an agent saved before these fields existed as auto with no card trust", () => {
        const { additionalInfoFormData } = transformRemoteAgentToFormData({ agentUrl: "https://api.example.com", name: "Agent", agentCardLocation: "well_known" } as any);
        expect(additionalInfoFormData.protocolVersion).toBe("auto");
        expect(additionalInfoFormData.trustedCardJwksUrls).toBe("");
        expect(additionalInfoFormData.requireSignedCard).toBe(false);
    });

    it("throws when the remote agent is null", () => {
        expect(() => transformRemoteAgentToFormData(null as any)).toThrow(/required/);
    });

    it("throws when agentUrl is missing", () => {
        expect(() => transformRemoteAgentToFormData({ name: "X" } as any)).toThrow(/agentUrl/);
    });

    it("filters <REDACTED> sentinel values back to empty strings", () => {
        const { connectFormData, additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "Agent",
            agentCardLocation: "none",
            agentCardAuthConfig: { type: "bearer", token: "<REDACTED>" },
            agentAuthConfig: { type: "apikey", headerName: "X-API-Key", apiKey: "<REDACTED>" },
        } as any);
        expect((connectFormData as any).authBearerToken).toBe("");
        expect((additionalInfoFormData as any).taskAuthApikeyValue).toBe("");
    });

    it("populates agentName when the agent has no card", () => {
        const { additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "PlainAgent",
            agentCardLocation: "none",
        } as any);
        expect(additionalInfoFormData.agentName).toBe("PlainAgent");
        expect(additionalInfoFormData.externalAgentName).toBeUndefined();
    });

    it("populates externalAgentName + solaceDisplayName when the agent has a card", () => {
        const { additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "CardAgent",
            agentCardLocation: "well_known",
        } as any);
        expect(additionalInfoFormData.externalAgentName).toBe("CardAgent");
        expect(additionalInfoFormData.solaceDisplayName).toBe("CardAgent");
        expect(additionalInfoFormData.agentName).toBeUndefined();
    });

    it("derives the well-known agentCardUrl when a well_known agent has none stored (builder-created)", () => {
        const { connectFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://a2a-test.example.net/addition/",
            name: "BuilderAgent",
            agentCardLocation: "well_known",
        } as any);
        expect(connectFormData.agentCardUrl).toBe("https://a2a-test.example.net/addition/.well-known/agent-card.json");
    });

    it("preserves an explicitly stored agentCardUrl over the derived one", () => {
        const { connectFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "FormAgent",
            agentCardLocation: "well_known",
            agentCardUrl: "https://api.example.com/custom/card.json",
        } as any);
        expect(connectFormData.agentCardUrl).toBe("https://api.example.com/custom/card.json");
    });

    it("does not derive a card URL for custom or none locations without one stored", () => {
        const custom = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "CustomAgent",
            agentCardLocation: "custom",
        } as any);
        expect(custom.connectFormData.agentCardUrl).toBe("");

        const none = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "NoCardAgent",
            agentCardLocation: "none",
        } as any);
        expect(none.connectFormData.agentCardUrl).toBe("");
    });

    it("joins oauth2 scopes with a space and prefills non-secret oauth2 fields from camelCase keys when extracting back to form data", () => {
        const { additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "Agent",
            agentCardLocation: "none",
            agentAuthConfig: {
                type: "oauth2",
                clientId: "id",
                clientSecret: "<REDACTED>",
                tokenEndpoint: "https://idp/token",
                scopes: ["read", "write"],
            },
        } as any);
        expect((additionalInfoFormData as any).taskAuthOauth2Scope).toBe("read write");
        expect((additionalInfoFormData as any).taskAuthOauth2ClientSecret).toBe("");
        expect((additionalInfoFormData as any).taskAuthOauth2ClientId).toBe("id");
        expect((additionalInfoFormData as any).taskAuthOauth2TokenUrl).toBe("https://idp/token");
    });

    it("converts header objects back into key/value arrays for form binding", () => {
        const { connectFormData, additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: "https://api.example.com",
            name: "Agent",
            agentCardLocation: "well_known",
            agentCardHeaders: { "X-Trace": "abc" },
            taskHeaders: { "X-Tenant": "acme" },
        } as any);
        expect(connectFormData.agentCardHeaders).toEqual([{ key: "X-Trace", value: "abc" }]);
        expect(additionalInfoFormData.taskHeaders).toEqual([{ key: "X-Tenant", value: "acme" }]);
    });
});

describe("transform round-trip", () => {
    it("preserves agentUrl, name, headers, and bearer token when the request is read back out as form data", () => {
        const request = transformToCreateRemoteAgentRequest(
            { agentUrl: "https://api.example.com", agentCardLocation: "none" },
            {
                agentName: "RoundtripAgent",
                taskAuthenticationType: "bearer",
                taskAuthBearerToken: "tok",
                taskHeaders: [{ key: "X-T", value: "1" }],
            }
        );

        const { additionalInfoFormData } = transformRemoteAgentToFormData({
            agentUrl: request.agentUrl,
            name: request.name,
            agentCardLocation: request.agentCardLocation,
            agentAuthType: "bearer",
            agentAuthConfig: request.agentAuthConfig,
            taskHeaders: request.taskHeaders,
        } as any);

        expect(additionalInfoFormData.agentName).toBe("RoundtripAgent");
        expect((additionalInfoFormData as any).taskAuthBearerToken).toBe("tok");
        expect(additionalInfoFormData.taskHeaders).toEqual([{ key: "X-T", value: "1" }]);
    });
});

describe("withFieldsTheFormKeeps", () => {
    const request = {
        agentUrl: "https://orders.example.com",
        name: "Orders",
        agentCardLocation: "well_known" as const,
        agentAuthConfig: { type: "none" as const },
        protocolVersion: "v0_3" as const,
        trustedCardJwksUrls: [],
        requireSignedCard: false,
    };

    it("sends back the stored request timeout and keeps the card trust the form edited", () => {
        const stored = { requestTimeoutSeconds: 120, protocolVersion: "v1_0", trustedCardJwksUrls: ["https://keys.example.com/jwks.json"], requireSignedCard: true } as RemoteAgentResponse;
        expect(withFieldsTheFormKeeps(request, stored)).toEqual({ ...request, requestTimeoutSeconds: 120 });
    });

    it("refuses the edit when the stored agent did not load, rather than reset what it cannot send back", () => {
        expect(() => withFieldsTheFormKeeps(request, undefined)).toThrow("did not load");
    });
});
