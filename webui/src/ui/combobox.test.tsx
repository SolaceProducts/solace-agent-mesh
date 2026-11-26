// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { ComboBox } from "./combobox";
import meta, { Basic } from "./combobox.stories";

const BasicComboBox = composeStory(Basic, meta);

function LazyComboBox({ onOpen, items = [] }: Readonly<{ onOpen: () => void; items?: Array<{ id: string; label: string }> }>) {
    const [value, setValue] = useState("");
    return <ComboBox value={value} onValueChange={setValue} items={items} placeholder="Type or select a model..." onOpen={onOpen} allowCustomValue />;
}

describe("ComboBox", () => {
    test("reopens the dropdown when the still-focused input is clicked again", async () => {
        render(<BasicComboBox />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Select an option...");

        await user.click(input);
        await user.click(await screen.findByText("First Option"));

        await waitFor(() => expect(screen.queryByText("Second Option")).not.toBeInTheDocument());
        expect(input).toHaveValue("First Option");
        expect(input).toHaveFocus();

        await user.click(input);

        expect(await screen.findByText("Second Option")).toBeInTheDocument();
        expect(await screen.findByText("Third Option")).toBeInTheDocument();
    });
});

describe("ComboBox - recovery after an open that yielded no items", () => {
    test("a second click re-fires onOpen", async () => {
        const onOpen = vi.fn();
        render(<LazyComboBox onOpen={onOpen} />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);
        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));

        await user.click(input);

        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(2));
    });

    test("typing re-fires onOpen", async () => {
        const onOpen = vi.fn();
        render(<LazyComboBox onOpen={onOpen} />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);
        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));

        await user.type(input, "gpt");

        await waitFor(() => expect(onOpen.mock.calls.length).toBeGreaterThan(1));
    });

    test("ArrowDown re-fires onOpen", async () => {
        const onOpen = vi.fn();
        render(<LazyComboBox onOpen={onOpen} />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);
        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));

        await user.keyboard("{ArrowDown}");

        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(2));
    });

    test("tells the user the list came back empty instead of showing nothing", async () => {
        render(<LazyComboBox onOpen={vi.fn()} />);
        const user = userEvent.setup();

        await user.click(await screen.findByPlaceholderText("Type or select a model..."));

        expect(await screen.findByText("No items found")).toBeInTheDocument();
    });

    test("renders the items that arrive on the retry", async () => {
        const onOpen = vi.fn();
        function RetryingComboBox() {
            const [items, setItems] = useState<Array<{ id: string; label: string }>>([]);
            return (
                <LazyComboBox
                    items={items}
                    onOpen={() => {
                        onOpen();
                        if (onOpen.mock.calls.length > 1) setItems([{ id: "gpt-4o-mini", label: "gpt-4o-mini" }]);
                    }}
                />
            );
        }
        render(<RetryingComboBox />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);
        await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
        expect(screen.queryByText("gpt-4o-mini")).not.toBeInTheDocument();

        await user.click(input);

        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
    });

    test("does not re-fire onOpen while the dropdown is already showing items", async () => {
        const onOpen = vi.fn();
        render(<LazyComboBox onOpen={onOpen} items={[{ id: "gpt-4o-mini", label: "gpt-4o-mini" }]} />);
        const user = userEvent.setup();

        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);
        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
        expect(onOpen).toHaveBeenCalledTimes(1);

        await user.click(input);

        expect(onOpen).toHaveBeenCalledTimes(1);
    });
});

describe("ComboBox - stays usable while its list loads", () => {
    function LoadingComboBox({ fetched }: { fetched: Promise<void> }) {
        const [items, setItems] = useState<Array<{ id: string; label: string }>>([]);
        const [isLoading, setIsLoading] = useState(false);
        const [value, setValue] = useState("");
        return (
            <ComboBox
                value={value}
                onValueChange={setValue}
                items={items}
                isLoading={isLoading}
                placeholder="Type or select a model..."
                onOpen={() => {
                    setIsLoading(true);
                    void fetched.then(() => {
                        setItems([{ id: "claude-sonnet-4-6", label: "claude-sonnet-4-6" }]);
                        setIsLoading(false);
                    });
                }}
                allowCustomValue
            />
        );
    }

    test("keeps the input enabled and focused while the list is loading", async () => {
        let finishFetch!: () => void;
        const fetched = new Promise<void>(resolve => {
            finishFetch = resolve;
        });
        render(<LoadingComboBox fetched={fetched} />);
        const user = userEvent.setup();
        const input = await screen.findByPlaceholderText("Type or select a model...");

        await user.click(input);

        expect(screen.getByText("Loading...")).toBeInTheDocument();
        expect(input).toBeEnabled();
        expect(input).toHaveFocus();

        finishFetch();
        expect(await screen.findByText("claude-sonnet-4-6")).toBeInTheDocument();
    });
});

