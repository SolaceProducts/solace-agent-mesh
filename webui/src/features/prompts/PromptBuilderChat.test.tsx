// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { PromptBuilderChat } from "./PromptBuilderChat";

Element.prototype.scrollIntoView = vi.fn();
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const mockGet = vi.spyOn(api.webui, "get" as any);
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const mockPost = vi.spyOn(api.webui, "post" as any);

const defaultConfig = {
    name: "",
    description: "",
    category: "",
    command: "",
    promptText: "",
};

const INIT_GREETING = "Hi! I'll help you create a prompt template. What kind of task would you like to template?";

function renderChat(props: Partial<React.ComponentProps<typeof PromptBuilderChat>> = {}, configContextValues: Partial<React.ComponentProps<typeof StoryProvider>["configContextValues"]> = {}) {
    const defaultProps = {
        onConfigUpdate: vi.fn(),
        currentConfig: defaultConfig,
        onReadyToSave: vi.fn(),
    };
    return render(
        <StoryProvider configContextValues={configContextValues}>
            <PromptBuilderChat {...defaultProps} {...props} />
        </StoryProvider>
    );
}

function setupInitSuccess(greeting = INIT_GREETING) {
    mockGet.mockResolvedValueOnce({ message: greeting });
}

function setupChatSuccess(overrides: Record<string, unknown> = {}) {
    mockPost.mockResolvedValueOnce({
        message: "I'll create that template for you.",
        templateUpdates: { name: "Test Template" },
        confidence: 0.9,
        readyToSave: true,
        isError: false,
        ...overrides,
    });
}

function setupChatErrorResponse(overrides: Record<string, unknown> = {}) {
    mockPost.mockResolvedValueOnce({
        message: "The LLM service rejected the authentication credentials.",
        templateUpdates: {},
        confidence: 0,
        readyToSave: false,
        isError: true,
        ...overrides,
    });
}

async function typeAndSubmit(text: string) {
    await waitFor(() => {
        expect(screen.getByText("AI Builder")).toBeInTheDocument();
    });
    const textarea = screen.getByRole("textbox");
    fireEvent.change(textarea, { target: { value: text } });
    await act(async () => {
        fireEvent.submit(textarea.closest("form")!);
    });
}

beforeEach(() => {
    vi.clearAllMocks();
});

describe("PromptBuilderChat rendering", () => {
    test("shows initializing state before init completes", () => {
        mockGet.mockReturnValueOnce(new Promise(() => {}));
        renderChat();
        expect(screen.getByText("Initializing AI assistant...")).toBeInTheDocument();
    });

    test("renders header with AI Builder title after init", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
    });

    test("displays greeting message from init API", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText(INIT_GREETING)).toBeInTheDocument();
        });
    });

    test("uses editing greeting when isEditing is true", async () => {
        setupInitSuccess();
        renderChat({ isEditing: true });
        const editingGreeting = "Hi! I'll help you edit this prompt template. What changes would you like to make?";
        await waitFor(() => {
            expect(screen.getByText(editingGreeting)).toBeInTheDocument();
        });
    });

    test("uses fallback greeting when init API fails", async () => {
        mockGet.mockRejectedValueOnce(new Error("Network error"));
        renderChat();
        const fallback = "Hi! I'll help you create a prompt template. What kind of recurring task would you like to template?";
        await waitFor(() => {
            expect(screen.getByText(fallback)).toBeInTheDocument();
        });
    });

    test("uses editing fallback greeting when init API fails and isEditing", async () => {
        mockGet.mockRejectedValueOnce(new Error("Network error"));
        renderChat({ isEditing: true });
        const editingFallback = "Hi! I'll help you edit this prompt template. What changes would you like to make?";
        await waitFor(() => {
            expect(screen.getByText(editingFallback)).toBeInTheDocument();
        });
    });
});

describe("PromptBuilderChat placeholder text", () => {
    test("shows initial placeholder before user sends a message", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByPlaceholderText("Describe your recurring task...")).toBeInTheDocument();
        });
    });

    test("changes placeholder after first message is sent", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess();
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(screen.getByPlaceholderText("Type your message...")).toBeInTheDocument();
        });
    });
});

