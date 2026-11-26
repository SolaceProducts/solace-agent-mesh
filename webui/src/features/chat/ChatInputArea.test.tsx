// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { agentUIMode } from "@/providers/contexts";

import { ChatInputArea } from "@/features/chat/ChatInputArea";
import { SNIP_TO_CHAT_EVENT } from "@/features/chat/preview/renderers/PdfRenderer";

import { SeedSessionArtifacts } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

document.execCommand = vi.fn().mockReturnValue(true);

function makeFile(name = "test.png", size = 100, lastModified = Date.now()): File {
    const file = new File(["x".repeat(size)], name, { type: "image/png" });
    Object.defineProperty(file, "lastModified", { value: lastModified });
    return file;
}

function renderComponent(chatContextValues = {}, configContextValues = {}, routerPath = "/") {
    return render(
        <MemoryRouter initialEntries={[routerPath]}>
            <StoryProvider chatContextValues={chatContextValues} configContextValues={configContextValues}>
                <ChatInputArea agents={[]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("session change useEffect", () => {
    test("clears input when session changes", async () => {
        const { rerender } = render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "session-1", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        await userEvent.type(input, "hello");
        expect(input).toHaveTextContent("hello");

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "session-2", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(input).toHaveTextContent("");
        });
    });

    test("does not clear input when session is first set (null -> value)", async () => {
        const { rerender } = render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        await userEvent.type(input, "keep me");

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "session-1", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(input).toHaveTextContent("keep me");
        });
    });
});

describe("pending redirect input", () => {
    afterEach(() => {
        sessionStorage.clear();
    });

    test("consumes untargeted pending input on mount", async () => {
        sessionStorage.setItem("sam_redirect_pending_input", "redirect prefill");
        renderComponent({ sessionId: "session-1", pendingPrompt: null });

        await waitFor(() => {
            expect(screen.getByTestId("chat-input")).toHaveTextContent("redirect prefill");
        });
        expect(sessionStorage.getItem("sam_redirect_pending_input")).toBeNull();
    });

    test("holds targeted pending input until the target session lands, surviving the session-change clear", async () => {
        sessionStorage.setItem("sam_redirect_pending_input", "builder context summary");
        sessionStorage.setItem("sam_redirect_pending_input_session", "dest-session");

        const { rerender } = render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "source-session", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        expect(input).toHaveTextContent("");
        expect(sessionStorage.getItem("sam_redirect_pending_input")).toBe("builder context summary");

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "dest-session", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(input).toHaveTextContent("builder context summary");
        });
        expect(sessionStorage.getItem("sam_redirect_pending_input")).toBeNull();
        expect(sessionStorage.getItem("sam_redirect_pending_input_session")).toBeNull();
    });

    test("does not consume targeted pending input when switching to a non-target session", async () => {
        sessionStorage.setItem("sam_redirect_pending_input", "builder context summary");
        sessionStorage.setItem("sam_redirect_pending_input_session", "dest-session");

        const { rerender } = render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "source-session", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "some-other-session", pendingPrompt: null }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        await waitFor(() => {
            expect(input).toHaveTextContent("");
        });
        expect(sessionStorage.getItem("sam_redirect_pending_input")).toBe("builder context summary");
    });
});

describe("isResponding useEffect", () => {
    test("focuses input when isResponding transitions from true to false", async () => {
        const { rerender } = render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ isResponding: true }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ isResponding: false }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(
            () => {
                expect(document.activeElement?.getAttribute("contenteditable")).toBe("true");
            },
            { timeout: 300 }
        );
    });
});

describe("focus-chat-input event listener", () => {
    test("focuses input when focus-chat-input event is dispatched", async () => {
        renderComponent();

        act(() => {
            window.dispatchEvent(new Event("focus-chat-input"));
        });

        await waitFor(
            () => {
                expect(document.activeElement?.getAttribute("contenteditable")).toBe("true");
            },
            { timeout: 300 }
        );
    });
});

