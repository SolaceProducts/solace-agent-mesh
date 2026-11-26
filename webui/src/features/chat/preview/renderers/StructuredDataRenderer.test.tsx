// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from "vitest";

import { StructuredDataRenderer } from "@/features/chat/preview/renderers/StructuredDataRenderer";

const TRUNCATED_MID_SCALAR = `- type: heading
  level: 1
  text: "Solace Agent Mesh"
- type: paragraph
  text: "Unlike traditional AI deployments that operate in`;

const VALID_YAML = `- type: heading
  level: 1
  text: "Solace Agent Mesh"
`;

describe("StructuredDataRenderer", () => {
    let setRenderError: Mock<(error: string | null) => void>;

    beforeEach(() => {
        setRenderError = vi.fn<(error: string | null) => void>();
        vi.spyOn(console, "error").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    describe("while streaming", () => {
        it("reports no error for a prefix cut mid-scalar", () => {
            render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            expect(setRenderError).not.toHaveBeenCalledWith(expect.stringContaining("parsing failed"));
            expect(setRenderError).toHaveBeenCalledWith(null);
        });

        it("shows no parse-failure banner", () => {
            render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            expect(screen.queryByText(/parse failed/i)).not.toBeInTheDocument();
        });

        it("shows the content that has arrived so far", () => {
            render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            expect(screen.getByText(/Solace Agent Mesh/)).toBeInTheDocument();
        });

        it("hides the structured/raw toggle, since there is nothing parsed to toggle to", () => {
            render(<StructuredDataRenderer content={VALID_YAML} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            expect(screen.queryByTitle(/Show Structured View|Show Raw Text/)).not.toBeInTheDocument();
        });

        it("suppresses partial-JSON errors the same way", () => {
            render(<StructuredDataRenderer content={'{"blocks": [{"type": "hea'} rendererType="json" setRenderError={setRenderError} isStreaming />);

            expect(setRenderError).toHaveBeenCalledWith(null);
            expect(screen.queryByText(/parse failed/i)).not.toBeInTheDocument();
        });
    });

    describe("once settled", () => {
        it("reports the error for the same truncated content", () => {
            render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming={false} />);

            expect(setRenderError).toHaveBeenCalledWith(expect.stringContaining("YAML parsing failed"));
            expect(screen.getByText(/parse failed/i)).toBeInTheDocument();
        });

        it("still shows the raw content alongside the error", () => {
            render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming={false} />);

            expect(screen.getByText(/Unlike traditional AI deployments/)).toBeInTheDocument();
        });

        it("reports no error for valid YAML and offers the toggle", () => {
            render(<StructuredDataRenderer content={VALID_YAML} rendererType="yaml" setRenderError={setRenderError} isStreaming={false} />);

            expect(setRenderError).toHaveBeenCalledWith(null);
            expect(screen.queryByText(/parse failed/i)).not.toBeInTheDocument();
            expect(screen.getByTitle("Show Raw Text")).toBeInTheDocument();
        });

        it("renders valid JSON as a structured tree and offers the toggle", () => {
            render(<StructuredDataRenderer content={'{"framework":"sam","level":1}'} rendererType="json" setRenderError={setRenderError} isStreaming={false} />);

            expect(setRenderError).toHaveBeenCalledWith(null);
            expect(screen.queryByText(/parse failed/i)).not.toBeInTheDocument();
            expect(screen.getByTitle("Show Raw Text")).toBeInTheDocument();
            expect(screen.getByText(/framework/)).toBeInTheDocument();
        });
    });

    describe("across the streaming → settled transition", () => {
        it("never surfaces a parsing-failed error when the content settles valid", () => {
            const { rerender } = render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            rerender(<StructuredDataRenderer content={VALID_YAML} rendererType="yaml" setRenderError={setRenderError} isStreaming={false} />);

            expect(setRenderError).not.toHaveBeenCalledWith(expect.stringContaining("parsing failed"));
            expect(screen.queryByText(/parse failed/i)).not.toBeInTheDocument();
        });

        it("surfaces the error when the content settles still malformed", () => {
            const { rerender } = render(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming />);

            expect(setRenderError).not.toHaveBeenCalledWith(expect.stringContaining("parsing failed"));

            rerender(<StructuredDataRenderer content={TRUNCATED_MID_SCALAR} rendererType="yaml" setRenderError={setRenderError} isStreaming={false} />);

            expect(setRenderError).toHaveBeenCalledWith(expect.stringContaining("YAML parsing failed"));
            expect(screen.getByText(/parse failed/i)).toBeInTheDocument();
        });
    });
});
