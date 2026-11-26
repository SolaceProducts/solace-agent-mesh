// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { CopyButton } from "./CopyButton";

const copyMock = vi.fn(async (_text: string) => true);
vi.mock("@/utils/clipboard", () => ({
    copyToClipboard: (text: string) => copyMock(text),
}));

afterEach(() => {
    copyMock.mockClear();
});

describe("CopyButton", () => {
    test("copies the value and briefly confirms", async () => {
        render(<CopyButton value="agent:abc:invoke" label="Copy invoke scope" />);

        fireEvent.click(screen.getByRole("button", { name: "Copy invoke scope" }));

        expect(copyMock).toHaveBeenCalledWith("agent:abc:invoke");
        await waitFor(() => expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument());
    });

    test("does not submit an enclosing form", () => {
        const onSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
        render(
            <form onSubmit={onSubmit}>
                <CopyButton value="https://issuer.example" label="Copy Cluster Issuer URL" />
            </form>
        );

        fireEvent.click(screen.getByRole("button", { name: "Copy Cluster Issuer URL" }));

        expect(onSubmit).not.toHaveBeenCalled();
    });
});