describe("PromptBuilderChat send button", () => {
    test("send button is disabled when input is empty", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        const sendButton = screen.getByRole("button", { name: /send message/i });
        expect(sendButton).toBeDisabled();
    });

    test("send button is enabled when input has text", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a code review template" } });
        const sendButton = screen.getByRole("button", { name: /send message/i });
        expect(sendButton).not.toBeDisabled();
    });

    test("send button is disabled when input is only whitespace", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "   " } });
        const sendButton = screen.getByRole("button", { name: /send message/i });
        expect(sendButton).toBeDisabled();
    });
});

describe("PromptBuilderChat message sending", () => {
    test("sends message and displays assistant response", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess();
        await typeAndSubmit("Create a code review template");

        await waitFor(() => {
            expect(screen.getByText("Create a code review template")).toBeInTheDocument();
        });
        await waitFor(() => {
            expect(screen.getByText("I'll create that template for you.")).toBeInTheDocument();
        });
    });

    test("renders assistant response markdown as formatted HTML", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess({ message: "**Template saved successfully!**" });
        await typeAndSubmit("Create a code review template");

        const strong = await screen.findByText("Template saved successfully!");
        expect(strong.tagName).toBe("STRONG");
        expect(screen.queryByText("**Template saved successfully!**")).not.toBeInTheDocument();
    });

    test("clears input after sending", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess();
        await typeAndSubmit("Create a template");

        const textarea = screen.getByRole("textbox");
        await waitFor(() => {
            expect(textarea).toHaveValue("");
        });
    });

    test("calls onConfigUpdate when is_error is false and template_updates is non-empty", async () => {
        setupInitSuccess();
        const onConfigUpdate = vi.fn();
        renderChat({ onConfigUpdate });
        setupChatSuccess({ templateUpdates: { name: "My Template", category: "Dev" }, isError: false });
        await typeAndSubmit("Create a code review template");

        await waitFor(() => {
            expect(onConfigUpdate).toHaveBeenCalledWith({ name: "My Template", category: "Dev" });
        });
    });

    test("does not call onConfigUpdate when is_error is true even with non-empty template_updates", async () => {
        setupInitSuccess();
        const onConfigUpdate = vi.fn();
        renderChat({ onConfigUpdate });
        setupChatErrorResponse({ templateUpdates: { name: "Should not apply" } });
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(screen.getByText(/LLM service rejected the authentication credentials/)).toBeInTheDocument();
        });
        expect(onConfigUpdate).not.toHaveBeenCalled();
    });

    test("does not call onConfigUpdate when template_updates is empty", async () => {
        setupInitSuccess();
        const onConfigUpdate = vi.fn();
        renderChat({ onConfigUpdate });
        setupChatSuccess({ templateUpdates: {}, isError: false });
        await typeAndSubmit("Tell me more");

        await waitFor(() => {
            expect(screen.getByText("I'll create that template for you.")).toBeInTheDocument();
        });
        expect(onConfigUpdate).not.toHaveBeenCalled();
    });

    test("calls onReadyToSave when response is not an error", async () => {
        setupInitSuccess();
        const onReadyToSave = vi.fn();
        renderChat({ onReadyToSave });
        setupChatSuccess({ readyToSave: true, isError: false });
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(onReadyToSave).toHaveBeenCalledWith(true);
        });
    });

    test("does not call onReadyToSave when response has is_error", async () => {
        setupInitSuccess();
        const onReadyToSave = vi.fn();
        renderChat({ onReadyToSave });
        setupChatErrorResponse();
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(screen.getByText(/LLM service rejected the authentication credentials/)).toBeInTheDocument();
        });
        expect(onReadyToSave).not.toHaveBeenCalled();
    });

    test("shows a budget refusal as a warning and a failure as an error", async () => {
        setupInitSuccess();
        renderChat();
        setupChatErrorResponse({ message: "Your usage budget is used up.", isWarning: true });
        await typeAndSubmit("Create a template");
        expect(await screen.findByTestId("inlineNotice-warning")).toHaveTextContent("Your usage budget is used up.");
        expect(screen.queryByTestId("inlineNotice-error")).not.toBeInTheDocument();

        setupChatErrorResponse({ message: "The model is down." });
        await typeAndSubmit("Try again");
        expect(await screen.findByTestId("inlineNotice-error")).toHaveTextContent("The model is down.");
        const historySent = (mockPost.mock.calls[1][1] as { conversationHistory: Array<{ content: string }> }).conversationHistory.map(m => m.content);
        expect(historySent).toContain("Create a template");
        expect(historySent).not.toContain("Your usage budget is used up.");
    });

    test("does not send when input is empty", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });

        const textarea = screen.getByRole("textbox");
        await act(async () => {
            fireEvent.submit(textarea.closest("form")!);
        });
        expect(mockPost).not.toHaveBeenCalled();
    });

    test("sends conversation history with correct format", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess();
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(mockPost).toHaveBeenCalledWith(
                "/api/v1/prompts/chat",
                expect.objectContaining({
                    message: "Create a template",
                    conversationHistory: expect.arrayContaining([expect.objectContaining({ role: "assistant", content: INIT_GREETING })]),
                    currentTemplate: defaultConfig,
                })
            );
        });
    });

    test("trims whitespace from user message", async () => {
        setupInitSuccess();
        renderChat();
        setupChatSuccess();
        await typeAndSubmit("  Create a template  ");

        await waitFor(() => {
            expect(screen.getByText("Create a template")).toBeInTheDocument();
        });
    });
});

