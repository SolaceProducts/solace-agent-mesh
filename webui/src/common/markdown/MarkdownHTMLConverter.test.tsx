// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { marked } from "marked";
import { afterEach, describe, expect, test, vi } from "vitest";

import { MarkdownHTMLConverter } from "./MarkdownHTMLConverter";

const renderMd = (md: string) => render(<MarkdownHTMLConverter>{md}</MarkdownHTMLConverter>);

describe("MarkdownHTMLConverter — GFM task lists", () => {
    test("keeps the native checkbox input and applies theming + non-interactive attrs (unchecked)", () => {
        const { container } = renderMd("- [ ] Open task");

        const input = container.querySelector("input[type=checkbox]");
        expect(input).not.toBeNull();
        expect(input!.hasAttribute("checked")).toBe(false);

        expect(input!.hasAttribute("disabled")).toBe(false);
        expect(input!.getAttribute("tabindex")).toBe("-1");
        expect(input!.getAttribute("aria-disabled")).toBe("true");

        const cls = input!.getAttribute("class") ?? "";
        expect(cls).toContain("size-3.5");
        expect(cls).toContain("shrink-0");
        expect(cls).toContain("accent-(--primary-wMain)");
        expect(cls).toContain("pointer-events-none");
    });

    test("keeps the native checkbox input with the checked attribute (checked)", () => {
        const { container } = renderMd("- [x] Done task");

        const input = container.querySelector("input[type=checkbox]");
        expect(input).not.toBeNull();
        expect(input!.hasAttribute("checked")).toBe(true);

        expect(input!.hasAttribute("disabled")).toBe(false);
        expect(input!.getAttribute("tabindex")).toBe("-1");
        expect(input!.getAttribute("aria-disabled")).toBe("true");

        const cls = input!.getAttribute("class") ?? "";
        expect(cls).toContain("accent-(--primary-wMain)");
    });

    test("drops the disc bullet on a <ul> whose <li>s start with a checkbox", () => {
        const { container } = renderMd("- [ ] one\n- [x] two");

        const ul = container.querySelector("ul");
        expect(ul).not.toBeNull();

        const ulStyle = ul!.getAttribute("style") ?? "";
        expect(ulStyle).toContain("list-style: none");
        expect(ulStyle).toContain("padding-left: 0.25rem");
    });

    test("renders task <li> as flex with baseline alignment and a small gap", () => {
        const { container } = renderMd("- [ ] hello world");

        const li = container.querySelector("li");
        expect(li).not.toBeNull();

        const liStyle = li!.getAttribute("style") ?? "";
        expect(liStyle).toContain("display: flex");
        expect(liStyle).toContain("align-items: baseline");
        expect(liStyle).toContain("gap: 0.5rem");
    });

    test("preserves the text content next to the checkbox", () => {
        const { getByText } = renderMd("- [ ] write tests\n- [x] ship feature");
        expect(getByText("write tests")).toBeInTheDocument();
        expect(getByText("ship feature")).toBeInTheDocument();
    });

    test("leaves regular bulleted lists untouched (no checkbox = keep disc bullet)", () => {
        const { container } = renderMd("- apple\n- banana");

        const ul = container.querySelector("ul");
        expect(ul).not.toBeNull();
        expect(ul!.getAttribute("style")).toBeNull();
        expect(container.querySelector("li")?.getAttribute("style")).toBeNull();
    });

    test("only restyles the task-list <ul>, not sibling regular lists", () => {
        const md = ["- regular item", "", "## Tasks", "", "- [ ] task item"].join("\n");
        const { container } = renderMd(md);

        const uls = container.querySelectorAll("ul");
        expect(uls.length).toBe(2);

        expect(uls[0].getAttribute("style")).toBeNull();
        expect(uls[1].getAttribute("style") ?? "").toContain("list-style: none");
    });

    test("handles mixed checked/unchecked items in a single list", () => {
        const { container } = renderMd("- [x] done\n- [ ] todo");

        const inputs = container.querySelectorAll("li input[type=checkbox]");
        expect(inputs.length).toBe(2);

        const [first, second] = Array.from(inputs);
        expect(first.hasAttribute("checked")).toBe(true);
        expect(second.hasAttribute("checked")).toBe(false);

        for (const el of [first, second]) {
            expect(el.hasAttribute("disabled")).toBe(false);
            const cls = el.getAttribute("class") ?? "";
            expect(cls).toContain("accent-(--primary-wMain)");
        }
    });
});

describe("MarkdownHTMLConverter — memoization", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("re-render with unchanged content does not re-parse (memoized)", () => {
        const parseSpy = vi.spyOn(marked, "parse");
        const { rerender, container } = render(<MarkdownHTMLConverter>{"# title"}</MarkdownHTMLConverter>);
        const callsAfterFirst = parseSpy.mock.calls.length;
        expect(callsAfterFirst).toBeGreaterThan(0);
        expect(container.querySelector("h1")?.textContent).toBe("title");

        rerender(<MarkdownHTMLConverter>{"# title"}</MarkdownHTMLConverter>);
        rerender(<MarkdownHTMLConverter>{"# title"}</MarkdownHTMLConverter>);
        expect(parseSpy.mock.calls.length).toBe(callsAfterFirst);

        rerender(<MarkdownHTMLConverter>{"# changed"}</MarkdownHTMLConverter>);
        expect(parseSpy.mock.calls.length).toBeGreaterThan(callsAfterFirst);
        expect(container.querySelector("h1")?.textContent).toBe("changed");
    });
});
