// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

import type { ThemeDefinition } from "@/providers/themes/palettes/themePalette";

export interface ThemeContextValue {
    currentTheme: string;
    themes: ThemeDefinition[];
    setTheme: (themeId: string) => void;
    toggleTheme: () => void;
}

export const ThemeContext = createContext<ThemeContextValue | null>(null);

export function useThemeContext(): ThemeContextValue {
    const context = useContext(ThemeContext);
    if (context === null) {
        throw new Error("useThemeContext must be used within a ThemeProvider");
    }
    return context;
}
