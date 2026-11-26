// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { A2UISurface } from "@/types";

import { A2UISurfaceRenderer } from "./A2UIRenderer";

const keepWhitespace = (text: string) => text;

function surfaceWith(text: { variant?: string; multiline?: boolean; tooltip?: string; literal: string }): A2UISurface {
    return {
        surfaceId: "test",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [
            {
                id: "txt",
                component: "Text",
                text: { literalString: text.literal },
                variant: text.variant,
                multiline: text.multiline,
                tooltip: text.tooltip,
            },
            { id: "main-col", component: "Column", children: { explicitList: ["txt"] } },
            { id: "root", component: "Card", child: "main-col" },
        ],
        dataModel: {},
    };
}

describe("A2UIRenderer / RenderText", () => {
    it("renders body variant in a span with whitespace-pre-wrap", () => {
        render(<A2UISurfaceRenderer surface={surfaceWith({ variant: "body", literal: "hello" })} onAction={() => {}} />);
        const el = screen.getByText("hello");
        expect(el.tagName).toBe("SPAN");
        expect(el.className).toContain("whitespace-pre-wrap");
    });

    it("renders h3 variant as a font-semibold span", () => {
        render(<A2UISurfaceRenderer surface={surfaceWith({ variant: "h3", literal: "Title" })} onAction={() => {}} />);
        const el = screen.getByText("Title");
        expect(el.tagName).toBe("SPAN");
        expect(el.className).toContain("font-semibold");
    });

    it("renders caption variant as a small span", () => {
        render(<A2UISurfaceRenderer surface={surfaceWith({ variant: "caption", literal: "label" })} onAction={() => {}} />);
        const el = screen.getByText("label");
        expect(el.tagName).toBe("SPAN");
        expect(el.className).toContain("text-xs");
    });

    it("surfaces tooltip via the native title attribute (no custom Tooltip primitive)", () => {
        render(<A2UISurfaceRenderer surface={surfaceWith({ variant: "caption", literal: "argName", tooltip: "schema description" })} onAction={() => {}} />);
        const el = screen.getByText("argName");
        expect(el.getAttribute("title")).toBe("schema description");
        expect(el.className).not.toContain("underline");
        expect(el.className).not.toContain("cursor-help");
    });

    it("renders multiline values in a bordered scrolling div with whitespace-pre-wrap", () => {
        const multilineText = "line one\nline two\nline three";
        const { container } = render(<A2UISurfaceRenderer surface={surfaceWith({ variant: "body", multiline: true, literal: multilineText })} onAction={() => {}} />);
        const el = screen.getByText(multilineText, { normalizer: keepWhitespace });
        expect(el.tagName).toBe("DIV");
        expect(el.className).toContain("whitespace-pre-wrap");
        expect(el.className).toContain("max-h-40");
        expect(el.className).toContain("overflow-y-auto");
        expect(el.className).toContain("font-mono");
        expect(el.className).toContain("border");
        expect(container.textContent).toContain("line one\nline two\nline three");
    });

    it("forwards tooltip onto multiline blocks as well", () => {
        render(
            <A2UISurfaceRenderer
                surface={surfaceWith({
                    variant: "body",
                    multiline: true,
                    literal: "x",
                    tooltip: "explain me",
                })}
                onAction={() => {}}
            />
        );
        expect(screen.getByText("x").getAttribute("title")).toBe("explain me");
    });
});

function pickerSurface(opts: { label: string; value: string; description?: string }[], initial?: string): A2UISurface {
    return {
        surfaceId: "test",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [
            {
                id: "q0-picker",
                component: "ChoicePicker",
                variant: "mutuallyExclusive",
                options: opts.map(o => ({
                    label: { literalString: o.label },
                    value: o.value,
                    description: o.description ? { literalString: o.description } : undefined,
                })),
                value: { path: "/answers/q0" },
            },
            { id: "main-col", component: "Column", children: { explicitList: ["q0-picker"] } },
            { id: "root", component: "Card", child: "main-col" },
        ],
        dataModel: initial !== undefined ? { answers: { q0: initial } } : {},
    };
}

