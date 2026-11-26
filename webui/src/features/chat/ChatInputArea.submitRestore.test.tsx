// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactWithSession } from "@/api/artifacts";
import * as artifactService from "@/api/artifacts/service";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { StoryProvider } from "@/testing/providers/StoryProvider";

document.execCommand = vi.fn().mockReturnValue(true);

class NoopIntersectionObserver {
    observe = vi.fn();
    unobserve = vi.fn();
    disconnect = vi.fn();
    takeRecords = vi.fn(() => [] as IntersectionObserverEntry[]);
    root: Element | Document | null = null;
    rootMargin = "";
    thresholds: ReadonlyArray<number> = [];
    constructor(_cb: IntersectionObserverCallback) {
        void _cb;
    }
}
vi.mock("@/hooks", async () => {
    const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
    return {
        ...actual,
        useDebounce: <T,>(value: T) => value,
    };
});

function seedArtifacts(artifacts: ArtifactWithSession[]) {
    const raw = artifacts.map(a => ({
        filename: a.filename,
        size: a.size,
        mimeType: a.mimeType ?? null,
        lastModified: a.lastModified ?? null,
        uri: a.uri ?? null,
        version: a.version ?? null,
        sessionId: a.sessionId,
        sessionName: a.sessionName ?? null,
        projectId: a.projectId ?? null,
        projectName: a.projectName ?? null,
        source: a.source ?? null,
        tags: a.tags ?? null,
    }));
    vi.spyOn(artifactService, "getAllArtifacts").mockResolvedValue({
        artifacts: raw,
        totalCount: raw.length,
        hasMore: false,
        nextPage: null,
    });
}

beforeEach(() => {
    vi.stubGlobal("IntersectionObserver", NoopIntersectionObserver);
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

function renderComponent(chatContextValues = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={chatContextValues} flagValues={{ artifact_attachment: true }}>
                <ChatInputArea agents={[]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("ChatInputArea submit failure — input state restoration", () => {
    test("clears input immediately on submit", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("my important message");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(input).toHaveTextContent("");
        });
    });

    test("restores input when handleSubmit rejects", async () => {
        const handleSubmit = vi.fn().mockRejectedValue(new Error("Network error"));
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("my important message");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(input).toHaveTextContent("my important message");
        });
    });

    test("does not overwrite user input typed during async submit gap on failure", async () => {
        let rejectFn: (err: Error) => void;
        const handleSubmit = vi.fn().mockImplementation(
            () =>
                new Promise<void>((_resolve, reject) => {
                    rejectFn = reject;
                })
        );
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("original message");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(input).toHaveTextContent("");
        });

        await userEvent.click(input);
        await userEvent.keyboard("new content typed during gap");

        await act(async () => {
            rejectFn!(new Error("Network error"));
        });
        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        await waitFor(() => {
            expect(input).toHaveTextContent("new content typed during gap");
        });
        expect(input).not.toHaveTextContent("original message");
    });

    test("shows error notification when handleSubmit rejects", async () => {
        const displayError = vi.fn();
        const handleSubmit = vi.fn().mockRejectedValue(new Error("Server error"));
        renderComponent({ handleSubmit, isResponding: false, displayError });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("test");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(displayError).toHaveBeenCalledWith(
                expect.objectContaining({
                    title: "Failed to Send Message",
                })
            );
        });
    });
});

describe("ChatInputArea aggregated upload error reporting", () => {
    test("shows consolidated error when pasted text artifact uploads fail", async () => {
        const displayError = vi.fn();
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        const uploadArtifactFile = vi.fn().mockResolvedValue({ error: "Upload quota exceeded" });

        renderComponent({ handleSubmit, isResponding: false, displayError, uploadArtifactFile });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);

        const largeText = "x".repeat(2100);
        const clipboardData = {
            getData: (type: string) => (type === "text/plain" || type === "text" ? largeText : ""),
            types: ["text/plain"],
            files: [],
            items: [],
        };

        fireEvent.paste(input, { clipboardData });

        await waitFor(() => {
            expect(screen.getByText(/snippet/i)).toBeInTheDocument();
        });

        await userEvent.keyboard("send with artifact");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(uploadArtifactFile).toHaveBeenCalled();
        });

        await waitFor(() => {
            expect(displayError).toHaveBeenCalledWith(
                expect.objectContaining({
                    title: "Some uploads failed",
                })
            );
        });
    });

    test("sends message successfully when no pasted text items fail", async () => {
        const displayError = vi.fn();
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        const uploadArtifactFile = vi.fn().mockResolvedValue({ uri: "artifact://test.txt", sessionId: "s1" });

        renderComponent({ handleSubmit, isResponding: false, displayError, uploadArtifactFile });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);

        const largeText = "y".repeat(2100);
        const clipboardData = {
            getData: (type: string) => (type === "text/plain" || type === "text" ? largeText : ""),
            types: ["text/plain"],
            files: [],
            items: [],
        };

        fireEvent.paste(input, { clipboardData });

        await waitFor(() => {
            expect(screen.getByText(/snippet/i)).toBeInTheDocument();
        });

        await userEvent.keyboard("send with artifact");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        expect(displayError).not.toHaveBeenCalledWith(expect.objectContaining({ title: "Some uploads failed" }));
    });
});

