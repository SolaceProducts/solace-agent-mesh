// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function detectVariables(promptText: string): string[] {
    const regex = /\{\{([^}]+)\}\}/g;
    const matches = promptText.matchAll(regex);
    const variables = new Set<string>();

    for (const match of matches) {
        variables.add(match[1].trim());
    }

    return Array.from(variables);
}

export function replaceVariables(promptText: string, values: Record<string, string>): string {
    let result = promptText;

    for (const [key, value] of Object.entries(values)) {
        const regex = new RegExp(`\\{\\{\\s*${key}\\s*\\}\\}`, "g");
        result = result.replace(regex, value);
    }

    return result;
}

export function validatePromptText(text: string): {
    valid: boolean;
    error?: string;
} {
    if (!text || text.trim().length === 0) {
        return { valid: false, error: "Prompt text cannot be empty" };
    }

    if (text.length > 10000) {
        return {
            valid: false,
            error: "Prompt text exceeds maximum length of 10,000 characters",
        };
    }

    return { valid: true };
}

export function validateCommand(command: string): {
    valid: boolean;
    error?: string;
} {
    if (!command) {
        return { valid: true };
    }

    if (command.length > 50) {
        return {
            valid: false,
            error: "Command exceeds maximum length of 50 characters",
        };
    }

    if (!/^[a-zA-Z0-9_-]+$/.test(command)) {
        return {
            valid: false,
            error: "Command can only contain letters, numbers, hyphens, and underscores",
        };
    }

    return { valid: true };
}

export function formatPromptDate(isoString: string): string {
    const date = new Date(isoString);
    return date.toLocaleDateString("en-US", {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
    });
}

export function extractCategories(groups: Array<{ category?: string }>): string[] {
    const categories = new Set<string>();

    for (const group of groups) {
        if (group.category) {
            categories.add(group.category);
        }
    }

    return Array.from(categories).sort();
}

export function filterPromptGroups(
    groups: Array<{
        name: string;
        description?: string;
        command?: string;
        category?: string;
    }>,
    searchTerm: string
): typeof groups {
    if (!searchTerm) return groups;

    const search = searchTerm.toLowerCase();
    return groups.filter(group => group.name.toLowerCase().includes(search) || group.description?.toLowerCase().includes(search) || group.command?.toLowerCase().includes(search) || group.category?.toLowerCase().includes(search));
}
