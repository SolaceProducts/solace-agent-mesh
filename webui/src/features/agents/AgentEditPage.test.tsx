// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { createElement } from "react";
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { createAgent, createDeployedAgent } from "@/testing/data/agents";
import { agentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentEditPage } from "./AgentEditPage";

expect.extend(matchers);

const AGENT_ID_DEPLOYED = "edit-d1";
const AGENT_ID_UNDEPLOYED = "edit-u1";
const AGENT_ID_STOPPED = "edit-s1";
const AGENT_ID_DEGRADED = "edit-g1";
const CREATED_ID = "new-agent-id";

const deployedAgent = createDeployedAgent(AGENT_ID_DEPLOYED, {
    name: "Deployed Agent",
    description: "A deployed agent",
    systemPrompt: "You are helpful.",
    modelProvider: ["general"],
});

const undeployedAgent = createAgent(AGENT_ID_UNDEPLOYED, {
    name: "Undeployed Agent",
    description: "An undeployed agent",
    deploymentStatus: "not_deployed",
    systemPrompt: "You are helpful.",
    modelProvider: ["general"],
});

const stoppedAgent = createDeployedAgent(AGENT_ID_STOPPED, {
    name: "Stopped Agent",
    description: "A deployed-but-stopped agent",
    systemPrompt: "You are helpful.",
    modelProvider: ["general"],
    runtimeStatus: undefined,
});

const degradedAgent = createDeployedAgent(AGENT_ID_DEGRADED, {
    name: "Degraded Agent",
    description: "A deployed-but-degraded agent",
    systemPrompt: "You are helpful.",
    modelProvider: ["general"],
    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
});

const agents = [deployedAgent, undeployedAgent, stoppedAgent, degradedAgent];
const savedAgent = (id: string) => HttpResponse.json({ data: agentToV2(agents.find(a => a.id === id) ?? createAgent(id, { systemPrompt: "You are helpful." })) });
const deployment = (agentId: string) => HttpResponse.json({ data: { id: "dep-1", agentId, action: "deploy", status: "success", failure: null, createdBy: "u", createdTime: null, updatedTime: null } }, { status: 201 });

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: agents.map(agentToV2), meta: getPaginatedMeta(agents.length) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.get("*/api/v2/platform/skills", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.put("*/api/v2/platform/agents/:id", ({ params }) => savedAgent(String(params.id))),
    http.post("*/api/v2/platform/agents", () => savedAgent(CREATED_ID)),
    http.post("*/api/v2/platform/agentDeployments", async ({ request }) => deployment(((await request.json()) as { agentId: string }).agentId))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderEditPage(agentId: string | null, navigate = vi.fn(), addNotification = vi.fn()) {
    const isNew = agentId === null;
    const loaderData = isNew ? { agentId: undefined, isNew: true } : { agentId, isNew: false };
    const router = createMemoryRouter(
        [
            {
                path: "/agents/:id/edit",
                loader: () => loaderData,
                element: createElement(StoryProvider, {
                    chatContextValues: {
                        navigate,
                        useLoaderData: (() => loaderData) as unknown as typeof import("react-router-dom").useLoaderData,
                        addNotification,
                    },
                    children: createElement(AgentEditPage),
                }),
            },
            { path: "*", element: createElement("div") },
        ],
        { initialEntries: [`/agents/${agentId ?? "new"}/edit`] }
    );
    render(createElement(RouterProvider, { router }));
    return { navigate, addNotification };
}

describe("AgentEditPage — post-save navigation", () => {
    test("Save (existing deployed agent) → /agents?id=<id>&tab=deployed", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_DEPLOYED, navigate);

        const saveBtn = await screen.findByTestId("agentSave");
        await userEvent.click(saveBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID_DEPLOYED}&tab=deployed`);
        });
    });

    test("Save (existing undeployed agent) → /agents?id=<id>&tab=undeployed", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_UNDEPLOYED, navigate);

        const saveBtn = await screen.findByTestId("agentSave");
        await userEvent.click(saveBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID_UNDEPLOYED}&tab=undeployed`);
        });
    });

    test("Save and Deploy (existing agent) → /agents?id=<id>&tab=deployed", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_UNDEPLOYED, navigate);

        const saveDeployBtn = await screen.findByTestId("agentSaveAndDeploy");
        await userEvent.click(saveDeployBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID_UNDEPLOYED}&tab=deployed`);
        });
    });

    test("Create and Deploy (new agent) → /agents?id=<created-id>&tab=deployed", async () => {
        const navigate = vi.fn();
        const loaderData = { agentId: undefined, isNew: true };
        const router = createMemoryRouter(
            [
                {
                    path: "/agents/new/edit",
                    loader: () => loaderData,
                    element: createElement(StoryProvider, {
                        chatContextValues: {
                            navigate,
                            useLoaderData: (() => loaderData) as unknown as typeof import("react-router-dom").useLoaderData,
                            addNotification: vi.fn(),
                            agentToEdit: {
                                name: "New Agent",
                                description: "New",
                                systemPrompt: "You are helpful.",
                                modelProvider: ["general"],
                            },
                        },
                        children: createElement(AgentEditPage),
                    }),
                },
                { path: "*", element: createElement("div") },
            ],
            { initialEntries: ["/agents/new/edit"] }
        );
        render(createElement(RouterProvider, { router }));

        const createDeployBtn = await screen.findByTestId("agentSaveAndDeploy");
        await userEvent.click(createDeployBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${CREATED_ID}&tab=deployed`);
        });
    });

    test("Save (deployed+stopped agent) → /agents?id=<id>&tab=undeployed", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_STOPPED, navigate);

        const saveBtn = await screen.findByTestId("agentSave");
        await userEvent.click(saveBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID_STOPPED}&tab=undeployed`);
        });
    });

    test("Save (deployed+degraded agent) → /agents?id=<id>&tab=deployed (still running, not routed as undeployed)", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_DEGRADED, navigate);

        const saveBtn = await screen.findByTestId("agentSave");
        await userEvent.click(saveBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID_DEGRADED}&tab=deployed`);
        });
    });
});

