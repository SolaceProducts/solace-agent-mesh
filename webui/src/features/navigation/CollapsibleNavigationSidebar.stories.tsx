// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { BookOpenText, Bot, Files, FolderOpen, LogOut, User } from "lucide-react";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import type { NavItemConfig } from "@/types/nav";

import { mockSessions } from "@/testing/data/sessions";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { CollapsibleNavigationSidebar } from "./CollapsibleNavigationSidebar";

const defaultItems: NavItemConfig[] = [
    {
        id: "projects",
        label: "Projects",
        icon: FolderOpen,
        route: "/projects",
        routeMatch: "/projects",
        position: "top",
    },
    {
        id: "assets",
        label: "Assets",
        icon: BookOpenText,
        position: "top",
        children: [
            {
                id: "prompts",
                label: "Prompts",
                icon: BookOpenText,
                route: "/prompts",
                routeMatch: "/prompts",
                tooltip: "Experimental Feature",
            },
            {
                id: "artifacts",
                label: "Artifacts",
                icon: Files,
                route: "/artifacts",
                routeMatch: "/artifacts",
                tooltip: "Experimental Feature",
            },
        ],
    },
    {
        id: "agents",
        label: "Agents",
        icon: Bot,
        route: "/agent-mesh",
        routeMatch: "/agent-mesh",
        position: "top",
    },
    {
        id: "userAccount",
        label: "User Account",
        icon: User,
        onClick: () => {},
        position: "bottom",
    },
    {
        id: "logout",
        label: "Log Out",
        icon: LogOut,
        onClick: () => {},
        position: "bottom",
    },
];

const sharedWithMeItems = [
    {
        id: "share-1",
        title: "Shared Analysis Report",
        ownerEmail: "alice@example.com",
        accessLevel: "resource_viewer",
        sharedAt: new Date(Date.now() - 86400000).toISOString(),
        shareUrl: "/shared/share-1",
    },
    {
        id: "share-2",
        title: "Collaborative Design Review",
        ownerEmail: "bob@example.com",
        accessLevel: "resource_editor",
        sharedAt: new Date(Date.now() - 172800000).toISOString(),
        shareUrl: "/shared/share-2",
        sessionId: "session-2",
    },
];

const sessionHandlers = [
    http.get("/api/v1/sessions", () => {
        return HttpResponse.json({
            data: mockSessions,
            meta: getPaginatedMeta(mockSessions.length),
        });
    }),
    http.get("/api/v1/share/shared-with-me", () => {
        return HttpResponse.json([]);
    }),
];

const sessionHandlersWithShared = [
    ...sessionHandlers.filter(h => !String(h.info.path).includes("shared-with-me")),
    http.get("/api/v1/share/shared-with-me", () => {
        return HttpResponse.json(sharedWithMeItems);
    }),
];

const meta = {
    title: "Navigation/CollapsibleNavigationSidebar",
    component: CollapsibleNavigationSidebar,
    parameters: {
        msw: { handlers: withDefaults(...sessionHandlers) },
    },
    decorators: [
        Story => (
            <div className="flex h-screen">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof CollapsibleNavigationSidebar>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Expanded: Story = {
    args: {
        items: defaultItems,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Projects")).toBeInTheDocument();
        expect(canvas.getByText("Assets")).toBeInTheDocument();
        expect(canvas.getByText("Agents")).toBeInTheDocument();
        expect(canvas.getByText("User Account")).toBeInTheDocument();
        expect(canvas.getByText("Log Out")).toBeInTheDocument();
        expect(canvas.getByText("New Chat")).toBeInTheDocument();
        expect(canvas.getByText("Recent Chats")).toBeInTheDocument();
    },
};

export const Collapsed: Story = {
    args: {
        items: defaultItems,
        isCollapsed: true,
    },
};

export const WithSubmenus: Story = {
    args: {
        items: defaultItems.map(item => (item.id === "assets" ? { ...item, defaultExpanded: true } : item)),
        activeItemId: "prompts",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Assets")).toBeInTheDocument();
        expect(canvas.getByText("Prompts")).toBeInTheDocument();
        expect(canvas.getByText("Artifacts")).toBeInTheDocument();
    },
};

export const HiddenItems: Story = {
    args: {
        items: [
            ...defaultItems.slice(0, 2),
            { id: "agents", label: "Agents", icon: Bot, route: "/agent-mesh", routeMatch: "/agent-mesh", position: "top" as const, hidden: true },
            ...defaultItems
                .filter(i => i.id === "assets")
                .map(item => ({
                    ...item,
                    id: "assets-with-hidden-child",
                    defaultExpanded: true,
                    children: item.children?.map(c => (c.id === "artifacts" ? { ...c, hidden: true } : c)),
                })),
            { id: "userAccount", label: "User Account", icon: User, onClick: () => {}, position: "bottom" as const },
            { id: "logout", label: "Log Out", icon: LogOut, onClick: () => {}, position: "bottom" as const, hidden: true },
        ],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Projects")).toBeInTheDocument();
        expect(canvas.getByText("User Account")).toBeInTheDocument();
        expect(canvas.queryByText("Agents")).not.toBeInTheDocument();
        expect(canvas.queryByText("Log Out")).not.toBeInTheDocument();
        expect(canvas.getByText("Prompts")).toBeInTheDocument();
        expect(canvas.queryByText("Artifacts")).not.toBeInTheDocument();
    },
};

export const CustomHeader: Story = {
    args: {
        items: defaultItems,
        header: (
            <div className="flex items-center gap-2">
                <span className="text-xl font-bold text-(--darkSurface-text)">Custom Logo</span>
            </div>
        ),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Custom Logo")).toBeInTheDocument();

        expect(canvasElement.querySelector(".navigation-sidebar")).toBeInTheDocument();
    },
};

export const SmallOnlyExpanded: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: null,
            configSmallLogoUrl: "https://placehold.co/60x60/dc2626/ffffff?text=Small+Only",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
    },
};

export const EmptyLogoDrawsDefault: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: "",
            configSmallLogoUrl: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByAltText("Logo")).not.toBeInTheDocument();
        expect(canvas.getByAltText("Solace")).toBeInTheDocument();
    },
};

