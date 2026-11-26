// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { ExampleArtifactField } from "@/features/evaluations/components/ExampleArtifactField";

const file = (name = "config.yaml") => new File(["name: x"], name, { type: "application/yaml" });

function attach(label: string, f: File) {
    return userEvent.upload(screen.getByLabelText(label) as HTMLInputElement, f);
}

describe("ExampleArtifactField", () => {
    test("uploads the chosen file from the empty state", async () => {
        const onUpload = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField onUpload={onUpload} />);

        await attach("Attach input file", file());

        await waitFor(() => expect(onUpload).toHaveBeenCalledTimes(1));
        expect((onUpload.mock.calls[0][0] as File).name).toBe("config.yaml");
    });

    test("replaces in place rather than offering a second slot", async () => {
        const onUpload = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField artifact={{ filename: "old.yaml", version: 0 }} onUpload={onUpload} />);

        expect(screen.getByText("old.yaml")).toBeTruthy();
        await attach("Replace input file", file("new.yaml"));

        await waitFor(() => expect(onUpload).toHaveBeenCalledTimes(1));
        expect((onUpload.mock.calls[0][0] as File).name).toBe("new.yaml");
    });

    test("renders an upload failure inline", async () => {
        const onUpload = vi.fn().mockRejectedValue(new Error("file exceeds the 10000000 byte limit"));
        render(<ExampleArtifactField onUpload={onUpload} />);

        await attach("Attach input file", file());

        await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("file exceeds the 10000000 byte limit"));
    });

    test("offers download and removal when the slot is occupied", async () => {
        const onDownload = vi.fn().mockResolvedValue(undefined);
        const onRemove = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField artifact={{ filename: "spec.yaml", version: 0, size: 12 }} onUpload={vi.fn()} onDownload={onDownload} onRemove={onRemove} />);

        await userEvent.click(screen.getByText("spec.yaml"));
        await waitFor(() => expect(onDownload).toHaveBeenCalledTimes(1));

        await userEvent.click(screen.getByRole("button", { name: /remove/i }));
        await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(1));
    });

    test("shows a placeholder and no attach control without onUpload", () => {
        const { container } = render(<ExampleArtifactField />);

        expect(container.textContent).toBe("—");
        expect(container.querySelector("input[type=file]")).toBeNull();
    });

    test("rejects an oversized file without calling the server", async () => {
        const onUpload = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField onUpload={onUpload} maxBytes={8} />);

        await attach("Attach input file", new File(["considerably more than eight bytes"], "big.yaml", { type: "application/yaml" }));

        expect(onUpload).not.toHaveBeenCalled();
        expect(screen.getByRole("alert").textContent).toBeTruthy();
    });

    test("both pickers open the hidden file input", async () => {
        const onUpload = vi.fn().mockResolvedValue(undefined);
        const { rerender } = render(<ExampleArtifactField onUpload={onUpload} />);
        const clicked = vi.spyOn(HTMLInputElement.prototype, "click").mockImplementation(() => {});

        await userEvent.click(screen.getByRole("button", { name: /attach/i }));
        expect(clicked).toHaveBeenCalledTimes(1);

        rerender(<ExampleArtifactField artifact={{ filename: "old.yaml", version: 0 }} onUpload={onUpload} />);
        await userEvent.click(screen.getByRole("button", { name: "Replace" }));
        expect(clicked).toHaveBeenCalledTimes(2);

        clicked.mockRestore();
    });

    test("locks the badge actions while an operation is in flight", async () => {
        let release!: () => void;
        const onUpload = vi.fn().mockReturnValue(new Promise<void>(res => (release = res)));
        const onRemove = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField artifact={{ filename: "old.yaml", version: 0 }} onUpload={onUpload} onRemove={onRemove} />);

        await attach("Replace input file", file("new.yaml"));
        await waitFor(() => expect(onUpload).toHaveBeenCalledTimes(1));

        expect(screen.queryByRole("button", { name: /remove/i })).toBeNull();

        release();
        await waitFor(() => expect(screen.getByRole("button", { name: /remove/i })).toBeTruthy());
        expect(onRemove).not.toHaveBeenCalled();
    });

    test("read-only occupied state downloads but cannot replace or remove", async () => {
        const onDownload = vi.fn().mockResolvedValue(undefined);
        render(<ExampleArtifactField artifact={{ filename: "frozen.yaml", version: 0 }} onDownload={onDownload} />);

        expect(screen.queryByText("Replace")).toBeNull();
        expect(screen.queryByRole("button", { name: /remove/i })).toBeNull();

        await userEvent.click(screen.getByText("frozen.yaml"));
        await waitFor(() => expect(onDownload).toHaveBeenCalledTimes(1));
    });
});
