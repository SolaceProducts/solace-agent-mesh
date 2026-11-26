// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SearchInput } from "./search-input";

describe("SearchInput clear button", () => {
    it("is absent while the input is empty", () => {
        render(<SearchInput value="" onChange={vi.fn()} />);

        expect(screen.queryByRole("button", { name: "Clear search" })).not.toBeInTheDocument();
    });

    it("clears the value when clicked", async () => {
        const onChange = vi.fn();
        render(<SearchInput value="abc" onChange={onChange} />);

        await userEvent.click(screen.getByRole("button", { name: "Clear search" }));

        expect(onChange).toHaveBeenCalledWith("");
        expect(screen.getByRole("textbox")).toHaveFocus();
    });
});
