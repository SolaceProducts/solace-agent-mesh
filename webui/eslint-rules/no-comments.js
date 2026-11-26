// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const DIRECTIVE =
    /^\s*(eslint-disable|eslint-enable|eslint\s|global\s|globals\s|exported\s|@ts-expect-error|@ts-nocheck|@ts-check|prettier-ignore|@vite-ignore|@vitest-environment|@jsxImportSource|@jsx\s|v8 ignore|c8 ignore|istanbul ignore|#__PURE__|@__PURE__|webpackChunkName|\/\s*<reference\s)/;

const MESSAGE =
    "Comments are not allowed: rename for intent, extract a named function, introduce a named constant, or state the rule as a test name.";

function isDirective(comment) {
    const body = comment.type === "Block" ? comment.value.replace(/^\*/, "") : comment.value;
    return DIRECTIVE.test(body);
}

function emptyJSXContainer(sourceCode, comment) {
    const node = sourceCode.getNodeByRangeIndex(comment.range[0]);
    if (node?.type === "JSXEmptyExpression" && node.parent?.type === "JSXExpressionContainer") {
        return node.parent;
    }
    return null;
}

function removalRange(sourceCode, comment) {
    const text = sourceCode.getText();
    const container = emptyJSXContainer(sourceCode, comment);
    let [start, end] = container ? container.range : comment.range;
    const lineStart = text.lastIndexOf("\n", start - 1) + 1;
    let lineEnd = text.indexOf("\n", end);
    if (lineEnd === -1) lineEnd = text.length;
    const before = text.slice(lineStart, start);
    const after = text.slice(end, lineEnd);
    if (before.trim() === "" && after.trim() === "") {
        return [lineStart, Math.min(lineEnd + 1, text.length)];
    }
    if (after.trim() === "") {
        return [lineStart + before.trimEnd().length, lineEnd];
    }
    return [start, end];
}

export default {
    rules: {
        "no-comments": {
            meta: {
                type: "suggestion",
                fixable: "code",
                schema: [],
                messages: { noComment: MESSAGE },
            },
            create(context) {
                const sourceCode = context.sourceCode;
                return {
                    Program() {
                        for (const comment of sourceCode.getAllComments()) {
                            if (comment.type === "Shebang" || isDirective(comment)) continue;
                            context.report({
                                loc: comment.loc,
                                messageId: "noComment",
                                fix: (fixer) => fixer.removeRange(removalRange(sourceCode, comment)),
                            });
                        }
                    },
                };
            },
        },
    },
};
