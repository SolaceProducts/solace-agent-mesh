// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, fn, screen, userEvent, waitFor, within } from "storybook/test";

import { mockCustomToolsets } from "@/testing/data";
import { toolsetToV2 } from "@/testing/data/platformWireV2";
import { withDefaults } from "@/testing/helpers";

import { ToolsetEditPage } from "./ToolsetEditPage";

const existingToolset = mockCustomToolsets[0];

const meta = {
    title: "Toolsets/ToolsetEditPage",
    component: ToolsetEditPage,
    parameters: {
        docs: {
            description: {
                component: "Full-page create/edit form for a custom toolset. Renders in create mode when `isNew: true`, edit mode otherwise.",
            },
        },
    },
} satisfies Meta<typeof ToolsetEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CreateNew: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const headings = await canvas.findAllByText("Create Toolset");
        expect(headings.length).toBeGreaterThan(0);

        expect(canvas.getByTestId("toolsetSaveButton")).toHaveTextContent("Create");
        expect(canvas.getByTestId("toolsetCancelButton")).toHaveTextContent("Discard Changes");

        const nameInput = canvas.getByTestId("toolset-name");
        expect(nameInput).toHaveValue("");

        const descInput = canvas.getByTestId("toolset-description");
        expect(descInput).toHaveValue("");
    },
};

export const EditExisting: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: existingToolset,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: false, toolsetId: existingToolset.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const headings = await canvas.findAllByText(`Edit ${existingToolset.name}`);
        expect(headings.length).toBeGreaterThan(0);

        const nameInput = await canvas.findByTestId("toolset-name");
        expect(nameInput).toHaveValue(existingToolset.name);

        expect(canvas.getByTestId("toolsetSaveButton")).toHaveTextContent("Save");
        expect(canvas.getByTestId("toolsetCancelButton")).toHaveTextContent("Cancel");

        const descInput = canvas.getByTestId("toolset-description");
        expect(descInput).toHaveValue(existingToolset.description);
    },
};

export const ValidationErrors: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("toolsetSaveButton");

        await userEvent.click(canvas.getByTestId("toolsetSaveButton"));

        await canvas.findByText("Name must be at least 3 characters");
        await canvas.findByText("Description must be at least 10 characters");
    },
};

export const CancelNavigation: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const navigate = parameters.chatContext.navigate as ReturnType<typeof fn>;

        await canvas.findByTestId("toolsetCancelButton");
        await userEvent.click(canvas.getByTestId("toolsetCancelButton"));

        expect(navigate).toHaveBeenCalledWith("/toolsets");
    },
};

const createdToolsetId = "new-toolset-abc";

export const PostOnCreate: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: toolsetToV2({ ...existingToolset, id: createdToolsetId, name: "My_New_Toolset" }) })),
                http.put(`*/api/v2/platform/toolsets/${createdToolsetId}/bundle`, () => HttpResponse.json({ data: toolsetToV2({ ...existingToolset, id: createdToolsetId, name: "My_New_Toolset" }) }))
            ),
        },
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const navigate = parameters.chatContext.navigate as ReturnType<typeof fn>;
        const setNewlyCreatedToolsetId = parameters.chatContext.setNewlyCreatedToolsetId as ReturnType<typeof fn>;

        await canvas.findByTestId("toolset-name");

        await userEvent.type(canvas.getByTestId("toolset-name"), "My_New_Toolset");

        await userEvent.type(canvas.getByTestId("toolset-description"), "A toolset created via Storybook test.");

        const fileInput = canvasElement.querySelector('input[type="file"]') as HTMLInputElement;
        const zipFile = new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x14, 0x00, 0x00, 0x00])], "my-toolset.zip", { type: "application/zip" });
        await userEvent.upload(fileInput, zipFile);

        await userEvent.click(canvas.getByTestId("toolsetSaveButton"));

        await waitFor(() => expect(navigate).toHaveBeenCalledWith("/toolsets"), { timeout: 3000 });

        expect(setNewlyCreatedToolsetId).toHaveBeenCalledWith(createdToolsetId);
    },
};

