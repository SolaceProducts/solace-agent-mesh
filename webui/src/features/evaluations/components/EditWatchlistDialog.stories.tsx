// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { onlineManager } from "@tanstack/react-query";
import { delay, http, HttpResponse } from "msw";
import { expect, fn, screen, userEvent, waitFor } from "storybook/test";

import { agentToV2, workflowToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent } from "@/types";

import { EditWatchlistDialog } from "./EditWatchlistDialog";

const WATCHLIST_URL = "*/api/v2/platform/evaluations/watchlist";

const DB_AGENTS: Agent[] = [
    { id: "agent-1", name: "MermaidAgent", instanceName: "MermaidAgent", description: "" },
    { id: "agent-2", name: "WebAgent", instanceName: "WebAgent", description: "" },
    { id: "agent-3", name: "MarkitdownAgent", instanceName: "MarkitdownAgent", description: "" },
];

const MESH_AGENTS = [
    { name: "OrchestratorAgent", description: "Coordinates other agents" },
    { name: "MermaidAgent", description: "Generates diagrams" },
];

const baseHandlers = (watchlist: { agentName: string; resolvedModelProvider: string; isBuiltIn: boolean; position: number }[] = []) =>
    withDefaults(
        http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse(watchlist))),
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
        http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS }))
    );

const meta = {
    title: "Evaluations/EditWatchlistDialog",
    component: EditWatchlistDialog,
    parameters: {
        docs: {
            description: {
                component: "Modal for managing the eval watchlist (max 5 agents). Always renders 5 selection rows with drag-handles. Options merge DB-registered agents and live mesh-discovered agents into a single de-duped list.",
            },
        },
    },
    args: {
        open: true,
        onOpenChange: fn(),
    },
} satisfies Meta<typeof EditWatchlistDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const EmptyDialog: Story = {
    parameters: { msw: { handlers: baseHandlers([]) } },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        await waitFor(async () => {
            await expect(screen.getByText(/Select up to 5 agents to display on your Watchlist/i)).toBeInTheDocument();
        });

        await waitFor(
            async () => {
                const triggers = await screen.findAllByRole("combobox");
                await expect(triggers).toHaveLength(5);
            },
            { timeout: 10_000 }
        );

        await expect(screen.getAllByText("Select an agent...")).toHaveLength(5);
    },
};

export const PrePopulatedFromWatchlist: Story = {
    parameters: {
        msw: {
            handlers: baseHandlers([
                { agentName: "MermaidAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 0 },
                { agentName: "WebAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 1 },
            ]),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");

        await waitFor(
            async () => {
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
                await expect(screen.getByText("WebAgent")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.getAllByText("Select an agent...")).toHaveLength(3);
    },
};

export const DiscardClosesWithoutSaving: Story = {
    parameters: { msw: { handlers: baseHandlers([]) } },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await userEvent.click(screen.getByRole("button", { name: /Discard Changes/i }));
        await expect(args.onOpenChange).toHaveBeenCalledWith(false);
    },
};

export const DedupesPlatformAgentSharedWithMeshCard: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse([agentToV2({ id: "uuid-orch", name: "Orchestrator", instanceName: "agent_uuid_orch", description: "" })], 20))),
                http.get("*/api/v1/agentCards", () =>
                    HttpResponse.json({
                        data: [
                            { name: "agent_uuid_orch", description: "Orchestrator card" },
                            { name: "SAM Builder", description: "The builder" },
                        ],
                    })
                )
            ),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");

        await waitFor(
            async () => {
                const triggers = await screen.findAllByRole("combobox");
                await expect(triggers).toHaveLength(5);
            },
            { timeout: 10_000 }
        );

        const triggers = screen.getAllByRole("combobox");
        await userEvent.click(triggers[0]);

        await waitFor(
            async () => {
                await expect(screen.getByPlaceholderText("Search agents...")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );

        await expect(screen.getByText("Orchestrator")).toBeInTheDocument();
        await expect(screen.getByText("SAM Builder")).toBeInTheDocument();
        await expect(screen.queryByText("agent_uuid_orch")).not.toBeInTheDocument();
    },
};

export const PersistsBrokerSlugNotDisplayName: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse([agentToV2({ id: "uuid-orch", name: "Orchestrator", instanceName: "agent_uuid_orch", description: "" })], 20))),
                http.get("*/api/v1/agentCards", () =>
                    HttpResponse.json({
                        data: [{ name: "agent_uuid_orch", description: "Orchestrator card" }],
                    })
                ),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    return HttpResponse.json(
                        paginatedResponse(
                            body.agentNames.map((name, i) => ({
                                agentName: name,
                                resolvedModelProvider: "general",
                                isBuiltIn: false,
                                position: i,
                            }))
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");

        await waitFor(
            async () => {
                const triggers = await screen.findAllByRole("combobox");
                await expect(triggers).toHaveLength(5);
            },
            { timeout: 10_000 }
        );

        let putRequestBody: { agentNames: string[] } | null = null;
        const originalFetch = window.fetch;
        window.fetch = async (input, init) => {
            if (init?.method === "PUT" && typeof input === "string" && input.includes("/watchlist")) {
                putRequestBody = JSON.parse(init.body as string);
            }
            return originalFetch(input, init);
        };

        try {
            const triggers = screen.getAllByRole("combobox");
            await userEvent.click(triggers[0]);
            await waitFor(
                async () => {
                    await expect(screen.getByPlaceholderText("Search agents...")).toBeInTheDocument();
                },
                { timeout: 10_000 }
            );
            await userEvent.click(await screen.findByText("Orchestrator"));
            await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));

            await waitFor(async () => {
                await expect(args.onOpenChange).toHaveBeenCalledWith(false);
            });
            await waitFor(async () => {
                await expect(putRequestBody).not.toBeNull();
            });
            await expect(putRequestBody!.agentNames).toEqual(["agent_uuid_orch"]);
        } finally {
            window.fetch = originalFetch;
        }
    },
};

