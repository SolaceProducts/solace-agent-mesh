// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

const LABEL = "(?:[a-z0-9]|[a-z0-9][a-z0-9-]*[a-z0-9])";
const DOMAIN_PATTERN = new RegExp(String.raw`^${LABEL}(?:\.${LABEL})+$`);

const PROBLEM = "Invalid entry. Enter only email domains.";

export function normalizeDomain(entry: string): { domain: string } | { problem: string } | null {
    let s = entry.trim();
    if (s.startsWith("@")) s = s.slice(1);
    const at = s.lastIndexOf("@");
    if (at >= 0) s = s.slice(at + 1);
    s = s.trim().toLowerCase();
    if (s === "") return null;
    return DOMAIN_PATTERN.test(s) ? { domain: s } : { problem: PROBLEM };
}

export function parseDomainEntry(raw: string): { value: string; problem?: string } | null {
    const result = normalizeDomain(raw);
    if (result === null) return null;
    return "domain" in result ? { value: result.domain } : { value: raw.trim(), problem: result.problem };
}

export const allowedDomainsSchema = z.array(z.string()).superRefine((entries, ctx) => {
    const bad = entries.findIndex(entry => {
        const result = normalizeDomain(entry);
        return result === null || "problem" in result;
    });
    if (bad >= 0) ctx.addIssue({ code: "custom", message: PROBLEM });
});