describe("PromptBuilderChat keyboard interaction", () => {
    test("Enter key sends message", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });

        setupChatSuccess();
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a template" } });

        await act(async () => {
            fireEvent.keyDown(textarea, { key: "Enter", shiftKey: false });
        });

        await waitFor(() => {
            expect(mockPost).toHaveBeenCalled();
        });
    });

    test("Shift+Enter does not send message", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });

        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a template" } });
        fireEvent.keyDown(textarea, { key: "Enter", shiftKey: true });

        expect(mockPost).not.toHaveBeenCalled();
    });
});

describe("PromptBuilderChat error handling", () => {
    test("displays error message when API call fails", async () => {
        setupInitSuccess();
        renderChat();
        mockPost.mockRejectedValueOnce(new Error("Network error"));
        await typeAndSubmit("Create a template");

        await waitFor(() => {
            expect(screen.getByText("I encountered an error. Could you please try again?")).toBeInTheDocument();
        });
    });

    test("shows loading indicator while waiting for response", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });

        mockPost.mockReturnValueOnce(new Promise(() => {}));
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a template" } });

        await act(async () => {
            fireEvent.submit(textarea.closest("form")!);
        });

        await waitFor(() => {
            expect(screen.getByLabelText("Waiting for response")).toBeInTheDocument();
        });
    });
    test("textarea is disabled while loading", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        mockPost.mockReturnValueOnce(new Promise(() => {}));
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a template" } });
        await act(async () => {
            fireEvent.submit(textarea.closest("form")!);
        });
        await waitFor(() => {
            expect(textarea).toBeDisabled();
        });
    });
    test("send button is disabled while loading", async () => {
        setupInitSuccess();
        renderChat();
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        mockPost.mockReturnValueOnce(new Promise(() => {}));
        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Create a template" } });
        await act(async () => {
            fireEvent.submit(textarea.closest("form")!);
        });
        const sendButton = screen.getByRole("button", { name: /send message/i });
        await waitFor(() => {
            expect(sendButton).toBeDisabled();
        });
    });
});

