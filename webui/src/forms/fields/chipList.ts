// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type ParsedEntry = { value: string; problem?: string } | null;

export type ParseEntry = (raw: string) => ParsedEntry;

export function addEntries(current: string[], raw: string, parseEntry: ParseEntry, splitPattern: RegExp): string[] {
    let values = current;
    for (const part of raw.split(splitPattern)) {
        const parsed = part.trim() === "" ? null : parseEntry(part);
        if (parsed && !values.includes(parsed.value)) values = [...values, parsed.value];
    }
    return values;
}

const PASTE_SEPARATOR = ",";
const STARTS_WITH_PUNCTUATION = /^[^\p{L}\p{N}]/u;
const ENDS_WITH_PUNCTUATION = /[^\p{L}\p{N}]$/u;

export function joinPaste(draft: string, pasted: string): string {
    if (draft.trim() === "") return pasted;
    if (pasted.startsWith("@") && !ENDS_WITH_PUNCTUATION.test(draft)) return draft + PASTE_SEPARATOR + pasted;
    return STARTS_WITH_PUNCTUATION.test(pasted) || ENDS_WITH_PUNCTUATION.test(draft) ? draft + pasted : draft + PASTE_SEPARATOR + pasted;
}
