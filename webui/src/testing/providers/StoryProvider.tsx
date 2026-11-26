// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient } from "@tanstack/react-query";

import type { AudioSettingsContextValue } from "@/providers/contexts/AudioSettingsContext";
import type { AuthContextValue } from "@/providers/contexts/AuthContext";
import type { ChatContextValue } from "@/providers/contexts/ChatContext";
import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import type { TaskContextValue } from "@/providers/contexts/TaskContext";
import { FULL_UI_MODE, type UIMode, UIModeContext } from "@/providers/contexts/UIModeContext";
import type { UserContextValue } from "@/providers/contexts/UserContext";

import type { ProjectContextValue } from "@/types/projects";

import type { SelectionContextValue } from "@/features/chat/selection/types";

import { useRadixPointerEventsFix } from "@/hooks";
import { QueryProvider, SSEProvider, ThemeProvider } from "@/providers";

import { MockAudioSettingsProvider } from "./MockAudioSettingsProvider";
import { MockAuthProvider } from "./MockAuthProvider";
import { MockChatProvider } from "./MockChatProvider";
import { MockConfigProvider } from "./MockConfigProvider";
import { MockProjectProvider } from "./MockProjectProvider";
import { MockTaskProvider } from "./MockTaskProvider";
import { MockTextSelectionProvider } from "./MockTextSelectionProvider";
import { MockUserProvider } from "./MockUserProvider";

interface StoryProviderProps {
    children: React.ReactNode;
    authContextValues?: Partial<AuthContextValue>;
    chatContextValues?: Partial<ChatContextValue>;
    textSelectionContextValues?: Partial<SelectionContextValue>;
    audioSettingsContextValues?: Partial<AudioSettingsContextValue>;
    projectContextValues?: Partial<ProjectContextValue>;
    taskContextValues?: Partial<TaskContextValue>;
    configContextValues?: Partial<ConfigContextValue>;
    userContextValues?: Partial<UserContextValue>;
    uiModeContextValues?: UIMode;
    flagValues?: Record<string, boolean>;
    skipFeatureFlagProvider?: boolean;
    shouldIsolateQueryClient?: boolean;
}

export const StoryProvider: React.FC<StoryProviderProps> = ({
    children,
    authContextValues = {},
    chatContextValues = {},
    textSelectionContextValues = {},
    audioSettingsContextValues = {},
    projectContextValues = {},
    taskContextValues = {},
    configContextValues = {},
    userContextValues = {},
    uiModeContextValues,
    flagValues = {},
    skipFeatureFlagProvider = false,
    shouldIsolateQueryClient = false,
}) => {
    useRadixPointerEventsFix();

    const [storyClient] = useState(() => (shouldIsolateQueryClient ? new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }) : undefined));

    const innerTree = (
        <ThemeProvider>
            <MockConfigProvider mockValues={configContextValues}>
                <MockAuthProvider mockValues={authContextValues}>
                    <MockUserProvider mockValues={userContextValues}>
                        <SSEProvider>
                            <MockAudioSettingsProvider mockValues={audioSettingsContextValues}>
                                <MockProjectProvider mockValues={projectContextValues}>
                                    <MockTextSelectionProvider mockValues={textSelectionContextValues}>
                                        <MockTaskProvider mockValues={taskContextValues}>
                                            <MockChatProvider mockValues={chatContextValues}>
                                                <UIModeContext.Provider value={uiModeContextValues ?? FULL_UI_MODE}>{children}</UIModeContext.Provider>
                                            </MockChatProvider>
                                        </MockTaskProvider>
                                    </MockTextSelectionProvider>
                                </MockProjectProvider>
                            </MockAudioSettingsProvider>
                        </SSEProvider>
                    </MockUserProvider>
                </MockAuthProvider>
            </MockConfigProvider>
        </ThemeProvider>
    );

    return <QueryProvider client={storyClient}>{skipFeatureFlagProvider ? innerTree : <OpenFeatureTestProvider flagValueMap={flagValues}>{innerTree}</OpenFeatureTestProvider>}</QueryProvider>;
};
