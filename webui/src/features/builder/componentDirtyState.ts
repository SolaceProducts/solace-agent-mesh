// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { decideSend, type DeployFreshness } from "./saveOnlyPolicy";

export type { DeployFreshness } from "./saveOnlyPolicy";

export interface DirtyStateComponent {
    filename?: string;
    origin?: string;
    type?: string;
    deployed_version?: number;
}

export type ArtifactVersions = ReadonlyMap<string, number> & { readonly unresolved?: ReadonlySet<string> };

export function artifactVersionMap(artifacts: ReadonlyArray<{ filename?: string; version?: unknown }> | undefined): ArtifactVersions {
    const map = new Map<string, number>();
    for (const a of artifacts ?? []) {
        if (!a.filename || typeof a.version !== "number") continue;
        const prev = map.get(a.filename);
        if (prev === undefined || a.version > prev) map.set(a.filename, a.version);
    }
    return map;
}

export function mergeArtifactVersions(base: ArtifactVersions, overlay: ArtifactVersions): ArtifactVersions {
    if (overlay.size === 0) return base;
    const merged = new Map(base);
    for (const [filename, version] of overlay) {
        const prev = merged.get(filename);
        if (prev === undefined || version > prev) merged.set(filename, version);
    }
    return merged;
}

export function componentFreshness(comp: DirtyStateComponent, versions: ArtifactVersions | undefined): DeployFreshness {
    if (comp.origin === "platform") return "clean";
    if (!comp.filename) return "stale";
    if (comp.deployed_version === undefined) return "unknown";
    const current = versions?.get(comp.filename);
    if (current === undefined) return "unknown";
    return current > comp.deployed_version ? "stale" : "clean";
}

export function isComponentDirty(comp: DirtyStateComponent, versions: ArtifactVersions | undefined): boolean {
    return componentFreshness(comp, versions) !== "clean";
}

export function hasUndeployedEdits(comp: DirtyStateComponent, versions: ArtifactVersions | undefined, serverApplyEnabled: boolean): boolean {
    if (!hasUpdatePath(comp, serverApplyEnabled)) return false;
    return isComponentDirty(comp, versions);
}

export function isRunningStaleConfig(comp: DirtyStateComponent, versions: ArtifactVersions | undefined, serverApplyEnabled: boolean): boolean {
    if (!hasUpdatePath(comp, serverApplyEnabled)) return false;
    return componentFreshness(comp, versions) === "stale";
}

export function isSaveOnlyType(type: string | undefined): boolean {
    return type === "connector" || type === "skill";
}

export function hasUnsavedSaveOnlyEdits(comp: DirtyStateComponent, versions: ArtifactVersions | undefined, serverApplyEnabled: boolean): boolean {
    if (!comp.filename || !isSaveOnlyType(comp.type)) return false;
    if (comp.type === "connector" && !serverApplyEnabled) return false;
    return saveOnlyFreshness(comp, versions) === "stale";
}

export function saveOnlyFreshness(comp: DirtyStateComponent, versions: ArtifactVersions | undefined): DeployFreshness {
    const freshness = componentFreshness(comp, versions);
    const current = comp.filename ? versions?.get(comp.filename) : undefined;
    if (freshness === "clean" && current !== undefined && comp.deployed_version !== undefined && comp.deployed_version > current) return "unknown";
    return freshness;
}

export function withSavedVersions<T extends { filename?: string; deployed_version?: number }>(components: T[] | undefined, savedVersions: ReadonlyMap<string, number>): T[] | undefined {
    return components?.map(c => {
        const version = c.filename === undefined ? undefined : savedVersions.get(c.filename);
        return version === undefined ? c : { ...c, deployed_version: version };
    });
}

function editEntry(name: string, version: number | undefined): string {
    return `${name}@${version}`;
}

export function latestVersions(listed: ArtifactVersions | undefined, read: ReadonlyMap<string, number>): ArtifactVersions {
    const latest = new Map(listed);
    for (const [filename, version] of read) latest.set(filename, Math.max(version, latest.get(filename) ?? version));
    return latest;
}

type SaveOnlyCandidate = DirtyStateComponent & { name: string };

function isSessionSaveOnly(c: DirtyStateComponent): boolean {
    return c.origin !== "platform" && !!c.filename && isSaveOnlyType(c.type);
}

export function saveOnlyFilenames(components: ReadonlyArray<DirtyStateComponent> | undefined): string[] {
    return [...new Set((components ?? []).filter(isSessionSaveOnly).map(c => c.filename as string))];
}

export function saveReadVersions(components: ReadonlyArray<DirtyStateComponent> | undefined, listed: ArtifactVersions | undefined, read: ReadonlyMap<string, number>): ArtifactVersions {
    const files = new Set(saveOnlyFilenames(components));
    return new Map([...read].filter(([filename]) => files.has(filename)).map(([filename, version]) => [filename, Math.max(version, listed?.get(filename) ?? version)] as const));
}

export function saveOnlyEntries(components: ReadonlyArray<SaveOnlyCandidate> | undefined, versions: ArtifactVersions | undefined): string[] {
    return (components ?? []).filter(isSessionSaveOnly).map(c => editEntry(c.name, versions?.get(c.filename ?? "")));
}

export interface SaveOnlyEditState {
    hasUnsavedEdits: boolean;
    allMatchLastSave: boolean;
    hasSkill: boolean;
    leftOut: string[];
}

export function saveOnlyEditState(components: ReadonlyArray<DirtyStateComponent & { name: string }> | undefined, versions: ArtifactVersions | undefined, serverApplyEnabled: boolean): SaveOnlyEditState {
    const session = (components ?? []).filter(c => c.origin !== "platform");
    const unsaved = session.filter(c => hasUnsavedSaveOnlyEdits(c, versions, serverApplyEnabled));
    const leftOut = session.filter(c => decideSend({ type: c.type, freshness: saveOnlyFreshness(c, versions) }) === "skip").map(c => c.name);
    return {
        hasUnsavedEdits: unsaved.length > 0,
        allMatchLastSave: session.length > 0 && leftOut.length === session.length,
        hasSkill: session.some(c => c.type === "skill"),
        leftOut,
    };
}

function hasUpdatePath(comp: DirtyStateComponent, serverApplyEnabled: boolean): boolean {
    return serverApplyEnabled || comp.type === "workflow";
}

export function versionToRecord(comp: DirtyStateComponent, versions: ArtifactVersions | undefined): number | undefined {
    if (comp.origin === "platform" || !comp.filename) return undefined;
    return versions?.get(comp.filename);
}
