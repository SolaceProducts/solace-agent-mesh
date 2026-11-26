// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { existsSync, readFileSync, readdirSync, realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import yaml from "js-yaml";

/* global console, process */

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const RULES_DIR = path.join(__dirname, "..", "agents", "rules");
const README_PATH = path.join(__dirname, "..", "agents", "README.md");
const REQUIRED_RULE_FIELDS = ["title", "impact", "impactDescription", "enforcement", "story", "tags"];
const AUTHORING_CONTRACT = "webui/agents/rules/reference-writing-rules.md";

export function parseFrontmatter(content) {
    const match = content.match(/^---\n([\s\S]*?)\n---/);
    if (!match) return null;
    return yaml.load(match[1]) ?? {};
}

function safeParseFrontmatter(content, file, errors) {
    try {
        return parseFrontmatter(content);
    } catch (e) {
        errors.push(`${file}: frontmatter is not valid YAML (${e.reason ?? e.message}) — quote values containing ": " — see ${AUTHORING_CONTRACT}`);
        return undefined;
    }
}

export function collectRuleFiles(rulesDir) {
    const errors = [];
    const files = readdirSync(rulesDir).filter(f => f.endsWith(".md") && f !== "_template.md");

    for (const file of files) {
        const content = readFileSync(path.join(rulesDir, file), "utf8");
        const frontmatter = safeParseFrontmatter(content, file, errors);
        if (frontmatter === undefined) continue;
        if (!frontmatter) {
            errors.push(`${file}: missing frontmatter block`);
            continue;
        }
        const isReference = file.startsWith("reference-");
        const requiredFields = isReference ? ["title"] : REQUIRED_RULE_FIELDS;
        for (const field of requiredFields) {
            const value = frontmatter[field];
            if (value === undefined || value === null || value === "") {
                errors.push(`${file}: missing required frontmatter field "${field}" — see ${AUTHORING_CONTRACT}`);
            }
        }
    }

    return { files, errors };
}

export function checkIndexCompleteness(files, readmeContent) {
    const errors = [];

    for (const file of files) {
        if (!readmeContent.includes(file)) {
            errors.push(`${file} missing from agents/README.md — see ${AUTHORING_CONTRACT}`);
        }
    }

    const listedFiles = [...readmeContent.matchAll(/`([a-z0-9-]+\.md)`/g)].map(m => m[1]);
    for (const listed of listedFiles) {
        if (!files.includes(listed)) {
            errors.push(`agents/README.md lists "${listed}" but that file does not exist in agents/rules/ — see ${AUTHORING_CONTRACT}`);
        }
    }

    return errors;
}

function main() {
    if (!existsSync(RULES_DIR)) {
        console.log("check-agents-index: skipped, agents/rules is not in this source tree");
        return;
    }
    const { files, errors: frontmatterErrors } = collectRuleFiles(RULES_DIR);
    const readmeContent = readFileSync(README_PATH, "utf8");
    const indexErrors = checkIndexCompleteness(files, readmeContent);
    const allErrors = [...frontmatterErrors, ...indexErrors];

    if (allErrors.length > 0) {
        console.error("check-agents-index: FAILED");
        for (const error of allErrors) {
            console.error(`  - ${error}`);
        }
        process.exit(1);
    }

    console.log(`check-agents-index: OK (${files.length} rule files indexed)`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
    main();
}
