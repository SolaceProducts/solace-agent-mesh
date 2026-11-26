// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { ArtifactInfo } from "@/types";

import { ArtifactDeleteAllDialog } from "./ArtifactDeleteAllDialog";

const file = (filename: string, tags?: string[]): ArtifactInfo => ({ filename, mimeType: "text/plain", size: 1, lastModified: "2026-09-25T00:00:00Z", uri: `artifact://s/${filename}`, tags });
const projectFile = (filename: string): ArtifactInfo => ({ ...file(filename), source: "project" });

describe("ArtifactDeleteAllDialog", () => {
    it("warns when revealed working files are part of the deletion", () => {
        renderWithProviders(<ArtifactDeleteAllDialog artifacts={[file("deck.md"), file("build-manifest.yaml", ["__working"])]} />, { chatContextValues: { isBatchDeleteModalOpen: true } });

        expect(screen.getByText("All 2 files will be permanently deleted. This includes 1 working file the agent may still need.")).toBeInTheDocument();
    });

    it("says project files stay in their projects", () => {
        renderWithProviders(<ArtifactDeleteAllDialog artifacts={[projectFile("a.md"), projectFile("b.md")]} />, { chatContextValues: { isBatchDeleteModalOpen: true } });

        expect(screen.getByText("All 2 files will be removed from this chat session. These files will remain in their projects.")).toBeInTheDocument();
    });

    it("separates deleted files from project files removed from the chat", () => {
        renderWithProviders(<ArtifactDeleteAllDialog artifacts={[file("deck.md"), projectFile("a.md")]} />, { chatContextValues: { isBatchDeleteModalOpen: true } });

        expect(screen.getByText("1 file will be permanently deleted. 1 project file will be removed from this chat but will remain in the project.")).toBeInTheDocument();
    });

    it("doesn't count working project files, which are only removed from the chat", () => {
        const workingProjectFile = { ...projectFile("plan.yaml"), tags: ["__working"] };
        renderWithProviders(<ArtifactDeleteAllDialog artifacts={[file("deck.md"), workingProjectFile]} />, { chatContextValues: { isBatchDeleteModalOpen: true } });

        expect(screen.getByText("1 file will be permanently deleted. 1 project file will be removed from this chat but will remain in the project.")).toBeInTheDocument();
    });

    it("says nothing about working files when none are included", () => {
        renderWithProviders(<ArtifactDeleteAllDialog artifacts={[file("deck.md"), file("notes.md")]} />, { chatContextValues: { isBatchDeleteModalOpen: true } });

        expect(screen.getByText("All 2 files will be permanently deleted.")).toBeInTheDocument();
    });
});
