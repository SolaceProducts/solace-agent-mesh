// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { deriveSessionNameFromFirstMessage, serializeChatMessage } from "@/providers/chat";

import { fileToBase64 } from "@/utils/file";
import { CURRENT_SCHEMA_VERSION } from "@/utils/taskMigration";
import { uuid } from "@/utils/uuid";

import type { AttachedArtifactRef, FilePart, MessageFE, TextPart } from "@/types";

const INLINE_FILE_SIZE_LIMIT_BYTES = 1 * 1024 * 1024;

export function buildUserMessage(params: {
    currentInput: string;
    currentFiles: File[];
    currentArtifactRefs: AttachedArtifactRef[];
    displayHtml?: string | null;
    contextQuote?: string | null;
    contextQuoteSourceId?: string | null;
    sessionId: string;
    extraMetadata?: Record<string, unknown>;
}): MessageFE {
    const { currentFiles, currentArtifactRefs } = params;
    return {
        role: "user",
        parts: [{ kind: "text", text: params.currentInput }],
        isUser: true,
        uploadedFiles: currentFiles.length > 0 ? currentFiles : undefined,
        attachedArtifacts: currentArtifactRefs.length > 0 ? currentArtifactRefs.map(r => ({ uri: r.uri, filename: r.filename, mimeType: r.mimeType })) : undefined,
        displayHtml: params.displayHtml || undefined,
        contextQuote: params.contextQuote || undefined,
        contextQuoteSourceId: params.contextQuoteSourceId || undefined,
        metadata: {
            messageId: `msg-${uuid()}`,
            sessionId: params.sessionId,
            lastProcessedEventSequence: 0,
            ...params.extraMetadata,
        },
    };
}

export function buildSubmitMessageMetadata(params: { selectedAgentName: string; backgroundTasksDefaultTimeoutMs?: number | null; activeProjectId?: string; extraMetadata?: Record<string, unknown> }): Record<string, unknown> {
    const messageMetadata: Record<string, unknown> = {
        agent_name: params.selectedAgentName,
        backgroundExecutionEnabled: true,
        maxExecutionTimeMs: params.backgroundTasksDefaultTimeoutMs ?? 3600000,
    };
    if (params.activeProjectId) {
        messageMetadata.project_id = params.activeProjectId;
    }
    if (params.extraMetadata) {
        Object.assign(messageMetadata, params.extraMetadata);
    }
    return messageMetadata;
}

export function extractResponseTraceId(result: unknown): string | undefined {
    if (!result || typeof result !== "object" || !("traceId" in result)) {
        return undefined;
    }
    const traceId = (result as { traceId: unknown }).traceId;
    return typeof traceId === "string" ? traceId : undefined;
}

export type SubmissionFilesResult = { ok: true; uploadedFileParts: FilePart[]; effectiveSessionId: string } | { ok: false; errorMessage: string };

type LargeFileUploadResult = { ok: true; part: FilePart; sessionId: string } | { ok: false; errorMessage: string };

async function uploadLargeSubmissionFile(params: {
    file: File;
    effectiveSessionId: string;
    successfullyUploadedFiles: Array<{ filename: string; sessionId: string }>;
    uploadArtifactFile: (file: File, overrideSessionId?: string) => Promise<{ uri: string; sessionId: string } | { error: string } | null>;
    cleanupUploadedFiles: (uploadedFiles: Array<{ filename: string; sessionId: string }>) => Promise<void>;
}): Promise<LargeFileUploadResult> {
    const { file, successfullyUploadedFiles, uploadArtifactFile, cleanupUploadedFiles } = params;
    const result = await uploadArtifactFile(file, params.effectiveSessionId);

    if (!(result && "uri" in result && result.uri && result.sessionId)) {
        console.error(`[handleSubmit] File upload failed for "${file.name}". Result:`, result);
        await cleanupUploadedFiles(successfullyUploadedFiles);
        const cleanupMessage = successfullyUploadedFiles.length > 0 ? " Previously uploaded files have been cleaned up." : "";
        const errorDetail = result && "error" in result ? ` (${result.error})` : "";
        return { ok: false, errorMessage: `Message not sent. File upload failed for "${file.name}"${errorDetail}.${cleanupMessage}.` };
    }

    return { ok: true, sessionId: result.sessionId, part: { kind: "file", file: { uri: result.uri, name: file.name, mimeType: file.type } } };
}