export const PatchOnEdit: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.patch(`*/api/v2/platform/toolsets/${existingToolset.id}`, () => HttpResponse.json({ data: toolsetToV2(existingToolset) }))),
        },
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: existingToolset,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: false, toolsetId: existingToolset.id }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const navigate = parameters.chatContext.navigate as ReturnType<typeof fn>;
        const addNotification = parameters.chatContext.addNotification as ReturnType<typeof fn>;

        await canvas.findByTestId("toolset-name");

        const nameInput = canvas.getByTestId("toolset-name") as HTMLInputElement;
        fireEvent.change(nameInput, { target: { value: "Customer_Data_Tools_Updated" } });

        await userEvent.click(canvas.getByTestId("toolsetSaveButton"), { pointerEventsCheck: 0 });

        await waitFor(() => expect(navigate).toHaveBeenCalledWith(`/toolsets/${existingToolset.id}`), { timeout: 3000 });

        expect(addNotification).toHaveBeenCalledWith("Toolset updated successfully", "success");
    },
};

export const PostOnCreateError: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.post("*/api/v2/platform/toolsets", () => HttpResponse.json({ message: "Name already in use", errorId: "stub" }, { status: 422 }))),
        },
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            displayError: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const navigate = parameters.chatContext.navigate as ReturnType<typeof fn>;

        await canvas.findByTestId("toolset-name");
        await userEvent.type(canvas.getByTestId("toolset-name"), "Duplicate_Toolset");
        await userEvent.type(canvas.getByTestId("toolset-description"), "This name is already taken.");

        const fileInput = canvasElement.querySelector('input[type="file"]') as HTMLInputElement;
        await userEvent.upload(fileInput, new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04])], "toolset.zip", { type: "application/zip" }));

        await userEvent.click(canvas.getByTestId("toolsetSaveButton"));

        await waitFor(
            () => {
                expect(navigate).not.toHaveBeenCalled();
            },
            { timeout: 3000 }
        );
    },
};

export const PutOnEditError: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.patch(`*/api/v2/platform/toolsets/${existingToolset.id}`, () => HttpResponse.json({ message: "Validation failed", errorId: "stub" }, { status: 422 }))),
        },
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: existingToolset,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            displayError: fn(),
            useLoaderData: () => ({ isNew: false, toolsetId: existingToolset.id }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const navigate = parameters.chatContext.navigate as ReturnType<typeof fn>;

        await canvas.findByTestId("toolset-name");

        const nameInput = canvas.getByTestId("toolset-name") as HTMLInputElement;
        fireEvent.change(nameInput, { target: { value: "Bad_Update" } });

        await userEvent.click(canvas.getByTestId("toolsetSaveButton"), { pointerEventsCheck: 0 });

        await waitFor(
            () => {
                expect(screen.getByText("Toolset Update Failed")).toBeInTheDocument();
            },
            { timeout: 3000 }
        );

        expect(navigate).not.toHaveBeenCalled();
    },
};

export const ConfigCogOnToolCard: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: existingToolset,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: false, toolsetId: existingToolset.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Query Customers");

        const configCog = canvas.getByRole("button", { name: /Configure/i });
        expect(configCog).toBeInTheDocument();

        fireEvent.click(configCog);
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Tool Configuration");
        expect(within(dialog).getByText(/inherited by every agent/i)).toBeInTheDocument();

        expect(within(dialog).getByText("api_key")).toBeInTheDocument();
        expect(within(dialog).getByText("endpoint_url")).toBeInTheDocument();
    },
};

export const FileUploadRendered: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetToEdit: null,
            setCustomToolsetToEdit: fn(),
            setNewlyCreatedToolsetId: fn(),
            customToolsetsRefetch: async () => {},
            navigate: fn(),
            addNotification: fn(),
            useLoaderData: () => ({ isNew: true, toolsetId: undefined }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Create Toolset");

        expect(canvas.getByText("Tools")).toBeInTheDocument();

        expect(canvas.getByRole("button", { name: /Upload File/i })).toBeInTheDocument();

        const fileInput = canvasElement.querySelector('input[type="file"][accept=".zip"]') as HTMLInputElement;
        expect(fileInput).not.toBeNull();
    },
};
