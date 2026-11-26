// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import simpleImportSort from "eslint-plugin-simple-import-sort";
import importPlugin from "eslint-plugin-import-x";
import tseslint from "typescript-eslint";
import { globalIgnores } from "eslint/config";

import localRules from "./eslint-rules/no-comments.js";

const SAVED_MODEL_ICON_MESSAGE = "Draw a saved model's icon with ModelConfigIcon from @/features/activation. The free model is stored with an OpenAI-compatible provider and must still show as Anthropic.";

export default tseslint.config(
    globalIgnores(["static", "dist", "coverage-*"]),
    js.configs.recommended,
    tseslint.configs.recommended,
    reactHooks.configs.flat.recommended,
    reactRefresh.configs.vite,
    {
        plugins: { local: localRules },
        rules: { "local/no-comments": "error", "no-empty": ["error", { allowEmptyCatch: true }] },
    },
    {
        files: ["**/*.{ts,tsx}"],
        plugins: {
            "simple-import-sort": simpleImportSort,
            "import-x": importPlugin,
        },
        languageOptions: {
            ecmaVersion: 2020,
            globals: globals.browser,
        },
        rules: {
            "@typescript-eslint/no-explicit-any": "warn",
            "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
            "@typescript-eslint/no-unused-expressions": ["warn", { allowShortCircuit: true, allowTernary: true }],
            "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
            "react-hooks/set-state-in-effect": "warn",
            "react-hooks/refs": "warn",
            "react-hooks/purity": "warn",
            "react-hooks/immutability": "warn",
            "react-hooks/static-components": "warn",
            "react-hooks/globals": "warn",
            "react-hooks/error-boundaries": "warn",
            "react-hooks/preserve-manual-memoization": "warn",
            "react-hooks/use-memo": "warn",
            "simple-import-sort/imports": [
                "warn",
                {
                    groups: [
                        ["^\\u0000"],
                        ["^node:", "^react$", "^react/", "^@?\\w"],
                        ["^@/api/"],
                        ["^@/providers/"],
                        ["^@/hooks/"],
                        ["^@/common/"],
                        ["^@/utils/"],
                        ["^@/ui/", "^@/types/", "^@/assets/"],
                        ["^@/features/"],
                        ["^@/"],
                        ["^\\.\\."],
                        ["^\\./"],
                    ],
                },
            ],
            "simple-import-sort/exports": "warn",
            "import-x/no-duplicates": "warn",
            "import-x/no-useless-path-segments": "warn",
            "import-x/no-cycle": ["error", { maxDepth: 10, ignoreExternal: true }],
        },
    },
    {
        files: ["src/**/*.{ts,tsx}"],
        ignores: ["src/common/**", "src/features/activation/ModelConfigIcon.tsx", "src/features/models/ProviderSelect.tsx"],
        rules: {
            "no-restricted-imports": [
                "error",
                {
                    paths: [
                        { name: "@/common", importNames: ["ModelProviderIcon"], message: SAVED_MODEL_ICON_MESSAGE },
                        { name: "@/common/ModelProviderIcon", message: SAVED_MODEL_ICON_MESSAGE },
                    ],
                },
            ],
        },
    }
);
