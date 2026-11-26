// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const SUPPORTED_VARIABLES_PATTERN = /\{\{\s*(?:(?:Prompt|Response|Expected\s+Response)\s*|Node\s+(?:Output|Input)\s*:[^{}\r\n]*)\}\}/gi;

type TextPart = { text: string; variable: boolean };

function splitVariables(text: string): TextPart[] {
    const parts: TextPart[] = [];
    SUPPORTED_VARIABLES_PATTERN.lastIndex = 0;
    let cursor = 0;
    for (let m = SUPPORTED_VARIABLES_PATTERN.exec(text); m !== null; m = SUPPORTED_VARIABLES_PATTERN.exec(text)) {
        if (m.index > cursor) parts.push({ text: text.slice(cursor, m.index), variable: false });
        parts.push({ text: m[0], variable: true });
        cursor = m.index + m[0].length;
    }
    if (cursor < text.length) parts.push({ text: text.slice(cursor), variable: false });
    return parts;
}

export function HighlightedPromptText({ text }: { readonly text: string }) {
    const parts = splitVariables(text);
    return (
        <>
            {parts.map((part, i) =>
                part.variable ? (
                    <span key={i} className="rounded bg-(--info-w20) px-0.5 text-(--info-wMain)">
                        {part.text}
                    </span>
                ) : (
                    <span key={i}>{part.text}</span>
                )
            )}
        </>
    );
}
