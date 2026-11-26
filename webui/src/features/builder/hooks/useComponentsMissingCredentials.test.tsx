// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import { SAM_REQUIRED_PLACEHOLDER } from "../builderUtils";

import { applyPlatformCredentialState, connectorsToCheck, countPlaceholdersStillOnPlatform, useComponentsMissingCredentials } from "./useComponentsMissingCredentials";

const connectorApi = vi.hoisted(() => ({
    getAllConnectors: vi.fn(),
    diffConnectorSecrets: vi.fn(),
    canCompare: true,
    serverApply: true,
}));

vi.mock("@/api/connectors", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/connectors")>()),
    useCanCompareConnectorSecrets: () => connectorApi.canCompare,
}));

vi.mock("@/hooks/useIsBuilderServerApplyEnabled", () => ({
    useIsBuilderServerApplyEnabled: () => connectorApi.serverApply,
}));

vi.mock("@/api/connectors/versioned", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/connectors/versioned")>();
    return {
        ...actual,
        connectorsApi: { ...actual.connectorsApi, getAll: () => connectorApi.getAllConnectors(), diffSecrets: (...args: unknown[]) => connectorApi.diffConnectorSecrets(...args) },
    };
});

const REDACTED = "<REDACTED>";
const PLACEHOLDER = SAM_REQUIRED_PLACEHOLDER;

const manifest = { components: [{ name: "EntKeyAPI", type: "connector", origin: "session" }] };

const metadata = (name: string, values: Record<string, unknown>) => ({ connectorMetadata: { name, type: "api", subtype: "openapi", values } });

const componentData = (values: Record<string, unknown>) => new Map([["EntKeyAPI", metadata("EntKeyAPI", values)]]);

const platformConnector = (values: Record<string, unknown> | undefined, name = "EntKeyAPI", id = "conn-1"): Connector => ({ id, name, type: "api", subtype: "openapi", values }) as unknown as Connector;

const check = (placeholderFields: string[]) => ({ componentName: "EntKeyAPI", platformName: "EntKeyAPI", type: "api", subtype: "openapi", placeholderFields });

let client: QueryClient;

