// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";
import { Ban, Link2, Paperclip, Quote, Send, Upload, X } from "lucide-react";
import type { ChangeEvent, ClipboardEvent, FormEvent } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { useFilteredChatArtifacts } from "@/api/artifacts";
import { useModelConfigStatus } from "@/api/models";

import { useAudioSettings, useBuilderContext, useChatContext, useConfigContext, useUIModeContext } from "@/providers/contexts";

import { AudioRecorder } from "@/common/audio";

import { buildMessageFromDOM, detectMentionTrigger, insertMention } from "@/utils/mentionUtils";
import { detectVariables } from "@/utils/promptUtils";
import { addRecentMention } from "@/utils/recentMentions";
import { consumePendingPrefill, hasPendingPrefill, stashPendingPrefill } from "@/utils/redirectPrefill";
import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";
import { BEFORE_RECOVERY_RELOAD_EVENT } from "@/utils/staleChunkRecovery";

import type { PromptGroup } from "@/types/prompts";

import { ComponentChip, type ComponentChipData } from "@/features/builder/ComponentChip";

import { ConfirmationDialog, MessageBanner, VariableDialog } from "@/common";
import { useAgentSelection, useDragAndDrop, useIsMentionsEnabled, useIsMobile, useIsTouchPrimaryDevice } from "@/hooks";
import type { AgentCardInfo, AttachedArtifactRef, Person } from "@/types";
import { Button, Dialog, DialogContent, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui";
import { escapeMarkdown, getErrorMessage, isImeComposing, uuid } from "@/utils";

import { ContextUsageIndicator } from "./ContextUsageIndicator";
import { ArtifactAttachmentCard } from "./file/ArtifactAttachmentCard";
import { AttachArtifactDialog } from "./file/AttachArtifactDialog";
import { mergeAttachments } from "./file/attachmentUtils";
import { FileBadge } from "./file/FileBadge";
import { FileUploadCard } from "./file/FileUploadCard";
import { LocalFilePreview } from "./file/LocalFilePreview";
import { StandaloneArtifactPreview } from "./file/StandaloneArtifactPreview";
import { useChatAttachments } from "./hooks/useChatAttachments";
import { useChatInputAutoFocus } from "./hooks/useChatInputAutoFocus";
import { useSessionAgentAvailability } from "./hooks/useSessionAgentAvailability";
import { MentionContentEditable } from "./MentionContentEditable";
import { MentionsCommand } from "./MentionsCommand";
import { PasteActionDialog } from "./paste/PasteActionDialog";
import { PendingPastedTextBadge } from "./paste/PastedTextBadge";
import { createPastedTextItem, defaultSnippetFilename, isLargeText, uploadPastedTextItems } from "./paste/pasteUtils";
import { SNIP_TO_CHAT_EVENT, type SnipToChatEventDetail } from "./preview/renderers/PdfRenderer";
import { type ChatCommand, PromptsCommand } from "./PromptsCommand";

const createEnhancedMessage = (command: ChatCommand, conversationContext?: string): string => {
    if (command === "create-template") {
        if (!conversationContext) {
            return "Help me create a new prompt template.";
        }

        return [
            "I want to create a reusable prompt template based on this conversation I just had:",
            "",
            "<conversationHistory>",
            conversationContext,
            "</conversationHistory>",
            "",
            "Please help me create a prompt template by:",
            "",
            "1. **Analyzing the Pattern**: Identify the core task/question pattern in this conversation",
            "2. **Extracting Variables**: Determine which parts should be variables (use {{variable_name}} syntax)",
            "3. **Generalizing**: Make it reusable for similar tasks",
            "4. **Suggesting Metadata**: Recommend a name, description, category, and chat shortcut",
            "",
            "Focus on capturing what made this conversation successful so it can be reused with different inputs.",
        ].join("\n");
    }

    return "";
};

type ContextQuote = { id: string; text: string; sourceId?: string; component?: ComponentChipData };

const addUniqueQuote = (quotes: ContextQuote[], text: string, sourceId?: string, component?: ComponentChipData): ContextQuote[] => {
    if (quotes.some(q => q.text === text)) return quotes;
    return [...quotes, { id: uuid(), text, sourceId, component }];
};

interface PlaceholderState {
    isRecording: boolean;
    selectedAgentUnavailable: boolean;
    isMobile: boolean;
    allowPrompts: boolean;
    mentionsEnabled: boolean;
}

const computeInputPlaceholder = ({ isRecording, selectedAgentUnavailable, isMobile, allowPrompts, mentionsEnabled }: PlaceholderState): string => {
    if (isRecording) return "Recording...";
    if (selectedAgentUnavailable) return "Select an agent below to continue this conversation.";
    if (isMobile) return "How can I help you today?";
    if (!allowPrompts) return mentionsEnabled ? "How can I help you today? (Type '@' to mention someone)" : "How can I help you today?";
    if (mentionsEnabled) return "How can I help you today? (Type '/' to insert a prompt, '@' to mention someone)";
    return "How can I help you today? (Type '/' to insert a prompt)";
};

export const ChatInputArea: React.FC<{
    agents: AgentCardInfo[];
    scrollToBottom?: () => void;
    allowAgentSelection?: boolean;
    formHeader?: React.ReactNode;
    autoFocus?: boolean;
}> = ({ agents = [], scrollToBottom, allowAgentSelection = true, formHeader, autoFocus = false }) => {
    const navigate = useNavigate();
    const location = useLocation();
    const {
        isResponding,
        isCancelling,
        selectedAgentName,
        sessionId,
        setSessionId,
        handleSubmit,
        handleCancel,
        uploadArtifactFile,
        displayError,
        addNotification,
        messages,
        startNewChatWithPrompt,
        pendingPrompt,
        clearPendingPrompt,
        handleSwitchSession,
        agentSessionRoutes,
    } = useChatContext();
    const { allArtifacts, isSessionScoped } = useFilteredChatArtifacts();
    const { builderMode, selectedBuilderNode, setSelectedBuilderNode } = useBuilderContext();
    const { allowMentions, allowPrompts, allowSpeech, showAgentSelector } = useUIModeContext();

    const { handleAgentSelection, retargetAgent, switchConfirmOpen, setSwitchConfirmOpen, confirmAgentSwitch, cancelAgentSwitch } = useAgentSelection();
    const isMobile = useIsMobile();
    const isTouchPrimary = useIsTouchPrimaryDevice();
    const { settings } = useAudioSettings();
    const { speechToTextEnabled } = useConfigContext();
    const { value: artifactAttachmentEnabled } = useBooleanFlagDetails("artifact_attachment", false);
    const { data: modelConfigStatus } = useModelConfigStatus();
    const modelNotConfigured = modelConfigStatus && !modelConfigStatus.configured;

    const sttEnabled = Boolean(speechToTextEnabled) && allowSpeech;
    const mentionsEnabled = useIsMentionsEnabled() && allowMentions;

    const fileInputRef = useRef<HTMLInputElement>(null);

    const {
        selectedFiles,
        selectedArtifactRefs,
        pendingPastedTextItems,
        selectedPendingPasteId,
        previewingArtifact,
        previewingLocalFile,
        showAttachArtifactDialog,
        showArtifactForm,
        setPendingPastedTextItems,
        setSelectedArtifactRefs,
        setPreviewingArtifact,
        setPreviewingLocalFile,
        setShowAttachArtifactDialog,
        addFiles: attachFiles,
        handleAttachArtifacts,
        handleRemoveArtifactRef,
        handleRemoveFile,
        handleRemovePendingPaste,
        handlePendingPasteClick,
        handleSaveMetadata,
        handleCancelArtifactForm,
        handlePreviewArtifactDownload,
        handlePreviewGoToChat,
        handlePreviewGoToProject,
        captureAndClearForSubmit,
        restoreFromCapture,
    } = useChatAttachments({
        isResponding,
        addNotification,
        displayError,
        handleSwitchSession,
        navigate,
        agentSessionRoutes,
    });

    const reservedArtifactNames = useMemo(() => (isSessionScoped ? allArtifacts.map(a => a.filename) : []), [isSessionScoped, allArtifacts]);

    const configuredSnippetNames = useMemo(() => pendingPastedTextItems.flatMap(item => (item.isConfigured && item.filename ? [item.filename] : [])), [pendingPastedTextItems]);

    const addFiles = useCallback((files: File[]) => attachFiles(files, [...reservedArtifactNames, ...configuredSnippetNames]), [attachFiles, reservedArtifactNames, configuredSnippetNames]);

    const reservedNamesWithAttachments = useMemo(() => [...reservedArtifactNames, ...selectedFiles.map(f => f.name)], [reservedArtifactNames, selectedFiles]);

    const [contextText, setContextText] = useState<string | null>(null);
    const [contextSourceId, setContextSourceId] = useState<string | null>(null);
    const [showContextBadge, setShowContextBadge] = useState(false);
    const [contextQuotes, setContextQuotes] = useState<ContextQuote[]>([]);

    const chatInputRef = useRef<HTMLDivElement>(null);
    const prevIsRespondingRef = useRef<boolean>(isResponding);

    const [inputValue, setInputValue] = useState<string>("");
    const [desiredCursorPosition, setDesiredCursorPosition] = useState<number | undefined>(undefined);

    const [showPromptsCommand, setShowPromptsCommand] = useState(false);
    const [showMentionsCommand, setShowMentionsCommand] = useState(false);
    const [mentionSearchQuery, setMentionSearchQuery] = useState("");
    const [mentionMap, setMentionMap] = useState<Map<string, Person>>(new Map());
    const [disambiguatedIds, setDisambiguatedIds] = useState<Set<string>>(new Set());

    const [showVariableDialog, setShowVariableDialog] = useState(false);
    const [pendingPromptGroup, setPendingPromptGroup] = useState<PromptGroup | null>(null);

    const [sttError, setSttError] = useState<string | null>(null);

    const [isRecording, setIsRecording] = useState(false);

    const prevSessionIdRef = useRef<string | null>(sessionId);

    const processedLocationStateRef = useRef<string | null>(null);

    useEffect(() => {
        if (location.state?.promptText && processedLocationStateRef.current !== location.state.groupId) {
            const { promptText, groupId, groupName, agentName } = location.state;

            processedLocationStateRef.current = groupId;

            navigate(location.pathname, { replace: true, state: {} });

            startNewChatWithPrompt({ promptText, groupId, groupName, agentName });

            setTimeout(() => {
                processedLocationStateRef.current = null;
            }, 1000);
        }
    }, [location.state, location.pathname, navigate, startNewChatWithPrompt]);

    useEffect(() => {
        if (pendingPrompt && selectedAgentName) {
            const { promptText, groupId, groupName } = pendingPrompt;

            const variables = detectVariables(promptText);
            if (variables.length > 0) {
                setPendingPromptGroup({
                    id: groupId,
                    name: groupName,
                    productionPrompt: { promptText },
                } as PromptGroup);
                setShowVariableDialog(true);
            } else {
                setInputValue(promptText);
                setTimeout(() => {
                    chatInputRef.current?.focus();
                }, 100);
            }

            clearPendingPrompt();
        }
    }, [pendingPrompt, selectedAgentName, clearPendingPrompt]);

    useEffect(() => {
        if (pendingPrompt) {
            prevSessionIdRef.current = sessionId;
            return;
        }

        if (prevSessionIdRef.current && prevSessionIdRef.current !== sessionId) {
            setInputValue("");
            setShowPromptsCommand(false);
            setPendingPastedTextItems([]);
            setSelectedArtifactRefs([]);
        }
        prevSessionIdRef.current = sessionId;
        setContextText(null);
    }, [pendingPrompt, sessionId, setPendingPastedTextItems, setSelectedArtifactRefs]);

    useEffect(() => {
        if (prevIsRespondingRef.current && !isResponding) {
            setTimeout(() => {
                chatInputRef.current?.focus();
            }, 100);
        }
        prevIsRespondingRef.current = isResponding;
    }, [isResponding]);

    useEffect(() => {
        const handleFocusChatInput = () => {
            setTimeout(() => {
                chatInputRef.current?.focus();
            }, 100);
        };

        window.addEventListener("focus-chat-input", handleFocusChatInput);
        return () => {
            window.removeEventListener("focus-chat-input", handleFocusChatInput);
        };
    }, []);

    useEffect(() => {
        const handleSetChatInput = (event: Event) => {
            const customEvent = event as CustomEvent<{ text: string }>;
            const text = customEvent.detail.text;
            setInputValue(text);
            setDesiredCursorPosition(text.length);
            requestAnimationFrame(() => {
                chatInputRef.current?.focus();
                setDesiredCursorPosition(undefined);
            });
        };

        window.addEventListener("set-chat-input", handleSetChatInput);
        return () => {
            window.removeEventListener("set-chat-input", handleSetChatInput);
        };
    }, []);

    useEffect(() => {
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS);
    }, []);

    useEffect(() => {
        const pending = consumePendingPrefill(sessionId);
        if (!pending) return;
        setInputValue(pending);
        setDesiredCursorPosition(pending.length);
        requestAnimationFrame(() => {
            chatInputRef.current?.focus();
            setDesiredCursorPosition(undefined);
        });
    }, [sessionId]);

    useEffect(() => {
        const stash = () => {
            if (!inputValue.trim()) return;
            try {
                if (hasPendingPrefill()) return;
                stashPendingPrefill(inputValue, null);
            } catch {}
        };
        globalThis.addEventListener(BEFORE_RECOVERY_RELOAD_EVENT, stash);
        return () => globalThis.removeEventListener(BEFORE_RECOVERY_RELOAD_EVENT, stash);
    }, [inputValue]);

    useEffect(() => {
        const handleFollowUp = async (event: Event) => {
            const customEvent = event as CustomEvent;
            const { text, prompt, autoSubmit, sourceMessageId, component: rawComponent } = customEvent.detail;
            const component: ComponentChipData | undefined = rawComponent && typeof rawComponent.name === "string" && typeof rawComponent.type === "string" ? rawComponent : undefined;

            if (prompt) {
                setContextText(text);
                setContextSourceId(sourceMessageId ?? null);
                setInputValue(prompt + " ");

                if (autoSubmit) {
                    setTimeout(async () => {
                        const fullMessage = `${prompt}\n\nContext: "${escapeMarkdown(text)}"`;
                        const fakeEvent = new Event("submit") as unknown as FormEvent;
                        await handleSubmit(fakeEvent, [], fullMessage);
                        setContextText(null);
                        setContextSourceId(null);
                        setShowContextBadge(false);
                        setInputValue("");
                        scrollToBottom?.();
                    }, 50);
                    return;
                }
            } else {
                setContextQuotes(prev => addUniqueQuote(prev, text, sourceMessageId ?? undefined, component));
                setContextText(text);
                setContextSourceId(sourceMessageId ?? null);
                setShowContextBadge(true);
            }

            setTimeout(() => {
                chatInputRef.current?.focus();
            }, 100);
        };

        window.addEventListener("follow-up-question", handleFollowUp);
        return () => {
            window.removeEventListener("follow-up-question", handleFollowUp);
        };
    }, [handleSubmit, scrollToBottom]);

    useEffect(() => {
        const handleSnipToChat = (event: Event) => {
            const customEvent = event as CustomEvent<SnipToChatEventDetail>;
            const { file } = customEvent.detail;

            addFiles([file]);

            setTimeout(() => {
                chatInputRef.current?.focus();
            }, 100);
        };

        window.addEventListener(SNIP_TO_CHAT_EVENT, handleSnipToChat);
        return () => {
            window.removeEventListener(SNIP_TO_CHAT_EVENT, handleSnipToChat);
        };
    }, [addFiles]);

    const handleFileSelect = () => {
        if (!isResponding) {
            fileInputRef.current?.click();
        }
    };

    const handleFileChange = (event: ChangeEvent<HTMLInputElement>) => {
        const files = event.target.files;
        if (files) {
            addFiles(Array.from(files));
        }

        if (event.target) {
            event.target.value = "";
        }

        setTimeout(() => {
            chatInputRef.current?.focus();
        }, 100);
    };

    const handlePaste = async (event: ClipboardEvent<Element>) => {
        if (isResponding) return;

        const clipboardData = event.clipboardData;
        if (!clipboardData) return;

        if (clipboardData.files && clipboardData.files.length > 0) {
            const pastedText = clipboardData.getData("text");
            const allFilesAreImages = Array.from(clipboardData.files).every(file => file.type.startsWith("image/"));

            const isRichTextCopy = pastedText && allFilesAreImages;
            if (!isRichTextCopy) {
                event.preventDefault();

                addFiles(Array.from(clipboardData.files));
                return;
            }
        }

        const pastedText = clipboardData.getData("text");
        if (pastedText && isLargeText(pastedText)) {
            event.preventDefault();
            const newItem = createPastedTextItem(pastedText);
            setPendingPastedTextItems(prev => [...prev, newItem]);
        }
    };

    const prevArtifactRefsLenRef = useRef(selectedArtifactRefs.length);
    useEffect(() => {
        if (selectedArtifactRefs.length > prevArtifactRefsLenRef.current) {
            chatInputRef.current?.focus();
        }
        prevArtifactRefsLenRef.current = selectedArtifactRefs.length;
    }, [selectedArtifactRefs.length]);

    const { unavailable: selectedAgentUnavailable, bannerMessage: unavailableAgentMessage } = useSessionAgentAvailability(agents, allowAgentSelection);
    const isComposerDisabled = useMemo(() => isRecording || selectedAgentUnavailable, [isRecording, selectedAgentUnavailable]);
    useChatInputAutoFocus({ ref: chatInputRef, enabled: autoFocus, composerDisabled: isComposerDisabled });
    const agentPickerValue = useMemo(() => (selectedAgentUnavailable ? "" : selectedAgentName), [selectedAgentUnavailable, selectedAgentName]);

    const isSubmittingEnabled = useMemo(
        () => !isResponding && !modelNotConfigured && !selectedAgentUnavailable && (inputValue?.trim() || selectedFiles.length !== 0 || pendingPastedTextItems.length !== 0 || selectedArtifactRefs.length !== 0),
        [isResponding, modelNotConfigured, selectedAgentUnavailable, inputValue, selectedFiles, pendingPastedTextItems, selectedArtifactRefs]
    );

    const isSubmittingRef = useRef(false);
    const onSubmit = async (event: FormEvent) => {
        event.preventDefault();
        if (isSubmittingRef.current) return;
        if (!isSubmittingEnabled) return;

        isSubmittingRef.current = true;
        let fullMessage = chatInputRef.current ? buildMessageFromDOM(chatInputRef.current).trim() : inputValue.trim();

        const displayHtml = chatInputRef.current?.innerHTML || null;

        if (contextQuotes.length > 0) {
            const quotesText = contextQuotes.map(q => `- "${escapeMarkdown(q.text)}"`).join("\n");
            fullMessage = `Context:\n${quotesText}\n\n${fullMessage}`;
        } else if (contextText && showContextBadge) {
            fullMessage = `Context: "${escapeMarkdown(contextText)}"\n\n${fullMessage}`;
        }

        const attachmentsCapture = captureAndClearForSubmit();
        const capturedContextText = contextText;
        const capturedContextSourceId = contextSourceId;
        const capturedShowContextBadge = showContextBadge;
        const capturedInputValue = inputValue;
        const capturedMentionMap = new Map(mentionMap);
        const capturedInnerHTML = chatInputRef.current?.innerHTML || "";
        const capturedContextQuotes = [...contextQuotes];

        const resetTextInput = () => {
            setInputValue("");
            setMentionMap(new Map());
            setContextText(null);
            setContextSourceId(null);
            setShowContextBadge(false);
            setContextQuotes([]);
            if (chatInputRef.current) {
                chatInputRef.current.innerHTML = "";
            }
        };

        const restoreInputState = () => {
            const currentInnerHTML = chatInputRef.current?.innerHTML || "";
            const userHasTypedNew = currentInnerHTML !== "";
            if (!userHasTypedNew) {
                setInputValue(capturedInputValue);
                setMentionMap(capturedMentionMap);
                if (chatInputRef.current) {
                    chatInputRef.current.innerHTML = capturedInnerHTML;
                }
            }
            restoreFromCapture(attachmentsCapture);
            setContextText(current => (current !== null ? current : capturedContextText));
            setContextSourceId(current => (current !== null ? current : capturedContextSourceId));
            setShowContextBadge(current => (current ? current : capturedShowContextBadge));
            setContextQuotes(current => (current.length > 0 ? current : capturedContextQuotes));
        };

        resetTextInput();

        try {
            const configuredCaptureNames = attachmentsCapture.pastedTextItems.flatMap(item => (item.isConfigured && item.filename ? [item.filename] : []));
            const filesToSend = mergeAttachments([], attachmentsCapture.files, [...reservedArtifactNames, ...configuredCaptureNames]);

            const {
                uploaded: uploadedArtifacts,
                errors: uploadErrors,
                sessionId: effectiveSessionId,
            } = await uploadPastedTextItems(attachmentsCapture.pastedTextItems, sessionId, [...reservedArtifactNames, ...filesToSend.map(f => f.name)], uploadArtifactFile);
            if (effectiveSessionId !== sessionId) {
                setSessionId(effectiveSessionId);
            }

            if (uploadErrors.length > 0) {
                displayError({ title: "Some uploads failed", error: uploadErrors.join("; "), errorSource: "api" });
            }

            const artifactReferences: AttachedArtifactRef[] = [
                ...uploadedArtifacts.map(item => ({ uri: item.uri, filename: item.filename, mimeType: item.mimeType })),
                ...attachmentsCapture.artifactRefs
                    .filter(artifact => !!artifact.uri)
                    .map(artifact => ({
                        uri: artifact.uri!,
                        filename: artifact.filename,
                        mimeType: artifact.mimeType,
                    })),
            ];

            const contextQuoteToPass = capturedContextText && capturedShowContextBadge ? capturedContextText : null;
            const contextQuoteSourceIdToPass = capturedContextSourceId && capturedShowContextBadge ? capturedContextSourceId : null;
            await handleSubmit(event, filesToSend, fullMessage, effectiveSessionId || null, displayHtml, contextQuoteToPass, contextQuoteSourceIdToPass, artifactReferences);
            scrollToBottom?.();
        } catch (error) {
            restoreInputState();
            displayError({ title: "Failed to Send Message", error: getErrorMessage(error), errorSource: "api" });
        } finally {
            isSubmittingRef.current = false;
        }
    };

    const handleFilesDropped = (files: File[]) => {
        if (isResponding) return;

        addFiles(files);
    };

    const { isDragging, handleDragEnter, handleDragOver, handleDragLeave, handleDrop } = useDragAndDrop({
        onFilesDropped: handleFilesDropped,
        disabled: isResponding,
    });

    const getCursorPosition = (): number => {
        const selection = window.getSelection();
        if (!selection || selection.rangeCount === 0) return 0;
        if (!chatInputRef.current) return 0;

        const range = selection.getRangeAt(0);
        let position = 0;
        let found = false;

        const walker = document.createTreeWalker(chatInputRef.current, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
            acceptNode: (node: Node) => {
                if (node.nodeType === Node.TEXT_NODE) {
                    if (node.parentElement?.classList.contains("mention-chip")) {
                        return NodeFilter.FILTER_REJECT;
                    }
                }
                return NodeFilter.FILTER_ACCEPT;
            },
        });

        let node: Node | null;
        node = walker.nextNode();
        while (node && !found) {
            if (node.nodeType === Node.TEXT_NODE) {
                if (node === range.startContainer) {
                    position += range.startOffset;
                    found = true;
                } else {
                    position += node.textContent?.length ?? 0;
                }
            } else if (node.nodeType === Node.ELEMENT_NODE) {
                const el = node as HTMLElement;
                if (el.classList.contains("mention-chip")) {
                    const internal = el.getAttribute("data-internal") ?? "";
                    position += internal.length;
                    if (range.startContainer === el || el.contains(range.startContainer)) {
                        found = true;
                    }
                } else if (el.tagName === "BR") {
                    position += 1;
                }
            }
            node = walker.nextNode();
        }

        return position;
    };

    const handleInputChange = (value: string) => {
        setInputValue(value);

        const cursorPosition = getCursorPosition();
        const textBeforeCursor = value.substring(0, cursorPosition);

        if (allowPrompts && textBeforeCursor === "/") {
            setShowPromptsCommand(true);
            setShowMentionsCommand(false);
        } else if (showPromptsCommand && !textBeforeCursor.startsWith("/")) {
            setShowPromptsCommand(false);
        }

        if (mentionsEnabled) {
            const mentionQuery = detectMentionTrigger(value, cursorPosition);
            if (mentionQuery !== null) {
                setMentionSearchQuery(mentionQuery);
                setShowMentionsCommand(true);
                setShowPromptsCommand(false);
            } else if (showMentionsCommand) {
                setShowMentionsCommand(false);
                setMentionSearchQuery("");
            }
        }
    };

    const handlePromptSelect = (promptText: string) => {
        const cursorPosition = getCursorPosition();
        const textBeforeCursor = inputValue.substring(0, cursorPosition);
        const textAfterCursor = inputValue.substring(cursorPosition);

        const lastSlashIndex = textBeforeCursor.lastIndexOf("/");
        const newText = textBeforeCursor.substring(0, lastSlashIndex) + promptText + textAfterCursor;

        setInputValue(newText);
        setShowPromptsCommand(false);

        setTimeout(() => {
            chatInputRef.current?.focus();
        }, 100);
    };

    const handlePersonSelect = (person: Person) => {
        const cursorPosition = getCursorPosition();

        const { newText, newCursorPosition } = insertMention(inputValue, cursorPosition, person);

        let needsDisambiguation = false;
        let existingPersonId: string | undefined;

        for (const [id, existingPerson] of mentionMap.entries()) {
            if (existingPerson.displayName === person.displayName && id !== person.id) {
                needsDisambiguation = true;
                existingPersonId = id;
                break;
            }
        }

        setMentionMap(prev => {
            const updated = new Map(prev);
            updated.set(person.id, person);
            return updated;
        });

        if (needsDisambiguation && existingPersonId) {
            setDisambiguatedIds(prev => {
                const updated = new Set(prev);
                updated.add(existingPersonId);
                updated.add(person.id);
                return updated;
            });
        }

        addRecentMention(person);

        setInputValue(newText);
        setDesiredCursorPosition(newCursorPosition);
        setShowMentionsCommand(false);
        setMentionSearchQuery("");

        setTimeout(() => {
            setDesiredCursorPosition(undefined);
        }, 10);
    };

    const handleChatCommand = (command: ChatCommand, context?: string) => {
        const enhancedMessage = createEnhancedMessage(command, context);

        if (command === "create-template") {
            navigate("/prompts/new?mode=ai-assisted", {
                state: { taskDescription: enhancedMessage },
            });

            setInputValue("");
            setShowPromptsCommand(false);
        }
    };

    const handleVariableSubmit = (processedPrompt: string) => {
        setInputValue(processedPrompt);
        setShowVariableDialog(false);
        setPendingPromptGroup(null);
        setTimeout(() => {
            chatInputRef.current?.focus();
        }, 100);
    };

    const handleTranscription = useCallback(
        (text: string) => {
            const newText = inputValue ? `${inputValue} ${text}` : text;
            setInputValue(newText);

            setTimeout(() => {
                chatInputRef.current?.focus();
            }, 100);
        },
        [inputValue]
    );

    const handleTranscriptionError = useCallback((error: string) => {
        setSttError(error);
    }, []);

    const handleAgentChange = useCallback(
        (agentName: string) => {
            if (selectedAgentUnavailable) {
                retargetAgent(agentName);
            } else {
                handleAgentSelection(agentName);
            }
        },
        [selectedAgentUnavailable, retargetAgent, handleAgentSelection]
    );

    const inputPlaceholder = computeInputPlaceholder({ isRecording, selectedAgentUnavailable, isMobile, allowPrompts, mentionsEnabled });

    const removeContextQuote = useCallback((id: string) => {
        setContextQuotes(prev => {
            const next = prev.filter(item => item.id !== id);
            if (next.length === 0) {
                setContextText(null);
                setShowContextBadge(false);
            }
            return next;
        });
    }, []);

    let contextBadges: React.ReactNode = null;
    if (contextQuotes.length > 0) {
        contextBadges = (
            <div className="mb-2 flex flex-wrap gap-1.5 overflow-hidden">
                {contextQuotes.map(q =>
                    q.component ? (
                        <ComponentChip key={q.id} component={q.component} onRemove={() => removeContextQuote(q.id)} />
                    ) : (
                        <div key={q.id} className="inline-flex max-w-full items-center gap-1.5 overflow-hidden rounded-md border bg-(--secondary-w10) px-2.5 py-1.5 text-xs">
                            <Quote className="h-3 w-3 shrink-0 text-(--secondary-text-wMain)" />
                            <span className="min-w-0 truncate text-(--secondary-text-wMain) italic">"{q.text}"</span>
                            <Button variant="ghost" className="h-4 w-4 shrink-0" onClick={() => removeContextQuote(q.id)} tooltip="Remove">
                                <X />
                            </Button>
                        </div>
                    )
                )}
            </div>
        );
    } else if (showContextBadge && contextText) {
        contextBadges = (
            <div className="mb-2 overflow-hidden">
                <div className="inline-flex max-w-full items-center gap-2 overflow-hidden rounded-md border bg-(--secondary-w10) px-3 py-2 text-sm">
                    <Quote className="h-4 w-4 shrink-0 text-(--secondary-text-wMain)" />
                    <span className="min-w-0 flex-1 truncate text-(--secondary-text-wMain) italic">"{contextText}"</span>
                    <Button
                        variant="ghost"
                        className="h-5 w-5 shrink-0"
                        onClick={() => {
                            setContextText(null);
                            setShowContextBadge(false);
                        }}
                        tooltip="Remove context"
                    >
                        <X />
                    </Button>
                </div>
            </div>
        );
    }

    return (
        <div
            className={`rounded-lg border bg-(--background-w10) p-4 shadow-sm ${isDragging ? "border-dotted border-(--primary-wMain) bg-(--background-w20)" : ""}`}
            onDragEnter={handleDragEnter}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
        >
            {formHeader}

            {selectedAgentUnavailable && unavailableAgentMessage && (
                <div className="mb-3">
                    <MessageBanner variant="warning" message={unavailableAgentMessage} />
                </div>
            )}

            {sttError && (
                <div className="mb-3">
                    <MessageBanner variant="error" message={sttError} dismissible onDismiss={() => setSttError(null)} />
                </div>
            )}

            <input type="file" ref={fileInputRef} className="hidden" multiple onChange={handleFileChange} accept="*/*" disabled={isResponding} />

            {contextBadges}

            {(selectedFiles.length > 0 || selectedArtifactRefs.length > 0 || pendingPastedTextItems.length > 0) && (
                <div className="mb-2 flex max-h-32 flex-wrap items-start gap-2 overflow-y-auto pt-2 pl-2">
                    {selectedFiles.map((file, index) =>
                        artifactAttachmentEnabled ? (
                            <FileUploadCard key={`file-${file.name}-${file.lastModified}-${index}`} file={file} onClick={() => setPreviewingLocalFile(file)} onRemove={() => handleRemoveFile(index)} />
                        ) : (
                            <FileBadge key={`file-${file.name}-${file.lastModified}-${index}`} fileName={file.name} onRemove={() => handleRemoveFile(index)} />
                        )
                    )}
                    {artifactAttachmentEnabled &&
                        selectedArtifactRefs.map(artifact => (
                            <ArtifactAttachmentCard key={`artifact-${artifact.uri}`} artifact={artifact} onClick={() => setPreviewingArtifact(artifact)} onRemove={() => artifact.uri && handleRemoveArtifactRef(artifact.uri)} />
                        ))}
                    {pendingPastedTextItems.map((item, index) => {
                        const defaultFilename = item.isConfigured ? "snippet.txt" : defaultSnippetFilename(pendingPastedTextItems, index, reservedNamesWithAttachments);

                        return (
                            <PendingPastedTextBadge
                                key={item.id}
                                content={item.content}
                                onClick={() => handlePendingPasteClick(item.id)}
                                onRemove={() => handleRemovePendingPaste(item.id)}
                                isConfigured={item.isConfigured}
                                filename={item.filename}
                                defaultFilename={defaultFilename}
                            />
                        );
                    })}
                </div>
            )}

            {(() => {
                const selectedItem = pendingPastedTextItems.find(item => item.id === selectedPendingPasteId);
                const selectedIndex = pendingPastedTextItems.findIndex(item => item.id === selectedPendingPasteId);

                const allExistingFilenames = new Set(reservedNamesWithAttachments);

                let tempFilename = "snippet.txt";
                pendingPastedTextItems.forEach(item => {
                    if (item.id === selectedPendingPasteId) {
                        return;
                    }

                    if (item.isConfigured && item.filename) {
                        allExistingFilenames.add(item.filename);
                    } else {
                        if (allExistingFilenames.has(tempFilename)) {
                            let counter = 2;
                            while (allExistingFilenames.has(`snippet-${counter}.txt`)) {
                                counter++;
                            }
                            tempFilename = `snippet-${counter}.txt`;
                        }
                        allExistingFilenames.add(tempFilename);
                        if (allExistingFilenames.has("snippet.txt")) {
                            let counter = 2;
                            while (allExistingFilenames.has(`snippet-${counter}.txt`)) {
                                counter++;
                            }
                            tempFilename = `snippet-${counter}.txt`;
                        } else {
                            tempFilename = "snippet.txt";
                        }
                    }
                });

                let computedDefaultFilename = "snippet.txt";
                if (selectedItem && !selectedItem.isConfigured && selectedIndex >= 0) {
                    const existingFilenames = new Set(reservedNamesWithAttachments);
                    pendingPastedTextItems.slice(0, selectedIndex).forEach(prevItem => {
                        if (prevItem.isConfigured && prevItem.filename) {
                            existingFilenames.add(prevItem.filename);
                        }
                    });
                    let defaultTempFilename = "snippet.txt";
                    for (let i = 0; i < selectedIndex; i++) {
                        const prevItem = pendingPastedTextItems[i];
                        if (!prevItem.isConfigured) {
                            existingFilenames.add(defaultTempFilename);
                            if (existingFilenames.has("snippet.txt")) {
                                let counter = 2;
                                while (existingFilenames.has(`snippet-${counter}.txt`)) {
                                    counter++;
                                }
                                defaultTempFilename = `snippet-${counter}.txt`;
                            }
                        }
                    }
                    if (existingFilenames.has("snippet.txt")) {
                        let counter = 2;
                        while (existingFilenames.has(`snippet-${counter}.txt`)) {
                            counter++;
                        }
                        computedDefaultFilename = `snippet-${counter}.txt`;
                    }
                }

                return (
                    <PasteActionDialog
                        isOpen={showArtifactForm}
                        content={selectedItem?.content || ""}
                        onSaveMetadata={handleSaveMetadata}
                        onCancel={handleCancelArtifactForm}
                        existingArtifacts={Array.from(allExistingFilenames)}
                        initialFilename={selectedItem?.filename}
                        initialMimeType={selectedItem?.mimeType}
                        initialDescription={selectedItem?.description}
                        defaultFilename={computedDefaultFilename}
                    />
                );
            })()}

            <PromptsCommand
                isOpen={showPromptsCommand}
                onClose={() => {
                    setShowPromptsCommand(false);
                }}
                textAreaRef={chatInputRef}
                onPromptSelect={handlePromptSelect}
                messages={messages}
                onReservedCommand={handleChatCommand}
                onBackspaceClose={() => {
                    if (inputValue.startsWith("/")) {
                        setInputValue(inputValue.substring(1));
                    }
                    setShowPromptsCommand(false);
                }}
            />

            {mentionsEnabled && (
                <MentionsCommand
                    isOpen={showMentionsCommand}
                    onClose={() => {
                        setShowMentionsCommand(false);
                        setMentionSearchQuery("");
                    }}
                    textAreaRef={chatInputRef}
                    onPersonSelect={handlePersonSelect}
                    searchQuery={mentionSearchQuery}
                />
            )}

            {showVariableDialog && pendingPromptGroup && (
                <VariableDialog
                    group={pendingPromptGroup}
                    onSubmit={handleVariableSubmit}
                    onClose={() => {
                        setShowVariableDialog(false);
                        setPendingPromptGroup(null);
                    }}
                />
            )}

            <MentionContentEditable
                ref={chatInputRef}
                value={inputValue}
                onChange={handleInputChange}
                cursorPosition={desiredCursorPosition}
                mentionMap={mentionMap}
                disambiguatedIds={disambiguatedIds}
                placeholder={inputPlaceholder}
                className="max-h-50 min-h-12 resize-none overflow-y-auto rounded-2xl border-none p-3 text-base/normal shadow-none focus-visible:outline-none"
                onPaste={handlePaste}
                disabled={isComposerDisabled}
                onKeyDown={event => {
                    if (showMentionsCommand || showPromptsCommand) {
                        return;
                    }

                    if (event.key === "Enter" && !event.shiftKey && !isTouchPrimary && isSubmittingEnabled && !isImeComposing(event)) {
                        onSubmit(event);
                    }
                }}
            />

            {artifactAttachmentEnabled && (
                <Dialog
                    open={previewingArtifact !== null}
                    onOpenChange={open => {
                        if (!open) setPreviewingArtifact(null);
                    }}
                >
                    <DialogContent className="flex h-[85vh] flex-col overflow-hidden rounded-lg sm:max-w-5xl">
                        {previewingArtifact && (
                            <StandaloneArtifactPreview artifact={previewingArtifact} onClose={() => setPreviewingArtifact(null)} onDownload={handlePreviewArtifactDownload} onGoToChat={handlePreviewGoToChat} onGoToProject={handlePreviewGoToProject} />
                        )}
                    </DialogContent>
                </Dialog>
            )}

            {artifactAttachmentEnabled && (
                <Dialog
                    open={previewingLocalFile !== null}
                    onOpenChange={open => {
                        if (!open) setPreviewingLocalFile(null);
                    }}
                >
                    <DialogContent className="flex h-[85vh] flex-col overflow-hidden rounded-lg sm:max-w-5xl">{previewingLocalFile && <LocalFilePreview file={previewingLocalFile} onClose={() => setPreviewingLocalFile(null)} />}</DialogContent>
                </Dialog>
            )}

            {artifactAttachmentEnabled && (
                <AttachArtifactDialog isOpen={showAttachArtifactDialog} onClose={() => setShowAttachArtifactDialog(false)} onAttach={handleAttachArtifacts} alreadyAttachedUris={selectedArtifactRefs.map(a => a.uri).filter((u): u is string => !!u)} />
            )}

            <div className="@container relative m-2 flex min-w-0 items-center gap-2">
                {artifactAttachmentEnabled ? (
                    <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                            <Button variant="ghost" disabled={isResponding} tooltip="Attach">
                                <Paperclip className="size-4" />
                            </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="start" className="w-56">
                            <DropdownMenuItem
                                onSelect={() => {
                                    handleFileSelect();
                                }}
                            >
                                <Upload className="text-(--primary-wMain)" />
                                <span>Upload from computer</span>
                            </DropdownMenuItem>
                            <DropdownMenuItem
                                onSelect={() => {
                                    setShowAttachArtifactDialog(true);
                                }}
                            >
                                <Link2 className="text-(--primary-wMain)" />
                                <span>Attach existing artifact</span>
                            </DropdownMenuItem>
                        </DropdownMenuContent>
                    </DropdownMenu>
                ) : (
                    <Button variant="ghost" onClick={handleFileSelect} disabled={isResponding} tooltip="Attach file">
                        <Paperclip className="size-4" />
                    </Button>
                )}

                {selectedBuilderNode && (
                    <span className="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium">
                        Selected: {selectedBuilderNode.name}
                        <button onClick={() => setSelectedBuilderNode(null)} className="ml-0.5" aria-label="Clear selection">
                            &times;
                        </button>
                    </span>
                )}

                {showAgentSelector && !builderMode && allowAgentSelection && (
                    <>
                        <div className="hidden @[480px]:block">Agent: </div>
                        <Select value={agentPickerValue} onValueChange={handleAgentChange} disabled={isResponding || agents.length === 0}>
                            <SelectTrigger className="min-w-0 flex-1 @[480px]:max-w-[250px]" data-testid="chatAgentSelect">
                                <SelectValue placeholder="Select an agent..." />
                            </SelectTrigger>
                            <SelectContent>
                                {agents
                                    .filter(agent => !agent.isWorkflow && !agent.isInternal)
                                    .slice()
                                    .sort((a, b) => {
                                        const pa = a.priority ?? 0;
                                        const pb = b.priority ?? 0;
                                        if (pa !== pb) return pb - pa;
                                        return (a.displayName ?? a.name ?? "").localeCompare(b.displayName ?? b.name ?? "");
                                    })
                                    .map(agent => (
                                        <SelectItem key={agent.name} value={agent.name}>
                                            {agent.displayName || agent.name}
                                        </SelectItem>
                                    ))}
                            </SelectContent>
                        </Select>
                    </>
                )}

                <div className="hidden flex-1 @[480px]:block" />

                {sessionId && <ContextUsageIndicator key={sessionId} sessionId={sessionId} messageCount={messages.length} />}

                {sttEnabled && settings.speechToText && <AudioRecorder disabled={isResponding} onTranscriptionComplete={handleTranscription} onError={handleTranscriptionError} onRecordingStateChange={setIsRecording} />}

                {isResponding && !isCancelling ? (
                    <Button data-testid="cancel" className="ml-auto gap-1.5" onClick={handleCancel} variant="outline" disabled={isCancelling} tooltip="Stop">
                        <Ban className="size-4" />
                        <span className="hidden @[480px]:inline">Stop</span>
                    </Button>
                ) : (
                    <Button data-testid="sendMessage" variant="ghost" className="ml-auto gap-1.5" onClick={onSubmit} disabled={!isSubmittingEnabled} tooltip="Send message">
                        <Send className="size-4" />
                    </Button>
                )}
            </div>

            <ConfirmationDialog
                open={switchConfirmOpen}
                onOpenChange={setSwitchConfirmOpen}
                title="Switch Agent"
                description="Switching agents will start a new chat and clear the current chat history and files. Are you sure you want to proceed?"
                actionLabels={{ confirm: "Switch Agent" }}
                onConfirm={confirmAgentSwitch}
                onCancel={cancelAgentSwitch}
            />
        </div>
    );
};
