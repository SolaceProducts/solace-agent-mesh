// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { GatewaySchema } from "@/types";

import { GatewayEdit } from "./GatewayEdit";

function withProviders(ui: ReactNode) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <MemoryRouter>
                <QueryClientProvider client={client}>{ui}</QueryClientProvider>
            </MemoryRouter>
        </OpenFeatureTestProvider>
    );
}

const schemaInfo = {
    type: "event_mesh",
    name: "Event Mesh",
    description: "test",
    schema: {
        broker_url: { type: "string", label: "Host URI", required: true, order: 1 },
        broker_vpn: { type: "string", label: "Message VPN", required: true, order: 2 },
        broker_username: { type: "string", label: "Username", required: true, order: 3 },
        broker_password: { type: "password", label: "Password", required: true, order: 4 },
    },
} as unknown as GatewaySchema;

const savedGateway = {
    id: "0190a5f0-0000-7000-8000-000000000001",
    name: "Orders broker",
    description: "Event mesh entrypoint",
    type: "event_mesh",
    values: { broker_url: "tcps://broker.example.com:55443", broker_vpn: "default", broker_username: "svc", broker_password: "<REDACTED>" },
} as never;

const KEEP_HINT = "Leave placeholder to keep the existing value.";
const REENTER_HINT = "Enter it again to use the changed connection settings.";

function renderEdit() {
    return render(withProviders(<GatewayEdit schemaInfo={schemaInfo} initialGatewayToEdit={savedGateway} onSubmit={vi.fn()} />));
}

const testButton = () => screen.getByText("Test Connection").closest("button")!;
const field = (name: string) => document.querySelector<HTMLInputElement>(`[name="values.${name}"]`)!;

