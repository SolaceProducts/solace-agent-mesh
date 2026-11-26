// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useNavigationItems } from "./useNavigationItems";

vi.mock("@/lib", () => ({}));

const baseProps = {
    persistenceEnabled: true,
    builderEnabled: true,
    artifactsPageEnabled: false,
    schedulerEnabled: false,
    isAuthenticated: true,
    logoutEnabled: true,
    onUserAccountClick: vi.fn(),
    onLogoutClick: vi.fn(),
    userCanReadAgents: false,
    userCanReadConnectors: false,
    userCanReadToolsets: false,
    userCanReadSkills: false,
    userCanReadWorkflows: false,
    userCanReadGateways: false,
    userCanReadModelConfig: false,
    userCanReadEvaluations: false,
    canSeeUserManagement: false,
    canSeeBudgets: false,
    canSeeFeedback: false,
    canSeeUsage: false,
};

const fullAdminProps = {
    ...baseProps,
    userCanReadAgents: true,
    userCanReadConnectors: true,
    userCanReadToolsets: true,
    userCanReadSkills: true,
    userCanReadWorkflows: true,
    userCanReadGateways: true,
    userCanReadModelConfig: true,
};

describe("useNavigationItems Builder group", () => {
    it("renders the group labelled 'Builder' with all admin children when capabilities are on", () => {
        const { result } = renderHook(() => useNavigationItems(fullAdminProps));

        const sysMgmt = result.current.items.find(item => item.id === "systemManagement");
        const childIds = sysMgmt?.children?.map((c: { id: string }) => c.id) ?? [];

        expect(sysMgmt?.label).toBe("Builder");
        expect(childIds).toEqual(["build", "agents", "workflows", "connectors-mgmt", "gateways-mgmt", "toolsets", "skills"]);
    });

    it("puts Models under System Settings, not Builder", () => {
        const { result } = renderHook(() => useNavigationItems(fullAdminProps));

        const settings = result.current.items.find(item => item.id === "systemSettings");
        expect(settings?.label).toBe("System Settings");
        expect((settings?.children ?? []).map((c: { id: string }) => c.id)).toEqual(["models"]);

        const sysMgmt = result.current.items.find(item => item.id === "systemManagement");
        expect((sysMgmt?.children ?? []).map((c: { id: string }) => c.id)).not.toContain("models");
    });

    it("hides System Settings entirely when the user cannot read model configs", () => {
        const { result } = renderHook(() => useNavigationItems({ ...fullAdminProps, userCanReadModelConfig: false }));

        expect(result.current.items.find(item => item.id === "systemSettings")).toBeUndefined();
    });

    it("hides the Builder group entirely when the user holds no child scopes and Quick Build is off", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, builderEnabled: false }));

        const sysMgmt = result.current.items.find(item => item.id === "systemManagement");
        expect(sysMgmt).toBeUndefined();
    });

    it("shows the Workflows child only when the user has workflow_builder:*:read", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, builderEnabled: false, userCanReadWorkflows: true }));

        const sysMgmt = result.current.items.find(item => item.id === "systemManagement");
        const childIds = sysMgmt?.children?.map((c: { id: string }) => c.id) ?? [];

        expect(childIds).toEqual(["workflows"]);
    });

    it("does not list Notifications under Assets, since the bell opens the Notifications page", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, artifactsPageEnabled: true }));
        const assets = result.current.items.find(item => item.id === "assets");
        expect((assets?.children ?? []).map((c: { id: string }) => c.id)).not.toContain("notifications");
    });

    it("narrows agentMesh.routeMatch to /agents so it no longer claims /connectors or /entrypoints", () => {
        const { result } = renderHook(() => useNavigationItems(fullAdminProps));

        const agentMesh = result.current.items.find(item => item.id === "agentMesh");
        expect(agentMesh?.routeMatch).toBe("/agent-mesh");
    });
});

