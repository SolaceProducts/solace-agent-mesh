// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor } from "storybook/test";

import type { ComponentAccessUser } from "@/api/rbac";

import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { UserAccessDialog } from "./UserAccessDialog";

const AGENT_ID = "agent-1";
const ACCESS = `*/api/v2/platform/agents/${AGENT_ID}/access`;

const mockUsers: ComponentAccessUser[] = [
    { id: "u1", displayName: "Alice Nguyen", userName: "", email: "alice@example.com", isSystemUser: false },
    { id: "u2", displayName: "Bartholomew Maximilian Fitzgerald-Montgomery III", userName: "", email: "bart@example.com", isSystemUser: false },
    { id: "u3", displayName: "Dana Osei", userName: "", email: "dana.okonkwo-adeyemi.longaddress.for.testing@a-very-long-corporate-subdomain.example.com", isSystemUser: false },
    { id: "u4", displayName: "Evangelina Aleksandrovna Konstantinopolitanaya-Vishnyakova", userName: "", email: "evangelina.aleksandrovna.konstantinopolitanaya@internationalized-domain-name.example.co", isSystemUser: false },
    { id: "u5", displayName: "", userName: "", email: "charlie.no-display-name.but.still.a.rather.long.email.address@example.com", isSystemUser: false },
    { id: "6dbe83db-3549-4d99-b4fb-c3aea7b75219", displayName: "Default", userName: "", email: "", isSystemUser: true },
];

const toAccessWire = ({ id, displayName, userName, email, isSystemUser }: ComponentAccessUser) => ({ userId: id, displayName: displayName || null, userName: userName || null, email: email || null, isSystemUser });

const usersHandler = (users: ComponentAccessUser[]) => http.get(ACCESS, () => HttpResponse.json({ data: users.map(toAccessWire), meta: getPaginatedMeta(users.length) }));

const manyUsers: ComponentAccessUser[] = Array.from({ length: 47 }, (_, i) => {
    const n = String(i + 1).padStart(2, "0");
    return { id: `user-${n}`, displayName: `User ${n} Example`, userName: "", email: `user${n}@example.com`, isSystemUser: false };
});

const pagedUsersHandler = (all: ComponentAccessUser[]) =>
    http.get(ACCESS, ({ request }) => {
        const url = new URL(request.url);
        const pageNumber = Math.max(1, Number(url.searchParams.get("pageNumber") ?? 1));
        const pageSize = Math.max(1, Number(url.searchParams.get("pageSize") ?? 20));
        const search = (url.searchParams.get("search") ?? "").trim().toLowerCase();
        const matched = search ? all.filter(u => (u.displayName ?? "").toLowerCase().includes(search) || (u.email ?? "").toLowerCase().includes(search)) : all;
        const totalPages = Math.max(1, Math.ceil(matched.length / pageSize));
        const start = (pageNumber - 1) * pageSize;
        return HttpResponse.json({
            data: matched.slice(start, start + pageSize).map(toAccessWire),
            meta: { pagination: { pageNumber, count: matched.length, pageSize, nextPage: pageNumber < totalPages ? pageNumber + 1 : null, totalPages } },
        });
    });

function OpenDialog({ name = "HR Agent" }: { readonly name?: string }) {
    const [open, setOpen] = useState(true);
    return <UserAccessDialog target={open ? { agentId: AGENT_ID } : null} name={name} onOpenChange={setOpen} />;
}

const meta = {
    title: "User Management/UserAccessDialog",
    component: UserAccessDialog,
    args: { target: { agentId: AGENT_ID }, name: "HR Agent", onOpenChange: () => {} },
    render: () => <OpenDialog />,
} satisfies Meta<typeof UserAccessDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: { msw: { handlers: withDefaults(usersHandler(mockUsers)) } },
    play: async () => {
        expect(await screen.findByText("User Access")).toBeInTheDocument();
        expect(await screen.findByText(`Users (${mockUsers.length})`)).toBeInTheDocument();
        expect(await screen.findByText("Alice Nguyen")).toBeInTheDocument();
        expect(await screen.findByText("Service Account")).toBeInTheDocument();
        expect(screen.getAllByText("Service Account")).toHaveLength(1);
    },
};

export const Paged: Story = {
    parameters: { msw: { handlers: withDefaults(pagedUsersHandler(manyUsers)) } },
    play: async () => {
        expect(await screen.findByText(`Users (${manyUsers.length})`)).toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "Load more" })).toBeInTheDocument();
    },
};

export const Search: Story = {
    parameters: { msw: { handlers: withDefaults(pagedUsersHandler(manyUsers)) } },
    play: async () => {
        await userEvent.type(await screen.findByTestId("userAccessSearch"), "User 05");
        await waitFor(() => expect(screen.getByText("Users (1)")).toBeInTheDocument());
        expect(screen.getByText("User 05 Example")).toBeInTheDocument();
    },
};

export const Empty: Story = {
    parameters: { msw: { handlers: withDefaults(usersHandler([])) } },
    play: async () => {
        expect(await screen.findByText(/No known user has access/)).toBeInTheDocument();
    },
};

export const ErrorState: Story = {
    parameters: { msw: { handlers: withDefaults(http.get(ACCESS, () => HttpResponse.json({ message: "Something went wrong" }, { status: 500 }))) } },
    play: async () => {
        expect(await screen.findByText(/Failed to load user access/)).toBeInTheDocument();
    },
};
