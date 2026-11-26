// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { createElement } from "react";
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentConnectExternalPage } from "./AgentConnectExternalPage";

expect.extend(matchers);

const server = setupServer(
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderConnectPage(loaderData: Record<string, unknown> = {}, navigate = vi.fn()) {
    const router = createMemoryRouter(
        [
            {
                path: "/agents/external/new",
                loader: () => loaderData,
                element: createElement(StoryProvider, {
                    chatContextValues: {
                        navigate,
                        addNotification: vi.fn(),
                    },
                    children: createElement(AgentConnectExternalPage),
                }),
            },
            { path: "*", element: createElement("div", { "data-testid": "other-page" }) },
        ],
        { initialEntries: ["/agents/external/new"] }
    );
    render(createElement(RouterProvider, { router }));
    return { navigate, router };
}

describe("AgentConnectExternalPage — Discard Changes navigation", () => {
    test("Discard Changes calls navigate(-1), NOT navigate('/agent-mesh')", async () => {
        const navigate = vi.fn();
        renderConnectPage({}, navigate);

        const cancelBtn = await screen.findByTestId("externalAgentStepCancel");
        await userEvent.click(cancelBtn);

        expect(navigate).toHaveBeenCalledWith(-1);
        expect(navigate).not.toHaveBeenCalledWith("/agent-mesh");
    });
});

describe("AgentConnectExternalPage — editing", () => {
    async function saveEditOf(stored: Record<string, unknown>): Promise<unknown> {
        let putBody: unknown;
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [stored], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/r1", () => HttpResponse.json({ data: stored })),
            http.put("*/api/v2/platform/remoteAgents/r1", async ({ request }) => {
                putBody = await request.json();
                return HttpResponse.json({ data: stored });
            })
        );
        renderConnectPage({ isEdit: true, remoteAgentId: "r1" });

        await waitFor(() => expect(screen.getByTestId("externalAgentStepNext")).toBeEnabled());
        await userEvent.click(screen.getByTestId("externalAgentStepNext"));
        await userEvent.click(await screen.findByText("Next: Review Agent"));
        await userEvent.click(await screen.findByTestId("externalAgentConnect"));
        await waitFor(() => expect(putBody).toBeDefined());
        return putBody;
    }

    test("keeps the stored request timeout and A2A version pin, since a v2 PUT resets what it leaves out", async () => {
        const stored = {
            id: "r1",
            name: "Orders",
            description: "Handles orders.",
            agentUrl: "https://orders.example.com",
            agentCardLocation: "none",
            agentAuthType: "none",
            requestTimeoutSeconds: 300,
            protocolVersion: "v0_3",
            trustedCardJwksUrls: [],
            requireSignedCard: false,
            skills: [{ name: "orders", description: "Looks up orders." }],
            inputModes: ["text"],
            outputModes: ["text"],
            deploymentStatus: "not_deployed",
            syncStatus: null,
            runtimeStatus: { status: "unknown", problems: [] },
            deployed: null,
            createdBy: "u",
            updatedBy: "u",
            createdTime: null,
            updatedTime: null,
        };
        expect(await saveEditOf(stored)).toMatchObject({ requestTimeoutSeconds: 300, protocolVersion: "v0_3", trustedCardJwksUrls: [], requireSignedCard: false });
    });
});

describe("AgentConnectExternalPage — editing a card agent's A2A version and card trust", () => {
    const storedCardAgent = {
        id: "r1",
        name: "Orders",
        description: "Handles orders.",
        agentUrl: "https://orders.example.com",
        agentCardLocation: "well_known",
        agentCardAuthType: "none",
        agentAuthType: "none",
        requestTimeoutSeconds: 300,
        protocolVersion: "v1_0",
        trustedCardJwksUrls: ["https://keys.example.com/jwks.json"],
        requireSignedCard: true,
        skills: [],
        inputModes: ["text"],
        outputModes: ["text"],
        deploymentStatus: "not_deployed",
        cardData: { name: "orders-agent", description: "Handles orders." },
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
    };
    const storedOnV2 = { ...storedCardAgent, type: "remoteAgent", syncStatus: null, runtimeStatus: { status: "unknown", problems: [] }, deployed: null, cardFetchError: null, createdTime: null, updatedTime: null };

    type CardAuthType = "none" | "missing";
    function storedAgent(cardAuthType: CardAuthType): Record<string, unknown> {
        return cardAuthType === "missing" ? { ...storedOnV2, agentCardAuthType: null } : { ...storedOnV2 };
    }

    async function saveUnchangedEdit(cardAuthType: CardAuthType = "none"): Promise<unknown> {
        const stored = storedAgent(cardAuthType);
        let putBody: unknown;
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [stored], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/r1", () => HttpResponse.json({ data: stored })),
            http.put("*/api/v2/platform/remoteAgents/r1", async ({ request }) => {
                putBody = await request.json();
                return HttpResponse.json({ data: stored });
            })
        );
        renderConnectPage({ isEdit: true, remoteAgentId: "r1" });

        await waitFor(() => expect(screen.getByTestId("externalAgentStepNext")).toBeEnabled());
        await userEvent.click(screen.getByTestId("externalAgentStepNext"));
        await userEvent.click(await screen.findByText("Next: Review Agent"));
        await userEvent.click(await screen.findByTestId("externalAgentConnect"));
        await waitFor(() => expect(putBody).toBeDefined());
        return putBody;
    }

    test.each(["none", "missing"] as const)("an unchanged edit of a card agent whose card auth type is %s sends the stored pin, card trust and a card auth of none", async cardAuthType => {
        expect(await saveUnchangedEdit(cardAuthType)).toMatchObject({
            agentCardLocation: "well_known",
            agentCardAuthConfig: { type: "none" },
            protocolVersion: "v1_0",
            trustedCardJwksUrls: ["https://keys.example.com/jwks.json"],
            requireSignedCard: true,
        });
    });
});

