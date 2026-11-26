// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { http, HttpResponse, type JsonBodyType } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { queryClient } from "@/providers/query/QueryClient";

import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { UserAccessDialog } from "./UserAccessDialog";

expect.extend(matchers);

const server = setupServer();

beforeAll(() => {
    server.listen();
    queryClient.setDefaultOptions({ queries: { retry: false } });
});
afterEach(() => {
    server.resetHandlers();
    queryClient.clear();
});
afterAll(() => server.close());

const accessHandler = (body: JsonBodyType, status = 200) => http.get("*/api/v2/platform/agents/:id/access", () => HttpResponse.json(body, { status }));

describe("UserAccessDialog", () => {
    test("lists the users who can access the component", async () => {
        server.use(
            accessHandler({
                data: [
                    { userId: "u1", displayName: "Alice", userName: null, email: "alice@example.com", isSystemUser: false },
                    { userId: "u2", displayName: null, userName: null, email: "bob@example.com", isSystemUser: false },
                ],
                meta: getPaginatedMeta(2),
            })
        );
        renderWithProviders(<UserAccessDialog target={{ agentId: "agent-populated" }} name="HR Agent" onOpenChange={() => {}} />);

        expect(await screen.findByText("User Access")).toBeInTheDocument();
        expect(await screen.findByText("Alice")).toBeInTheDocument();
        expect(await screen.findByText("bob@example.com")).toBeInTheDocument();
    });

    test("shows an empty state when no users have access", async () => {
        server.use(accessHandler({ data: [], meta: getPaginatedMeta(0) }));
        renderWithProviders(<UserAccessDialog target={{ agentId: "agent-empty" }} name="HR Agent" onOpenChange={() => {}} />);

        expect(await screen.findByText(/No known user has access/)).toBeInTheDocument();
    });

    test("shows an error state when the request fails", async () => {
        server.use(accessHandler({ message: "boom" }, 500));
        renderWithProviders(<UserAccessDialog target={{ agentId: "agent-error" }} name="HR Agent" onOpenChange={() => {}} />);

        expect(await screen.findByText(/Failed to load user access/)).toBeInTheDocument();
    });

    test("stays closed and issues no request when target is null", () => {
        renderWithProviders(<UserAccessDialog target={null} name="HR Agent" onOpenChange={() => {}} />);

        expect(screen.queryByText("User Access")).not.toBeInTheDocument();
    });
});
