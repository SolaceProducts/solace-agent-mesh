// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { BuildCompleteCTA } from "./BuildCompleteCTA";
import { type BuildManifest, BuildManifestSchema } from "./hooks/useBuilderManifest";
import { REGISTRATION_POLL_LIMIT_MS } from "./hooks/useToolSourceFailures";

const h = vi.hoisted(() => ({
    connectors: undefined as { id?: string; name: string }[] | undefined,
    agents: undefined as unknown[] | undefined,
    entrypoints: undefined as { name: string; values: Record<string, unknown>; deployed?: { values: Record<string, unknown> } }[] | undefined,
    entrypointsFailed: false,
    lastAgentsOptions: undefined as { enabled?: boolean; refetchOnMount?: string; refetchInterval?: (agents: unknown[] | undefined) => number | false } | undefined,
}));
vi.mock("@/api/connectors", () => ({ useConnectors: () => ({ data: h.connectors }) }));
vi.mock("@/api/gateways", () => ({ useGateways: () => ({ data: h.entrypoints, isError: h.entrypointsFailed }) }));
vi.mock("@/api/agents", () => ({
    useAgents: (options: typeof h.lastAgentsOptions) => {
        h.lastAgentsOptions = options;
        return { data: h.agents };
    },
}));

beforeEach(() => {
    h.connectors = undefined;
    h.agents = undefined;
    h.entrypoints = undefined;
    h.entrypointsFailed = false;
    h.lastAgentsOptions = undefined;
});

const manifest = (components: NonNullable<BuildManifest["components"]>): BuildManifest => ({ name: "build", components });

function renderCTA(components: NonNullable<BuildManifest["components"]>, props: Partial<React.ComponentProps<typeof BuildCompleteCTA>> = {}) {
    const stamped = components.map(c => (c.deployed_version === undefined ? { ...c, deployed_version: 0 } : c));
    const versions = new Map(stamped.filter(c => !!c.filename).map(c => [c.filename as string, c.deployed_version as number]));
    return render(<BuildCompleteCTA manifest={manifest(stamped)} deployEdits={{ versions, serverApplyEnabled: false }} onSwitchToTest={vi.fn()} onAddToAgent={vi.fn()} {...props} />);
}

