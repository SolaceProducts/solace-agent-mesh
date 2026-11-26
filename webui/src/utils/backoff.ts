// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function jitteredBackoff(baseMs: number): number {
    return baseMs * (0.5 + crypto.getRandomValues(new Uint32Array(1))[0] / 2 ** 32);
}