describe("A2UIRenderer / RenderChoicePicker (single-select)", () => {
    it('renders each option as a native <input type="radio">', () => {
        render(
            <A2UISurfaceRenderer
                surface={pickerSurface([
                    { label: "Use existing", value: "existing" },
                    { label: "Create new", value: "new" },
                ])}
                onAction={() => {}}
            />
        );
        const radios = screen.getAllByRole("radio");
        expect(radios).toHaveLength(2);
        radios.forEach(r => {
            expect(r.tagName).toBe("INPUT");
            expect((r as HTMLInputElement).type).toBe("radio");
        });
    });

    it("groups all options under the same name so the browser enforces single-select", () => {
        render(
            <A2UISurfaceRenderer
                surface={pickerSurface([
                    { label: "A", value: "a" },
                    { label: "B", value: "b" },
                    { label: "C", value: "c" },
                ])}
                onAction={() => {}}
            />
        );
        const radios = screen.getAllByRole("radio") as HTMLInputElement[];
        const names = new Set(radios.map(r => r.name));
        expect(names.size).toBe(1);
    });

    it("marks the option matching the data-model value as checked", () => {
        render(
            <A2UISurfaceRenderer
                surface={pickerSurface(
                    [
                        { label: "A", value: "a" },
                        { label: "B", value: "b" },
                    ],
                    "b"
                )}
                onAction={() => {}}
            />
        );
        const radios = screen.getAllByRole("radio") as HTMLInputElement[];
        expect(radios[0].checked).toBe(false);
        expect(radios[1].checked).toBe(true);
    });

    it("renders the white center-dot overlay only on the checked row", () => {
        const { container } = render(
            <A2UISurfaceRenderer
                surface={pickerSurface(
                    [
                        { label: "A", value: "a" },
                        { label: "B", value: "b" },
                    ],
                    "a"
                )}
                onAction={() => {}}
            />
        );
        const dots = container.querySelectorAll("span.pointer-events-none.absolute");
        expect(dots).toHaveLength(1);
    });

    it("clicking a radio updates the data model so the new option becomes checked", () => {
        render(
            <A2UISurfaceRenderer
                surface={pickerSurface(
                    [
                        { label: "A", value: "a" },
                        { label: "B", value: "b" },
                    ],
                    "a"
                )}
                onAction={() => {}}
            />
        );
        const radios = screen.getAllByRole("radio") as HTMLInputElement[];
        expect(radios[0].checked).toBe(true);
        expect(radios[1].checked).toBe(false);

        fireEvent.click(radios[1]);

        expect(radios[0].checked).toBe(false);
        expect(radios[1].checked).toBe(true);
    });

    it("clicking the label row (not the input) also updates selection via htmlFor", () => {
        render(
            <A2UISurfaceRenderer
                surface={pickerSurface([
                    { label: "Create new", value: "new" },
                    { label: "Use existing", value: "existing" },
                ])}
                onAction={() => {}}
            />
        );
        const radios = screen.getAllByRole("radio") as HTMLInputElement[];
        expect(radios[1].checked).toBe(false);

        fireEvent.click(screen.getByText("Use existing"));

        expect(radios[1].checked).toBe(true);
    });

    it("submit action resolves the selected value out of the data model", () => {
        const onAction = vi.fn();
        const surface: A2UISurface = {
            surfaceId: "test",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                {
                    id: "q0-picker",
                    component: "ChoicePicker",
                    variant: "mutuallyExclusive",
                    options: [
                        { label: { literalString: "A" }, value: "a" },
                        { label: { literalString: "B" }, value: "b" },
                    ],
                    value: { path: "/answers/q0" },
                },
                {
                    id: "submit-btn",
                    component: "Button",
                    variant: "primary",
                    child: "submit-label",
                    action: { event: { name: "submit", context: { answer: { path: "/answers/q0" } } } },
                },
                { id: "submit-label", component: "Text", text: { literalString: "Submit" } },
                { id: "main-col", component: "Column", children: { explicitList: ["q0-picker", "submit-btn"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        render(<A2UISurfaceRenderer surface={surface} onAction={onAction} />);

        fireEvent.click(screen.getAllByRole("radio")[1]);
        fireEvent.click(screen.getByRole("button", { name: "Submit" }));

        expect(onAction).toHaveBeenCalledTimes(1);
        expect(onAction.mock.calls[0][0]).toMatchObject({
            eventName: "submit",
            context: { answer: "b" },
        });
    });
});

describe("A2UISurfaceRenderer / countdown timer", () => {
    const body = surfaceWith({ variant: "body", literal: "form body" });

    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date("2026-01-01T00:00:00.000Z"));
    });
    afterEach(() => vi.useRealTimers());

    const expiresIn = (ms: number) => new Date(Date.now() + ms).toISOString();

    it("stays hidden while the deadline is far off (no manufactured urgency)", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt={expiresIn(10 * 60_000)} />);
        expect(screen.queryByText(/\d+s/)).toBeNull();
    });

    it("shows the countdown once the deadline is within the final minute", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt={expiresIn(30_000)} />);
        expect(screen.getByText("30s")).toBeInTheDocument();
    });

    it("reveals the countdown as a far-off deadline approaches the threshold", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt={expiresIn(10 * 60_000)} />);
        expect(screen.queryByText(/\d+s/)).toBeNull();
        act(() => vi.advanceTimersByTime(9 * 60_000 + 30_000));
        expect(screen.getByText("30s")).toBeInTheDocument();
    });

    it("ticks down once per second", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt={expiresIn(30_000)} />);
        expect(screen.getByText("30s")).toBeInTheDocument();
        act(() => vi.advanceTimersByTime(1_000));
        expect(screen.getByText("29s")).toBeInTheDocument();
    });

    it("replaces the countdown with an expiry note once the deadline passes", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt={expiresIn(5_000)} />);
        expect(screen.getByText("5s")).toBeInTheDocument();
        act(() => vi.advanceTimersByTime(5_000));
        expect(screen.getByText(/This request expired/)).toBeInTheDocument();
        expect(screen.queryByText(/\d+s/)).toBeNull();
    });

    it("renders nothing for an invalid expiresAt", () => {
        render(<A2UISurfaceRenderer surface={body} onAction={() => {}} expiresAt="not-a-date" />);
        expect(screen.queryByText(/\d+s|Expired/)).toBeNull();
    });
});

