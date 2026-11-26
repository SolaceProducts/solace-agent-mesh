// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type Prompt, type PromptGroup } from "@/types/prompts";

export const defaultVersions: Prompt[] = [
    {
        id: "prompt-1",
        promptText: "What is the weather in {{weather}}",
        groupId: "group-1",
        userId: "user-id",
        version: 1,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
    },
    {
        promptText: "get the weather for {{place}}",
        id: "prompt-2",
        groupId: "group-1",
        userId: "user-id",
        version: 2,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
    },
];

export const weatherProductionPrompt: Prompt = defaultVersions[1];
export const weatherPromptGroup: PromptGroup = {
    id: "group-1",
    name: "weather-group",
    userId: "user-id",
    description: "Prompt for getting weather",
    category: "Other",
    isShared: false,
    isPinned: false,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    productionPromptId: weatherProductionPrompt.id,
    productionPrompt: weatherProductionPrompt,
};

export const languagePrompts: Prompt[] = [
    {
        id: "prompt-3",
        promptText: "What is {{word}} in french",
        groupId: "group-2",
        userId: "user-id",
        version: 1,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
    },
];
export const languageProductionPrompt = languagePrompts[0];
export const languagePromptGroup: PromptGroup = {
    id: "group-2",
    name: "language-group",
    userId: "user-id",
    description: "Prompt for translating from english to french",
    category: "Communication",
    isShared: false,
    isPinned: false,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    productionPromptId: weatherProductionPrompt.id,
    productionPrompt: weatherProductionPrompt,
};

export const defaultPromptGroups: PromptGroup[] = [weatherPromptGroup, languagePromptGroup];