describe("GatewayEdit — connection test and the saved secret", () => {
    test("an unchanged destination keeps the saved secret and lets the test run", () => {
        renderEdit();

        expect(screen.getByText(KEEP_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(false);
    });

    test("changing the host asks for the secret again and holds the test until it is typed", () => {
        renderEdit();

        fireEvent.change(field("broker_url"), { target: { value: "tcps://other.example.com:55443" } });

        expect(screen.getByText(REENTER_HINT)).toBeTruthy();
        expect(screen.queryByText(KEEP_HINT)).toBeNull();
        expect(testButton().disabled).toBe(true);

        fireEvent.change(field("broker_password"), { target: { value: "typed-again" } });

        expect(testButton().disabled).toBe(false);
    });

    test("restoring the saved host brings the keep-the-secret behaviour back", () => {
        renderEdit();

        fireEvent.change(field("broker_url"), { target: { value: "tcps://other.example.com:55443" } });
        fireEvent.change(field("broker_url"), { target: { value: "tcps://broker.example.com:55443" } });

        expect(screen.getByText(KEEP_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(false);
    });

    test("a new entrypoint is not asked to re-enter anything", () => {
        render(withProviders(<GatewayEdit schemaInfo={schemaInfo} isNew onSubmit={vi.fn()} />));

        expect(screen.queryByText(REENTER_HINT)).toBeNull();
    });
});

const eventMeshWithTLS = {
    ...schemaInfo,
    schema: {
        ...schemaInfo.schema,
        tls_skip_verify: { type: "string", label: "TLS Certificate Verification", required: false, order: 5 },
    },
} as unknown as GatewaySchema;

const emailSchema = {
    type: "email",
    name: "Email",
    description: "test",
    schema: {
        mailbox: { type: "string", label: "Mailbox", required: true, order: 1 },
        imap_host: { type: "string", label: "IMAP server host", required: true, order: 2 },
        imap_port: { type: "number", label: "IMAP port", required: false, order: 3 },
        auth_mode: { type: "string", label: "Authentication method", required: true, order: 4 },
        imap_password: { type: "password", label: "IMAP password", required: true, order: 5, conditionalDisplay: { auth_mode: "basic" }, showTestConnection: true },
        oauth2_tenant_id: { type: "string", label: "Tenant", required: true, order: 6, conditionalDisplay: { auth_mode: "oauth2" } },
        oauth2_client_id: { type: "string", label: "Client ID", required: true, order: 7, conditionalDisplay: { auth_mode: "oauth2" } },
        oauth2_client_secret: { type: "password", label: "Client secret", required: true, order: 8, conditionalDisplay: { auth_mode: "oauth2" }, showTestConnection: true },
    },
} as unknown as GatewaySchema;

const savedEmail = (authMode: "basic" | "oauth2") =>
    ({
        id: "0190a5f0-0000-7000-8000-000000000002",
        name: "Support mailbox",
        description: "Email entrypoint",
        type: "email",
        values: {
            mailbox: "ops@example.com",
            imap_host: "imap.example.com",
            imap_port: 993,
            auth_mode: authMode,
            imap_password: authMode === "basic" ? "<REDACTED>" : undefined,
            oauth2_tenant_id: "tenant",
            oauth2_client_id: "client",
            oauth2_client_secret: authMode === "oauth2" ? "<REDACTED>" : undefined,
        },
    }) as never;

describe("GatewayEdit — each setting that moves the connection test asks for the secret again", () => {
    test("a TLS verification change alone, with the host untouched", () => {
        const saved = { ...(savedGateway as object), values: { ...(savedGateway as { values: object }).values, tls_skip_verify: "false" } } as never;
        render(withProviders(<GatewayEdit schemaInfo={eventMeshWithTLS} initialGatewayToEdit={saved} onSubmit={vi.fn()} />));

        fireEvent.change(field("tls_skip_verify"), { target: { value: "true" } });

        expect(screen.getByText(REENTER_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(true);

        fireEvent.change(field("broker_password"), { target: { value: "typed-again" } });

        expect(testButton().disabled).toBe(false);
    });

    test.each(["basic", "oauth2"] as const)("an IMAP port change alone, with %s authentication", authMode => {
        render(withProviders(<GatewayEdit schemaInfo={emailSchema} initialGatewayToEdit={savedEmail(authMode)} onSubmit={vi.fn()} />));
        const secret = authMode === "basic" ? "imap_password" : "oauth2_client_secret";

        expect(testButton().disabled).toBe(false);
        fireEvent.change(field("imap_port"), { target: { value: "143" } });

        expect(screen.getByText(REENTER_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(true);

        fireEvent.change(field(secret), { target: { value: "typed-again" } });

        expect(testButton().disabled).toBe(false);
    });

    test.each(["basic", "oauth2"] as const)("an IMAP host change, with %s authentication", authMode => {
        render(withProviders(<GatewayEdit schemaInfo={emailSchema} initialGatewayToEdit={savedEmail(authMode)} onSubmit={vi.fn()} />));

        fireEvent.change(field("imap_host"), { target: { value: "imap.other.example.com" } });

        expect(screen.getByText(REENTER_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(true);
    });

    test("clearing the IMAP port is not a change", () => {
        render(withProviders(<GatewayEdit schemaInfo={emailSchema} initialGatewayToEdit={savedEmail("basic")} onSubmit={vi.fn()} />));

        fireEvent.change(field("imap_port"), { target: { value: "" } });

        expect(screen.getByText(KEEP_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(false);
    });
});

describe("GatewayEdit — why the connection test is held", () => {
    test("names the saved credential while it still has to be typed again", () => {
        renderEdit();

        fireEvent.change(field("broker_url"), { target: { value: "tcps://other.example.com:55443" } });

        expect(testButton().getAttribute("aria-label")).toBe("Enter the credential again to test the changed connection settings");
    });

    test("names the missing information once the credential is typed", () => {
        renderEdit();

        fireEvent.change(field("broker_url"), { target: { value: "tcps://other.example.com:55443" } });
        fireEvent.change(field("broker_password"), { target: { value: "typed-again" } });
        fireEvent.change(field("broker_vpn"), { target: { value: "" } });

        expect(testButton().getAttribute("aria-label")).toBe("Required connection information is missing");
    });

    test("a saved text port is not the same place as the number the form sends, since the probe reads text as the default port", () => {
        const saved = savedEmail("basic") as unknown as { values: Record<string, unknown> };
        saved.values.imap_port = "143";
        render(withProviders(<GatewayEdit schemaInfo={emailSchema} initialGatewayToEdit={saved as never} onSubmit={vi.fn()} />));

        fireEvent.change(field("imap_port"), { target: { value: "144" } });
        fireEvent.change(field("imap_port"), { target: { value: "143" } });

        expect(screen.getByText(REENTER_HINT)).toBeTruthy();
        expect(testButton().disabled).toBe(true);
    });
});