describe("BuildCompleteCTA", () => {
    test("shows the Switch to Test CTA once the whole build — gateway included — is deployed", () => {
        renderCTA([
            { filename: "gw.yaml", type: "gateway", name: "OrderValidationGateway", status: "deployed", origin: "session" },
            { filename: "a.yaml", type: "agent", name: "OrderValidator", status: "deployed", origin: "session" },
        ]);
        expect(screen.getByText("Switch to Test Mode")).toBeInTheDocument();
    });

    test("does NOT show the Test CTA while the gateway (entrypoint) is undeployed/failed — the build isn't fully activated", () => {
        const { container } = renderCTA([
            { filename: "gw.yaml", type: "gateway", name: "OrderValidationGateway", status: "validated", origin: "session" },
            { filename: "a.yaml", type: "agent", name: "OrderValidator", status: "deployed", origin: "session" },
        ]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("does not render while a deployable component (agent/workflow) is still validated", () => {
        const { container } = renderCTA([{ filename: "a.yaml", type: "agent", name: "OrderValidator", status: "validated", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("does not render for a gateway-only build while the gateway is not deployed", () => {
        const { container } = renderCTA([{ filename: "gw.yaml", type: "gateway", name: "OrderValidationGateway", status: "validated", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    describe("entrypoint-only build", () => {
        const gateway = (name: string, extra: Record<string, unknown> = {}) => ({ filename: `${name}.yaml`, type: "gateway", name, status: "deployed", origin: "session", ...extra }) as const;
        const PLACEHOLDER = "<<__SAM_REQUIRED__>>";
        const record = (name: string, values: Record<string, unknown>, deployed?: Record<string, unknown>) => ({ name, values, ...(deployed ? { deployed: { values: deployed } } : {}) });
        const FILLED = { broker_url: "tcps://b:1" };
        const deployedOne = (name: string) => `Your “${name}” entrypoint is deployed — find it under Entrypoints.`;
        const deployedMany = (count: number) => `Your ${count} entrypoints are deployed — find them under Entrypoints.`;
        const holdPlaceholders = (names: string, count: number) =>
            ` The broker connection settings of ${names} still hold placeholders. With no system broker configured they are ignored and ${count === 1 ? "the entrypoint runs" : "the entrypoints run"} on the built-in broker; set real values on the Entrypoints page to connect your own.`;
        const couldNotCheck = (names: string) => ` Could not check the broker connection settings of ${names}; open Entrypoints to confirm they are filled in.`;

        test.each([
            ["a placeholder in the platform record", [gateway("team_events")], [record("team_events", { broker_url: PLACEHOLDER })], false, deployedOne("team_events") + holdPlaceholders("“team_events”", 1)],
            ["no placeholder in the platform record", [gateway("team_events")], [record("team_events", { broker_url: "tcps://broker.example.com:55443" })], false, deployedOne("team_events")],
            [
                "a placeholder among several settings",
                [gateway("team_events")],
                [record("team_events", { broker_url: "tcps://b:1", broker_vpn: PLACEHOLDER, broker_password: "<REDACTED>" })],
                false,
                deployedOne("team_events") + holdPlaceholders("“team_events”", 1),
            ],
            [
                "a placeholder after several filled settings",
                [gateway("team_events")],
                [record("team_events", { broker_url: "tcps://b:1", broker_vpn: "default", broker_username: "u", broker_password: "<REDACTED>", extra: PLACEHOLDER })],
                false,
                deployedOne("team_events") + holdPlaceholders("“team_events”", 1),
            ],
            ["only a redacted secret, which says nothing either way", [gateway("team_events")], [record("team_events", { broker_password: "<REDACTED>" })], false, deployedOne("team_events")],
            ["a placeholder only in the draft settings of a deployed record", [gateway("team_events")], [record("team_events", { broker_url: PLACEHOLDER }, FILLED)], false, deployedOne("team_events")],
            ["a placeholder only in the deployed settings of a record", [gateway("team_events")], [record("team_events", FILLED, { broker_url: PLACEHOLDER })], false, deployedOne("team_events") + holdPlaceholders("“team_events”", 1)],
            ["platform records not loaded yet", [gateway("team_events")], undefined, false, deployedOne("team_events")],
            ["platform records failed to load", [gateway("team_events")], undefined, true, deployedOne("team_events") + couldNotCheck("“team_events”")],
            ["a refetch that failed while cached records are present", [gateway("team_events")], [record("team_events", FILLED)], true, deployedOne("team_events") + couldNotCheck("“team_events”")],
            ["the record missing from the loaded list", [gateway("team_events")], [record("other", { broker_url: PLACEHOLDER })], false, deployedOne("team_events") + couldNotCheck("“team_events”")],
            ["a placeholder only in an unrelated entrypoint", [gateway("team_events")], [record("team_events", FILLED), record("other", { broker_url: PLACEHOLDER })], false, deployedOne("team_events")],
            ["a differently spelled platform name", [gateway("TeamEvents")], [record("team_events", { broker_url: PLACEHOLDER })], false, deployedOne("TeamEvents") + holdPlaceholders("“TeamEvents”", 1)],
            ["the exact name over a look-alike", [gateway("team_events")], [record("teamevents", { broker_url: PLACEHOLDER }), record("team_events", FILLED)], false, deployedOne("team_events")],
            ["two entrypoints, one with a placeholder", [gateway("a_events"), gateway("b_events")], [record("a_events", FILLED), record("b_events", { broker_url: PLACEHOLDER })], false, deployedMany(2) + holdPlaceholders("“b_events”", 1)],
            ["two entrypoints, one missing from the list", [gateway("a_events"), gateway("b_events")], [record("a_events", FILLED)], false, deployedMany(2) + couldNotCheck("“b_events”")],
            ["one placeholder and one unresolvable entrypoint", [gateway("a_events"), gateway("b_events")], [record("a_events", { broker_url: PLACEHOLDER })], false, deployedMany(2) + holdPlaceholders("“a_events”", 1) + couldNotCheck("“b_events”")],
            [
                "two entrypoints holding a placeholder",
                [gateway("a_events"), gateway("b_events")],
                [record("a_events", { broker_url: PLACEHOLDER }), record("b_events", { broker_url: PLACEHOLDER })],
                false,
                deployedMany(2) + holdPlaceholders("“a_events”, “b_events”", 2),
            ],
            ["two entrypoints with no matching record", [gateway("a_events"), gateway("b_events")], [record("other", FILLED)], false, deployedMany(2) + couldNotCheck("“a_events”, “b_events”")],
            [
                "the manifest id picking the filled record over a look-alike that sorts first",
                [gateway("Team Events", { id: "u1" })],
                [
                    { id: "u2", ...record("teamevents", { broker_url: PLACEHOLDER }) },
                    { id: "u1", ...record("team_events", FILLED) },
                ],
                false,
                deployedOne("Team Events"),
            ],
            [
                "the manifest id picking the placeholder record over a filled look-alike that sorts first",
                [gateway("Team Events", { id: "u1" })],
                [
                    { id: "u2", ...record("teamevents", FILLED) },
                    { id: "u1", ...record("team_events", { broker_url: PLACEHOLDER }) },
                ],
                false,
                deployedOne("Team Events") + holdPlaceholders("“Team Events”", 1),
            ],
            ["a platform-origin entrypoint beside one session entrypoint", [gateway("a_events"), gateway("platform_events", { origin: "platform" })], [record("a_events", FILLED)], false, deployedOne("a_events")],
        ])("%s", (_label, components, records, failed, expectedText) => {
            h.entrypoints = records;
            h.entrypointsFailed = failed;
            const { container } = renderCTA(components);
            expect(container.textContent).toBe(expectedText);
        });

        test("does not render while the entrypoint is not deployed", () => {
            const { container } = renderCTA([gateway("team_events", { status: "validated" })]);
            expect(container.textContent).toBe("");
        });

        test("a build with a session agent still gets only the Test card", () => {
            renderCTA([gateway("team_events"), { filename: "a.yaml", type: "agent", name: "OrderValidator", status: "deployed", origin: "session" }]);
            expect(screen.getByText("Switch to Test Mode")).toBeInTheDocument();
            expect(screen.queryByText(/find it under Entrypoints/)).toBeNull();
        });
    });

    test("shows the skill-completion CTA once a skill-only build is built (validated)", () => {
        renderCTA([{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }]);
        expect(screen.getByText("Add to an agent")).toBeInTheDocument();
        expect(screen.getByText(/built and saved/)).toBeInTheDocument();
    });

    test("does not render while a skill is still a draft (not yet built)", () => {
        const { container } = renderCTA([{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "draft", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("the skill CTA submits an attach request via onAddToAgent (not a quote badge)", () => {
        const onAddToAgent = vi.fn();
        renderCTA([{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }], { onAddToAgent });
        fireEvent.click(screen.getByRole("button", { name: "Add to an agent" }));
        expect(onAddToAgent).toHaveBeenCalledTimes(1);
        expect(onAddToAgent).toHaveBeenCalledWith("Add the sample-geography skill to an agent.");
    });

    test("renders plural copy and submits both names for a multi-skill build", () => {
        const onAddToAgent = vi.fn();
        renderCTA(
            [
                { filename: "a.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" },
                { filename: "b.yaml", type: "skill", name: "sample-history", status: "validated", origin: "session" },
            ],
            { onAddToAgent }
        );
        expect(screen.getByText(/2 skills are built and saved/)).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Add to an agent" }));
        expect(onAddToAgent).toHaveBeenCalledWith("Add these skills to an agent: sample-geography, sample-history.");
    });

    test("a mixed agent+skill build shows the Test CTA, not the skill CTA (testable branch wins)", () => {
        renderCTA([
            { filename: "a.yaml", type: "agent", name: "A", status: "deployed", origin: "session" },
            { filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" },
        ]);
        expect(screen.getByText("Switch to Test Mode")).toBeInTheDocument();
        expect(screen.queryByText("Add to an agent")).toBeNull();
    });

    test("a mixed build whose agent isn't deployed yet renders nothing — the built skill must not surface a CTA mid-build", () => {
        const { container } = renderCTA([
            { filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" },
            { filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" },
        ]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("does not render for a skill in error status", () => {
        const { container } = renderCTA([{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "error", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("shows the connector-completion CTA once a connector-only build is built (validated) — the fix", () => {
        renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" }]);
        expect(screen.getByText("Add to an agent")).toBeInTheDocument();
        expect(screen.getByText(/connector is built and saved/)).toBeInTheDocument();
    });

    test("suppresses the connector CTA once the connectors list has loaded and the connector is NOT there (failed save) — the false-success fix", () => {
        h.connectors = [];
        const { container } = renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
        expect(screen.queryByText(/built and saved/)).not.toBeInTheDocument();
    });

    test("shows the connector CTA when the connector IS present on the platform (real save)", () => {
        h.connectors = [{ name: "SalesPostgres" }];
        renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" }]);
        expect(screen.getByText("Add to an agent")).toBeInTheDocument();
    });

    test("matches the platform connector by NORMALIZED name (manifest PascalCase vs platform snake_case)", () => {
        h.connectors = [{ name: "sales_postgres" }];
        renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" }]);
        expect(screen.getByText("Add to an agent")).toBeInTheDocument();
    });

    test("multi-connector: suppresses the CTA when NOT all connectors saved (one present, one absent)", () => {
        h.connectors = [{ name: "SalesPostgres" }];
        const { container } = renderCTA([
            { filename: "a.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" },
            { filename: "b.yaml", type: "connector", name: "InventoryPostgres", status: "validated", origin: "session" },
        ]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("the connector CTA submits an attach request via onAddToAgent", () => {
        const onAddToAgent = vi.fn();
        renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" }], { onAddToAgent });
        fireEvent.click(screen.getByRole("button", { name: "Add to an agent" }));
        expect(onAddToAgent).toHaveBeenCalledWith("Add the SalesPostgres connector to an agent.");
    });

    test("renders plural copy and submits both names for a multi-connector build", () => {
        const onAddToAgent = vi.fn();
        renderCTA(
            [
                { filename: "a.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" },
                { filename: "b.yaml", type: "connector", name: "InventoryPostgres", status: "validated", origin: "session" },
            ],
            { onAddToAgent }
        );
        expect(screen.getByText(/2 connectors are built and saved/)).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Add to an agent" }));
        expect(onAddToAgent).toHaveBeenCalledWith("Add these connectors to an agent: SalesPostgres, InventoryPostgres.");
    });

    test("does not render while a connector is still a draft (not yet built)", () => {
        const { container } = renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "draft", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("does not render for a connector in error status (parity with the skill error case)", () => {
        const { container } = renderCTA([{ filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "error", origin: "session" }]);
        expect(container.querySelector("button")).toBeNull();
    });

    test("a mixed skill+connector build shows the skill CTA, not the connector CTA (skill branch wins)", () => {
        renderCTA([
            { filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" },
            { filename: "c.yaml", type: "connector", name: "SalesPostgres", status: "validated", origin: "session" },
        ]);
        expect(screen.getByText(/skill is built and saved/)).toBeInTheDocument();
        expect(screen.queryByText(/connector is built and saved/)).toBeNull();
    });
});

describe("BuildCompleteCTA tool source failures", () => {
    const deployedAgent = [{ filename: "a.yaml", type: "agent", name: "Order Helper", status: "deployed", origin: "session" }];
    const failedRegistration = { toolType: "mcp", connectorId: "conn-1", status: "failed", toolCount: 0, error: "connection refused" };

    test("names the agent and connector instead of claiming a clean success when the connector could not connect", () => {
        h.connectors = [{ id: "conn-1", name: "Orders MCP" }];
        h.agents = [{ name: "Order Helper", toolRegistrations: [failedRegistration] }];
        renderCTA(deployedAgent);

        expect(screen.getByRole("alert")).toHaveTextContent("“Order Helper” could not load tools from the “Orders MCP” connector");
        expect(screen.queryByText(/is created and deployed!/)).toBeNull();
        expect(screen.getByText("Switch to Test Mode")).toBeInTheDocument();
    });

    test("stays a plain success when every connector registered", () => {
        h.connectors = [{ id: "conn-1", name: "Orders MCP" }];
        h.agents = [{ name: "Order Helper", toolRegistrations: [{ ...failedRegistration, status: "ready", toolCount: 4 }] }];
        renderCTA(deployedAgent);

        expect(screen.getByText(/Your agent is created and deployed!/)).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
    });

    test("stays a plain success before the platform agents have loaded", () => {
        renderCTA(deployedAgent);
        expect(screen.getByText(/Your agent is created and deployed!/)).toBeInTheDocument();
    });

    test("re-reads the agents on mount and polls only while an agent has not reported yet", () => {
        h.agents = [{ name: "Order Helper", runtimeStatus: { status: "starting" } }];
        renderCTA(deployedAgent);

        const options = h.lastAgentsOptions;
        expect(options?.enabled).toBe(true);
        expect(options?.refetchOnMount).toBe("always");
        expect(options?.refetchInterval?.([{ name: "Order Helper", runtimeStatus: { status: "starting" } }])).toBeGreaterThan(0);
        expect(options?.refetchInterval?.([{ name: "Order Helper", runtimeStatus: { status: "running" } }])).toBe(false);
    });

    describe("polling window", () => {
        const starting = [{ name: "Order Helper", runtimeStatus: { status: "starting" } }];
        const running = [{ name: "Order Helper", runtimeStatus: { status: "running" } }];
        const cta = (components: NonNullable<BuildManifest["components"]>) => {
            const stamped = components.map(c => ({ ...c, deployed_version: 0 }));
            const versions = new Map(stamped.map(c => [c.filename, 0]));
            return <BuildCompleteCTA manifest={manifest(stamped)} deployEdits={{ versions, serverApplyEnabled: false }} onSwitchToTest={vi.fn()} onAddToAgent={vi.fn()} />;
        };
        const interval = (agents: unknown[]) => h.lastAgentsOptions?.refetchInterval?.(agents);

        test("starts when the agent is first seen starting, however long the card sat before the deploy", () => {
            vi.useFakeTimers({ toFake: ["Date"] });
            try {
                const { rerender } = render(cta([{ ...deployedAgent[0], status: "validated" }]));
                vi.setSystemTime(Date.now() + REGISTRATION_POLL_LIMIT_MS + 1);

                rerender(cta(deployedAgent));
                expect(interval(starting)).toBeGreaterThan(0);
            } finally {
                vi.useRealTimers();
            }
        });

        test("ends a fixed time after it started, across re-renders", () => {
            vi.useFakeTimers({ toFake: ["Date"] });
            try {
                const { rerender } = render(cta(deployedAgent));
                expect(interval(starting)).toBeGreaterThan(0);

                vi.setSystemTime(Date.now() + REGISTRATION_POLL_LIMIT_MS - 1000);
                rerender(cta(deployedAgent));
                expect(interval(starting)).toBeGreaterThan(0);

                vi.setSystemTime(Date.now() + 2000);
                rerender(cta(deployedAgent));
                expect(interval(starting)).toBe(false);
            } finally {
                vi.useRealTimers();
            }
        });

        test("restarts after the agent settles and is deployed again", () => {
            vi.useFakeTimers({ toFake: ["Date"] });
            try {
                render(cta(deployedAgent));
                expect(interval(starting)).toBeGreaterThan(0);
                vi.setSystemTime(Date.now() + REGISTRATION_POLL_LIMIT_MS + 1);
                expect(interval(starting)).toBe(false);

                expect(interval(running)).toBe(false);
                expect(interval(starting)).toBeGreaterThan(0);
            } finally {
                vi.useRealTimers();
            }
        });
    });

    test("does not read the agents for a build with no session agent", () => {
        renderCTA([{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }]);
        expect(h.lastAgentsOptions?.enabled).toBe(false);
    });

    describe("a build with a blocked component", () => {
        const REASON = "the http(s) URL of the Inventory API's OpenAPI document";
        const blockedConnector = { filename: "inventory_api.json", type: "connector", name: "inventory_api", status: "blocked", blocked_reason: REASON, origin: "session" };
        const deployedChecker = { filename: "checker.yaml", type: "agent", name: "InventoryChecker", status: "deployed", origin: "session", connectors: ["inventory_api"], depends_on: ["inventory_api"] };
        const deployedIntake = { filename: "intake.yaml", type: "agent", name: "OrderIntake", status: "deployed", origin: "session", connectors: [] };

        test("never says the build is created and deployed, and offers no Test Mode, even when every deployable component reads deployed", () => {
            renderCTA([blockedConnector, deployedIntake, deployedChecker]);
            expect(screen.queryByText(/created and deployed/)).toBeNull();
            expect(screen.queryByText("Switch to Test Mode")).toBeNull();
            expect(screen.getByRole("alert")).toBeInTheDocument();
        });

        test("names the component, the exact thing the user must provide, and what is held back", () => {
            renderCTA([blockedConnector, deployedIntake, deployedChecker]);
            const card = screen.getByRole("alert");
            expect(card).toHaveTextContent("Inventory Api");
            expect(card).toHaveTextContent(`needs ${REASON}`);
            expect(card).toHaveTextContent("Held back because they use it: InventoryChecker");
            expect(card).not.toHaveTextContent("Order Intake");
            expect(card).toHaveTextContent("Nothing new has been deployed");
        });

        test("counts several blocked components", () => {
            renderCTA([blockedConnector, { ...blockedConnector, name: "crm_api", filename: "crm_api.json", blocked_reason: "the CRM spec URL" }, deployedIntake]);
            const card = screen.getByRole("alert");
            expect(card).toHaveTextContent("2 planned components cannot be created yet");
            expect(card).toHaveTextContent("needs the CRM spec URL");
        });

        test("replaces the connector-only and skill-only completion cards too", () => {
            renderCTA([blockedConnector, { filename: "s.yaml", type: "skill", name: "geo", status: "validated", origin: "session" }]);
            expect(screen.queryByText("Add to an agent")).toBeNull();
            expect(screen.getByRole("alert")).toBeInTheDocument();
        });

        describe("a value of the wrong type in a field the blocked reading uses", () => {
            const oddValues: [string, unknown][] = [
                ["an array", ["inventory_api"]],
                ["an object", { name: "inventory_api" }],
                ["a number", 42],
                ["a boolean", true],
                ["null", null],
                ["an empty string", ""],
            ];
            const manifestWith = (where: "connector" | "blocked_reason" | "skills", value: unknown) => {
                const workflow: Record<string, unknown> = {
                    filename: "wf.yaml",
                    type: "workflow",
                    name: "StockFlow",
                    origin: "session",
                    status: "validated",
                    nodes: [{ id: "fetch", type: "tool", connector: where === "connector" ? value : "inventory_api" }],
                };
                if (where === "skills") workflow.skills = value;
                return { name: "build", components: [{ ...blockedConnector, ...(where === "blocked_reason" ? { blocked_reason: value } : {}) }, workflow] };
            };

            test.each(["connector", "blocked_reason", "skills"] as const)("%s: every odd value leaves the manifest readable and the card rendering", where => {
                for (const [label, value] of oddValues) {
                    const parsed = BuildManifestSchema.safeParse(manifestWith(where, value));
                    expect(parsed.success, `${where} as ${label} made the manifest unreadable`).toBe(true);
                    const { unmount } = render(<BuildCompleteCTA manifest={parsed.data as BuildManifest} deployEdits={{ versions: new Map(), serverApplyEnabled: false }} onSwitchToTest={vi.fn()} onAddToAgent={vi.fn()} />);
                    expect(screen.getByRole("alert"), `${where} as ${label}`).toHaveTextContent("Inventory Api");
                    unmount();
                }
            });
        });

        test("a platform component with the same status is not a blocked build", () => {
            renderCTA([{ ...blockedConnector, origin: "platform", filename: "" }, deployedIntake]);
            expect(screen.queryByRole("alert")).toBeNull();
            expect(screen.getByText("Switch to Test Mode")).toBeInTheDocument();
        });
    });
});
