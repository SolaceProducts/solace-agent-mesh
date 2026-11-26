// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

const { mockNavigate } = vi.hoisted(() => ({ mockNavigate: vi.fn() }));
vi.mock("react-router-dom", async importOriginal => {
    const actual = await importOriginal<typeof import("react-router-dom")>();
    return { ...actual, useNavigate: () => mockNavigate };
});

import { SkillsView } from "./SkillsView";

const emptyEnvelope = () => {
    const body = JSON.stringify({ data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 0, nextPage: null, totalPages: 1 } } });
    return {
        ok: true,
        status: 200,
        text: () => Promise.resolve(body),
        json: () => Promise.resolve(JSON.parse(body)),
        headers: new Headers({ "content-type": "application/json" }),
    };
};

const dataTransferWith = (files: File[]) => ({ files, types: ["Files"], dropEffect: "" });

const findDropZone = async (container: HTMLElement): Promise<HTMLElement> => {
    await waitFor(() => expect(container.querySelector('[role="presentation"]')).not.toBeNull());
    return container.querySelector('[role="presentation"]') as HTMLElement;
};

describe("SkillsView drag-and-drop upload", () => {
    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });
    beforeEach(() => {
        vi.stubGlobal(
            "fetch",
            vi.fn().mockImplementation(() => Promise.resolve(emptyEnvelope()))
        );
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("navigates to the upload page carrying a dropped .zip file", async () => {
        const { container } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        const zone = await findDropZone(container);

        const file = new File(["x"], "my-skill.zip", { type: "application/zip" });
        fireEvent.drop(zone, { dataTransfer: dataTransferWith([file]) });

        expect(mockNavigate).toHaveBeenCalledWith("/skills/upload", { state: { droppedFile: file } });
    });

    it("warns and does not navigate when a non-.zip file is dropped", async () => {
        const addNotification = vi.fn();
        const { container } = renderWithProviders(<SkillsView />, { initialPath: "/skills", chatContextValues: { addNotification } });
        const zone = await findDropZone(container);

        const file = new File(["x"], "notes.pdf", { type: "application/pdf" });
        fireEvent.drop(zone, { dataTransfer: dataTransferWith([file]) });

        expect(mockNavigate).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledWith(expect.any(String), "warning");
    });
});
