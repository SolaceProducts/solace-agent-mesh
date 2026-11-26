// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const formatMilliseconds = (ms: number) => `${(ms / 1000).toFixed(2)}s`;

export const formatRowTimestamp = (date: Date): string =>
    date.toLocaleString([], {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });

export const formatTokenUsageTitle = (usage: { input: number; output: number }): string =>
    `Tokens: ${(usage.input + usage.output).toLocaleString()} total · ${usage.input.toLocaleString()} sent (prompt, cached input included) · ${usage.output.toLocaleString()} received (completion)`;
