// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function formatWarnPercentage(warnPercentage: number | null): string {
    return warnPercentage === null ? "No alert" : `${warnPercentage}%`;
}