describe("follow-up-question event", () => {
    test("shows context badge when event fires without a prompt", async () => {
        renderComponent();

        act(() => {
            window.dispatchEvent(
                new CustomEvent("follow-up-question", {
                    detail: { text: "selected text", prompt: null, autoSubmit: false, sourceMessageId: null },
                })
            );
        });

        await waitFor(() => {
            expect(screen.getByText(/selected text/)).toBeInTheDocument();
        });
    });

    test("sets input value when event fires with a prompt", async () => {
        renderComponent();

        act(() => {
            window.dispatchEvent(
                new CustomEvent("follow-up-question", {
                    detail: { text: "some context", prompt: "Summarise this", autoSubmit: false, sourceMessageId: null },
                })
            );
        });

        await waitFor(() => {
            const input = screen.getByTestId("chat-input");
            expect(input).toHaveTextContent("Summarise this");
        });
    });

    test("renders a component card (not a text quote) when the event carries a component", async () => {
        renderComponent();
        act(() => {
            window.dispatchEvent(
                new CustomEvent("follow-up-question", {
                    detail: { text: "MyAgent (agent)", component: { name: "MyAgent", type: "agent", origin: "session" } },
                })
            );
        });
        await waitFor(() => {
            expect(screen.getByText("MyAgent")).toBeInTheDocument();
            expect(screen.getByText("Agent")).toBeInTheDocument();
        });
        expect(screen.queryByText('"MyAgent (agent)"')).toBeNull();
    });

    test("ignores a malformed component payload and falls back to a text quote (no crash)", async () => {
        renderComponent();
        act(() => {
            window.dispatchEvent(
                new CustomEvent("follow-up-question", {
                    detail: { text: "broken payload", component: { type: 42 } },
                })
            );
        });
        await waitFor(() => expect(screen.getByText(/broken payload/)).toBeInTheDocument());
    });

    test("auto-submits when autoSubmit is true and prompt is provided", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        act(() => {
            window.dispatchEvent(
                new CustomEvent("follow-up-question", {
                    detail: { text: "context text", prompt: "Do something", autoSubmit: true, sourceMessageId: null },
                })
            );
        });

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled(), { timeout: 500 });
    });
});

describe("snip-to-chat event", () => {
    test("adds file to selected files when snip-to-chat fires", async () => {
        renderComponent();
        const file = makeFile("snip.png");

        act(() => {
            window.dispatchEvent(
                new CustomEvent(SNIP_TO_CHAT_EVENT, {
                    detail: { file },
                })
            );
        });

        await waitFor(() => {
            expect(screen.getByText("snip.png")).toBeInTheDocument();
        });
    });

    test("deduplicates files with same name, size, and lastModified", async () => {
        renderComponent();
        const file = makeFile("snip.png", 100, 1234567890);

        act(() => {
            window.dispatchEvent(new CustomEvent(SNIP_TO_CHAT_EVENT, { detail: { file } }));
        });
        act(() => {
            window.dispatchEvent(new CustomEvent(SNIP_TO_CHAT_EVENT, { detail: { file } }));
        });

        await waitFor(() => {
            const badges = screen.getAllByText("snip.png");
            expect(badges).toHaveLength(1);
        });
    });
});

describe("pending prompt useEffect", () => {
    test("sets input value when pendingPrompt has no variables and agent is selected", async () => {
        const clearPendingPrompt = vi.fn();
        render(
            <MemoryRouter>
                <StoryProvider
                    chatContextValues={{
                        pendingPrompt: { promptText: "Hello world", groupId: "g1", groupName: "G1" },
                        selectedAgentName: "agent-1",
                        clearPendingPrompt,
                    }}
                >
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            const input = screen.getByTestId("chat-input");
            expect(input).toHaveTextContent("Hello world");
        });
        expect(clearPendingPrompt).toHaveBeenCalled();
    });

    test("shows variable dialog when pendingPrompt has {{variables}}", async () => {
        render(
            <MemoryRouter>
                <StoryProvider
                    chatContextValues={{
                        pendingPrompt: { promptText: "Hello {{name}}", groupId: "g1", groupName: "G1" },
                        selectedAgentName: "agent-1",
                        clearPendingPrompt: vi.fn(),
                    }}
                >
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(screen.getByText("Insert G1")).toBeInTheDocument();
        });
    });
});

