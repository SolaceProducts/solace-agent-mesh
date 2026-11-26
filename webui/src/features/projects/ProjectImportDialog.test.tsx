// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import JSZip from "jszip";
import { describe, expect, test, vi } from "vitest";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { ProjectImportDialog } from "./ProjectImportDialog";

async function createProjectZip(projectJson: object, artifacts: Array<{ name: string; content: string }> = []): Promise<File> {
    const zip = new JSZip();
    zip.file("project.json", JSON.stringify(projectJson));
    for (const { name, content } of artifacts) {
        zip.file(`artifacts/${name}`, content);
    }
    const blob = await zip.generateAsync({ type: "blob" });
    return new File([blob], "test-project.zip", { type: "application/zip" });
}

const BASE_PROJECT_JSON = {
    version: "1.0",
    project: { name: "Test Project", description: "A test project" },
    artifacts: [] as object[],
};

function renderDialog(onImport = vi.fn().mockResolvedValue(undefined), maxPerFileUploadSizeBytes?: number) {
    const onOpenChange = vi.fn();
    render(
        <MockConfigProvider mockValues={{ validationLimits: maxPerFileUploadSizeBytes !== undefined ? { maxPerFileUploadSizeBytes } : undefined }}>
            <ProjectImportDialog open={true} onOpenChange={onOpenChange} onImport={onImport} />
        </MockConfigProvider>
    );
    return { onOpenChange, onImport };
}

describe("ProjectImportDialog", () => {
    describe("oversized artifact warning", () => {
        test("shows warning when preview has oversized artifacts", async () => {
            renderDialog(undefined, 10);

            const zipFile = await createProjectZip({ ...BASE_PROJECT_JSON, artifacts: [{ filename: "big.txt", size: 100 }] }, [{ name: "big.txt", content: "x".repeat(100) }]);

            await userEvent.upload(screen.getByTestId("projectImportFileInput"), zipFile);

            await waitFor(() => expect(screen.getByText("Test Project")).toBeInTheDocument());
            expect(screen.getByText(/will be skipped during import/i)).toBeInTheDocument();
        });

        test("hides oversized artifact warning and shows only error banner when import fails", async () => {
            const onImport = vi.fn().mockRejectedValue(new Error("server rejected the import"));
            const { onOpenChange } = renderDialog(onImport, 10);

            const zipFile = await createProjectZip({ ...BASE_PROJECT_JSON, artifacts: [{ filename: "big.txt", size: 100 }] }, [{ name: "big.txt", content: "x".repeat(100) }]);

            await userEvent.upload(screen.getByTestId("projectImportFileInput"), zipFile);
            await waitFor(() => expect(screen.getByText(/will be skipped during import/i)).toBeInTheDocument());

            await userEvent.click(screen.getByTestId("dialogConfirmButton"));

            await waitFor(() => expect(screen.getByText(/server rejected the import/i)).toBeInTheDocument());
            expect(screen.queryByText(/will be skipped during import/i)).not.toBeInTheDocument();
            expect(onOpenChange).not.toHaveBeenCalled();
        });
    });

    describe("missing project name in project.json", () => {
        test("loads preview without crashing and keeps Import disabled until a name is entered", async () => {
            renderDialog();

            const zipFile = await createProjectZip({ version: "1.0", project: { description: "No name" }, artifacts: [] });

            await userEvent.upload(screen.getByTestId("projectImportFileInput"), zipFile);

            await waitFor(() => expect(screen.getByLabelText("Project Name")).toBeInTheDocument());
            expect(screen.getByTestId("dialogConfirmButton")).toBeDisabled();
        });

        test("imports with the entered name once the user fills in the empty name field", async () => {
            const onImport = vi.fn().mockResolvedValue(undefined);
            renderDialog(onImport);

            const zipFile = await createProjectZip({ version: "1.0", project: { description: "No name" }, artifacts: [] });
            await userEvent.upload(screen.getByTestId("projectImportFileInput"), zipFile);
            await waitFor(() => expect(screen.getByLabelText("Project Name")).toBeInTheDocument());

            await userEvent.type(screen.getByLabelText("Project Name"), "My Project");
            await waitFor(() => expect(screen.getByTestId("dialogConfirmButton")).not.toBeDisabled());

            await userEvent.click(screen.getByTestId("dialogConfirmButton"));

            await waitFor(() => expect(onImport).toHaveBeenCalledOnce());
            expect(onImport).toHaveBeenCalledWith(expect.any(File), expect.objectContaining({ customName: "My Project" }));
        });
    });
});
