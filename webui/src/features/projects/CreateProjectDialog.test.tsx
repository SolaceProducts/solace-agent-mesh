// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import meta, { Default } from "./CreateProjectDialog.stories";

const DUPLICATE_ERROR = "A project with this name already exists";

const typeNameAndSubmit = async (user: ReturnType<typeof userEvent.setup>, name: string) => {
    await user.type(await screen.findByPlaceholderText("Project name"), name);
    await user.click(await screen.findByRole("button", { name: "Create Project" }));
};

describe("CreateProjectDialog duplicate-name handling", () => {
    test("blocks submit and shows an error for a name that matches an existing one (case-insensitive, trimmed)", async () => {
        const user = userEvent.setup();
        const onSubmit = vi.fn().mockResolvedValue(undefined);
        const Story = composeStory(Default, meta);
        render(<Story onSubmit={onSubmit} existingNames={["Existing Project"]} />);

        await typeNameAndSubmit(user, "  existing project  ");

        expect(await screen.findByText(DUPLICATE_ERROR)).toBeInTheDocument();
        expect(onSubmit).not.toHaveBeenCalled();
    });

    test("maps a 409 rejection from onSubmit to the name error instead of the generic banner", async () => {
        const user = userEvent.setup();
        const onSubmit = vi.fn().mockRejectedValue({ status: 409 });
        const Story = composeStory(Default, meta);
        render(<Story onSubmit={onSubmit} />);

        await typeNameAndSubmit(user, "Brand New");

        expect(await screen.findByText(DUPLICATE_ERROR)).toBeInTheDocument();
        expect(onSubmit).toHaveBeenCalledOnce();
    });

    test("shows the generic error banner for a non-409 rejection", async () => {
        const user = userEvent.setup();
        const onSubmit = vi.fn().mockRejectedValue({ status: 500 });
        const Story = composeStory(Default, meta);
        render(<Story onSubmit={onSubmit} />);

        await typeNameAndSubmit(user, "Brand New");

        expect(await screen.findByText("Failed to create project")).toBeInTheDocument();
        expect(screen.queryByText(DUPLICATE_ERROR)).not.toBeInTheDocument();
    });

    test("submits the trimmed name for a unique project", async () => {
        const user = userEvent.setup();
        const onSubmit = vi.fn().mockResolvedValue(undefined);
        const Story = composeStory(Default, meta);
        render(<Story onSubmit={onSubmit} existingNames={["Existing Project"]} />);

        await typeNameAndSubmit(user, "  Unique Name  ");

        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "Unique Name", description: "" }));
        expect(screen.queryByText(DUPLICATE_ERROR)).not.toBeInTheDocument();
    });
});
