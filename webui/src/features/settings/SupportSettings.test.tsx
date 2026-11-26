// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { SupportSettings } from "./SupportSettings";

const openExternalLink = vi.fn();
vi.mock("@/utils/openExternalLink", () => ({ openExternalLink: (url: string) => openExternalLink(url) }));

const openDiagnosticsFolder = vi.fn();
const saveDiagnosticsZip = vi.fn();
const addNotification = vi.fn();

type GlobalWithBindings = typeof globalThis & { go?: unknown };

beforeEach(() => {
    (globalThis as GlobalWithBindings).go = { desktop: { App: { OpenDiagnosticsFolder: openDiagnosticsFolder, SaveDiagnosticsZip: saveDiagnosticsZip } } };
});

afterEach(() => {
    delete (globalThis as GlobalWithBindings).go;
    vi.clearAllMocks();
});

const renderSupportSettings = () => renderWithProviders(<SupportSettings />, { chatContextValues: { addNotification } });

describe("SupportSettings", () => {
    it("hands the Solace support link to the OS browser instead of the WebView", () => {
        renderSupportSettings();

        const link = screen.getByRole("link", { name: /Contact Solace/ });
        expect(link).toHaveAttribute("href", "https://solace.com/support/");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");

        fireEvent.click(link);

        expect(openExternalLink).toHaveBeenCalledWith("https://solace.com/support/");
    });

    it("opens the diagnostics folder via the desktop binding", async () => {
        openDiagnosticsFolder.mockResolvedValue(undefined);
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Open Diagnostics Folder" }));

        await waitFor(() => expect(openDiagnosticsFolder).toHaveBeenCalledOnce());
        expect(addNotification).not.toHaveBeenCalled();
    });

    it("warns when the diagnostics folder cannot be opened", async () => {
        openDiagnosticsFolder.mockRejectedValue(new Error("no file manager"));
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Open Diagnostics Folder" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Could not open the diagnostics folder: no file manager", "warning"));
    });

    it("downloads a diagnostics zip in one click and reports where it went", async () => {
        saveDiagnosticsZip.mockResolvedValue("/Users/me/Downloads/sam-diagnostics-1.0.0-20260810-143022.zip");
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Download Diagnostics Zip" }));

        await waitFor(() => expect(saveDiagnosticsZip).toHaveBeenCalledOnce());
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Diagnostics zip saved to /Users/me/Downloads/sam-diagnostics-1.0.0-20260810-143022.zip", "success"));
    });

    it("stays quiet when the user cancels the save panel", async () => {
        saveDiagnosticsZip.mockResolvedValue("");
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Download Diagnostics Zip" }));

        await waitFor(() => expect(saveDiagnosticsZip).toHaveBeenCalledOnce());
        expect(addNotification).not.toHaveBeenCalled();
    });

    it("warns when writing the diagnostics zip fails", async () => {
        saveDiagnosticsZip.mockRejectedValue(new Error("disk full"));
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Download Diagnostics Zip" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Could not save the diagnostics zip: disk full", "warning"));
    });

    it("degrades to a no-op when the desktop bindings are absent", async () => {
        delete (globalThis as GlobalWithBindings).go;
        renderSupportSettings();

        fireEvent.click(screen.getByRole("button", { name: "Open Diagnostics Folder" }));
        fireEvent.click(screen.getByRole("button", { name: "Download Diagnostics Zip" }));

        await waitFor(() => expect(screen.getByRole("button", { name: "Download Diagnostics Zip" })).toBeEnabled());
        expect(addNotification).not.toHaveBeenCalled();
    });
});