describe("file handling", () => {
    test("selecting a file shows a file badge", async () => {
        renderComponent({ isResponding: false });
        const fileInput = document.querySelector("input[type='file']") as HTMLInputElement;

        const file = makeFile("report.pdf");
        await act(async () => {
            Object.defineProperty(fileInput, "files", { value: [file], configurable: true });
            fireEvent.change(fileInput);
        });

        await waitFor(() => {
            expect(screen.getByText("report.pdf")).toBeInTheDocument();
        });
    });

    test("removing a file badge removes it from the list", async () => {
        renderComponent({ isResponding: false });
        const fileInput = document.querySelector("input[type='file']") as HTMLInputElement;

        const file = makeFile("report.pdf");
        await act(async () => {
            Object.defineProperty(fileInput, "files", { value: [file], configurable: true });
            fireEvent.change(fileInput);
        });

        await waitFor(() => screen.getByText("report.pdf"));

        const badge = screen.getByText("report.pdf").closest("div");
        const xBtn = badge?.querySelector("button");
        if (xBtn) {
            await userEvent.click(xBtn);
            await waitFor(() => {
                expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
            });
        }
    });

    test("duplicate files are not added twice", async () => {
        renderComponent({ isResponding: false });
        const fileInput = document.querySelector("input[type='file']") as HTMLInputElement;

        const file = makeFile("dup.png", 50, 111);

        await act(async () => {
            Object.defineProperty(fileInput, "files", { value: [file], configurable: true });
            fireEvent.change(fileInput);
        });
        await act(async () => {
            Object.defineProperty(fileInput, "files", { value: [file], configurable: true });
            fireEvent.change(fileInput);
        });

        await waitFor(() => {
            const badges = screen.getAllByText("dup.png");
            expect(badges).toHaveLength(1);
        });
    });
});

