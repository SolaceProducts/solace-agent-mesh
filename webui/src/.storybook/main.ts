// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fileURLToPath } from "node:url";
import type { StorybookConfig } from "@storybook/react-vite";
import path, { dirname } from "path";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const config: StorybookConfig = {
    stories: ["../**/*.stories.@(js|jsx|mjs|ts|tsx)", "!../**/node_modules/**"],
    addons: ["@storybook/addon-vitest"],
    framework: {
        name: "@storybook/react-vite",
        options: {
            builder: {
                viteConfigPath: path.resolve(__dirname, "../../vite.config.ts"),
            },
        },
    },
    core: {
        disableTelemetry: true,
    },
    staticDirs: ["./public"],
};

export default config;
