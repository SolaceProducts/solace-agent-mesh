// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const ARTIFACT_MARKER_REGEX = /«{1,3}artifact(?:_return)?:([^»]+)»{1,3}/g;

export function stripVersionSuffix(filename: string): string {
    if (filename.includes(":")) {
        const parts = filename.split(":");
        const lastPart = parts[parts.length - 1];
        if (/^\d+$/.test(lastPart)) {
            return parts.slice(0, -1).join(":");
        }
    }
    return filename;
}

export function removeArtifactMarkers(text: string, shouldRemove: (filename: string) => boolean = () => true): string {
    return text.replaceAll(ARTIFACT_MARKER_REGEX, (marker: string, spec: string) => (shouldRemove(stripVersionSuffix(spec)) ? "" : marker));
}
