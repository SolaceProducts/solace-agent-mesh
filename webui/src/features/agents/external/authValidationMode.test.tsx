// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { waitFor } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AdditionalInfoStep } from "./AdditionalInfoStep";
import { ConnectAgentForm } from "./ConnectAgentForm";

const REDACTED = "<REDACTED>";

const errorsAfterValidating = async (render: (onFormReady: (m: any) => void) => void): Promise<Record<string, unknown>> => {
    let methods: any;
    render(m => {
        methods = m;
    });
    await waitFor(() => expect(methods).toBeDefined());
    await methods.trigger();
    return methods.control._formState.errors ?? {};
};

const storedCardAuth = (type: string, secretField: string) => ({
    id: "agent-1",
    name: "stored",
    agentCardAuthType: type,
    agentCardAuthConfig: { type, [secretField]: REDACTED },
});

const storedTaskAuth = (type: string, secretField: string) => ({
    id: "agent-1",
    name: "stored",
    agentAuthType: type,
    agentAuthConfig: { type, [secretField]: REDACTED },
});

describe("agent-card auth validation mode", () => {
    const defaults = {
        agentUrl: "https://example.com/agent",
        agentCardUrl: "https://example.com/agent/.well-known/agent-card.json",
        agentCardLocation: "well_known" as const,
        authenticationType: "bearer",
        authBearerToken: "",
    };

    test("a blank credential on create is refused, not sent to the backend to reject", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(<ConnectAgentForm defaultValues={defaults} onFormReady={onFormReady} />);
        });
        expect(errors).toHaveProperty("authBearerToken");
    });

    test("a blank credential on an edit is accepted — the stored one is preserved", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(<ConnectAgentForm defaultValues={defaults} onFormReady={onFormReady} initialRemoteAgent={storedCardAuth("bearer", "token") as any} />);
        });
        expect(errors).not.toHaveProperty("authBearerToken");
    });

    test("switching auth type on an edit demands its own credential — nothing stored can cover it", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(<ConnectAgentForm defaultValues={{ ...defaults, authenticationType: "apikey", authApikeyValue: "" }} onFormReady={onFormReady} initialRemoteAgent={storedCardAuth("bearer", "token") as any} />);
        });
        expect(errors).toHaveProperty("authApikeyValue");
    });
});

describe("agent-card auth when there is no card", () => {
    test("does not demand a credential for a section it does not render", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(
                <ConnectAgentForm
                    defaultValues={{
                        agentUrl: "https://example.com/agent",
                        agentCardUrl: "",
                        agentCardLocation: "none" as const,
                        authenticationType: "bearer",
                        authBearerToken: "",
                    }}
                    onFormReady={onFormReady}
                />
            );
        });
        expect(errors).not.toHaveProperty("authBearerToken");
    });
});

describe("task auth validation mode", () => {
    const defaults = {
        externalAgentName: "stored",
        solaceDisplayName: "Stored",
        taskAuthenticationType: "bearer",
        taskAuthBearerToken: "",
    };

    test("a blank credential on create is refused", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(<AdditionalInfoStep agentCardLocation="well_known" defaultValues={defaults} onFormReady={onFormReady} />);
        });
        expect(errors).toHaveProperty("taskAuthBearerToken");
    });

    test("a blank credential on an edit is accepted", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(<AdditionalInfoStep agentCardLocation="well_known" defaultValues={defaults} onFormReady={onFormReady} initialRemoteAgent={storedTaskAuth("bearer", "token") as any} />);
        });
        expect(errors).not.toHaveProperty("taskAuthBearerToken");
    });

    test("switching auth type on an edit demands its own credential", async () => {
        const errors = await errorsAfterValidating(onFormReady => {
            renderWithProviders(
                <AdditionalInfoStep agentCardLocation="well_known" defaultValues={{ ...defaults, taskAuthenticationType: "apikey", taskAuthApikeyValue: "" }} onFormReady={onFormReady} initialRemoteAgent={storedTaskAuth("bearer", "token") as any} />
            );
        });
        expect(errors).toHaveProperty("taskAuthApikeyValue");
    });
});
