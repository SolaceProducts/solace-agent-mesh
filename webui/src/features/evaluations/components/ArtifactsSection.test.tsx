// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { EvalArtifact } from "@/types/eval";

import { ArtifactsSection } from "@/features/evaluations/components/ArtifactsSection";

const displayError = vi.fn();

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return { ...actual, useChatContext: () => ({ displayError }) };
});

const artifact = (overrides: Partial<EvalArtifact> = {}): EvalArtifact => ({
    filename: "report.md",
    mimeType: "text/markdown",
    version: 0,
    size: 42,
    ...overrides,
});

describe("ArtifactsSection", () => {
    test("downloads through the injected loader, not a hard-coded endpoint", async () => {
        const fetchBlob = vi.fn().mockResolvedValue(new Blob(["x"]));
        render(<ArtifactsSection artifacts={[artifact()]} fetchBlob={fetchBlob} />);

        await userEvent.click(screen.getByRole("button", { name: "Download" }));

        await waitFor(() => expect(fetchBlob).toHaveBeenCalledTimes(1));
        expect((fetchBlob.mock.calls[0][0] as EvalArtifact).filename).toBe("report.md");
    });

    test("reports a failed download with the loader's message", async () => {
        displayError.mockClear();
        const fetchBlob = vi.fn().mockRejectedValue(new Error("This input file is no longer available."));
        render(<ArtifactsSection artifacts={[artifact()]} fetchBlob={fetchBlob} />);

        await userEvent.click(screen.getByRole("button", { name: "Download" }));

        await waitFor(() => expect(displayError).toHaveBeenCalledWith({ title: "Download failed", error: "This input file is no longer available.", errorSource: "api" }));
    });

    test("renders a purged artifact as failed with no download", () => {
        render(<ArtifactsSection artifacts={[artifact({ snapshotFailed: true, snapshotFailureReason: "artifact bytes were purged" })]} fetchBlob={vi.fn()} />);

        expect(screen.getByText("artifact bytes were purged")).toBeTruthy();
        expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
    });

    test("renders nothing for an empty list", () => {
        const { container } = render(<ArtifactsSection artifacts={[]} fetchBlob={vi.fn()} />);

        expect(container.firstChild).toBeNull();
    });
});
