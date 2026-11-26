// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const KEY_PREFIX = "sam-last-used-agent";

const keyFor = (userId: string) => `${KEY_PREFIX}:${userId}`;

export function getLastUsedAgentName(userId: string): string | null {
    if (!userId) return null;
    try {
        return localStorage.getItem(keyFor(userId)) || null;
    } catch (error) {
        console.warn("Error reading last used agent from localStorage:", error);
        return null;
    }
}

export function setLastUsedAgentName(userId: string, agentName: string): void {
    if (!userId || !agentName) return;
    try {
        localStorage.setItem(keyFor(userId), agentName);
    } catch (error) {
        console.warn("Error saving last used agent to localStorage:", error);
    }
}

export function clearLastUsedAgentName(userId: string): void {
    if (!userId) return;
    try {
        localStorage.removeItem(keyFor(userId));
    } catch (error) {
        console.warn("Error clearing last used agent from localStorage:", error);
    }
}