export async function processSubmissionFiles(params: {
    currentFiles: File[];
    currentArtifactRefs: AttachedArtifactRef[];
    effectiveSessionId: string;
    uploadArtifactFile: (file: File, overrideSessionId?: string) => Promise<{ uri: string; sessionId: string } | { error: string } | null>;
    cleanupUploadedFiles: (uploadedFiles: Array<{ filename: string; sessionId: string }>) => Promise<void>;
}): Promise<SubmissionFilesResult> {
    const { currentFiles, currentArtifactRefs, uploadArtifactFile, cleanupUploadedFiles } = params;
    const uploadedFileParts: FilePart[] = [];
    const successfullyUploadedFiles: Array<{ filename: string; sessionId: string }> = [];
    let effectiveSessionId = params.effectiveSessionId;

    console.log(`[handleSubmit] Processing ${currentFiles.length} file(s) and ${currentArtifactRefs.length} artifact reference(s)`);

    for (const ref of currentArtifactRefs) {
        console.log(`[handleSubmit] Adding artifact reference: ${ref.filename} (${ref.uri})`);
        uploadedFileParts.push({
            kind: "file",
            file: { uri: ref.uri, name: ref.filename, mimeType: ref.mimeType || "application/octet-stream" },
        });
    }

    for (const file of currentFiles) {
        if (file.size < INLINE_FILE_SIZE_LIMIT_BYTES) {
            const base64Content = await fileToBase64(file);
            uploadedFileParts.push({ kind: "file", file: { bytes: base64Content, name: file.name, mimeType: file.type } });
            continue;
        }

        const uploaded = await uploadLargeSubmissionFile({ file, effectiveSessionId, successfullyUploadedFiles, uploadArtifactFile, cleanupUploadedFiles });
        if (!uploaded.ok) {
            return uploaded;
        }

        if (!effectiveSessionId) {
            effectiveSessionId = uploaded.sessionId;
        }
        successfullyUploadedFiles.push({ filename: file.name, sessionId: uploaded.sessionId });
        uploadedFileParts.push(uploaded.part);
    }

    return { ok: true, uploadedFileParts, effectiveSessionId };
}

export async function finalizeSubmittedTask(params: {
    taskId: string;
    responseSessionId: string | undefined;
    sessionId: string;
    userMsg: MessageFE;
    currentInput: string;
    currentFiles: File[];
    selectedAgentName: string;
    autoTitleGenerationEnabled: boolean;
    setSessionId: (id: string) => void;
    setMessages: (updater: (prev: MessageFE[]) => MessageFE[]) => void;
    setSessionName: (name: string) => void;
    updateSessionName: (sessionId: string, name: string) => Promise<unknown>;
    saveTaskToBackend: (taskData: { taskId: string; userMessage?: string; messageBubbles: unknown[]; taskMetadata?: unknown }, overrideSessionId?: string) => Promise<boolean>;
    registerTaskEarly: (taskId: string, initialRequestText: string) => void;
    registerBackgroundTask: (taskId: string, sessionId: string, agentName?: string) => void;
    setCurrentTaskId: (taskId: string | null) => void;
    setTaskIdInSidePanel: (taskId: string | null) => void;
}): Promise<string> {
    const { taskId, responseSessionId, sessionId, userMsg } = params;
    const isNewSession = !sessionId || sessionId === "";
    const finalSessionId = responseSessionId || sessionId;

    if (responseSessionId && responseSessionId !== sessionId) {
        params.setSessionId(responseSessionId);
        params.setMessages(prev => prev.map(msg => (msg.metadata?.messageId === userMsg.metadata?.messageId ? { ...msg, metadata: { ...msg.metadata, sessionId: responseSessionId } } : msg)));

        if (isNewSession) {
            const newSessionName = deriveSessionNameFromFirstMessage({
                userMessage: userMsg,
                files: params.currentFiles,
                autoTitleEnabled: params.autoTitleGenerationEnabled,
            });
            if (newSessionName) {
                params.setSessionName(newSessionName);
                await params.updateSessionName(responseSessionId, newSessionName);
            }
        }

        if (isNewSession && typeof globalThis !== "undefined") {
            globalThis.dispatchEvent(new CustomEvent("new-chat-session"));
        }
    }

    if (finalSessionId) {
        await params.saveTaskToBackend(
            {
                taskId,
                userMessage: params.currentInput,
                messageBubbles: [serializeChatMessage(userMsg)],
                taskMetadata: {
                    schemaVersion: CURRENT_SCHEMA_VERSION,
                    status: "pending",
                    agentName: params.selectedAgentName,
                    is_background_task: true,
                },
            },
            finalSessionId
        );
    }

    params.setCurrentTaskId(taskId);
    params.setTaskIdInSidePanel(taskId);

    const textParts = userMsg.parts.filter(p => p.kind === "text") as TextPart[];
    const initialRequestText =
        textParts
            .map(p => p.text)
            .join(" ")
            .trim() || "Task started...";
    params.registerTaskEarly(taskId, initialRequestText);

    params.registerBackgroundTask(taskId, finalSessionId, params.selectedAgentName);

    if (typeof globalThis !== "undefined") {
        globalThis.dispatchEvent(new CustomEvent("new-chat-session"));
    }

    params.setMessages(prev => prev.map(msg => (msg.metadata?.messageId === userMsg.metadata?.messageId ? { ...msg, taskId } : msg)));

    return finalSessionId;
}
