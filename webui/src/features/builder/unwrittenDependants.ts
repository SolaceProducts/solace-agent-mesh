// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { extractAgentMetadata, normalizeComponentName } from "./builderUtils";
import type { BuildManifest } from "./hooks/useBuilderManifest";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

export type ReadComponentConfig = (component: ManifestComponent) => Promise<string | null>;

function namesIn(value: unknown): string[] {
    if (!Array.isArray(value)) return [];
    return value.map(entry => (typeof entry === "string" ? entry : (entry as { name?: unknown } | null)?.name)).filter((name): name is string => typeof name === "string");
}

type References = { names: string[] } | "unreadable";

async function loadedSkillNames(component: ManifestComponent, read: ReadComponentConfig): Promise<string[] | "unreadable"> {
    try {
        const config = await read(component);
        return config ? (extractAgentMetadata(config)?.loadableSkillNames ?? []) : "unreadable";
    } catch {
        return "unreadable";
    }
}

async function referencesOf(component: ManifestComponent, read: ReadComponentConfig): Promise<References> {
    const listed = [...namesIn(component.depends_on), ...namesIn(component.connectors), ...namesIn(component.skills)];
    if (component.type !== "agent") return { names: listed };
    const loaded = await loadedSkillNames(component, read);
    return loaded === "unreadable" ? "unreadable" : { names: [...listed, ...loaded] };
}

function usesWithheld(references: References, withheld: ReadonlySet<string>): boolean {
    return references === "unreadable" || references.names.some(name => withheld.has(normalizeComponentName(name)));
}

export async function dependantsOfUnwritten(unwritten: readonly string[], components: readonly ManifestComponent[], read: ReadComponentConfig): Promise<string[]> {
    const withheld = new Set(unwritten.map(normalizeComponentName));
    const found: string[] = [];
    const references = new Map<ManifestComponent, References>();
    let grew = true;
    while (grew) {
        grew = false;
        for (const component of components) {
            if (component.origin === "platform" || withheld.has(normalizeComponentName(component.name))) continue;
            if (!references.has(component)) references.set(component, await referencesOf(component, read));
            if (usesWithheld(references.get(component)!, withheld)) {
                withheld.add(normalizeComponentName(component.name));
                found.push(component.name);
                grew = true;
            }
        }
    }
    return found;
}
