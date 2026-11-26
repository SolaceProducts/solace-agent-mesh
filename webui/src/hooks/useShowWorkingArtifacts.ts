// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useSyncExternalStore } from "react";

import { SHOW_WORKING_ARTIFACTS_STORAGE_KEY } from "@/features/chat/artifactTags";

function readStoredPreference(): boolean {
    try {
        return localStorage.getItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY) === "true";
    } catch {
        return false;
    }
}

let showWorkingArtifacts = readStoredPreference();
const subscribers = new Set<() => void>();

function publish(next: boolean): void {
    if (next === showWorkingArtifacts) return;
    showWorkingArtifacts = next;
    for (const notify of subscribers) {
        notify();
    }
}

function syncFromOtherTab(e: StorageEvent): void {
    if (e.key === null) {
        publish(false);
    } else if (e.key === SHOW_WORKING_ARTIFACTS_STORAGE_KEY) {
        publish(e.newValue === "true");
    }
}

function subscribe(onStoreChange: () => void): () => void {
    if (subscribers.size === 0) {
        showWorkingArtifacts = readStoredPreference();
        globalThis.addEventListener("storage", syncFromOtherTab);
    }
    subscribers.add(onStoreChange);
    return () => {
        subscribers.delete(onStoreChange);
        if (subscribers.size === 0) {
            globalThis.removeEventListener("storage", syncFromOtherTab);
        }
    };
}

function getSnapshot(): boolean {
    return showWorkingArtifacts;
}

function toggleShowWorkingArtifacts(): void {
    const next = !showWorkingArtifacts;
    try {
        localStorage.setItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY, String(next));
    } catch {}
    publish(next);
}

export function useShowWorkingArtifacts(): {
    showWorkingArtifacts: boolean;
    toggleShowWorkingArtifacts: () => void;
} {
    return { showWorkingArtifacts: useSyncExternalStore(subscribe, getSnapshot), toggleShowWorkingArtifacts };
}

export function __resetShowWorkingArtifactsForTest(): void {
    showWorkingArtifacts = readStoredPreference();
    for (const notify of subscribers) {
        notify();
    }
}
