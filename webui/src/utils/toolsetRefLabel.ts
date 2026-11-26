// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const toolsetRefLabel = (ref: string): string => {
    const words = ref
        .replace(/^builtin_/, "")
        .split(/[-_\s]+/)
        .filter(Boolean)
        .map(word => word.charAt(0).toUpperCase() + word.slice(1));
    return words.length > 0 ? words.join(" ") : ref;
};
