// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { BuildManifest } from "./useBuilderManifest";

export interface ConfigsWithoutManifestInput {
    configCount: number;
    manifest: BuildManifest | null;
    manifestSettled: boolean;
    manifestUnreadable: boolean;
    isResponding: boolean;
}

export function configsWithoutManifest({ configCount, manifest, manifestSettled, manifestUnreadable, isResponding }: ConfigsWithoutManifestInput): boolean {
    return configCount > 0 && manifest === null && !manifestUnreadable && manifestSettled && !isResponding;
}

export function rewriteManifestRequest(showingAManifest: boolean): string {
    const symptom = showingAManifest ? "so the canvas is showing an older version and the build cannot pick up recent changes" : "so the canvas is empty and the build cannot be saved";
    return `[automated] \`build_manifest.yaml\` exists in this session but could not be parsed, ${symptom}.\n\n` + "Read it, repair the invalid YAML in place, and keep the components it already lists.";
}

export function manifestWarning(unreadable: boolean, showingAManifest: boolean): string {
    if (!unreadable) return "No build manifest in this session — the canvas stays empty and this build can't be saved.";
    if (showingAManifest) return "The latest build manifest couldn't be read — the canvas is showing the last version that parsed, so recent changes may be missing.";
    return "The build manifest in this session couldn't be read — the canvas stays empty and this build can't be saved.";
}

export function finishBuildRequest(configCount: number): string {
    const configs = configCount === 1 ? "1 component config" : `${configCount} component configs`;
    return `[automated] This session has ${configs} but no \`build_manifest.yaml\`, so nothing can be saved or shown on the canvas.\n\n` + "Write `build_manifest.yaml` listing the component config file(s) you already created, then resume the build.";
}
