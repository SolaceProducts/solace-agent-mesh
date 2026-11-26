// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, renderHook } from "@testing-library/react";
import { createMemoryRouter, type RouteObject, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { BudgetUsagePageContainer, FeedbackGate, UsageGate } from "@/features/observability";
import { BudgetsGate, BudgetsPageContainer } from "@/features/systemSettings";

import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { useCreateRouter } from "./router";

const { mockWhenProviderReady } = vi.hoisted(() => ({ mockWhenProviderReady: vi.fn(() => true) }));

vi.mock("@openfeature/react-sdk", async importOriginal => ({
    ...(await importOriginal<typeof import("@openfeature/react-sdk")>()),
    useWhenProviderReady: mockWhenProviderReady,
}));

beforeEach(() => {
    mockWhenProviderReady.mockReturnValue(true);
});

interface RouterOptions {
    readonly held?: readonly Capability[];
    readonly isSuperAdmin?: boolean;
    readonly rbacEnabled?: boolean;
    readonly configUseAuthorization?: boolean;
}

function buildRoutes({ held = [], isSuperAdmin = false, rbacEnabled = false, configUseAuthorization = true }: RouterOptions = {}): RouteObject[] {
    const { result } = renderHook(() => useCreateRouter(), {
        wrapper: ({ children }) => (
            <StoryProvider userContextValues={{ userHasCapability: capability => held.includes(capability), isSuperAdmin }} configContextValues={{ configUseAuthorization }} flagValues={{ rbac: rbacEnabled }}>
                {children}
            </StoryProvider>
        ),
    });
    return result.current.routes;
}

function surface(routes: RouteObject[], path: string): RouteObject {
    const shell = routes.find(route => route.path === "/");
    const page = shell?.children?.[0]?.children?.find(route => route.path === path);
    if (!page) throw new Error(`no route registered for "${path}"`);
    return page;
}

const childPaths = (route: RouteObject) => (route.children ?? []).map(child => child.path ?? "(index)");

const isRedirectedTo = (route: RouteObject, target: "chat" | "agent-mesh") => {
    const children = route.children ?? [];
    const element = children[0]?.element as { props?: { to?: string } } | undefined;
    return children[0]?.index === true && element?.props?.to === `/${target}`;
};

const READ_GATES: ReadonlyArray<[path: string, capability: Capability, target: "chat" | "agent-mesh"]> = [
    ["workflows", CAPABILITIES.readWorkflows, "chat"],
    ["agents", CAPABILITIES.readAgents, "chat"],
    ["entrypoints", CAPABILITIES.readGateways, "chat"],
    ["evaluations", CAPABILITIES.readEvaluations, "chat"],
    ["models", CAPABILITIES.readModelConfig, "chat"],
    ["connectors", CAPABILITIES.readConnectors, "agent-mesh"],
    ["toolsets", CAPABILITIES.readToolsets, "agent-mesh"],
    ["skills", CAPABILITIES.readSkills, "agent-mesh"],
];

describe("router surface gating", () => {
    test.each(READ_GATES)("%s redirects to %s without %s", (path, capability, target) => {
        expect(isRedirectedTo(surface(buildRoutes(), path), target)).toBe(true);
        expect(isRedirectedTo(surface(buildRoutes({ held: [capability] }), path), target)).toBe(false);
    });

    test("agent-mesh keeps its list and the workflow view without readAgents", () => {
        const paths = childPaths(surface(buildRoutes(), "agent-mesh"));

        expect(paths).toContain("(index)");
        expect(paths).toContain("workflows/:workflowName");
        expect(isRedirectedTo(surface(buildRoutes(), "agent-mesh"), "chat")).toBe(false);
    });

    test("only toolsets and skills carry a splat in their fallback", () => {
        const routes = buildRoutes();

        expect(childPaths(surface(routes, "toolsets"))).toContain("*");
        expect(childPaths(surface(routes, "skills"))).toContain("*");
        expect(childPaths(surface(routes, "connectors"))).not.toContain("*");
        expect(childPaths(surface(routes, "models"))).not.toContain("*");
    });

    test("a read scope does not unlock any other surface", () => {
        const routes = buildRoutes({ held: [CAPABILITIES.readModelConfig] });

        expect(isRedirectedTo(surface(routes, "models"), "chat")).toBe(false);
        expect(isRedirectedTo(surface(routes, "connectors"), "agent-mesh")).toBe(true);
        expect(isRedirectedTo(surface(routes, "entrypoints"), "chat")).toBe(true);
        expect(isRedirectedTo(surface(routes, "evaluations"), "chat")).toBe(true);
    });
});

describe("router write-gated children", () => {
    test("connector edit routes need a connector write verb", () => {
        expect(childPaths(surface(buildRoutes({ held: [CAPABILITIES.readConnectors] }), "connectors"))).not.toContain("new/edit");

        expect(childPaths(surface(buildRoutes({ held: [CAPABILITIES.readConnectors, CAPABILITIES.createConnectors] }), "connectors"))).toEqual(expect.arrayContaining(["new/edit", ":id/edit"]));
    });

    test("model edit routes need a model write verb", () => {
        expect(childPaths(surface(buildRoutes({ held: [CAPABILITIES.readModelConfig] }), "models"))).not.toContain("new/edit");

        expect(childPaths(surface(buildRoutes({ held: [CAPABILITIES.readModelConfig, CAPABILITIES.updateModelConfig] }), "models"))).toEqual(expect.arrayContaining(["new/edit", ":id/edit"]));
    });
});

describe("router user-management gating", () => {
    test("needs the rbac flag and a universal wildcard, not merely one of them", () => {
        expect(isRedirectedTo(surface(buildRoutes({ rbacEnabled: true, isSuperAdmin: false }), "user-management"), "chat")).toBe(true);
        expect(isRedirectedTo(surface(buildRoutes({ rbacEnabled: false, isSuperAdmin: true }), "user-management"), "chat")).toBe(true);
        expect(isRedirectedTo(surface(buildRoutes({ rbacEnabled: true, isSuperAdmin: true }), "user-management"), "chat")).toBe(false);
    });

    test("stays hidden when authorization is off, even for a super admin", () => {
        expect(isRedirectedTo(surface(buildRoutes({ rbacEnabled: true, isSuperAdmin: true, configUseAuthorization: false }), "user-management"), "chat")).toBe(true);
    });

    test("stays hidden in the desktop app", () => {
        (globalThis as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: () => {} };
        try {
            expect(isRedirectedTo(surface(buildRoutes({ rbacEnabled: true, isSuperAdmin: true }), "user-management"), "chat")).toBe(true);
        } finally {
            delete (globalThis as unknown as { runtime?: unknown }).runtime;
        }
    });
});

describe("router budgets gating", () => {
    test("without budget:*:read, Budgets still mounts behind BudgetsGate rather than redirecting", () => {
        const budgets = surface(buildRoutes(), "budgets");
        expect((budgets.element as { type?: unknown } | undefined)?.type).toBe(BudgetsGate);
        expect(isRedirectedTo(budgets, "chat")).toBe(false);
        expect(childPaths(budgets)).toEqual(["(index)", ":budgetId/usage"]);
        expect((budgets.children?.[0]?.element as { type?: unknown } | undefined)?.type).toBe(BudgetsPageContainer);
    });

    test("a budget's usage page is behind the usage gate", () => {
        const usage = surface(buildRoutes({ held: [CAPABILITIES.readBudgets] }), "budgets").children?.[1];
        expect((usage?.element as { type?: unknown } | undefined)?.type).toBe(UsageGate);
        expect((usage?.children?.[0]?.element as { type?: unknown } | undefined)?.type).toBe(BudgetUsagePageContainer);
    });

    test("in the desktop app, a budget's usage page link lands on Budgets, replacing the dead entry", () => {
        enableWailsRuntime();
        try {
            const usage = surface(buildRoutes({ held: [CAPABILITIES.readBudgets] }), "budgets").children?.[1];
            const router = createMemoryRouter([{ path: "/budgets", children: [{ index: true, element: <p>Budgets page</p> }, usage!] }], { initialEntries: ["/budgets/b1/usage"] });
            render(<RouterProvider router={router} />);
            expect(router.state.location.pathname).toBe("/budgets");
            expect(router.state.historyAction).toBe("REPLACE");
        } finally {
            disableWailsRuntime();
        }
    });
});

describe("signals and usage surfaces", () => {
    test("mounts feedback with dashboard and history children", () => {
        const feedback = surface(buildRoutes(), "feedback");
        const layout = (feedback?.children ?? [])[0];

        expect((layout?.children ?? []).map(child => child.path ?? "(index)")).toEqual(["(index)", "history"]);
    });

    test("mounts usage behind its own gate, regardless of capability or scope", () => {
        for (const isSuperAdmin of [false, true]) {
            const usage = surface(buildRoutes({ isSuperAdmin }), "usage");

            expect((usage?.element as { type?: unknown })?.type).toBe(UsageGate);
            expect((usage?.children ?? []).map(child => child.index)).toEqual([true]);
        }
    });

    test("the feedback subtree mounts regardless of capability", () => {
        for (const held of [[], [CAPABILITIES.readFeedback]]) {
            const feedback = surface(buildRoutes({ held }), "feedback");

            expect((feedback.element as { type?: unknown } | undefined)?.type).toBe(FeedbackGate);
        }
    });
});
