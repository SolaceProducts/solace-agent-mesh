// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import path from "node:path";
import { fileURLToPath } from "node:url";
import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import react from "@vitejs/plugin-react";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig, mergeConfig } from "vitest/config";

import viteConfig from "./vite.config.ts";

const dirname = path.dirname(fileURLToPath(import.meta.url));

const baseViteConfig = viteConfig({ mode: "development", command: "serve" });
if (baseViteConfig.server) {
    delete baseViteConfig.server.proxy;
}

const assetsDir = path.resolve(dirname, "src/assets");
const srcDir = path.resolve(dirname, "src");

const projectAlias = [
    { find: /^@\/assets\/(.+)$/, replacement: `${assetsDir}/$1` },
    { find: /^@\/(.+)$/, replacement: `${srcDir}/$1` },
];

export default mergeConfig(
    baseViteConfig,
    defineConfig({
        optimizeDeps: {
            exclude: ["@storybook/react-vite/template/*"],
        },
        test: {
            coverage: {
                enabled: true,
                provider: "v8",
                reporter: ["lcov", "text", "json"],
                reportsDirectory: "./coverage",
                include: ["**/*.{ts,tsx}"],
                exclude: ["**/*.test.{ts,tsx}", "**/*.stories.{ts,tsx}", "**/*.d.ts", "**/test-support/**", "**/.storybook/**", "**/node_modules/**", "*.config.ts", "scripts/**"],
            },
            projects: [
                {
                    extends: true,
                    root: "./src",
                    plugins: [
                        storybookTest({
                            configDir: path.join(dirname, "src/.storybook"),
                            storybookScript: "npm run storybook -- --no-open",
                        }),
                    ],
                    test: {
                        name: "storybook",
                        exclude: ["**/node_modules/**"],
                        testTimeout: 10000,
                        retry: process.env.CI ? 2 : 0,
                        browser: {
                            enabled: true,
                            provider: playwright({}),
                            headless: true,
                            instances: [{ browser: "chromium" }],
                        },
                        setupFiles: ["./.storybook/vitest.globals.ts", "./.storybook/vitest.setup.ts", "./.storybook/vitest.enforceHandlers.ts"],
                    },
                },
                {
                    extends: false,
                    plugins: [react()],
                    resolve: {
                        alias: projectAlias,
                    },
                    test: {
                        name: "unit",
                        globals: true,
                        environment: "jsdom",
                        environmentOptions: { jsdom: { url: "http://test.invalid/" } },
                        include: ["src/**/*.test.{ts,tsx}", "scripts/**/*.test.{ts,tsx}"],
                        setupFiles: [
                            "./src/.storybook/vitest.globals.ts",
                            "./src/.storybook/vitest.unit.setup.ts",
                            "./src/.storybook/vitest.setup.ts",
                        ],
                    },
                },
            ],
        },
    })
);