describe("AgentEditPage — authored extras survive a save", () => {
    test("the save request carries additionalConfigurations", async () => {
        const bodies: Array<Record<string, unknown>> = [];
        server.use(
            http.get("*/api/v2/platform/agents", () =>
                HttpResponse.json({
                    data: [
                        agentToV2({
                            ...deployedAgent,
                            additionalConfigurations: { volumes: [{ name: "workspace", type: "ephemeral" }], supports_streaming: false },
                        }),
                    ],
                    meta: getPaginatedMeta(1),
                })
            ),
            http.put("*/api/v2/platform/agents/:id", async ({ request }) => {
                bodies.push((await request.json()) as Record<string, unknown>);
                return savedAgent(AGENT_ID_DEPLOYED);
            })
        );
        renderEditPage(AGENT_ID_DEPLOYED);

        await userEvent.click(await screen.findByTestId("agentSave"));

        await waitFor(() => expect(bodies).toHaveLength(1));
        expect(bodies[0].additionalConfigurations).toEqual({
            volumes: [{ name: "workspace", type: "ephemeral" }],
            supports_streaming: false,
        });
    });
});

describe("AgentEditPage — a save whose deploy fails", () => {
    test("says the agent was saved but not deployed", async () => {
        server.use(
            http.post("*/api/v2/platform/agentDeployments", () => HttpResponse.json({ message: "Unable to deploy agent", errorId: "e1" }, { status: 502, headers: { Location: "/api/v2/platform/agentDeployments/d1" } })),
            http.get("*/api/v2/platform/agentDeployments/d1", () =>
                HttpResponse.json({ data: { id: "d1", agentId: AGENT_ID_UNDEPLOYED, action: "deploy", status: "failed", failure: { reason: "runtime_error", message: "", errorId: "e1" }, createdBy: "u", createdTime: null, updatedTime: null } })
            )
        );
        const { addNotification } = renderEditPage(AGENT_ID_UNDEPLOYED);

        await userEvent.click(await screen.findByTestId("agentSaveAndDeploy"));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Agent saved but not deployed. Check the agent's deployment status for details.", "warning"));
    });

    test("gives the reason from the failed deployment's failure.message", async () => {
        const failure = "The agent runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.";
        const wire = {
            id: AGENT_ID_UNDEPLOYED,
            type: "agent",
            agentType: "standard",
            name: "Undeployed Agent",
            instanceName: "agent_edit_u1",
            description: "An undeployed agent",
            systemPrompt: "You are helpful.",
            toolsets: [],
            toolsetExcludedTools: null,
            toolsetConfigs: [],
            skills: [],
            skillRefs: [],
            skillIds: [],
            skillConfigs: [],
            inputModes: ["text"],
            outputModes: ["text"],
            connectorIds: [],
            modelProvider: ["general"],
            additionalConfigurations: null,
            deploymentStatus: "not_deployed",
            syncStatus: null,
            deployed: null,
            runtimeStatus: { status: "unknown", problems: [] },
            toolRegistrations: [],
            isEphemeral: false,
            sourceSessionId: null,
            sourceComponentName: null,
            sourceComponentKind: null,
            expiresTime: null,
            createdBy: "u",
            updatedBy: "u",
            createdTime: null,
            updatedTime: null,
        };
        server.use(
            http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [wire], meta: getPaginatedMeta(1) })),
            http.put("*/api/v2/platform/agents/:id", () => HttpResponse.json({ data: wire })),
            http.post("*/api/v2/platform/agentDeployments", () => HttpResponse.json({ message: "Unable to deploy agent", errorId: "e1" }, { status: 502, headers: { Location: "/api/v2/platform/agentDeployments/d1" } })),
            http.get("*/api/v2/platform/agentDeployments/d1", () =>
                HttpResponse.json({
                    data: { id: "d1", agentId: AGENT_ID_UNDEPLOYED, action: "deploy", status: "failed", failure: { reason: "runtime_error", message: failure, errorId: "e1" }, createdBy: "u", createdTime: null, updatedTime: null },
                })
            )
        );
        const { addNotification } = renderEditPage(AGENT_ID_UNDEPLOYED);

        await userEvent.click(await screen.findByTestId("agentSaveAndDeploy"));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(`Agent saved but not deployed: ${failure}`, "warning"));
    });
});

describe("AgentEditPage — save failure", () => {
    test("a failed save surfaces an error dialog instead of navigating away", async () => {
        server.use(http.put("*/api/v2/platform/agents/:id", () => HttpResponse.json({ message: "agent name already in use", errorId: "e1" }, { status: 409 })));
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_DEPLOYED, navigate);

        await userEvent.click(await screen.findByTestId("agentSave"));

        expect(await screen.findByText("Agent Update Failed")).toBeInTheDocument();
        expect(navigate).not.toHaveBeenCalled();
    });
});

describe("AgentEditPage — breadcrumb", () => {
    test("breadcrumb returns to Agents", async () => {
        const navigate = vi.fn();
        renderEditPage(AGENT_ID_DEPLOYED, navigate);

        await screen.findByTestId("agentSave");

        await userEvent.click(screen.getByRole("button", { name: "Agents" }));
        expect(navigate).toHaveBeenCalledWith("/agents");
    });
});