describe("AgentConnectExternalPage — trusted URLs stored without a signature requirement", () => {
    const storedUrl = "https://keys.example.com/jwks.json";
    const storedWithoutRequirement = {
        id: "r1",
        type: "remoteAgent",
        name: "Orders",
        description: "Handles orders.",
        agentUrl: "https://orders.example.com",
        agentCardLocation: "well_known",
        agentCardAuthType: "none",
        agentAuthType: "none",
        requestTimeoutSeconds: 300,
        protocolVersion: "auto",
        trustedCardJwksUrls: [storedUrl],
        requireSignedCard: false,
        skills: [],
        inputModes: ["text"],
        outputModes: ["text"],
        deploymentStatus: "not_deployed",
        syncStatus: null,
        runtimeStatus: { status: "unknown", problems: [] },
        deployed: null,
        cardData: { name: "orders-agent", description: "Handles orders." },
        cardFetchError: null,
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdTime: null,
        updatedTime: null,
    };

    async function editAndSave(onStepTwo: () => Promise<void>, stored: Record<string, unknown> = storedWithoutRequirement): Promise<unknown> {
        let putBody: unknown;
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [stored], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/r1", () => HttpResponse.json({ data: stored })),
            http.put("*/api/v2/platform/remoteAgents/r1", async ({ request }) => {
                putBody = await request.json();
                return HttpResponse.json({ data: stored });
            })
        );
        renderConnectPage({ isEdit: true, remoteAgentId: "r1" });
        await waitFor(() => expect(screen.getByTestId("externalAgentStepNext")).toBeEnabled());
        await userEvent.click(screen.getByTestId("externalAgentStepNext"));
        await screen.findByText("Next: Review Agent");
        await onStepTwo();
        await userEvent.click(await screen.findByText("Next: Review Agent"));
        await userEvent.click(await screen.findByTestId("externalAgentConnect"));
        await waitFor(() => expect(putBody).toBeDefined());
        return putBody;
    }

    const requireSignedCard = () => screen.getByRole("checkbox", { name: "Require Signed Card" });

    test("an unchanged edit sends the stored URLs and no requirement", async () => {
        expect(await editAndSave(async () => {})).toMatchObject({ trustedCardJwksUrls: [storedUrl], requireSignedCard: false });
    });

    test("ticking, going to the review step and back, then unticking still sends the stored URLs", async () => {
        const body = await editAndSave(async () => {
            await userEvent.click(requireSignedCard());
            await userEvent.click(screen.getByText("Next: Review Agent"));
            await userEvent.click(await screen.findByTestId("externalAgentStepBack"));
            await userEvent.click(await screen.findByRole("checkbox", { name: "Require Signed Card" }));
        });
        expect(body).toMatchObject({ trustedCardJwksUrls: [storedUrl], requireSignedCard: false });
    });

    test("unticking an agent stored with URLs and the requirement sends no URLs and no requirement", async () => {
        const body = await editAndSave(
            async () => {
                await userEvent.click(requireSignedCard());
            },
            { ...storedWithoutRequirement, requireSignedCard: true }
        );
        expect(body).toMatchObject({ trustedCardJwksUrls: [], requireSignedCard: false });
    });
});