export const DropsOrphanRowsFromPersistedWatchlist: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () =>
                    HttpResponse.json(
                        paginatedResponse([
                            { agentName: "agent_long_gone", resolvedModelProvider: "general", isBuiltIn: false, position: 0 },
                            { agentName: "MermaidAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 1 },
                        ])
                    )
                ),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS }))
            ),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");

        await waitFor(
            async () => {
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText("agent_long_gone")).not.toBeInTheDocument();
        await expect(screen.getByText(/no longer available/i)).toBeInTheDocument();

        await expect(screen.getAllByText("Select an agent...")).toHaveLength(4);
    },
};

const savedWorkflowWatchlist: string[][] = [];
export const KeepsWatchedWorkflowItCannotLookUp: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () =>
                    HttpResponse.json(
                        paginatedResponse([
                            { agentName: "order_flow", targetType: "workflow", resolvedModelProvider: "", isBuiltIn: false, position: 0 },
                            { agentName: "MermaidAgent", targetType: "agent", resolvedModelProvider: "general", isBuiltIn: false, position: 1 },
                        ])
                    )
                ),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    savedWorkflowWatchlist.push(body.agentNames);
                    return HttpResponse.json(paginatedResponse(body.agentNames.map((name, i) => ({ agentName: name, resolvedModelProvider: "general", isBuiltIn: false, position: i }))));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("order_flow (workflow)")).toBeInTheDocument();
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText(/no longer available/i)).not.toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));

        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
        await expect(savedWorkflowWatchlist.at(-1)).toEqual(["order_flow", "MermaidAgent"]);
    },
};

const savedAfterFailedLoad: string[][] = [];
export const KeepsWatchedWorkflowWhenTheListFails: Story = {
    parameters: {
        featureContext: { workflow_evals: true },
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () =>
                    HttpResponse.json(
                        paginatedResponse([
                            { agentName: "order_flow", targetType: "workflow", resolvedModelProvider: "", isBuiltIn: false, position: 0 },
                            { agentName: "MermaidAgent", targetType: "agent", resolvedModelProvider: "general", isBuiltIn: false, position: 1 },
                        ])
                    )
                ),
                http.get("*/api/v2/platform/workflows", () => HttpResponse.json({ message: "boom", errorId: "e1" }, { status: 500 })),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    savedAfterFailedLoad.push(body.agentNames);
                    return HttpResponse.json(paginatedResponse(body.agentNames.map((name, i) => ({ agentName: name, resolvedModelProvider: "general", isBuiltIn: false, position: i }))));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("order_flow (workflow)")).toBeInTheDocument();
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText(/no longer available/i)).not.toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));

        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
        await expect(savedAfterFailedLoad.at(-1)).toEqual(["order_flow", "MermaidAgent"]);
    },
};

