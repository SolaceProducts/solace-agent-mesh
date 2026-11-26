// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LinkifiedText } from "./LinkifiedText";

describe("LinkifiedText", () => {
    it("renders text with no URL unchanged", () => {
        render(<LinkifiedText text="Status: completed" />);

        expect(screen.getByText("Status: completed")).toBeInTheDocument();
        expect(screen.queryByRole("link")).not.toBeInTheDocument();
    });

    it("linkifies a bare URL and hardens the anchor", () => {
        render(<LinkifiedText text="https://sam.example.test/#/share/mX5ubvZQh" />);

        const link = screen.getByRole("link", { name: "https://sam.example.test/#/share/mX5ubvZQh" });
        expect(link).toHaveAttribute("href", "https://sam.example.test/#/share/mX5ubvZQh");
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    it("keeps the surrounding prose around a URL mid-sentence", () => {
        const { container } = render(<LinkifiedText text="Open http://localhost:8000/#/share/abc to review it" />);

        expect(screen.getByRole("link")).toHaveAttribute("href", "http://localhost:8000/#/share/abc");
        expect(container.textContent).toBe("Open http://localhost:8000/#/share/abc to review it");
    });

    it("linkifies every URL in the text", () => {
        render(<LinkifiedText text="first https://a.example/one then https://b.example/two" />);

        const hrefs = screen.getAllByRole("link").map(a => a.getAttribute("href"));
        expect(hrefs).toEqual(["https://a.example/one", "https://b.example/two"]);
    });

    it("leaves sentence punctuation out of the href but keeps it in the text", () => {
        const { container } = render(<LinkifiedText text="See https://a.example/page." />);

        expect(screen.getByRole("link")).toHaveAttribute("href", "https://a.example/page");
        expect(container.textContent).toBe("See https://a.example/page.");
    });

    it("keeps a closing bracket that the URL itself opened", () => {
        render(<LinkifiedText text="https://a.example/wiki/Foo_(bar)" />);

        expect(screen.getByRole("link")).toHaveAttribute("href", "https://a.example/wiki/Foo_(bar)");
    });

    it("drops a closing bracket the URL never opened", () => {
        render(<LinkifiedText text="(see https://a.example/page)" />);

        expect(screen.getByRole("link")).toHaveAttribute("href", "https://a.example/page");
    });

    it("never turns a producer-authored javascript: URL into a link", () => {
        render(<LinkifiedText text="javascript:alert(1)" />);

        expect(screen.queryByRole("link")).not.toBeInTheDocument();
        expect(screen.getByText("javascript:alert(1)")).toBeInTheDocument();
    });

    it("preserves line breaks so a two-line body renders on two lines", () => {
        const { container } = render(<LinkifiedText text={'"Stonehenge Overview and History"\nhttps://a.example/share/x'} />);

        expect(container.firstElementChild).toHaveClass("whitespace-pre-line");
        expect(container.textContent).toBe('"Stonehenge Overview and History"\nhttps://a.example/share/x');
        expect(screen.getByRole("link")).toHaveAttribute("href", "https://a.example/share/x");
    });
});