const makeArtifact = (overrides: Partial<ArtifactWithSession> = {}): ArtifactWithSession => ({
    filename: "ref.txt",
    size: 100,
    mimeType: "text/plain",
    lastModified: "2026-01-01T00:00:00Z",
    uri: "artifact://sess-1/ref.txt",
    sessionId: "sess-1",
    sessionName: "Session One",
    ...overrides,
});

async function attachArtifactsViaDialog(filenames: string[]) {
    await userEvent.click(screen.getByRole("button", { name: "Attach" }));
    await userEvent.click(await screen.findByText(/attach existing artifact/i));

    for (const name of filenames) {
        await userEvent.click(await screen.findByText(name));
    }
    await userEvent.click(screen.getByRole("button", { name: new RegExp(`^attach ${filenames.length}$`, "i") }));

    await waitFor(() => {
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    document.body.style.pointerEvents = "";
}

describe("ChatInputArea — artifactReferences arg to handleSubmit", () => {
    test("submit passes attached artifacts as a typed artifactReferences arg, not as fake-File envelopes", async () => {
        seedArtifacts([makeArtifact({ filename: "ref.txt", uri: "artifact://sess-1/ref.txt", mimeType: "text/plain" })]);
        const handleSubmit = vi.fn().mockResolvedValue(undefined);

        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("hello");

        await attachArtifactsViaDialog(["ref.txt"]);

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        const callArgs = handleSubmit.mock.calls[0];
        const files = callArgs[1] as File[];
        const artifactReferences = callArgs[7] as Array<{ uri: string; filename: string; mimeType: string }>;

        expect(files.find(f => f.type === "application/x-artifact-reference")).toBeUndefined();

        expect(artifactReferences).toEqual([{ uri: "artifact://sess-1/ref.txt", filename: "ref.txt", mimeType: "text/plain" }]);
    });
});

describe("ChatInputArea — selectedArtifactRefs restore on submit failure", () => {
    test("restores the attached artifact card when handleSubmit rejects", async () => {
        seedArtifacts([makeArtifact({ filename: "ref.txt", uri: "artifact://sess-1/ref.txt" })]);
        const handleSubmit = vi.fn().mockRejectedValue(new Error("Network error"));

        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("attached message");

        await attachArtifactsViaDialog(["ref.txt"]);

        expect(screen.getByTitle("ref.txt")).toBeInTheDocument();

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
        await waitFor(() => {
            expect(screen.getByTitle("ref.txt")).toBeInTheDocument();
        });
    });

    test("suppresses restore for a slice the user explicitly cleared during the in-flight submit", async () => {
        seedArtifacts([makeArtifact({ filename: "to-remove.txt", uri: "artifact://sess-1/to-remove.txt" })]);
        let rejectFn: (err: Error) => void;
        const handleSubmit = vi.fn().mockImplementation(
            () =>
                new Promise<void>((_resolve, reject) => {
                    rejectFn = reject;
                })
        );

        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("message");

        await attachArtifactsViaDialog(["to-remove.txt"]);
        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(screen.queryByTitle("to-remove.txt")).not.toBeInTheDocument();
        });

        await attachArtifactsViaDialog(["to-remove.txt"]);
        await waitFor(() => {
            expect(screen.getByTitle("to-remove.txt")).toBeInTheDocument();
        });

        await userEvent.click(screen.getByRole("button", { name: /remove attached artifact/i }));
        await waitFor(() => {
            expect(screen.queryByTitle("to-remove.txt")).not.toBeInTheDocument();
        });

        await act(async () => {
            rejectFn!(new Error("Network error"));
        });
        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        expect(screen.queryByTitle("to-remove.txt")).not.toBeInTheDocument();
    });

    test("does not clobber newly-attached artifacts if the user attached a different ref during the async gap", async () => {
        seedArtifacts([makeArtifact({ filename: "original.txt", uri: "artifact://sess-1/original.txt" }), makeArtifact({ filename: "new.txt", uri: "artifact://sess-1/new.txt" })]);
        let rejectFn: (err: Error) => void;
        const handleSubmit = vi.fn().mockImplementation(
            () =>
                new Promise<void>((_resolve, reject) => {
                    rejectFn = reject;
                })
        );

        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("message");

        await attachArtifactsViaDialog(["original.txt"]);
        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(screen.queryByTitle("original.txt")).not.toBeInTheDocument();
        });

        await attachArtifactsViaDialog(["new.txt"]);
        await waitFor(() => {
            expect(screen.getByTitle("new.txt")).toBeInTheDocument();
        });

        await act(async () => {
            rejectFn!(new Error("Network error"));
        });

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        expect(screen.getByTitle("new.txt")).toBeInTheDocument();
        expect(screen.queryByTitle("original.txt")).not.toBeInTheDocument();
    });
});