function wrapper() {
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const settled = () => waitFor(() => expect(client.isFetching()).toBe(0));

describe("countPlaceholdersStillOnPlatform", () => {
    beforeEach(() => {
        connectorApi.diffConnectorSecrets.mockReset();
    });

    test("a secret still equal to the placeholder stays unfilled", async () => {
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: true } } });
        await expect(countPlaceholdersStillOnPlatform(check(["api_key"]), platformConnector({ api_key: REDACTED }))).resolves.toBe(1);
        expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledWith("conn-1", { api_key: PLACEHOLDER });
    });

    test("a secret that no longer equals the placeholder is filled", async () => {
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        await expect(countPlaceholdersStillOnPlatform(check(["api_key"]), platformConnector({ api_key: REDACTED }))).resolves.toBe(0);
    });

    test.each([
        ["no entry for the field", { secrets: {} }],
        ["an entry without a verdict", { secrets: { api_key: {} } }],
        ["a null verdict", { secrets: { api_key: { matches: null } } }],
    ])("a secret the platform gave %s stays unfilled", async (_label, response) => {
        connectorApi.diffConnectorSecrets.mockResolvedValue(response);
        await expect(countPlaceholdersStillOnPlatform(check(["api_key"]), platformConnector({ api_key: REDACTED }))).resolves.toBe(1);
    });

    describe.each([
        ["with server apply", true],
        ["without server apply", false],
    ])("%s", (_mode, keepsStoredPlainValues) => {
        test.each([
            ["absent from the record", { other: "x" }],
            ["empty on the record", { api_key: "" }],
            ["null on the record", { api_key: null }],
            ["still the placeholder", { api_key: PLACEHOLDER }],
            ["a different SAM placeholder", { api_key: "<<__SAM_OTHER__>>" }],
            ["the record having no values at all", undefined],
        ])("a field that is %s is never shown filled, and the platform is not asked", async (_label, values) => {
            await expect(countPlaceholdersStillOnPlatform(check(["api_key"]), platformConnector(values), undefined, keepsStoredPlainValues)).resolves.toBe(1);
            expect(connectorApi.diffConnectorSecrets).not.toHaveBeenCalled();
        });
    });

    test.each([
        ["a string", "db.example.com"],
        ["a number", 5436],
        ["false", false],
    ])("a plain field holding %s on the record is filled under server apply, which keeps it over the placeholder, and the platform is not asked", async (_label, value) => {
        await expect(countPlaceholdersStillOnPlatform(check(["hostname"]), platformConnector({ hostname: value }), undefined, true)).resolves.toBe(0);
        expect(connectorApi.diffConnectorSecrets).not.toHaveBeenCalled();
    });

    test("a plain field holding a value stays unfilled without server apply, whose create-only save cannot reconcile it with the artifact", async () => {
        await expect(countPlaceholdersStillOnPlatform(check(["hostname"]), platformConnector({ hostname: "db.example.com" }))).resolves.toBe(1);
        expect(connectorApi.diffConnectorSecrets).not.toHaveBeenCalled();
    });

    test.each([
        ["with server apply", true, 0],
        ["without server apply", false, 1],
    ])("a plain field holding a value next to a filled secret, %s, leaves %i unfilled, and only the secret is sent for comparison", async (_mode, keepsStoredPlainValues, expected) => {
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { broker_password: { matches: false } } });
        const stored = platformConnector({ broker_url: "tcp://broker:55555", broker_password: REDACTED });
        await expect(countPlaceholdersStillOnPlatform(check(["broker_url", "broker_password"]), stored, undefined, keepsStoredPlainValues)).resolves.toBe(expected);
        expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledWith("conn-1", { broker_password: PLACEHOLDER });
    });

    test("counts a plain placeholder and an unfilled secret together", async () => {
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { broker_password: { matches: true } } });
        const stored = platformConnector({ broker_url: PLACEHOLDER, broker_password: REDACTED });
        await expect(countPlaceholdersStillOnPlatform(check(["broker_url", "broker_password"]), stored)).resolves.toBe(2);
    });
});

describe("connectorsToCheck", () => {
    test("reads the platform name from the connector config, not the manifest component name", () => {
        const data = new Map([["Component1", metadata("PlatformName", { api_key: PLACEHOLDER, host: "h" })]]);
        expect(connectorsToCheck(new Map([["Component1", 1]]), data)).toEqual([{ componentName: "Component1", platformName: "PlatformName", type: "api", subtype: "openapi", placeholderFields: ["api_key"] }]);
    });

    test("only considers components the artifact check reported", () => {
        const data = new Map([
            ["Reported", metadata("Reported", { api_key: PLACEHOLDER })],
            ["Other", metadata("Other", { api_key: PLACEHOLDER })],
        ]);
        expect(connectorsToCheck(new Map([["Reported", 1]]), data).map(c => c.componentName)).toEqual(["Reported"]);
    });

    test("leaves out a connector whose config no longer holds a placeholder, or has no name", () => {
        const data = new Map([
            ["Filled", metadata("Filled", { api_key: "sk-real" })],
            ["Nameless", metadata("", { api_key: PLACEHOLDER })],
        ]);
        expect(
            connectorsToCheck(
                new Map([
                    ["Filled", 1],
                    ["Nameless", 1],
                ]),
                data
            )
        ).toEqual([]);
    });

    test("leaves out a component that has no connector config, such as a remote agent", () => {
        const data = new Map([["Remote", { a2aAgentMetadata: { name: "Remote" } }]]);
        expect(connectorsToCheck(new Map([["Remote", 1]]), data as never)).toEqual([]);
    });
});

describe("applyPlatformCredentialState", () => {
    test("leaves the artifact count in force until the platform has answered", () => {
        const fromArtifacts = new Map([["EntKeyAPI", 1]]);
        expect(applyPlatformCredentialState(fromArtifacts, undefined)).toBe(fromArtifacts);
    });

    test("drops a component the platform reports fully filled and lowers a partly filled one", () => {
        const fromArtifacts = new Map([
            ["A", 1],
            ["B", 2],
            ["C", 1],
        ]);
        const result = applyPlatformCredentialState(
            fromArtifacts,
            new Map([
                ["A", 0],
                ["B", 1],
            ])
        );
        expect([...result.entries()]).toEqual([
            ["B", 1],
            ["C", 1],
        ]);
    });
});

