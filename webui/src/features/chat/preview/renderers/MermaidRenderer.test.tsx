// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, waitFor } from "@testing-library/react";
import mermaid from "mermaid";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("@use-gesture/react", () => ({
    useGesture: () => () => ({}),
}));

import { MermaidRenderer } from "@/features/chat/preview/renderers/MermaidRenderer";

describe("MermaidRenderer", () => {
    let setRenderError: (error: string | null) => void;

    beforeEach(() => {
        setRenderError = vi.fn<(error: string | null) => void>();
        (SVGElement.prototype as unknown as { getBBox: () => DOMRect }).getBBox = () => ({ x: 0, y: 0, width: 100, height: 20 }) as DOMRect;
        vi.spyOn(console, "error").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
        delete (SVGElement.prototype as unknown as { getBBox?: () => DOMRect }).getBBox;
    });

    test("sets render error for invalid diagram syntax", async () => {
        render(<MermaidRenderer content="not a valid diagram at all" setRenderError={setRenderError} />);

        await waitFor(() => {
            expect(setRenderError).toHaveBeenCalledWith(expect.stringContaining("No diagram type detected"));
        });
    });

    describe("input validation (XSS prevention)", () => {
        const dangerousInputs = [
            { name: "script tags", content: 'graph TD; A["<script>alert(1)</script>"]-->B;' },
            { name: "javascript: protocol", content: 'graph TD; click A "javascript:alert(1)"' },
            { name: "event handlers", content: 'graph TD; A["<div onclick=alert(1)>"]-->B;' },
            { name: "iframe injection", content: 'graph TD; A["<iframe src=evil.com>"]-->B;' },
            { name: "object embed", content: 'graph TD; A["<object data=evil.swf>"]-->B;' },
            { name: "embed tag", content: 'graph TD; A["<embed src=evil>"]-->B;' },
            { name: "data URI with HTML", content: 'graph TD; click A "data:text/html,<script>alert(1)</script>"' },
        ];

        for (const { name, content } of dangerousInputs) {
            test(`rejects ${name}`, async () => {
                render(<MermaidRenderer content={content} setRenderError={setRenderError} />);

                await waitFor(() => {
                    expect(setRenderError).toHaveBeenCalledWith("Invalid diagram content: potentially unsafe patterns detected");
                });
            });
        }
    });

    test("clears error on successful re-render after failure", async () => {
        const { rerender } = render(<MermaidRenderer content="not valid" setRenderError={setRenderError} />);

        await waitFor(() => {
            expect(setRenderError).toHaveBeenCalledWith(expect.stringContaining("No diagram type detected"));
        });

        (setRenderError as ReturnType<typeof vi.fn>).mockClear();

        rerender(<MermaidRenderer content="graph TD; A-->B;" setRenderError={setRenderError} />);

        await waitFor(() => {
            expect(setRenderError).toHaveBeenCalledWith(null);
        });
    });

    describe("while streaming", () => {
        test("suppresses the transient render error for a truncated diagram", async () => {
            render(<MermaidRenderer content="graph TD; A--" setRenderError={setRenderError} isStreaming />);

            await waitFor(() => {
                expect(setRenderError).toHaveBeenCalledWith(null);
            });
            expect(setRenderError).not.toHaveBeenCalledWith(expect.stringContaining("Failed to render diagram"));
            expect(setRenderError).not.toHaveBeenCalledWith(expect.stringContaining("No diagram type detected"));
        });

        test("renders the diagram SVG once it settles", async () => {
            const mermaidRender = vi.spyOn(mermaid, "render");

            const { container, rerender } = render(<MermaidRenderer content="graph TD; A--" setRenderError={setRenderError} isStreaming />);

            await waitFor(() => {
                expect(setRenderError).toHaveBeenCalledWith(null);
            });
            expect(mermaidRender).not.toHaveBeenCalled();
            expect(container.querySelector('svg[id^="mermaid-"]')).toBeNull();

            (setRenderError as ReturnType<typeof vi.fn>).mockClear();

            rerender(<MermaidRenderer content="graph TD; A-->B;" setRenderError={setRenderError} isStreaming={false} />);

            await waitFor(() => {
                expect(mermaidRender).toHaveBeenCalledTimes(1);
            });
            await mermaidRender.mock.results[0].value;

            await waitFor(() => {
                expect(container.querySelector('svg[id^="mermaid-"]')).not.toBeNull();
            });

            expect(mermaidRender).toHaveBeenCalledTimes(1);
            expect(mermaidRender).toHaveBeenCalledWith(expect.any(String), "graph TD; A-->B;", expect.anything());

            expect(container.querySelector('[aria-hidden="true"] svg')).toBeNull();

            expect(setRenderError).not.toHaveBeenCalledWith(expect.any(String));
        });
    });
});
