// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FieldErrors, FieldValues } from "react-hook-form";

import { isReportableFormField } from "./formFields";
import { report } from "./report";
import { currentRoutePattern } from "./routePattern";
import type { ProductAnalyticsFormField, ProductAnalyticsFormName } from "./types";

const MAX_REPORTED_PATH_SEGMENTS = 2;

type ErrorNode = Record<string, unknown>;

interface ReportedError {
    field: ProductAnalyticsFormField;
    errorType: string;
}

const ERROR_METADATA_KEYS = new Set(["ref", "types"]);

function isLeaf(node: ErrorNode): boolean {
    if (typeof node.type === "string" || typeof node.message === "string") return true;
    return childErrors(node).length === 0;
}

function childErrors(node: ErrorNode): Array<[string, ErrorNode]> {
    return Object.entries(node).filter(([key, value]) => !ERROR_METADATA_KEYS.has(key) && !!value && typeof value === "object") as Array<[string, ErrorNode]>;
}

function collectLeaves(node: ErrorNode, segments: string[], out: ReportedError[]): void {
    if (isLeaf(node)) {
        const path = segments.slice(0, MAX_REPORTED_PATH_SEGMENTS).join(".");
        const field = isReportableFormField(path) ? path : (segments[0] as ProductAnalyticsFormField);
        out.push({ field, errorType: typeof node.type === "string" ? node.type : "unknown" });
        return;
    }
    for (const [key, child] of childErrors(node)) {
        collectLeaves(child, [...segments, key], out);
    }
}

export function trackFormErrors<T extends FieldValues>(errors: FieldErrors<T>, formName: ProductAnalyticsFormName): void {
    const collected: ReportedError[] = [];
    for (const [field, error] of Object.entries(errors)) {
        if (!error || typeof error !== "object") continue;
        collectLeaves(error as ErrorNode, [field], collected);
    }
    const seen = new Set<string>();
    for (const { field, errorType } of collected) {
        if (seen.has(`${field}|${errorType}`)) continue;
        seen.add(`${field}|${errorType}`);
        report("form:validation_error", {
            form: formName,
            field,
            errorType,
            path: currentRoutePattern(),
        });
    }
}
