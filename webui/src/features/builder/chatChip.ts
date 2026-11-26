// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function chatChip(text: string): string {
    return `<span style="display:inline-flex;align-items:center;gap:4px;padding:4px 12px;border-radius:6px;font-size:13px;font-weight:500;opacity:0.7">${text}</span>`;
}
