// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "@/index.css";

import type { Preview } from "@storybook/react";
import { mswLoader } from "msw-storybook-addon/csf3";

import { defaultHandlers } from "@/testing/helpers";

import { withProviders } from "./decorators/withProviders";
import { withTheme } from "./decorators/withTheme";
import { startWorker } from "./mswWorker";

const preview: Preview = {
    decorators: [withTheme, withProviders],
    loaders: [mswLoader(startWorker)],

    parameters: {
        actions: { argTypesRegex: "^on[A-Z].*" },
        controls: {
            matchers: {
                color: /(background|color)$/i,
                date: /Date$/i,
            },
        },
        layout: "fullscreen",
        msw: { handlers: defaultHandlers },
        backgrounds: { disable: true },
    },

    globalTypes: {
        theme: {
            description: "Global theme for components",
            defaultValue: "light",
            toolbar: {
                title: "Theme",
                icon: "circlehollow",
                items: [
                    { value: "light", icon: "sun", title: "Light" },
                    { value: "dark", icon: "moon", title: "Dark" },
                ],
                dynamicTitle: true,
            },
        },
    },
};

export default preview;
