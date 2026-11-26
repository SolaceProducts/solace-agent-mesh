// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type LogoResolution = { kind: "image"; url: string } | { kind: "default" };

export function resolveLogo(preferred: string | null | undefined, other: string | null | undefined): LogoResolution {
    const url = preferred || other;
    if (!url) return { kind: "default" };
    return { kind: "image", url };
}
