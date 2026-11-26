// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function escapeMarkdown(text: string): string {
    if (!text || typeof text !== "string") {
        return text || "";
    }

    return text.replace(/([\\`*_{}[\]()#+\-.!|~])/g, "\\$1");
}