describe("useComponentsMissingCredentials", () => {
    const render = (data: ReturnType<typeof componentData>, manifestArg: typeof manifest = manifest) => renderHook(({ d }) => useComponentsMissingCredentials(manifestArg, d), { wrapper: wrapper(), initialProps: { d: data } });

    beforeEach(() => {
        connectorApi.getAllConnectors.mockReset();
        connectorApi.diffConnectorSecrets.mockReset();
        connectorApi.canCompare = true;
        connectorApi.serverApply = true;
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    describe("a database connector whose details were filled on the Connectors page after the Builder saved it", () => {
        const dbManifest = { components: [{ name: "MemberDatabase", type: "connector", origin: "session" }] };
        const dbData = new Map([
            ["MemberDatabase", { connectorMetadata: { name: "MemberDatabase", type: "sql", subtype: "postgres", values: { database: PLACEHOLDER, hostname: PLACEHOLDER, port: 5432, username: PLACEHOLDER, password: PLACEHOLDER } } }],
        ]);
        const saved = { id: "conn-db", name: "MemberDatabase", type: "sql", subtype: "postgres", values: { database: "members", hostname: "db.example.com", port: 5436, username: "postgres", password: REDACTED } } as unknown as Connector;

        beforeEach(() => {
            connectorApi.getAllConnectors.mockResolvedValue([saved]);
            connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { password: { matches: false } } });
        });

        test("unblocks under server apply, which keeps the stored database, host and username over the placeholders", async () => {
            const { result } = renderHook(() => useComponentsMissingCredentials(dbManifest, dbData), { wrapper: wrapper() });
            expect(result.current.get("MemberDatabase")).toBe(4);
            await waitFor(() => expect(result.current.size).toBe(0));
            expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledWith("conn-db", { password: PLACEHOLDER });
        });

        test("stays blocked on the plain fields without server apply", async () => {
            connectorApi.serverApply = false;
            const { result } = renderHook(() => useComponentsMissingCredentials(dbManifest, dbData), { wrapper: wrapper() });
            await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalled());
            await waitFor(() => expect(result.current.get("MemberDatabase")).toBe(3));
        });

        test("credits only the plain fields that hold a value, so a half-filled connector stays blocked on the rest", async () => {
            connectorApi.getAllConnectors.mockResolvedValue([{ ...saved, values: { ...saved.values, hostname: PLACEHOLDER } }]);
            const { result } = renderHook(() => useComponentsMissingCredentials(dbManifest, dbData), { wrapper: wrapper() });
            await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalled());
            await settled();
            expect(result.current.get("MemberDatabase")).toBe(1);
        });

        test("does not reuse the server-apply answer once the flag turns off", async () => {
            const { result, rerender } = renderHook(() => useComponentsMissingCredentials(dbManifest, dbData), { wrapper: wrapper() });
            await waitFor(() => expect(result.current.size).toBe(0));
            connectorApi.serverApply = false;
            rerender();
            expect(result.current.get("MemberDatabase")).toBe(4);
            await waitFor(() => expect(result.current.get("MemberDatabase")).toBe(3));
        });

        test("keeps the artifact count when the platform refuses the secret comparison, though the plain fields hold values", async () => {
            connectorApi.diffConnectorSecrets.mockRejectedValue(new Error("403"));
            vi.spyOn(console, "warn").mockImplementation(() => {});
            const { result } = renderHook(() => useComponentsMissingCredentials(dbManifest, dbData), { wrapper: wrapper() });
            await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalled());
            await settled();
            expect(result.current.get("MemberDatabase")).toBe(4);
        });
    });

    test("stays blocked on an artifact that holds the placeholder while the connector is not saved", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([]);
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.getAllConnectors).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("unblocks once the saved connector holds a real secret, though the artifact still has the placeholder", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        expect(result.current.get("EntKeyAPI")).toBe(1);
        await waitFor(() => expect(result.current.size).toBe(0));
    });

    test("finds the platform connector by the config's name when the manifest names the component differently", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED }, "PlatformName")]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = renderHook(() => useComponentsMissingCredentials({ components: [{ name: "Component1", type: "connector", origin: "session" }] }, new Map([["Component1", metadata("PlatformName", { api_key: PLACEHOLDER })]])), {
            wrapper: wrapper(),
        });
        await waitFor(() => expect(result.current.size).toBe(0));
    });

    test("clears one connector and keeps another that is still unfilled", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED }, "First", "conn-1"), platformConnector({ api_key: REDACTED }, "Second", "conn-2")]);
        connectorApi.diffConnectorSecrets.mockImplementation(async (id: string) => ({ secrets: { api_key: { matches: id === "conn-2" } } }));
        const two = {
            components: [
                { name: "First", type: "connector", origin: "session" },
                { name: "Second", type: "connector", origin: "session" },
            ],
        };
        const data = new Map([
            ["First", metadata("First", { api_key: PLACEHOLDER })],
            ["Second", metadata("Second", { api_key: PLACEHOLDER })],
        ]);
        const { result } = renderHook(() => useComponentsMissingCredentials(two, data), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.has("First")).toBe(false));
        expect([...result.current.keys()]).toEqual(["Second"]);
    });

    test("stays blocked when the saved connector still holds the placeholder", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: true } } });
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("a connector that gains a second placeholder is blocked again rather than served the earlier answer", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED, client_secret: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockImplementation(async (_id: string, candidate: Record<string, string>) => ({ secrets: Object.fromEntries(Object.keys(candidate).map(field => [field, { matches: field === "client_secret" }])) }));
        const { result, rerender } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(result.current.size).toBe(0));

        rerender({ d: componentData({ api_key: PLACEHOLDER, client_secret: PLACEHOLDER }) });
        await settled();

        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("a placeholder that moves to another field is blocked again, though the count is unchanged", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED, client_secret: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockImplementation(async (_id: string, candidate: Record<string, string>) => ({ secrets: Object.fromEntries(Object.keys(candidate).map(field => [field, { matches: field !== "api_key" }])) }));
        const { result, rerender } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(result.current.size).toBe(0));

        rerender({ d: componentData({ client_secret: PLACEHOLDER }) });
        await settled();

        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("a component pointed at a different platform connector is blocked again, though the fields are unchanged", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED }, "First", "conn-1"), platformConnector({ api_key: REDACTED }, "Second", "conn-2")]);
        connectorApi.diffConnectorSecrets.mockImplementation(async (id: string) => ({ secrets: { api_key: { matches: id === "conn-2" } } }));
        const { result, rerender } = renderHook(({ d }) => useComponentsMissingCredentials(manifest, d), { wrapper: wrapper(), initialProps: { d: new Map([["EntKeyAPI", metadata("First", { api_key: PLACEHOLDER })]]) } });
        await waitFor(() => expect(result.current.size).toBe(0));

        rerender({ d: new Map([["EntKeyAPI", metadata("Second", { api_key: PLACEHOLDER })]]) });
        await settled();

        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("stays blocked when the platform refuses the comparison", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockRejectedValue(new Error("403"));
        vi.spyOn(console, "warn").mockImplementation(() => {});
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("stays blocked, and says why in the console, when the connector list cannot be read", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        connectorApi.getAllConnectors.mockRejectedValue(new Error("500"));
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.getAllConnectors).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
        expect(warn).toHaveBeenCalledWith(expect.stringContaining("could not list connectors"), expect.any(Error));
    });

    test.each([
        ["type", { type: "other" }],
        ["subtype", { subtype: "other" }],
    ])("stays blocked when the platform connector differs from the artifact in its %s", async (_label, difference) => {
        connectorApi.getAllConnectors.mockResolvedValue([{ ...platformConnector({ api_key: REDACTED }), ...difference }]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.getAllConnectors).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
        expect(connectorApi.diffConnectorSecrets).not.toHaveBeenCalled();
    });

    test("matches the platform connector by the platform's own name rule, ignoring case and spaces", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED }, "EntKeyAPI")]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = renderHook(() => useComponentsMissingCredentials(manifest, new Map([["EntKeyAPI", metadata("ent key api", { api_key: PLACEHOLDER })]])), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.size).toBe(0));
    });

    test("does not treat names that differ by punctuation as the same connector", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED }, "myconn")]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = renderHook(() => useComponentsMissingCredentials(manifest, new Map([["EntKeyAPI", metadata("my-conn", { api_key: PLACEHOLDER })]])), { wrapper: wrapper() });
        await waitFor(() => expect(connectorApi.getAllConnectors).toHaveBeenCalled());
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);
        expect(connectorApi.diffConnectorSecrets).not.toHaveBeenCalled();
    });

    test("stops trusting a cleared answer once the user can no longer compare secrets", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result, rerender } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(result.current.size).toBe(0));

        connectorApi.canCompare = false;
        rerender({ d: componentData({ api_key: PLACEHOLDER }) });

        expect(result.current.get("EntKeyAPI")).toBe(1);
    });

    test("coalesces focus events that arrive while a check is already running", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: true } } });
        render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledTimes(1));
        await settled();

        let release: (value: Connector[]) => void = () => {};
        connectorApi.getAllConnectors.mockReturnValue(new Promise<Connector[]>(resolve => (release = resolve)));
        await act(async () => {
            window.dispatchEvent(new Event("focus"));
            window.dispatchEvent(new Event("focus"));
            window.dispatchEvent(new Event("focus"));
        });
        release([platformConnector({ api_key: REDACTED })]);
        await settled();

        expect(connectorApi.getAllConnectors).toHaveBeenCalledTimes(2);
    });

    test("blocks again when a later check fails after an earlier one cleared the gate", async () => {
        connectorApi.getAllConnectors.mockResolvedValueOnce([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(result.current.size).toBe(0));

        connectorApi.getAllConnectors.mockRejectedValue(new Error("500"));
        await act(async () => {
            window.dispatchEvent(new Event("focus"));
        });

        await waitFor(() => expect(result.current.get("EntKeyAPI")).toBe(1));
    });

    test("unblocks when the window regains focus after the key was entered elsewhere", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: true } } });
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledTimes(1));
        await settled();
        expect(result.current.get("EntKeyAPI")).toBe(1);

        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: false } } });
        window.dispatchEvent(new Event("focus"));

        await waitFor(() => expect(result.current.size).toBe(0));
    });

    test("stops listening for focus once unmounted", async () => {
        connectorApi.getAllConnectors.mockResolvedValue([platformConnector({ api_key: REDACTED })]);
        connectorApi.diffConnectorSecrets.mockResolvedValue({ secrets: { api_key: { matches: true } } });
        const { unmount } = render(componentData({ api_key: PLACEHOLDER }));
        await waitFor(() => expect(connectorApi.diffConnectorSecrets).toHaveBeenCalledTimes(1));
        await settled();
        unmount();

        window.dispatchEvent(new Event("focus"));
        await settled();

        expect(connectorApi.getAllConnectors).toHaveBeenCalledTimes(1);
    });

    test("does not call the platform when the user cannot compare connector secrets", async () => {
        connectorApi.canCompare = false;
        const { result } = render(componentData({ api_key: PLACEHOLDER }));
        await act(async () => {
            window.dispatchEvent(new Event("focus"));
        });
        expect(result.current.get("EntKeyAPI")).toBe(1);
        expect(connectorApi.getAllConnectors).not.toHaveBeenCalled();
    });

    test("makes no platform call and listens for no focus when no placeholder is outstanding", async () => {
        const { result } = render(componentData({ api_key: "sk-real" }));
        await act(async () => {
            window.dispatchEvent(new Event("focus"));
        });
        expect(result.current.size).toBe(0);
        expect(connectorApi.getAllConnectors).not.toHaveBeenCalled();
    });
});
