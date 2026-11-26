// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AttachmentCardShell } from "./AttachmentCardShell";

const setMobile = (mobile: boolean) => {
    Object.defineProperty(window, "innerWidth", { value: mobile ? 375 : 1280, configurable: true });
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({
        matches: mobile && q.includes("max-width"),
        media: q,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    }));
};

const preview = <div data-testid="preview">preview</div>;

beforeEach(() => setMobile(false));
afterEach(() => vi.restoreAllMocks());

describe("AttachmentCardShell", () => {
    test("desktop: clickable card is a real button and fires onClick", async () => {
        const onClick = vi.fn();
        renderWithProviders(<AttachmentCardShell filename="report.pdf" preview={preview} onClick={onClick} />);
        const card = screen.getByTestId("preview").closest("button");
        expect(card).not.toBeNull();
        await userEvent.click(card!);
        expect(onClick).toHaveBeenCalledOnce();
    });

    test("remove button fires onRemove without triggering the card onClick", async () => {
        const onClick = vi.fn();
        const onRemove = vi.fn();
        renderWithProviders(<AttachmentCardShell filename="report.pdf" preview={preview} onClick={onClick} onRemove={onRemove} removeTooltip="Remove file" />);
        await userEvent.click(screen.getByRole("button", { name: /remove file/i }));
        expect(onRemove).toHaveBeenCalledOnce();
        expect(onClick).not.toHaveBeenCalled();
    });

    test("non-clickable card renders no button", () => {
        renderWithProviders(<AttachmentCardShell filename="report.pdf" preview={preview} />);
        expect(screen.getByTestId("preview").closest("button")).toBeNull();
        expect(screen.getByText("report.pdf")).toBeInTheDocument();
    });

    test("mobile: renders the compact thumb as a button and fires onClick", async () => {
        setMobile(true);
        const onClick = vi.fn();
        renderWithProviders(<AttachmentCardShell filename="image.png" preview={preview} onClick={onClick} />);
        const thumb = screen.getByTestId("preview").closest("button");
        expect(thumb).not.toBeNull();
        await userEvent.click(thumb!);
        expect(onClick).toHaveBeenCalledOnce();
    });
});
