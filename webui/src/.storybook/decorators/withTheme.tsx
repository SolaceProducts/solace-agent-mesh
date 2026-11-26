// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import type { Decorator, StoryContext, StoryFn } from "@storybook/react-vite";

function ThemeWrapper({ theme, children }: { theme: string; children: React.ReactNode }) {
    useEffect(() => {
        localStorage.setItem("sam-theme", theme);

        window.dispatchEvent(new StorageEvent("storage", { key: "sam-theme", newValue: theme }));

        document.body.style.backgroundColor = "var(--background-w10)";
    }, [theme]);

    return <>{children}</>;
}

export const withTheme: Decorator = (Story: StoryFn, context: StoryContext) => {
    const theme = context.globals.theme || "light";

    return <ThemeWrapper theme={theme}>{Story(context.args, context)}</ThemeWrapper>;
};
