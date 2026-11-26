// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isBlockedSessionComponent } from "./builderUtils";
import type { ArtifactVersions } from "./componentDirtyState";
import type { BuildManifest } from "./hooks/useBuilderManifest";

export type ValidationStampState = "missing" | "current" | "stale" | "unknown";

export interface ValidationStampReading {
    state: ValidationStampState;
    changed: ReadonlySet<string>;
}

type StampedComponent = NonNullable<BuildManifest["components"]>[number];

function isValidated(c: StampedComponent): boolean {
    return c.origin !== "platform" && !!c.filename?.trim() && !isBlockedSessionComponent(c);
}

export function validationStampState(manifest: BuildManifest | null | undefined, versions: ArtifactVersions): ValidationStampReading {
    if (manifest?.full_validation_passed !== true) return { state: "missing", changed: new Set() };

    const filenames = (manifest.components ?? []).filter(isValidated).map(c => c.filename);
    const recorded = manifest.full_validation_versions;
    if (!recorded) return { state: "stale", changed: new Set(filenames) };

    const compared = new Set(filenames);
    const removed = Object.keys(recorded).some(filename => !compared.has(filename));

    const changed = new Set<string>();
    let unknown = false;
    for (const filename of filenames) {
        const validatedAt = recorded[filename];
        const current = versions.get(filename);
        if (validatedAt === undefined) changed.add(filename);
        else if (current === undefined && versions.unresolved?.has(filename)) changed.add(filename);
        else if (current === undefined || current < validatedAt) unknown = true;
        else if (current > validatedAt) changed.add(filename);
    }
    if (changed.size > 0 || removed) return { state: "stale", changed };
    return { state: unknown ? "unknown" : "current", changed };
}
