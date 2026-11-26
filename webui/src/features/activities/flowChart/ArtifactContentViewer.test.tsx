// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

interface CapturedRendererProps {
    content: string;
    rendererType: string;
    mimeType?: string;
    url?: string;
    filename?: string;
}
let lastRendererProps: CapturedRendererProps | null = null;

const getArtifactContentMock = vi.fn();
const getArtifactUrlMock = vi.fn();

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({ sessionId: "chat-session" }),
}));

vi.mock("./artifactSessionContext", () => ({
    useArtifactSession: () => undefined,
}));

vi.mock("@/features/chat/preview", async importActual => {
    const actual = await importActual<typeof import("@/features/chat/preview")>();
    return {
        ...actual,
        ContentRenderer: (props: CapturedRendererProps) => {
            lastRendererProps = props;
            return <div data-testid="content-renderer" data-render-type={props.rendererType} data-filename={props.filename} data-url={props.url} />;
        },
    };
});

vi.mock("@/utils", async importActual => {
    const actual = await importActual<typeof import("@/utils")>();
    return {
        ...actual,
        getArtifactContent: getArtifactContentMock,
        getArtifactUrl: getArtifactUrlMock,
    };
});

const toBase64 = (s: string) => btoa(s);

const PPTX_MIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation";

let ArtifactContentViewer: React.ComponentType<{ uri?: string; name: string; version?: number; mimeType?: string }>;

beforeEach(async () => {
    lastRendererProps = null;
    getArtifactContentMock.mockReset();
    getArtifactUrlMock.mockReset();
    getArtifactUrlMock.mockImplementation(({ filename }: { filename: string }) => `https://example.test/artifacts/${filename}`);
    const mod = await import("./NodeDetailsCard");
    ArtifactContentViewer = mod.ArtifactContentViewer;
});

describe("ArtifactContentViewer", () => {
    test("does not truncate or re-encode a binary pptx artifact and passes url + filename", async () => {
        const rawBytes = "PK".repeat(400);
        const originalBase64 = toBase64(rawBytes);
        expect(originalBase64.length).toBeGreaterThan(1000);
        getArtifactContentMock.mockResolvedValue({ content: originalBase64, mimeType: PPTX_MIME });

        render(<ArtifactContentViewer name="deck.pptx" version={2} />);

        await screen.findByTestId("content-renderer");

        expect(lastRendererProps).not.toBeNull();
        expect(lastRendererProps!.rendererType).toBe("pptx");
        expect(lastRendererProps!.content).toBe(originalBase64);
        expect(lastRendererProps!.content.length).toBeGreaterThan(0);
        expect(lastRendererProps!.filename).toBe("deck.pptx");
        expect(lastRendererProps!.url).toBeDefined();
        expect(lastRendererProps!.url).toContain("deck.pptx");
        expect(screen.queryByText(/Content truncated/)).not.toBeInTheDocument();
    });

    test("still truncates a long text/markdown artifact to MAX_ARTIFACT_DISPLAY_LENGTH decoded chars", async () => {
        const longText = "a".repeat(5000);
        getArtifactContentMock.mockResolvedValue({ content: toBase64(longText), mimeType: "text/markdown" });

        render(<ArtifactContentViewer name="notes.md" />);

        await screen.findByTestId("content-renderer");

        expect(lastRendererProps).not.toBeNull();
        expect(lastRendererProps!.rendererType).toBe("markdown");
        expect(lastRendererProps!.content.length).toBeLessThanOrEqual(1000);
        expect(screen.getByText(/Content truncated/)).toBeInTheDocument();
    });

    test("passes a defined url and filename for a pdf artifact (url-only renderer)", async () => {
        getArtifactContentMock.mockResolvedValue({ content: toBase64("ignored pdf bytes"), mimeType: "application/pdf" });

        render(<ArtifactContentViewer name="report.pdf" />);

        await screen.findByTestId("content-renderer");

        expect(lastRendererProps).not.toBeNull();
        expect(lastRendererProps!.rendererType).toBe("pdf");
        expect(lastRendererProps!.url).toBeDefined();
        expect(lastRendererProps!.url).not.toBeNull();
        expect(lastRendererProps!.url).toContain("report.pdf");
        expect(lastRendererProps!.filename).toBe("report.pdf");
    });

    test("resolves filename from the uri, not the placeholder name prop", async () => {
        const originalBase64 = toBase64("PK".repeat(400));
        getArtifactContentMock.mockResolvedValue({ content: originalBase64, mimeType: PPTX_MIME });

        render(<ArtifactContentViewer uri="artifact://SESSION/report.pptx?version=1" name="input" />);

        await screen.findByTestId("content-renderer");

        expect(lastRendererProps).not.toBeNull();
        expect(lastRendererProps!.filename).toBe("report.pptx");
        expect(lastRendererProps!.rendererType).toBe("pptx");
        await waitFor(() => expect(getArtifactContentMock).toHaveBeenCalled());
        expect(getArtifactContentMock.mock.calls[0][0]).toMatchObject({ filename: "report.pptx" });
    });
});