describe("paste handling", () => {
    test("large pasted text creates a pending badge", async () => {
        renderComponent({ isResponding: false });
        const input = screen.getByTestId("chat-input");

        const largeText = "a".repeat(2000);
        const clipboardData = {
            files: { length: 0 },
            getData: (type: string) => (type === "text" ? largeText : ""),
        };

        fireEvent.paste(input, { clipboardData });

        await waitFor(() => {
            expect(screen.getByText(/snippet/)).toBeInTheDocument();
        });
    });

    test("pasting a file adds it to selected files", async () => {
        renderComponent({ isResponding: false });
        const input = screen.getByTestId("chat-input");

        const file = makeFile("pasted.png");
        const fileList = [file];
        Object.defineProperty(fileList, "length", { value: 1 });

        const clipboardData = {
            files: fileList,
            getData: () => "",
        };

        fireEvent.paste(input, { clipboardData });

        await waitFor(() => {
            expect(screen.getByText("pasted.png")).toBeInTheDocument();
        });
    });

    test("a second clipboard image keeps its own name instead of colliding", async () => {
        renderComponent({ isResponding: false });
        const input = screen.getByTestId("chat-input");

        const pasteImage = (lastModified: number) => {
            const fileList = [makeFile("image.png", 100, lastModified)];
            Object.defineProperty(fileList, "length", { value: 1 });
            fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });
        };

        pasteImage(1000);
        await waitFor(() => expect(screen.getByText("image.png")).toBeInTheDocument());

        pasteImage(2000);
        await waitFor(() => expect(screen.getByText("image-2.png")).toBeInTheDocument());

        expect(screen.getByText("image.png")).toBeInTheDocument();
    });

    test("a pasted image is renamed rather than uploaded as a new version of a same-named session artifact", async () => {
        const existing = { filename: "image.png", mimeType: "image/png", size: 100, lastModified: new Date().toISOString(), uri: "artifact://image.png" };
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "reserved-names-session", isResponding: false }}>
                    <SeedSessionArtifacts sessionId="reserved-names-session" artifacts={[existing as unknown as ArtifactInfo]} />
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        const fileList = [makeFile("image.png", 100, 3000)];
        Object.defineProperty(fileList, "length", { value: 1 });
        fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });

        await waitFor(() => expect(screen.getByText("image-2.png")).toBeInTheDocument());
        expect(screen.queryByText("image.png")).not.toBeInTheDocument();
    });

    test("a hidden __working session artifact still reserves its name", async () => {
        const hidden = { filename: "image.png", mimeType: "image/png", size: 100, lastModified: new Date().toISOString(), uri: "artifact://image.png", tags: ["__working"] };
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "working-artifact-session", isResponding: false }}>
                    <SeedSessionArtifacts sessionId="working-artifact-session" artifacts={[hidden as unknown as ArtifactInfo]} />
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        const input = screen.getByTestId("chat-input");
        const fileList = [makeFile("image.png", 100, 4000)];
        Object.defineProperty(fileList, "length", { value: 1 });
        fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });

        await waitFor(() => expect(screen.getByText("image-2.png")).toBeInTheDocument());
        expect(screen.queryByText("image.png")).not.toBeInTheDocument();
    });

    test("an attachment defers to the name the user gave a pasted-text snippet", async () => {
        renderComponent({ isResponding: false });
        const input = screen.getByTestId("chat-input");

        fireEvent.paste(input, { clipboardData: { files: { length: 0 }, getData: (t: string) => (t === "text" ? "a".repeat(2000) : "") } });
        await waitFor(() => expect(screen.getByText(/snippet/)).toBeInTheDocument());

        await userEvent.click(screen.getByText(/snippet/));
        const filenameInput = await screen.findByLabelText("Filename");
        await userEvent.clear(filenameInput);
        await userEvent.type(filenameInput, "notes.md");
        await userEvent.click(screen.getByRole("button", { name: "Customize" }));

        const fileList = [makeFile("notes.md", 100, 5000)];
        Object.defineProperty(fileList, "length", { value: 1 });
        fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });

        await waitFor(() => expect(screen.getByText("notes-2.md")).toBeInTheDocument());
    });

    test("re-checks attachment names at submit against artifacts that loaded after the paste", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        const late = { filename: "image.png", mimeType: "image/png", size: 100, lastModified: new Date().toISOString(), uri: "artifact://image.png" };
        const view = (seeded: ArtifactInfo[]) => (
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "late-load-session", isResponding: false, handleSubmit }}>
                    <SeedSessionArtifacts sessionId="late-load-session" artifacts={seeded} />
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );
        const { rerender } = render(view([]));

        const input = screen.getByTestId("chat-input");
        const fileList = [makeFile("image.png", 100, 6000)];
        Object.defineProperty(fileList, "length", { value: 1 });
        fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });

        await waitFor(() => expect(screen.getByText("image.png")).toBeInTheDocument());

        rerender(view([late as unknown as ArtifactInfo]));

        await userEvent.click(input);
        await userEvent.keyboard("here it is");
        await userEvent.keyboard("{Enter}");

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const sentFiles = handleSubmit.mock.calls[0][1] as File[];
        expect(sentFiles.map(f => f.name)).toEqual(["image-2.png"]);
    });

    test("the submit-time re-check does not move an attachment onto a configured snippet's name", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        const late = { filename: "notes.md", mimeType: "text/markdown", size: 100, lastModified: new Date().toISOString(), uri: "artifact://notes.md" };
        const view = (seeded: ArtifactInfo[]) => (
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "snippet-clash-session", isResponding: false, handleSubmit }}>
                    <SeedSessionArtifacts sessionId="snippet-clash-session" artifacts={seeded} />
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );
        const { rerender } = render(view([]));
        const input = screen.getByTestId("chat-input");

        fireEvent.paste(input, { clipboardData: { files: { length: 0 }, getData: (t: string) => (t === "text" ? "a".repeat(2000) : "") } });
        await waitFor(() => expect(screen.getByText(/snippet/)).toBeInTheDocument());
        await userEvent.click(screen.getByText(/snippet/));
        const filenameInput = await screen.findByLabelText("Filename");
        await userEvent.clear(filenameInput);
        await userEvent.type(filenameInput, "notes-2.md");
        await userEvent.click(screen.getByRole("button", { name: "Customize" }));

        const fileList = [makeFile("notes.md", 100, 7000)];
        Object.defineProperty(fileList, "length", { value: 1 });
        fireEvent.paste(input, { clipboardData: { files: fileList, getData: () => "" } });
        await waitFor(() => expect(screen.getByText("notes.md")).toBeInTheDocument());

        rerender(view([late as unknown as ArtifactInfo]));

        await userEvent.click(input);
        await userEvent.keyboard("both attached");
        await userEvent.keyboard("{Enter}");

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const sentFiles = handleSubmit.mock.calls[0][1] as File[];
        expect(sentFiles.map(f => f.name)).toEqual(["notes-3.md"]);
    });
});

describe("prompt command trigger", () => {
    test("typing '/' shows the prompts command popup", async () => {
        renderComponent();
        const input = screen.getByTestId("chat-input");

        await userEvent.click(input);
        await userEvent.keyboard("/");

        await waitFor(() => {
            expect(screen.getByTestId("promptCommand")).toBeInTheDocument();
        });
    });
});

