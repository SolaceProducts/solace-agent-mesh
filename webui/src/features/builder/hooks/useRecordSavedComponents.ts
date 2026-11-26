// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { builderKeys, listManifestVersions } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";

import { normalizeComponentName } from "../builderUtils";
import { isSaveOnlyType, saveOnlyFilenames } from "../componentDirtyState";

import type { BuildManifest, SaveManifestOptions, SaveManifestResult } from "./useBuilderManifest";

type ManifestComponents = NonNullable<BuildManifest["components"]>;

export type SaveManifest = (updater: (prev: BuildManifest) => BuildManifest, options?: SaveManifestOptions) => Promise<SaveManifestResult>;

export interface SaveProof {
    changed: readonly string[];
    matched: readonly string[];
    declined: readonly string[];
}

export function saveOnlyHeldByPlatform(components: ManifestComponents | undefined, proof: SaveProof): string[] {
    const held = new Set([...proof.changed, ...proof.matched].map(normalizeComponentName));
    const declined = new Set(proof.declined.map(normalizeComponentName));
    return (components ?? []).filter(c => isSaveOnlyType(c.type) && c.origin !== "platform" && !!c.filename && held.has(normalizeComponentName(c.name)) && !declined.has(normalizeComponentName(c.name))).map(c => c.name);
}

export function recordSavedVersions(names: ReadonlySet<string>, versions: ReadonlyMap<string, number>) {
    return (prev: BuildManifest): BuildManifest => ({
        ...prev,
        components: prev.components?.map(c => {
            const version = isSaveOnlyType(c.type) && names.has(c.name) ? versions.get(c.filename) : undefined;
            return version === undefined ? c : { ...c, deployed_version: version };
        }),
    });
}

export type VersionsByFile = ReadonlyMap<string, number>;

export interface VersionsRead {
    at: VersionsByFile;
    failed: readonly string[];
}

export interface SavedComponentsRecorder {
    versionsBeforeSave: (components: ManifestComponents) => Promise<VersionsRead>;
    record: (proof: SaveProof, readAt: VersionsByFile, components: ManifestComponents) => Promise<VersionsByFile>;
}

export function useRecordSavedComponents(saveManifest: SaveManifest | undefined): SavedComponentsRecorder {
    const { sessionId } = useChatContext();
    const queryClient = useQueryClient();

    const versionsBeforeSave = useCallback(
        async (all: ManifestComponents): Promise<VersionsRead> => {
            const filenames = saveOnlyFilenames(all);
            if (!sessionId || filenames.length === 0) return { at: new Map(), failed: [] };
            const looked = await Promise.all(
                filenames.map(async filename => {
                    try {
                        const listed: unknown = await queryClient.fetchQuery({ queryKey: builderKeys.manifestVersions(sessionId, filename), queryFn: () => listManifestVersions(sessionId, filename), staleTime: 0 });
                        if (!Array.isArray(listed)) throw new Error("the versions answer is not a list");
                        const versions = listed.filter(Number.isInteger) as number[];
                        if (versions.length === 0) console.warn("[builder] a config has no versions in the store", { filename });
                        return { filename, version: versions.length > 0 ? Math.max(...versions) : undefined, failed: false };
                    } catch (err) {
                        console.warn("[builder] could not read the versions a save is about to send", { filename, error: getErrorMessage(err) });
                        return { filename, version: undefined, failed: true };
                    }
                })
            );
            const at = new Map(looked.flatMap(({ filename, version }) => (version === undefined ? [] : [[filename, version] as const])));
            return { at, failed: looked.filter(entry => entry.failed).map(entry => entry.filename) };
        },
        [queryClient, sessionId]
    );

    const record = useCallback(
        async (proof: SaveProof, readAt: VersionsByFile, components: ManifestComponents): Promise<VersionsByFile> => {
            const recorded = new Map<string, number>();
            try {
                const names = saveOnlyHeldByPlatform(components, proof).filter(name => components.some(c => c.name === name && readAt.has(c.filename)));
                if (names.length === 0) return recorded;
                if (!saveManifest || !sessionId) {
                    console.warn("[builder] saved component versions not recorded: no manifest writer or session", { sessionId, names });
                    return recorded;
                }
                for (const comp of components) {
                    const version = names.includes(comp.name) ? readAt.get(comp.filename) : undefined;
                    if (version !== undefined) recorded.set(comp.filename, version);
                }
                const result = await saveManifest(recordSavedVersions(new Set(names), recorded), { forSession: sessionId });
                if (!result.ok) console.warn("[builder] could not record saved component versions", result);
            } catch (err) {
                console.warn("[builder] could not record saved component versions", getErrorMessage(err));
            }
            return recorded;
        },
        [saveManifest, sessionId]
    );

    return useMemo(() => ({ versionsBeforeSave, record }), [versionsBeforeSave, record]);
}