describe("useNavigationItems User Management item", () => {
    it("sits at the top of System Settings, above Budgets", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, canSeeUserManagement: true, canSeeBudgets: true }));

        const settings = result.current.items.find(item => item.id === "systemSettings");
        expect(settings?.children?.map(c => ({ id: c.id, route: c.route }))).toEqual([
            { id: "user-management", route: "/user-management" },
            { id: "budgets", route: "/budgets" },
        ]);
        expect(result.current.items.find(item => item.id === "user-management")).toBeUndefined();
    });

    it("appears nowhere when the viewer can't see it", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, canSeeUserManagement: false, canSeeBudgets: true }));

        const settings = result.current.items.find(item => item.id === "systemSettings");
        expect(settings?.children?.map(c => c.id)).toEqual(["budgets"]);
        expect(result.current.items.find(item => item.id === "user-management")).toBeUndefined();
    });

    it("alone keeps System Settings visible", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, canSeeUserManagement: true, canSeeBudgets: false, userCanReadModelConfig: false }));

        const settings = result.current.items.find(item => item.id === "systemSettings");
        expect(settings?.children?.map(c => c.id)).toEqual(["user-management"]);
    });
});

describe("useNavigationItems Evaluations item", () => {
    it("hides the Evaluations nav item when the user lacks evaluation:_:read", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, userCanReadEvaluations: false }));

        const evals = result.current.items.find(item => item.id === "evaluations");
        expect(evals?.hidden).toBe(true);
    });

    it("shows the Evaluations nav item when the user has evaluation:_:read", () => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, userCanReadEvaluations: true }));

        const evals = result.current.items.find(item => item.id === "evaluations");
        expect(evals?.hidden).toBe(false);
    });
});

describe("useNavigationItems System Settings group", () => {
    it("hides the group when the viewer can see nothing in it", () => {
        const { result } = renderHook(() => useNavigationItems(baseProps));
        expect(result.current.items.find(item => item.id === "systemSettings")).toBeUndefined();
    });

    it("shows Budgets under System Settings, directly after Builder", () => {
        const { result } = renderHook(() => useNavigationItems({ ...fullAdminProps, userCanReadModelConfig: false, canSeeBudgets: true }));
        const ids = result.current.items.map(item => item.id);
        const settings = result.current.items.find(item => item.id === "systemSettings");

        expect(settings?.label).toBe("System Settings");
        expect(settings?.children?.map(c => ({ id: c.id, route: c.route }))).toEqual([{ id: "budgets", route: "/budgets" }]);
        expect(ids.indexOf("systemSettings")).toBe(ids.indexOf("systemManagement") + 1);
    });

    it("lists Budgets then Models when the viewer can see both", () => {
        const { result } = renderHook(() => useNavigationItems({ ...fullAdminProps, canSeeBudgets: true }));
        const settingsGroups = result.current.items.filter(item => item.id === "systemSettings");

        expect(settingsGroups).toHaveLength(1);
        expect(settingsGroups[0].children?.map(c => c.id)).toEqual(["budgets", "models"]);
    });
});

describe("useNavigationItems Signals & Usage group", () => {
    const itemsWith = (canSeeFeedback: boolean, canSeeUsage = false) => {
        const { result } = renderHook(() => useNavigationItems({ ...baseProps, canSeeFeedback, canSeeUsage }));
        return result.current.items;
    };

    it("leaves Activities at the top level when the viewer has no other Signals & Usage page", () => {
        const items = itemsWith(false);

        expect(items.find(item => item.id === "observability")).toBeUndefined();
        expect(items.find(item => item.id === "activities")).toMatchObject({ route: "/activities", position: "top" });
    });

    it("moves Activities into the group beside Feedback when the viewer can see Feedback", () => {
        const items = itemsWith(true);

        expect(items.find(item => item.id === "activities")).toBeUndefined();
        const observability = items.find(item => item.id === "observability");
        expect(observability).toMatchObject({ label: "Signals & Usage", position: "top" });
        expect((observability?.children ?? []).map(child => child.id)).toEqual(["activities", "feedback"]);
        expect(observability?.children?.[0]?.route).toBe("/activities");
        expect(observability?.children?.[1]?.route).toBe("/feedback");
    });

    it("lists Usage last, routing to /usage, and forms the group on its own when Feedback is unavailable", () => {
        const route = (items: ReturnType<typeof itemsWith>) => (items.find(item => item.id === "observability")?.children ?? []).map(child => [child.id, child.route]);

        expect(route(itemsWith(true, true))).toEqual([
            ["activities", "/activities"],
            ["feedback", "/feedback"],
            ["usage", "/usage"],
        ]);
        expect(route(itemsWith(false, true))).toEqual([
            ["activities", "/activities"],
            ["usage", "/usage"],
        ]);
    });
});
