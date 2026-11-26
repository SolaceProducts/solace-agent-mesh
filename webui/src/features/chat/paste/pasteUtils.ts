// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { getErrorMessage } from "@/utils";

export interface PastedTextItem {
    id: string;
    content: string;
    timestamp: number;
    filename?: string;
    mimeType?: string;
    description?: string;
    isConfigured?: boolean;
}

export interface PastedArtifactItem {
    id: string;
    artifactId: string;
    filename: string;
    mimeType: string;
    timestamp: number;
}

export const isLargeText = (text: string): boolean => {
    const charCount = text.length;
    const lineCount = text.split("\n").length;
    return charCount >= 2000 || lineCount >= 50;
};

export const generateArtifactDescription = (content: string): string => {
    const charCount = content.length;
    const lineCount = content.split("\n").length;

    const preview = content.substring(0, 50).replace(/\n/g, " ").trim();
    const previewText = preview.length < content.length ? `${preview}...` : preview;

    return `Pasted text (${charCount} chars, ${lineCount} lines): ${previewText}`;
};

export const createPastedTextItem = (content: string): PastedTextItem => ({
    id: `paste-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`,
    content,
    timestamp: Date.now(),
});

export function nextUniqueFilename(filename: string, taken: ReadonlySet<string>): string {
    if (!taken.has(filename)) return filename;

    const lastDot = filename.lastIndexOf(".");
    const hasExtension = lastDot > 0;
    const base = hasExtension ? filename.slice(0, lastDot) : filename;
    const extension = hasExtension ? filename.slice(lastDot) : "";

    let counter = 2;
    while (taken.has(`${base}-${counter}${extension}`)) counter++;
    return `${base}-${counter}${extension}`;
}

export function nextUniqueSnippetFilename(taken: ReadonlySet<string>): string {
    return nextUniqueFilename("snippet.txt", taken);
}

export function defaultSnippetFilename(items: PastedTextItem[], index: number, existingArtifactFilenames: Iterable<string>): string {
    const taken = new Set(existingArtifactFilenames);
    for (let i = 0; i < index; i++) {
        const prev = items[i];
        if (prev.isConfigured && prev.filename) taken.add(prev.filename);
    }
    for (let i = 0; i < index; i++) {
        if (!items[i].isConfigured) taken.add(nextUniqueSnippetFilename(taken));
    }
    return nextUniqueSnippetFilename(taken);
}

export interface UploadedPastedArtifact {
    uri: string;
    filename: string;
    mimeType: string;
}

type UploadArtifactFile = (file: File, overrideSessionId?: string, description?: string, silent?: boolean) => Promise<{ uri: string; sessionId: string } | { error: string } | null>;

interface PastedItemOutcome {
    uploaded?: UploadedPastedArtifact;
    error?: string;
    sessionId: string;
}

async function uploadOnePastedItem(item: PastedTextItem, sessionId: string, filename: string, mimeType: string, description: string | undefined, uploadArtifactFile: UploadArtifactFile): Promise<PastedItemOutcome> {
    try {
        const file = new File([new Blob([item.content], { type: mimeType })], filename, { type: mimeType });
        const result = await uploadArtifactFile(file, sessionId, description, true);
        if (result && !("error" in result)) {
            return { uploaded: { uri: result.uri, filename, mimeType }, sessionId: result.sessionId || sessionId };
        }
        const reason = result && "error" in result ? result.error : "An unknown upload error occurred.";
        return { error: `${filename}: ${reason}`, sessionId };
    } catch (uploadErr) {
        return { error: `${filename}: ${getErrorMessage(uploadErr)}`, sessionId };
    }
}

function resolvePastedItemFile(item: PastedTextItem, taken: ReadonlySet<string>): { filename: string; mimeType: string; description?: string } {
    if (item.isConfigured && item.filename && item.mimeType) {
        return { filename: item.filename, mimeType: item.mimeType, description: item.description };
    }
    return { filename: nextUniqueSnippetFilename(taken), mimeType: "text/plain" };
}

export async function uploadPastedTextItems(
    items: PastedTextItem[],
    startSessionId: string,
    existingArtifactFilenames: Iterable<string>,
    uploadArtifactFile: UploadArtifactFile
): Promise<{ uploaded: UploadedPastedArtifact[]; errors: string[]; sessionId: string }> {
    const taken = new Set(existingArtifactFilenames);
    const uploaded: UploadedPastedArtifact[] = [];
    const errors: string[] = [];
    let sessionId = startSessionId;

    for (const item of items) {
        const { filename, mimeType, description } = resolvePastedItemFile(item, taken);
        taken.add(filename);

        const outcome = await uploadOnePastedItem(item, sessionId, filename, mimeType, description, uploadArtifactFile);
        sessionId = outcome.sessionId;
        if (outcome.uploaded) uploaded.push(outcome.uploaded);
        if (outcome.error) errors.push(outcome.error);
    }

    return { uploaded, errors, sessionId };
}

export const getTextPreview = (text: string, maxLength: number = 50): string => {
    const singleLine = text.replace(/\n/g, " ").trim();
    return singleLine.length > maxLength ? `${singleLine.substring(0, maxLength)}...` : singleLine;
};
