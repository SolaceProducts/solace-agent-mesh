// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, type RenderOptions, type RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter } from "react-router-dom";

import type { AudioSettingsContextValue, AuthContextValue, ChatContextValue, ConfigContextValue, TaskContextValue, UIMode, UserContextValue } from "@/providers/contexts";

import type { ProjectContextValue } from "@/types/projects";

import type { SelectionContextValue } from "@/features/chat/selection/types";

import { StoryProvider } from "../providers/StoryProvider";

type FeatureFlagMap = Record<string, boolean>;

interface RenderWithProvidersOptions extends Omit<RenderOptions, "wrapper"> {
    featureFlags?: FeatureFlagMap;
    chatContextValues?: Partial<ChatContextValue>;
    authContextValues?: Partial<AuthContextValue>;
    configContextValues?: Partial<ConfigContextValue>;
    projectContextValues?: Partial<ProjectContextValue>;
    taskContextValues?: Partial<TaskContextValue>;
    userContextValues?: Partial<UserContextValue>;
    uiModeContextValues?: UIMode;
    textSelectionContextValues?: Partial<SelectionContextValue>;
    audioSettingsContextValues?: Partial<AudioSettingsContextValue>;
    initialPath?: string;
}

export function renderWithProviders(
    ui: ReactElement,
    {
        featureFlags = {},
        chatContextValues,
        authContextValues,
        configContextValues,
        projectContextValues,
        taskContextValues,
        userContextValues,
        uiModeContextValues,
        textSelectionContextValues,
        audioSettingsContextValues,
        initialPath = "/",
        ...rtlOptions
    }: RenderWithProvidersOptions = {}
): RenderResult {
    return render(
        <MemoryRouter initialEntries={[initialPath]}>
            <StoryProvider
                chatContextValues={chatContextValues}
                authContextValues={authContextValues}
                configContextValues={configContextValues}
                projectContextValues={projectContextValues}
                taskContextValues={taskContextValues}
                userContextValues={userContextValues}
                uiModeContextValues={uiModeContextValues}
                textSelectionContextValues={textSelectionContextValues}
                audioSettingsContextValues={audioSettingsContextValues}
                flagValues={featureFlags}
            >
                {ui}
            </StoryProvider>
        </MemoryRouter>,
        rtlOptions
    );
}