export const EmptyLogoFallsThroughToSmallLogo: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: "",
            configSmallLogoUrl: "https://placehold.co/60x60/dc2626/ffffff?text=Small+Only",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
        expect(canvas.queryByAltText("Solace")).not.toBeInTheDocument();
    },
};

export const SingleCustomLogoExpanded: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/200x60/4338ca/ffffff?text=Acme+Logo",
            configSmallLogoUrl: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo).toBeInTheDocument();
        expect(logo.src).toContain("Acme+Logo");
    },
};

export const SingleCustomLogoCollapsed: Story = {
    args: {
        items: defaultItems,
        isCollapsed: true,
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/60x60/4338ca/ffffff?text=Acme",
            configSmallLogoUrl: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo).toBeInTheDocument();
        expect(logo.src).toContain("Acme");
    },
};

export const BothCustomLogosExpanded: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/200x60/059669/ffffff?text=Large+Logo",
            configSmallLogoUrl: "https://placehold.co/60x60/dc2626/ffffff?text=S",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("Large+Logo");
    },
};

export const BothCustomLogosCollapsed: Story = {
    args: {
        items: defaultItems,
        isCollapsed: true,
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/200x60/059669/ffffff?text=Large+Logo",
            configSmallLogoUrl: "https://placehold.co/60x60/dc2626/ffffff?text=S",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const logo = canvas.getByAltText("Logo") as HTMLImageElement;
        expect(logo.src).toContain("text=S");
    },
};

export const BrokenCustomLogoFallback: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://logo.invalid.local/missing.png",
            configSmallLogoUrl: null,
        },
    },
};

const scrollItems: NavItemConfig[] = [
    { id: "alpha", label: "Alpha", icon: Bot, route: "/alpha", routeMatch: "/alpha", position: "top" },
    { id: "bravo", label: "Bravo", icon: Bot, route: "/bravo", routeMatch: "/bravo", position: "top" },
    { id: "charlie", label: "Charlie", icon: Bot, route: "/charlie", routeMatch: "/charlie", position: "top" },
    {
        id: "reports",
        label: "Reports",
        icon: BookOpenText,
        position: "top",
        children: Array.from({ length: 10 }, (_, i) => ({
            id: `report-${i}`,
            label: `Report ${i + 1}`,
            icon: Files,
            route: `/reports/${i}`,
            routeMatch: `/reports/${i}`,
        })),
    },
    { id: "delta", label: "Delta", icon: Bot, route: "/delta", routeMatch: "/delta", position: "top" },
    { id: "echo", label: "Echo", icon: Bot, route: "/echo", routeMatch: "/echo", position: "top" },
    { id: "userAccount", label: "User Account", icon: User, onClick: () => {}, position: "bottom" },
    { id: "logout", label: "Log Out", icon: LogOut, onClick: () => {}, position: "bottom" },
];

export const SubmenuScrollsIntoView: Story = {
    args: {
        items: scrollItems,
    },
    decorators: [
        Story => (
            <div className="flex" style={{ height: 500 }}>
                <Story />
            </div>
        ),
    ],
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const scroller = canvasElement.querySelector<HTMLElement>(".overflow-y-auto");
        expect(scroller).not.toBeNull();
        expect(scroller!.scrollTop).toBe(0);

        await userEvent.click(canvas.getByText("Reports"));

        await waitFor(() => {
            expect(scroller!.scrollTop).toBeGreaterThan(0);
        });
        expect(canvas.getByText("Report 1")).toBeInTheDocument();
    },
};

export const CollapsedSubmenuExpandsIntoView: Story = {
    args: {
        items: scrollItems,
        defaultCollapsed: true,
    },
    beforeEach: () => {
        sessionStorage.removeItem("nav-collapsed");
    },
    decorators: [
        Story => (
            <div className="flex" style={{ height: 500 }}>
                <Story />
            </div>
        ),
    ],
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const sidebar = canvasElement.querySelector(".navigation-sidebar")!;
        expect(sidebar).toHaveClass("w-20");

        await userEvent.click(canvas.getByRole("button", { name: /reports/i }));

        await waitFor(() => expect(sidebar).toHaveClass("w-64"));
        expect(await canvas.findByText("Report 1")).toBeInTheDocument();
    },
};

export const WithSharedChats: Story = {
    args: {
        items: defaultItems,
    },
    parameters: {
        msw: { handlers: withDefaults(...sessionHandlersWithShared) },
        configContext: {
            persistenceEnabled: true,
            identityServiceType: "okta",
        },
        featureContext: {
            chat_sharing: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByText("Recent Chats")).toBeInTheDocument();
        await expect(canvas.queryByText("Shared with Me")).not.toBeInTheDocument();
    },
};