describe("PromptBuilderChat initial message", () => {
    test("sends initialMessage automatically on mount", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Great, I'll create that for you.",
                    templateUpdates: { name: "Review Template" },
                    confidence: 0.9,
                    readyToSave: true,
                    isError: false,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        renderChat({ initialMessage: "Create a code review template" });
        await waitFor(() => {
            expect(screen.getByText("Create a code review template")).toBeInTheDocument();
        });
        await waitFor(() => {
            expect(screen.getByText("Great, I'll create that for you.")).toBeInTheDocument();
        });
    });

    test("calls onConfigUpdate when initial message response has is_error false and non-empty template_updates", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Done!",
                    templateUpdates: { name: "Review Template", category: "Dev" },
                    confidence: 0.9,
                    readyToSave: true,
                    isError: false,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);

        const onConfigUpdate = vi.fn();
        renderChat({ initialMessage: "Create a review template", onConfigUpdate });

        await waitFor(() => {
            expect(onConfigUpdate).toHaveBeenCalledWith({ name: "Review Template", category: "Dev" });
        });
    });
    test("does not call onConfigUpdate when initial message response has is_error true with non-empty template_updates", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Auth error",
                    templateUpdates: { name: "Should not apply" },
                    confidence: 0,
                    readyToSave: false,
                    isError: true,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        const onConfigUpdate = vi.fn();
        renderChat({ initialMessage: "Create a template", onConfigUpdate });
        await waitFor(() => {
            expect(screen.getByText("Auth error")).toBeInTheDocument();
        });
        expect(onConfigUpdate).not.toHaveBeenCalled();
    });
    test.each([
        { isWarning: true, notice: "inlineNotice-warning" },
        { isWarning: false, notice: "inlineNotice-error" },
    ])("renders a refused initial message as $notice when isWarning is $isWarning", async ({ isWarning, notice }) => {
        setupInitSuccess();
        mockPost.mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({ message: "Your usage budget is used up.", templateUpdates: {}, confidence: 0, readyToSave: false, isError: true, isWarning }),
        });

        renderChat({ initialMessage: "Create a template" });

        expect(await screen.findByTestId(notice)).toHaveTextContent("Your usage budget is used up.");
    });

    test("calls onReadyToSave from initial message response", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Done!",
                    templateUpdates: {},
                    confidence: 0.9,
                    readyToSave: true,
                    isError: false,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        const onReadyToSave = vi.fn();
        renderChat({ initialMessage: "Create a template", onReadyToSave });
        await waitFor(() => {
            expect(onReadyToSave).toHaveBeenCalledWith(true);
        });
    });
    test("does not call onReadyToSave when initial message response has is_error", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Auth error",
                    templateUpdates: {},
                    confidence: 0,
                    readyToSave: false,
                    isError: true,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        const onReadyToSave = vi.fn();
        renderChat({ initialMessage: "Create a template", onReadyToSave });
        await waitFor(() => {
            expect(screen.getByText("Auth error")).toBeInTheDocument();
        });
        expect(onReadyToSave).not.toHaveBeenCalled();
    });
    test("shows error when initial message API returns non-ok response", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: false,
            json: () => Promise.resolve({}),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        renderChat({ initialMessage: "Create a template" });
        await waitFor(() => {
            expect(screen.getByText(/conversation history is too long/)).toBeInTheDocument();
        });
    });
    test("shows error when initial message API throws", async () => {
        setupInitSuccess();
        mockPost.mockRejectedValueOnce(new Error("Network failure"));

        renderChat({ initialMessage: "Create a template" });

        await waitFor(() => {
            expect(screen.getByText(/encountered an error processing your request/)).toBeInTheDocument();
        });
    });
    test("passes conversation history to initial message API with fullResponse option", async () => {
        setupInitSuccess();
        const chatResponse = {
            ok: true,
            json: () =>
                Promise.resolve({
                    message: "Got it.",
                    templateUpdates: {},
                    confidence: 0.5,
                    readyToSave: false,
                    isError: false,
                }),
        };
        mockPost.mockResolvedValueOnce(chatResponse);
        renderChat({ initialMessage: "Create a template" });
        await waitFor(() => {
            expect(mockPost).toHaveBeenCalledWith(
                "/api/v1/prompts/chat",
                expect.objectContaining({
                    message: "Create a template",
                    conversationHistory: [{ role: "assistant", content: INIT_GREETING }],
                    currentTemplate: defaultConfig,
                }),
                { fullResponse: true }
            );
        });
    });
});
describe("PromptBuilderChat STT feature flag", () => {
    test("does not render AudioRecorder when speechToText feature is disabled", async () => {
        setupInitSuccess();
        renderChat({}, { speechToTextEnabled: false });
        await waitFor(() => {
            expect(screen.getByText("AI Builder")).toBeInTheDocument();
        });
        expect(screen.queryByRole("button", { name: /record/i })).not.toBeInTheDocument();
    });
});