export const DropsAWorkflowThatNoLongerExists: Story = {
    parameters: {
        featureContext: { workflow_evals: true },
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () =>
                    HttpResponse.json(
                        paginatedResponse([
                            { agentName: "gone_flow", targetType: "workflow", resolvedModelProvider: "", isBuiltIn: false, position: 0 },
                            { agentName: "MermaidAgent", targetType: "agent", resolvedModelProvider: "general", isBuiltIn: false, position: 1 },
                        ])
                    )
                ),
                http.get("*/api/v2/platform/workflows", () => HttpResponse.json(paginatedResponse([]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS }))
            ),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText("gone_flow (workflow)")).not.toBeInTheDocument();
        await expect(screen.getByText(/no longer available/i)).toBeInTheDocument();
    },
};

const remoteAgentsGate: { served: Set<string>; release?: () => void } = { served: new Set() };
export const WaitsForRemoteAgentsBeforeSaving: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => {
                    remoteAgentsGate.served.add("watchlist");
                    return HttpResponse.json(paginatedResponse([{ agentName: "MermaidAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 0 }]));
                }),
                http.get("*/api/v2/platform/agents", () => {
                    remoteAgentsGate.served.add("agents");
                    return HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20));
                }),
                http.get("*/api/v1/agentCards", () => {
                    remoteAgentsGate.served.add("cards");
                    return HttpResponse.json({ data: MESH_AGENTS });
                }),
                http.get("*/api/v2/platform/remoteAgents", async () => {
                    await new Promise<void>(resolve => {
                        remoteAgentsGate.release = resolve;
                    });
                    return HttpResponse.json(paginatedResponse([]));
                })
            ),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(remoteAgentsGate.served.size).toBe(3);
                await expect(remoteAgentsGate.release).toBeDefined();
            },
            { timeout: 10_000 }
        );
        await delay(200);
        await expect(screen.getByRole("button", { name: /^Save$/i })).toBeDisabled();
        await expect(screen.queryByText("MermaidAgent")).not.toBeInTheDocument();

        remoteAgentsGate.release?.();
        await waitFor(
            async () => {
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
                await expect(screen.getByRole("button", { name: /^Save$/i })).toBeEnabled();
            },
            { timeout: 10_000 }
        );
    },
};

const savedAfterAgentsFailed: string[][] = [];
export const KeepsWatchedAgentWhenTheAgentListFails: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([{ agentName: "agent_platform_only", resolvedModelProvider: "general", isBuiltIn: false, position: 0 }]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json({ message: "boom", errorId: "e1" }, { status: 500 })),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    savedAfterAgentsFailed.push(body.agentNames);
                    return HttpResponse.json(paginatedResponse(body.agentNames.map((name, i) => ({ agentName: name, resolvedModelProvider: "general", isBuiltIn: false, position: i }))));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("Could not load agents. Try again.")).toBeInTheDocument();
                await expect(screen.getByText("agent_platform_only")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
        await expect(savedAfterAgentsFailed.at(-1)).toEqual(["agent_platform_only"]);
    },
};

const savedListedWorkflow: string[][] = [];
export const SavesAListedWorkflowByName: Story = {
    parameters: {
        featureContext: { workflow_evals: true },
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([{ agentName: "order_flow", targetType: "workflow", resolvedModelProvider: "", isBuiltIn: false, position: 0 }]))),
                http.get("*/api/v2/platform/workflows", () =>
                    HttpResponse.json(
                        paginatedResponse([workflowToV2({ id: "wf-1", name: "order_flow", displayName: "", description: "", appConfig: {}, deploymentStatus: "deployed", createdBy: "u", updatedBy: "u", createdTime: 0, updatedTime: 0 })])
                    )
                ),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    savedListedWorkflow.push(body.agentNames);
                    return HttpResponse.json(paginatedResponse(body.agentNames.map((name, i) => ({ agentName: name, resolvedModelProvider: "general", isBuiltIn: false, position: i }))));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("order_flow (workflow)")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText(/no longer available/i)).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
        await expect(savedListedWorkflow.at(-1)).toEqual(["order_flow"]);
    },
};

