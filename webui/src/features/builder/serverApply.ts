// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { BuildApplyKind, BuildComponentInput, BuildOperationResult } from "@/api/builder/applyTypes";

import type { OwnsResource } from "./builderOwnership";
import { type BuildManifest, platformId } from "./hooks/useBuilderManifest";
import { decideWrite } from "./saveOnlyPolicy";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

const SERVER_APPLY_KINDS: Record<string, BuildApplyKind> = {
    connector: "connector",
    a2a_agent: "a2a_agent",
    agent: "agent",
    workflow: "workflow",
    gateway: "gateway",
};

export function isServerApplyComponent(comp: ManifestComponent): boolean {
    if (comp.origin === "platform") return false;
    return serverApplyKind(comp) !== null;
}

export function serverApplyKind(comp: ManifestComponent): BuildApplyKind | null {
    return SERVER_APPLY_KINDS[comp.type ?? ""] ?? null;
}

export function connectorNamesFor(comp: ManifestComponent, all: ManifestComponent[]): string[] {
    const names = new Set<string>(comp.connectors ?? []);
    for (const dep of comp.depends_on ?? []) {
        if (all.some(c => c.name === dep && c.type === "connector")) names.add(dep);
    }
    return [...names];
}

const RENAMED_BY_ID: ReadonlySet<BuildApplyKind> = new Set(["agent", "workflow"]);

function recordedPlatformId(kind: BuildApplyKind, comp: ManifestComponent): { resourceId?: string } {
    const resourceId = RENAMED_BY_ID.has(kind) ? platformId(comp) : undefined;
    return resourceId ? { resourceId } : {};
}

export interface ConfigReadOptions {
    skipAccumulated?: boolean;
    version?: number;
}

export type FetchConfigByFilename = (filename: string, opts?: ConfigReadOptions) => Promise<string | null>;

export async function toBuildComponentInputs(components: ManifestComponent[], fetchConfigByFilename: FetchConfigByFilename, onComponent?: (comp: ManifestComponent) => void): Promise<BuildComponentInput[]> {
    const out: BuildComponentInput[] = [];
    for (const comp of components) {
        if (!isServerApplyComponent(comp)) continue;
        const kind = serverApplyKind(comp);
        if (!kind) continue;

        onComponent?.(comp);
        const content = await fetchConfigByFilename(comp.filename, { skipAccumulated: true });
        if (!content) {
            throw new Error(`Could not resolve config for "${comp.name}" (${comp.filename}).`);
        }

        const dependsOnAConnector = (comp.depends_on ?? []).some(dep => components.some(c => c.name === dep && c.type === "connector"));
        const statesConnectors = comp.connectors !== undefined || dependsOnAConnector;
        const connectors = kind === "agent" ? connectorNamesFor(comp, components) : [];
        out.push({
            kind,
            name: comp.name,
            content,
            sourceFilename: comp.filename,
            ...recordedPlatformId(kind, comp),
            ...(kind === "agent" && statesConnectors ? { connectors } : {}),
        });
    }
    return out;
}

export function saveWarnings(operations: BuildOperationResult[]): string[] {
    const componentsByWarning = new Map<string, string[]>();
    for (const op of operations) {
        for (const warning of op.warnings ?? []) {
            const names = componentsByWarning.get(warning) ?? [];
            if (!names.includes(op.name)) componentsByWarning.set(warning, [...names, op.name]);
        }
    }
    return [...componentsByWarning].map(([warning, names]) => `${names.join(", ")}: ${warning}`);
}

export function componentNamedIn(message: string, components: ManifestComponent[]): ManifestComponent | null {
    let found: ManifestComponent | null = null;
    let foundAt = Infinity;
    for (const comp of components) {
        const at = message.indexOf(`"${comp.name}"`);
        if (at !== -1 && at < foundAt) {
            found = comp;
            foundAt = at;
        }
    }
    return found;
}

export function platformMatchedNames(operations: BuildOperationResult[]): string[] {
    return operations.filter(op => op.status === "succeeded" && op.action === "unchanged" && !op.changesDeclined).map(op => op.name);
}

export interface PlannedOverwrite {
    name: string;
    kind: string;
    changedFields: string[];
}

const KIND_LABELS: Partial<Record<string, string>> = { gateway: "entrypoint", a2a_agent: "remote agent" };

function kindLabel(kind: string): string {
    return KIND_LABELS[kind] ?? kind;
}

export function plannedOverwrites(operations: BuildOperationResult[], owns: OwnsResource): PlannedOverwrite[] {
    const isTheBuilders = (op: BuildOperationResult) => !!op.resourceId && owns({ id: op.resourceId, kind: op.kind });
    return operations
        .filter(op => op.status === "planned" && op.action === "update" && decideWrite(isTheBuilders(op)) === "confirm")
        .map(op => ({ name: op.renamedFrom ? `${op.renamedFrom} → ${op.resolvedName ?? op.name}` : op.name, kind: kindLabel(op.kind), changedFields: op.changedFields ?? [] }));
}

export function writtenResources(operations: BuildOperationResult[]): Array<{ id: string; kind: string }> {
    const written: Array<{ id: string; kind: string }> = [];
    for (const op of operations) {
        if (op.status === "succeeded" && (op.action === "create" || op.action === "update") && op.resourceId) written.push({ id: op.resourceId, kind: op.kind });
    }
    return written;
}

export function updatedInPlaceNames(operations: BuildOperationResult[]): string[] {
    return operations.filter(op => op.status === "succeeded" && op.action === "update").map(op => op.name);
}
