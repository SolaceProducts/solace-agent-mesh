// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { withoutIdentityService } from "@/testing/helpers/parameters";

import meta from "./ShareProjectDialog.stories";

const mockSharesResponse = {
    projectId: "project-1",
    ownerEmail: "owner@example.com",
    shares: [
        {
            id: "share-1",
            projectId: "project-1",
            userEmail: "viewer1@example.com",
            accessLevel: "resource_viewer",
            sharedByEmail: "owner@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        },
    ],
};

const server = setupServer(
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.get("*/api/v1/projects/*/shares", () => HttpResponse.json(mockSharesResponse))
);

const mockProject = {
    id: "project-1",
    name: "Test Project",
    userId: "user-1",
    createdAt: "2024-01-01T00:00:00Z",
    updatedAt: "2024-01-01T00:00:00Z",
};

const WithoutIdentityService = composeStory(
    {
        args: { isOpen: true, onClose: () => {}, project: mockProject },
        parameters: withoutIdentityService,
    },
    meta
);

const WithExistingShares = composeStory({ args: { isOpen: true, onClose: () => {}, project: mockProject } }, meta);

describe("ShareProjectDialog", () => {
    beforeAll(() => server.listen());
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    test("submits create shares API with correct payload", async () => {
        const user = userEvent.setup();
        let capturedRequestBody: unknown = null;

        server.use(
            http.post("*/api/v1/projects/*/shares", async ({ request }) => {
                capturedRequestBody = await request.json();
                return HttpResponse.json(
                    {
                        projectId: "project-1",
                        created: [
                            {
                                id: "new-share",
                                projectId: "project-1",
                                userEmail: "test@example.com",
                                accessLevel: "resource_viewer",
                                sharedByEmail: "owner@example.com",
                                createdAt: "2024-01-01T00:00:00Z",
                                updatedAt: "2024-01-01T00:00:00Z",
                            },
                        ],
                        updated: [],
                        totalProcessed: 1,
                    },
                    { status: 201 }
                );
            })
        );

        render(<WithoutIdentityService />);
        expect(await screen.findByRole("dialog")).toBeInTheDocument();

        await user.click(await screen.findByRole("button", { name: /^add$/i }));
        await user.type(await screen.findByPlaceholderText(/enter email/i), "test@example.com{Enter}");
        await user.click(await screen.findByRole("button", { name: /^save$/i }));

        await waitFor(() => {
            expect(capturedRequestBody).not.toBeNull();
        });
        expect(capturedRequestBody).toMatchObject({
            shares: [{ userEmail: "test@example.com", accessLevel: "resource_viewer" }],
        });
    });

    test("submits delete shares API when removing existing viewer", async () => {
        const user = userEvent.setup();
        let capturedDeleteBody: unknown = null;

        server.use(
            http.delete("*/api/v1/projects/*/shares", async ({ request }) => {
                capturedDeleteBody = await request.json();
                return HttpResponse.json({
                    projectId: "project-1",
                    deletedCount: 1,
                    deletedEmails: ["viewer1@example.com"],
                });
            })
        );

        render(<WithExistingShares />);

        expect(await screen.findByRole("dialog")).toBeInTheDocument();
        expect(await screen.findByText("viewer1@example.com")).toBeInTheDocument();

        const viewerRow = screen.getByText("viewer1@example.com").closest("[data-testid='share-row']") as HTMLElement;
        const removeButton = viewerRow.querySelector("[data-testid='remove-share']") as HTMLButtonElement;
        await user.click(removeButton);
        await user.click(await screen.findByRole("button", { name: /^save$/i }));

        await waitFor(() => {
            expect(capturedDeleteBody).not.toBeNull();
        });
        expect(capturedDeleteBody).toMatchObject({
            userEmails: ["viewer1@example.com"],
        });
    });
});
