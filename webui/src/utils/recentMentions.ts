// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Person } from "@/types";

const STORAGE_KEY = "recent_mentions";
const MAX_RECENT_MENTIONS = 10;

export function getRecentMentions(): Person[] {
    try {
        const stored = localStorage.getItem(STORAGE_KEY);
        if (!stored) return [];

        const parsed = JSON.parse(stored);
        return Array.isArray(parsed) ? parsed : [];
    } catch (error) {
        console.error("Error reading recent mentions from localStorage:", error);
        return [];
    }
}

export function addRecentMention(person: Person): void {
    try {
        let recent = getRecentMentions();

        recent = recent.filter(p => p.id !== person.id);

        recent.unshift(person);

        recent = recent.slice(0, MAX_RECENT_MENTIONS);

        localStorage.setItem(STORAGE_KEY, JSON.stringify(recent));
    } catch (error) {
        console.error("Error saving recent mention to localStorage:", error);
    }
}

export function clearRecentMentions(): void {
    try {
        localStorage.removeItem(STORAGE_KEY);
    } catch (error) {
        console.error("Error clearing recent mentions from localStorage:", error);
    }
}
