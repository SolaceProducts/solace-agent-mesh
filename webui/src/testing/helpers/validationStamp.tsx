// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentType, type ReactNode, useMemo } from "react";

import type { BuildManifest } from "@/features/builder/hooks/useBuilderManifest";

export const STAMPED_VERSION = 1;

export function stampedAtCurrentVersions(manifest: BuildManifest, versionOf: (filename: string) => number | undefined): BuildManifest {
    if (manifest.full_validation_passed !== true) return manifest;
    const recorded = new Proxy<Record<string, number>>({}, { get: (_target, filename) => (typeof filename === "string" ? versionOf(filename) : undefined) });
    return { ...manifest, full_validation_versions: recorded };
}

export function withCurrentStamp<F extends (props: never) => ReactNode>(Actions: F, versionOf: (filename: string) => number | undefined): F {
    type Props = { manifest?: BuildManifest | null } & Record<string, unknown>;
    const Inner = Actions as unknown as ComponentType<Props>;
    function Stamped(props: Props) {
        const { manifest } = props;
        const stamped = useMemo(() => (manifest ? stampedAtCurrentVersions(manifest, versionOf) : manifest), [manifest]);
        return <Inner {...props} manifest={stamped} />;
    }
    return Stamped as unknown as F;
}