describe("ComboBox - a caller that searches for itself", () => {
    const items = [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta" },
    ];

    test("shows the items as given, reports the typed text after the open, and clears it on a pick", async () => {
        const calls: string[] = [];
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Person" items={items} value="" onValueChange={onValueChange} searchText="" onSearchTextChange={text => calls.push(`search:${text}`)} onOpen={() => calls.push("open")} />);
        const input = screen.getByRole("combobox", { name: "Person" });

        await userEvent.type(input, "z");
        expect(calls).toEqual(["open", "search:z"]);
        expect(screen.getByText("Alpha")).toBeInTheDocument();

        await userEvent.click(screen.getByText("Beta"));
        expect(onValueChange).toHaveBeenCalledWith("b");
        expect(calls.at(-1)).toBe("search:");
    });
});

describe("ComboBox - disabled items", () => {
    const items = [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta", disabled: true },
        { id: "c", label: "Gamma" },
    ];

    test("are shown but cannot be picked by click or Enter, and the arrows skip them", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Letter" items={items} value="" onValueChange={onValueChange} />);
        const input = screen.getByRole("combobox", { name: "Letter" });
        await userEvent.click(input);

        expect(screen.getByText("Beta").closest("button")).toHaveAttribute("aria-disabled", "true");
        await userEvent.click(screen.getByText("Beta"));
        expect(onValueChange).not.toHaveBeenCalled();
        expect(input).toHaveFocus();

        await userEvent.keyboard("{ArrowDown}{ArrowDown}{Enter}");
        expect(onValueChange).toHaveBeenLastCalledWith("c");

        await userEvent.click(input);
        await userEvent.keyboard("{ArrowUp}{Enter}");
        expect(onValueChange).toHaveBeenLastCalledWith("a");
    });

    test("Enter on a disabled item the typed text highlighted does nothing", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Letter" items={items} value="" onValueChange={onValueChange} />);
        await userEvent.type(screen.getByRole("combobox", { name: "Letter" }), "Bet{Enter}");
        expect(onValueChange).not.toHaveBeenCalledWith("b");
    });
});

describe("ComboBox - whitespace in the typed text", () => {
    const items = [
        { id: "gpt-4o", label: "gpt-4o" },
        { id: "gpt-4o-mini", label: "gpt-4o-mini" },
    ];

    test("whitespace-only text is treated as no search: every item shows, no blank custom option, and the value clears", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Model" items={items} value="" onValueChange={onValueChange} allowCustomValue />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), "   ");

        expect(await screen.findByText("gpt-4o")).toBeInTheDocument();
        expect(screen.getByText("gpt-4o-mini")).toBeInTheDocument();
        expect(screen.getAllByRole("button")).toHaveLength(items.length);
        expect(onValueChange).toHaveBeenLastCalledWith("");
    });

    test("whitespace-only text over an empty list shows the empty message, not a blank custom option", async () => {
        render(<ComboBox aria-label="Model" items={[]} value="" onValueChange={vi.fn()} noItemsFoundText="No models found" allowCustomValue />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), " ");

        expect(await screen.findByText("No models found")).toBeInTheDocument();
        expect(screen.queryAllByRole("button")).toHaveLength(0);
    });

    test("leading and trailing spaces are ignored when matching", async () => {
        render(<ComboBox aria-label="Model" items={items} value="" onValueChange={vi.fn()} allowCustomValue />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), "  mini ");

        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
        expect(screen.queryByText("gpt-4o")).not.toBeInTheDocument();
    });

    test("a custom value is offered and reported without surrounding spaces", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Model" items={items} value="" onValueChange={onValueChange} allowCustomValue />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), " my-model ");

        expect(onValueChange).toHaveBeenLastCalledWith("my-model");
        await userEvent.click(await screen.findByText("my-model"));
        expect(onValueChange).toHaveBeenLastCalledWith("my-model");
    });

    test("without custom values, text matching nothing shows the default empty message", async () => {
        render(<ComboBox aria-label="Model" items={items} value="" onValueChange={vi.fn()} />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), " claude ");

        expect(await screen.findByText("No items found")).toBeInTheDocument();
    });

    test("without custom values, replacing the text with a space keeps the selection", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Model" items={items} value="gpt-4o" onValueChange={onValueChange} />);
        const input = screen.getByRole("combobox", { name: "Model" });

        await userEvent.tripleClick(input);
        await userEvent.keyboard(" ");

        expect(onValueChange).not.toHaveBeenCalledWith("");
    });

    test("without custom values, typing padded text does not change the value", async () => {
        const onValueChange = vi.fn();
        render(<ComboBox aria-label="Model" items={items} value="" onValueChange={onValueChange} />);

        await userEvent.type(screen.getByRole("combobox", { name: "Model" }), " claude ");

        expect(onValueChange).not.toHaveBeenCalled();
    });
});
