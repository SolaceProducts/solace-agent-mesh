// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface Prompt {
    type?: "prompt";
    id: string;
    promptText: string;
    groupId: string;
    userId: string;
    createdBy?: string;
    version: number;
    name?: string;
    description?: string;
    category?: string;
    command?: string;
    createdAt: string;
    updatedAt: string;
}

export interface PromptGroup {
    id: string;
    name: string;
    description?: string;
    category?: string;
    command?: string;
    userId: string;
    createdBy?: string;
    authorName?: string;
    productionPromptId?: string;
    isShared: boolean;
    isPinned: boolean;
    accessLevel?: "owner" | "resource_editor" | "resource_viewer";
    createdAt: string;
    updatedAt: string;
    productionPrompt?: Prompt;
    _editingPromptId?: string;
    _isEditingActiveVersion?: boolean;
    _selectedVersionId?: string;
}

export interface PromptGroupCreate {
    name: string;
    description?: string;
    category?: string;
    command?: string;
    initialPrompt: string;
}

export interface PromptGroupUpdate {
    name?: string;
    description?: string;
    category?: string;
    command?: string;
}

export interface PromptCreate {
    promptText: string;
}

export interface PromptChatMessage {
    role: "user" | "assistant";
    content: string;
}

export interface PromptBuilderChatRequest {
    message: string;
    conversationHistory: PromptChatMessage[];
    currentTemplate?: Record<string, unknown>;
}

export interface PromptBuilderChatResponse {
    message: string;
    templateUpdates: Record<string, unknown>;
    confidence: number;
    readyToSave: boolean;
    isError?: boolean;
    isWarning?: boolean;
}

export interface TemplateConfig {
    name?: string;
    category?: string;
    command?: string;
    promptText?: string;
    description?: string;
    detectedVariables?: string[];
}