describe("location state promptText useEffect", () => {
    test("calls startNewChatWithPrompt when location state has promptText", async () => {
        const startNewChatWithPrompt = vi.fn().mockResolvedValue(undefined);

        render(
            <MemoryRouter
                initialEntries={[
                    {
                        pathname: "/chat",
                        state: { promptText: "Hello from router", groupId: "g1", groupName: "Group 1" },
                    },
                ]}
            >
                <StoryProvider chatContextValues={{ startNewChatWithPrompt }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(startNewChatWithPrompt).toHaveBeenCalledWith({
                promptText: "Hello from router",
                groupId: "g1",
                groupName: "Group 1",
            });
        });
    });

    test("forwards agentName from location state to startNewChatWithPrompt", async () => {
        const startNewChatWithPrompt = vi.fn().mockResolvedValue(undefined);

        render(
            <MemoryRouter
                initialEntries={[
                    {
                        pathname: "/chat",
                        state: { promptText: "About the Feedback Dashboard: ", groupId: "ask-ai", groupName: "Ask AI", agentName: "activity-monitor-uuid" },
                    },
                ]}
            >
                <StoryProvider chatContextValues={{ startNewChatWithPrompt }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => {
            expect(startNewChatWithPrompt).toHaveBeenCalledWith({
                promptText: "About the Feedback Dashboard: ",
                groupId: "ask-ai",
                groupName: "Ask AI",
                agentName: "activity-monitor-uuid",
            });
        });
    });
});

describe("form submission", () => {
    test("calls handleSubmit when form is submitted with text", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("Test message");

        const sendBtn = screen.getByTestId("sendMessage");
        await userEvent.click(sendBtn);

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
    });

    test("stop button calls handleCancel when responding", () => {
        const handleCancel = vi.fn();
        renderComponent({ isResponding: true, isCancelling: false, handleCancel });

        const cancelBtn = screen.getByTestId("cancel");
        fireEvent.click(cancelBtn);

        expect(handleCancel).toHaveBeenCalled();
    });
});

describe("Enter key submit behaviour", () => {
    const originalMatchMedia = window.matchMedia;
    const originalInnerWidth = window.innerWidth;

    function setPointer({ coarse, fine = false, width = 1024 }: { coarse: boolean; fine?: boolean; width?: number }) {
        Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: width });
        window.matchMedia = ((query: string) => ({
            matches: query === "(pointer: coarse)" ? coarse : query === "(any-pointer: fine)" ? fine : false,
            media: query,
            onchange: null,
            addListener: () => {},
            removeListener: () => {},
            addEventListener: () => {},
            removeEventListener: () => {},
            dispatchEvent: () => false,
        })) as unknown as typeof window.matchMedia;
    }

    afterEach(() => {
        window.matchMedia = originalMatchMedia;
        Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: originalInnerWidth });
    });

    test("submits on Enter with a fine pointer (desktop)", async () => {
        setPointer({ coarse: false });
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("Test message");

        fireEvent.keyDown(input, { key: "Enter" });

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
    });

    test("submits on Enter on a hybrid device with both a touchscreen and a trackpad/mouse", async () => {
        setPointer({ coarse: true, fine: true });
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("Test message");

        fireEvent.keyDown(input, { key: "Enter" });

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
    });

    test("does not submit on Enter on a narrow-viewport touch device with a paired mouse; Send button still submits", async () => {
        setPointer({ coarse: true, fine: true, width: 500 });
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("Test message");

        fireEvent.keyDown(input, { key: "Enter" });

        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 0));
        });
        expect(handleSubmit).not.toHaveBeenCalled();
        expect(input).toHaveTextContent("Test message");

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
    });

    test("does not submit on Enter on a touch-only device; Send button still submits", async () => {
        setPointer({ coarse: true });
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("Test message");

        fireEvent.keyDown(input, { key: "Enter" });

        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 0));
        });
        expect(handleSubmit).not.toHaveBeenCalled();
        expect(input).toHaveTextContent("Test message");

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });
    });
});

describe("STT mic gating by UI mode", () => {
    const MIC_LABEL = "Start voice recording";

    test("shows the mic recorder when STT is enabled in full mode", () => {
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "session-1", pendingPrompt: null }} configContextValues={{ speechToTextEnabled: true }}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );
        expect(screen.getByLabelText(MIC_LABEL)).toBeInTheDocument();
    });

    test("hides the mic recorder in agent mode even when STT is enabled in config", () => {
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "session-1", pendingPrompt: null }} configContextValues={{ speechToTextEnabled: true }} uiModeContextValues={agentUIMode("OrchestratorAgent")}>
                    <ChatInputArea agents={[]} />
                </StoryProvider>
            </MemoryRouter>
        );
        expect(screen.queryByLabelText(MIC_LABEL)).not.toBeInTheDocument();
    });
});