describe("AgentConnectExternalPage — the card fetch banner", () => {
    const fetchedCard = (overrides: Record<string, unknown> = {}) => ({
        type: "remoteAgentCard",
        name: "orders-agent",
        description: "Handles orders.",
        skills: [],
        inputModes: ["text"],
        outputModes: ["text"],
        interfaces: [{ version: "v1_0", binding: "jsonrpc" }],
        signed: true,
        ...overrides,
    });
    const answerFetch = (respond: () => Response | Promise<Response>) => server.use(http.put("*/api/v2/platform/remoteAgents/fetchCard", respond));
    const cardJson = (overrides: Record<string, unknown> = {}) => HttpResponse.json({ data: fetchedCard(overrides) });

    async function pick(select: string, option: string) {
        await userEvent.click(screen.getByTestId(`externalAgentSelect-${select}`));
        await userEvent.click(await screen.findByTestId(`externalAgentSelect-${select}-option-${option}`));
    }

    async function openStepOneAndFetch() {
        renderConnectPage();
        await userEvent.type(await screen.findByTestId("externalAgentInput-agentUrl"), "https://orders.example.com");
        await pick("agentCardLocation", "well_known");
        await pick("authenticationType", "none");
        await userEvent.click(screen.getByTestId("externalAgentFetchCard"));
    }

    const successBanner = async () => (await screen.findByText(/Successfully fetched agent card: orders-agent/)).closest("[data-testid='messageBanner']") as HTMLElement;

    test("a fetched card's interfaces and signature are inside the success banner", async () => {
        answerFetch(() => cardJson());
        await openStepOneAndFetch();
        const banner = await successBanner();
        expect(within(banner).getByText("Supported interfaces: A2A 1.0 over JSON-RPC")).toBeInTheDocument();
        expect(within(banner).getByText("Card signature: Signed")).toBeInTheDocument();
    });

    test("a fetch error after a success shows only the error, without the old card's details or warning", async () => {
        answerFetch(() => cardJson({ interfaces: [{ version: "v0_3", binding: "jsonrpc" }] }));
        await openStepOneAndFetch();
        await successBanner();
        answerFetch(async () => {
            await delay(60);
            return HttpResponse.json({ message: "The agent card could not be fetched.", errorId: "e1" }, { status: 502 });
        });
        await userEvent.click(screen.getByTestId("externalAgentFetchCard"));
        const banner = (await screen.findByText("The agent card could not be fetched.")).closest("[data-testid='messageBanner']") as HTMLElement;
        expect(banner).toBeInTheDocument();
        expect(screen.queryByText(/Successfully fetched agent card/)).toBeNull();
        expect(screen.queryByText(/Card signature/)).toBeNull();
        expect(screen.queryByText(/offers only A2A 0.3/)).toBeNull();
    });

    test("closing the banner and fetching again shows it again", async () => {
        answerFetch(() => cardJson());
        await openStepOneAndFetch();
        await userEvent.click(within(await successBanner()).getByRole("button"));
        expect(screen.queryByText(/Successfully fetched agent card/)).toBeNull();
        await userEvent.click(screen.getByTestId("externalAgentFetchCard"));
        expect(within(await successBanner()).getByText("Card signature: Signed")).toBeInTheDocument();
    });

    test("closing the banner of a card that offers only A2A 0.3 keeps the warning", async () => {
        answerFetch(() => cardJson({ interfaces: [{ version: "v0_3", binding: "jsonrpc" }], signed: false }));
        await openStepOneAndFetch();
        expect(within(await successBanner()).getByText("Card signature: Not signed")).toBeInTheDocument();
        await userEvent.click(within(await successBanner()).getByRole("button"));
        expect(screen.getByText(/offers only A2A 0.3/)).toBeInTheDocument();
    });

    test("the previous card's banner is not shown while a new fetch is in flight", async () => {
        answerFetch(() => cardJson());
        await openStepOneAndFetch();
        await successBanner();
        let release: () => void = () => {};
        const held = new Promise<void>(resolve => {
            release = resolve;
        });
        answerFetch(async () => {
            await held;
            return cardJson();
        });
        try {
            await userEvent.click(screen.getByTestId("externalAgentFetchCard"));
            await waitFor(() => expect(screen.getByTestId("externalAgentFetchCard")).toHaveTextContent("Fetching..."));
            expect(screen.queryByText(/Successfully fetched agent card/)).toBeNull();
        } finally {
            release();
        }
        await successBanner();
    });

    test("the edit page shows the stored agent's card details in the banner on step 1", async () => {
        const stored = {
            id: "r1",
            type: "remoteAgent",
            name: "Orders",
            description: "Handles orders.",
            agentUrl: "https://orders.example.com",
            agentCardLocation: "well_known",
            agentCardAuthType: "none",
            agentAuthType: "none",
            requestTimeoutSeconds: 300,
            protocolVersion: "auto",
            trustedCardJwksUrls: [],
            requireSignedCard: false,
            skills: [],
            inputModes: ["text"],
            outputModes: ["text"],
            deploymentStatus: "not_deployed",
            syncStatus: null,
            runtimeStatus: { status: "unknown", problems: [] },
            deployed: null,
            cardData: fetchedCard({ interfaces: [{ version: "v1_0", binding: "http_json" }], signed: true }),
            cardFetchError: null,
            createdBy: "alice@example.com",
            updatedBy: "alice@example.com",
            createdTime: null,
            updatedTime: null,
        };
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [stored], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/r1", () => HttpResponse.json({ data: stored }))
        );
        renderConnectPage({ isEdit: true, remoteAgentId: "r1" });
        const banner = await successBanner();
        expect(within(banner).getByText("Supported interfaces: A2A 1.0 over HTTP+JSON")).toBeInTheDocument();
        expect(within(banner).getByText("Card signature: Signed")).toBeInTheDocument();
    });
});