export const DoesNotSaveAfterTheWatchlistFailedToLoad: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json({ message: "boom", errorId: "e1" }, { status: 500 })),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS }))
            ),
        },
    },
    play: async () => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("Could not load your watchlist.")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.getByRole("button", { name: /^Save$/i })).toBeDisabled();
    },
};

export const DoesNotSaveWhileOffline: Story = {
    parameters: { msw: { handlers: baseHandlers([{ agentName: "MermaidAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 0 }]) } },
    beforeEach: () => {
        onlineManager.setOnline(false);
        return () => onlineManager.setOnline(true);
    },
    play: async () => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("Could not load your watchlist.")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.getByRole("button", { name: /^Save$/i })).toBeDisabled();
    },
};

let watchlistLoads = 0;
export const RetriesAWatchlistThatFailedToLoad: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => {
                    watchlistLoads += 1;
                    if (watchlistLoads === 1) return HttpResponse.json({ message: "boom", errorId: "e1" }, { status: 500 });
                    return HttpResponse.json(paginatedResponse([{ agentName: "MermaidAgent", resolvedModelProvider: "general", isBuiltIn: false, position: 0 }]));
                }),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS }))
            ),
        },
    },
    play: async () => {
        watchlistLoads = 0;
        await screen.findByRole("dialog");
        await userEvent.click(await screen.findByRole("button", { name: "Retry" }, { timeout: 10_000 }));
        await waitFor(
            async () => {
                await expect(screen.getByText("MermaidAgent")).toBeInTheDocument();
                await expect(screen.getByRole("button", { name: /^Save$/i })).toBeEnabled();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText("Could not load your watchlist.")).not.toBeInTheDocument();
    },
};

const savedWithoutWorkflowRead: string[][] = [];
export const KeepsWatchedWorkflowForAUserWhoCannotReadWorkflows: Story = {
    parameters: {
        featureContext: { workflow_evals: true },
        userContext: { userHasCapability: (capability: string) => capability !== "readWorkflows" },
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([{ agentName: "order_flow", targetType: "workflow", resolvedModelProvider: "", isBuiltIn: false, position: 0 }]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    savedWithoutWorkflowRead.push(body.agentNames);
                    return HttpResponse.json(paginatedResponse(body.agentNames.map((name, i) => ({ agentName: name, resolvedModelProvider: "general", isBuiltIn: false, position: i }))));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");
        await waitFor(
            async () => {
                await expect(screen.getByText("order_flow (workflow)")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await expect(screen.queryByText(/no longer available/i)).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
        await expect(savedWithoutWorkflowRead.at(-1)).toEqual(["order_flow"]);
    },
};

export const SavePersistsSelectionAndCloses: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([]))),
                http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(DB_AGENTS.map(agentToV2), 20))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: MESH_AGENTS })),
                http.put(WATCHLIST_URL, async ({ request }) => {
                    const body = (await request.json()) as { agentNames: string[] };
                    return HttpResponse.json(
                        paginatedResponse(
                            body.agentNames.map((name, i) => ({
                                agentName: name,
                                resolvedModelProvider: "general",
                                isBuiltIn: false,
                                position: i,
                            }))
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByRole("dialog");

        await waitFor(
            async () => {
                const triggers = await screen.findAllByRole("combobox");
                await expect(triggers).toHaveLength(5);
            },
            { timeout: 10_000 }
        );

        const triggers = screen.getAllByRole("combobox");
        await userEvent.click(triggers[0]);

        await waitFor(
            async () => {
                await expect(screen.getByPlaceholderText("Search agents...")).toBeInTheDocument();
            },
            { timeout: 10_000 }
        );
        await userEvent.click(await screen.findByText("MermaidAgent"));

        await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));

        await waitFor(async () => {
            await expect(args.onOpenChange).toHaveBeenCalledWith(false);
        });
    },
};
