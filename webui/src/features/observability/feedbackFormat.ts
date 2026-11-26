// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const NEGATIVE_RATIO_DECIMALS = 3;

export function formatNegativeRatio(ratio: number): string {
    return `${(ratio * 100).toFixed(NEGATIVE_RATIO_DECIMALS)}%`;
}

export function formatCount(value: number): string {
    return value.toLocaleString("en-US");
}