describe("A2UISurfaceRenderer / expired-prompt disabling", () => {
    const buttonSurface: A2UISurface = {
        surfaceId: "test",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [
            { id: "submit-btn", component: "Button", variant: "primary", child: "submit-label", action: { event: { name: "submit", context: {} } } },
            { id: "submit-label", component: "Text", text: { literalString: "Submit" } },
            { id: "main-col", component: "Column", children: { explicitList: ["submit-btn"] } },
            { id: "root", component: "Card", child: "main-col" },
        ],
        dataModel: {},
    };

    const isoFromNow = (ms: number) => new Date(Date.now() + ms).toISOString();

    it("disables the Submit button and ignores clicks when already past expiry (cold revisit)", () => {
        const onAction = vi.fn();
        render(<A2UISurfaceRenderer surface={buttonSurface} onAction={onAction} expiresAt={isoFromNow(-1_000)} />);
        const btn = screen.getByRole("button", { name: "Submit" });
        expect(btn).toBeDisabled();
        fireEvent.click(btn);
        expect(onAction).not.toHaveBeenCalled();
        expect(screen.getByText(/This request expired/)).toBeInTheDocument();
    });

    it("keeps the Submit button live while the prompt has not expired", () => {
        const onAction = vi.fn();
        render(<A2UISurfaceRenderer surface={buttonSurface} onAction={onAction} expiresAt={isoFromNow(30_000)} />);
        const btn = screen.getByRole("button", { name: "Submit" });
        expect(btn).not.toBeDisabled();
        fireEvent.click(btn);
        expect(onAction).toHaveBeenCalledTimes(1);
    });

    it("disables once the deadline lapses while the prompt is open", () => {
        vi.useFakeTimers();
        try {
            const onAction = vi.fn();
            render(<A2UISurfaceRenderer surface={buttonSurface} onAction={onAction} expiresAt={new Date(Date.now() + 5_000).toISOString()} />);
            expect(screen.getByRole("button", { name: "Submit" })).not.toBeDisabled();
            act(() => vi.advanceTimersByTime(5_000));
            expect(screen.getByRole("button", { name: "Submit" })).toBeDisabled();
            fireEvent.click(screen.getByRole("button", { name: "Submit" }));
            expect(onAction).not.toHaveBeenCalled();
            expect(screen.getByText(/This request expired/)).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });
});

describe("A2UIRenderer / flush", () => {
    const catalogId = "https://a2ui.org/specification/v0_9/basic_catalog.json";
    const columnOf = (text: string) => screen.getByText(text).closest("div.flex-col");

    const nested: A2UISurface = {
        surfaceId: "flush",
        catalogId,
        components: [
            { id: "outside", component: "Text", text: { literalString: "outside the card" }, variant: "body" },
            { id: "inside", component: "Text", text: { literalString: "inside the card" }, variant: "body" },
            { id: "card-col", component: "Column", children: { explicitList: ["inside"] } },
            { id: "card", component: "Card", child: "card-col" },
            { id: "root", component: "Column", children: { explicitList: ["outside", "card"] } },
        ],
        dataModel: {},
    };

    it("pads columns by default", () => {
        render(<A2UISurfaceRenderer surface={nested} onAction={vi.fn()} />);
        expect(columnOf("outside the card")).toHaveClass("px-5", "py-3");
    });

    it("drops column padding outside a card and keeps it inside one", () => {
        render(<A2UISurfaceRenderer surface={nested} onAction={vi.fn()} flush />);
        expect(columnOf("outside the card")).not.toHaveClass("px-5");
        expect(columnOf("inside the card")).toHaveClass("px-5", "py-3");
    });

    it("draws the selection icon", () => {
        const surface: A2UISurface = {
            surfaceId: "icon",
            catalogId,
            components: [
                { id: "icon", component: "Icon", icon: "selection" },
                { id: "root", component: "Row", children: { explicitList: ["icon"] } },
            ],
            dataModel: {},
        };
        const { container } = render(<A2UISurfaceRenderer surface={surface} onAction={vi.fn()} />);
        expect(container.querySelector("svg")).toBeInTheDocument();
    });

    it("leaves out the per-tab Next button", () => {
        const surface: A2UISurface = {
            surfaceId: "tabs",
            catalogId,
            components: [
                { id: "a", component: "Text", text: { literalString: "first question" }, variant: "h3" },
                { id: "b", component: "Text", text: { literalString: "second question" }, variant: "h3" },
                {
                    id: "tabs",
                    component: "Tabs",
                    tabItems: [
                        { title: { literalString: "One" }, child: "a" },
                        { title: { literalString: "Two" }, child: "b" },
                    ],
                },
                { id: "root", component: "Column", children: { explicitList: ["tabs"] } },
            ],
            dataModel: {},
        };
        const { unmount } = render(<A2UISurfaceRenderer surface={surface} onAction={vi.fn()} disabled />);
        expect(screen.getByRole("button", { name: "Next" })).toBeInTheDocument();
        unmount();
        render(<A2UISurfaceRenderer surface={surface} onAction={vi.fn()} disabled flush />);
        expect(screen.queryByRole("button", { name: "Next" })).not.toBeInTheDocument();
    });
});
