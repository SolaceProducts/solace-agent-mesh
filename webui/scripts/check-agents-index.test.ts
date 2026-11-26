// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

import { checkIndexCompleteness, collectRuleFiles } from "./check-agents-index.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FIXTURES = path.join(__dirname, "__fixtures__");

describe("collectRuleFiles", () => {
    test("accepts a rule file with all required frontmatter fields", () => {
        const { files, errors } = collectRuleFiles(path.join(FIXTURES, "valid-rules"));
        expect(files).toContain("quality-example-rule.md");
        expect(errors).toEqual([]);
    });

    test("only requires a title for reference-* files", () => {
        const { files, errors } = collectRuleFiles(path.join(FIXTURES, "valid-rules"));
        expect(files).toContain("reference-example.md");
        expect(errors).toEqual([]);
    });

    test("ignores _template.md", () => {
        const { files } = collectRuleFiles(path.join(FIXTURES, "valid-rules"));
        expect(files).not.toContain("_template.md");
    });

    test("flags a rule file missing a required frontmatter field", () => {
        const { errors } = collectRuleFiles(path.join(FIXTURES, "broken-rules"));
        expect(errors.some(e => e.includes('missing required frontmatter field "impact"'))).toBe(true);
    });
});

describe("checkIndexCompleteness", () => {
    test("passes when every rule file is listed in the README", () => {
        const errors = checkIndexCompleteness(["quality-example-rule.md"], "| `quality-example-rule.md` | Example | quality | LOW |");
        expect(errors).toEqual([]);
    });

    test("flags a rule file missing from the README", () => {
        const errors = checkIndexCompleteness(["quality-example-rule.md"], "no rules listed here");
        expect(errors.some(e => e.includes("missing from agents/README.md"))).toBe(true);
    });

    test("flags a README row whose file doesn't exist on disk", () => {
        const errors = checkIndexCompleteness([], "| `ghost-rule.md` | Ghost | quality | LOW |");
        expect(errors.some(e => e.includes("ghost-rule.md"))).toBe(true);
    });
});
