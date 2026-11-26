// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { solaceTheme } from "./solace";
import { solaceDarkTheme } from "./solaceDark";
import type { ThemeDefinition } from "./themePalette";

export type { ThemePalette } from "./themePalette";

export const themes: ThemeDefinition[] = [solaceTheme, solaceDarkTheme];
export const themeMap = new Map(themes.map(t => [t.id, t]));
