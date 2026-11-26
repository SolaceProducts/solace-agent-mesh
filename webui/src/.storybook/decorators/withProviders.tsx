// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Decorator, StoryContext, StoryFn } from "@storybook/react-vite";
import { createMemoryRouter, RouterProvider } from "react-router-dom";

import { StoryProvider } from "@/testing/providers/StoryProvider";

export const withProviders: Decorator = (Story: StoryFn, context: StoryContext) => {
    const authContextValues = {
        ...(context.parameters.authContext || {}),
        ...(context.args.authContext || {}),
    };

    const chatContextValues = {
        ...(context.parameters.chatContext || {}),
        ...(context.args.chatContext || {}),
    };

    const taskContextValues = {
        ...(context.parameters.taskContext || {}),
        ...(context.args.taskContext || {}),
    };

    const configContextValues = {
        ...(context.parameters.configContext || {}),
        ...(context.args.configContext || {}),
    };

    const userContextValues = {
        ...(context.parameters.userContext || {}),
        ...(context.args.userContext || {}),
    };

    const uiModeContextValues = context.args.uiMode ?? context.parameters.uiMode;

    const projectContextValues = {
        ...(context.parameters.projectContext || {}),
        ...(context.args.projectContext || {}),
    };

    const textSelectionContextValues = {
        ...(context.parameters.textSelectionContext || {}),
        ...(context.args.textSelectionContext || {}),
    };

    const audioSettingsContextValues = {
        ...(context.parameters.audioSettingsContext || {}),
        ...(context.args.audioSettingsContext || {}),
    };

    const featureFlags: Record<string, boolean> = {
        ...(configContextValues.configFeatureEnablement || {}),
        ...(context.parameters.featureContext || {}),
        ...(context.args.featureContext || {}),
    };

    const routerValues = {
        initialPath: "/",
        routePath: "/*",
        ...(context.parameters.routerValues || {}),
        ...(context.args.routerValues || {}),
    };

    const router = createMemoryRouter(
        [
            {
                path: routerValues.routePath ?? "*",
                loader: routerValues.loader,
                children: routerValues.children,
                element: (
                    <StoryProvider
                        shouldIsolateQueryClient
                        authContextValues={authContextValues}
                        chatContextValues={chatContextValues}
                        taskContextValues={taskContextValues}
                        configContextValues={configContextValues}
                        userContextValues={userContextValues}
                        uiModeContextValues={uiModeContextValues}
                        projectContextValues={projectContextValues}
                        textSelectionContextValues={textSelectionContextValues}
                        audioSettingsContextValues={audioSettingsContextValues}
                        flagValues={featureFlags}
                    >
                        <div
                            style={{
                                display: "flex",
                                flexDirection: "column",
                                height: "100vh",
                                width: "100vw",
                                boxSizing: "border-box",
                            }}
                        >
                            {Story(context.args, context)}
                        </div>
                    </StoryProvider>
                ),
            },
            {
                path: "*",
                element: <div />,
            },
        ],
        { initialEntries: [routerValues.initialPath] }
    );

    return <RouterProvider router={router} />;
};
