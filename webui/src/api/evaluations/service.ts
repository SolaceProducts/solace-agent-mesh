// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EvalArtifactRole } from "@/types/eval";

export function buildQs(params: Record<string, string | number | undefined | null>): string {
    const qs = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
        if (value === undefined || value === null || value === "") continue;
        qs.set(key, String(value));
    }
    const s = qs.toString();
    return s ? `?${s}` : "";
}

export function artifactVersionQuery(version?: number): string {
    return version == null ? "" : `?version=${version}`;
}

export function exampleArtifactSegment(role: EvalArtifactRole): string {
    return role === "expected" ? "expectedArtifact" : "inputArtifact";
}

export const EXAMPLE_ARTIFACT_GONE_MESSAGE = "This file is no longer available. Replacing or removing an example's file destroys the bytes earlier runs recorded.";
