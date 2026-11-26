// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { captureRadixDialogDiagnostics } from "@/testing/helpers/radixDialogDiagnostics";
import type { ArtifactInfo } from "@/types";

import { ApplyChangesDialog } from "./ApplyChangesDialog";
import { BuilderArtifactPreviewModal } from "./BuilderArtifactPreviewModal";
import { ConnectorEditDialog } from "./ConnectorEditDialog";

let mockPreviewArtifact: ArtifactInfo | null = null;
vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({
        previewArtifact: mockPreviewArtifact,
        setPreviewArtifact: vi.fn(),
        addNotification: vi.fn(),
        getConnectorSchema: () => ({ id: "schema" }),
    }),
}));

vi.mock("@/features/chat/artifact/ArtifactPreviewContent", () => ({
    ArtifactPreviewContent: () => <div data-testid="artifact-preview-content" />,
}));

vi.mock("@/api/connectors", () => ({ useConnectors: () => ({ data: [] }) }));

vi.mock("@/features/connectors/ConnectorEdit", () => ({
    CONNECTOR_WIZARD_STEPS: [],
    ConnectorEdit: () => <div data-testid="connector-edit" />,
}));

vi.mock("@/features/connectors/HighlightFieldsContext", () => ({
    HighlightFieldsProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("@/features/connectors/hooks", () => ({ isConnectorWizard: () => false }));

const validConnectorJson = JSON.stringify({ name: "Orders DB", type: "sql", subtype: "postgres", values: {} });

describe("Builder dialogs expose an accessible name and description", () => {
    let diagnostics: ReturnType<typeof captureRadixDialogDiagnostics>;

    beforeEach(() => {
        diagnostics = captureRadixDialogDiagnostics();
    });

    afterEach(() => {
        diagnostics.restore();
        mockPreviewArtifact = null;
    });

    test("ApplyChangesDialog is named by its heading and described by its body", () => {
        render(<ApplyChangesDialog open onConfirm={vi.fn()} onCancel={vi.fn()} />);

        expect(screen.getByRole("dialog", { name: "Apply Changes", description: /switch to Build Mode/ })).toBeInTheDocument();
        expect(diagnostics.messages()).toEqual([]);
    });

    test("BuilderArtifactPreviewModal is named by the artifact filename and described by its type", () => {
        mockPreviewArtifact = { filename: "notes.txt", mimeType: "text/plain", size: 4, lastModified: "2026-01-01T00:00:00Z" };

        render(<BuilderArtifactPreviewModal />);

        expect(screen.getByRole("dialog", { name: "notes.txt", description: "text/plain" })).toBeInTheDocument();
        expect(diagnostics.messages()).toEqual([]);
    });

    test("BuilderArtifactPreviewModal renders no dialog and emits no diagnostics while closed", () => {
        render(<BuilderArtifactPreviewModal />);

        expect(screen.queryByRole("dialog")).toBeNull();
        expect(diagnostics.messages()).toEqual([]);
    });

    test("ConnectorEditDialog is named by the connector name and has a description", () => {
        render(<ConnectorEditDialog isOpen onClose={vi.fn()} connectorJson={validConnectorJson} onSave={vi.fn()} />);

        expect(screen.getByRole("dialog", { name: "Orders DB", description: "Edit the connector configuration" })).toBeInTheDocument();
        expect(diagnostics.messages()).toEqual([]);
    });

    test("ConnectorEditDialog stays named when the connector JSON cannot be parsed", () => {
        render(<ConnectorEditDialog isOpen onClose={vi.fn()} connectorJson="not json" onSave={vi.fn()} />);

        expect(screen.getByRole("dialog", { name: "Connector configuration", description: "Unable to parse connector configuration." })).toBeInTheDocument();
        expect(diagnostics.messages()).toEqual([]);
    });
});
