// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { FileDropZone } from "./FileDropZone";

const dataTransferWith = (files: File[]) => ({ files, types: ["Files"], dropEffect: "" });

const renderZone = (props: { onZipDropped: (f: File) => void; addNotification?: (message: string, type?: "success" | "info" | "warning") => void }) => {
    const { onZipDropped, addNotification = vi.fn() } = props;
    const utils = renderWithProviders(
        <FileDropZone overlayLabel="Drop package here" onZipDropped={onZipDropped}>
            <div data-testid="child">child content</div>
        </FileDropZone>,
        { chatContextValues: { addNotification } }
    );
    const zone = utils.getByTestId("child").parentElement as HTMLElement;
    return { ...utils, zone };
};

describe("FileDropZone", () => {
    it("invokes onZipDropped with the dropped .zip file (case-insensitive extension)", () => {
        const onZipDropped = vi.fn();
        const { zone } = renderZone({ onZipDropped });

        const file = new File(["x"], "toolset.ZIP", { type: "application/zip" });
        fireEvent.drop(zone, { dataTransfer: dataTransferWith([file]) });

        expect(onZipDropped).toHaveBeenCalledTimes(1);
        expect(onZipDropped.mock.calls[0][0]).toBe(file);
    });

    it("warns and ignores a non-.zip drop", () => {
        const onZipDropped = vi.fn();
        const addNotification = vi.fn();
        const { zone } = renderZone({ onZipDropped, addNotification });

        const file = new File(["x"], "notes.txt", { type: "text/plain" });
        fireEvent.drop(zone, { dataTransfer: dataTransferWith([file]) });

        expect(onZipDropped).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledWith(expect.any(String), "warning");
    });

    it("is a no-op when the drop carries no files", () => {
        const onZipDropped = vi.fn();
        const addNotification = vi.fn();
        const { zone } = renderZone({ onZipDropped, addNotification });

        fireEvent.drop(zone, { dataTransfer: dataTransferWith([]) });

        expect(onZipDropped).not.toHaveBeenCalled();
        expect(addNotification).not.toHaveBeenCalled();
    });

    it("shows the overlay label only while a file is dragged over the zone", () => {
        const { zone, getByText, queryByText } = renderZone({ onZipDropped: vi.fn() });

        expect(queryByText("Drop package here")).not.toBeInTheDocument();

        fireEvent.dragEnter(zone, { dataTransfer: dataTransferWith([]) });
        expect(getByText("Drop package here")).toBeInTheDocument();

        fireEvent.dragLeave(zone, { dataTransfer: dataTransferWith([]) });
        expect(queryByText("Drop package here")).not.toBeInTheDocument();
    });
});
