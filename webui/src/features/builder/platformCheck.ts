// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ApplyBuildResponse, BuildOperationResult } from "@/api/builder/applyTypes";
import type { ApiError } from "@/api/client";

import { getErrorMessage } from "@/utils/api";

import { chatChip } from "./chatChip";
import { saveWarnings } from "./serverApply";

export interface PlatformProblem {
    kind: string | null;
    name: string | null;
    message: string;
}

export type PlatformCheck =
    | { kind: "idle" }
    | { kind: "checking" }
    | { kind: "passed"; warnings: string[] }
    | { kind: "refused"; problems: PlatformProblem[]; forbidden: PlatformProblem[] }
    | { kind: "notAllowed"; problems: PlatformProblem[] }
    | { kind: "unavailable"; message: string };

export interface PlatformCheckView {
    check: PlatformCheck;
    canAskBuilder: boolean;
    turnRunning?: boolean;
}

export function awaitsActivation(canBuildActivate: boolean | undefined, view: PlatformCheckView | undefined): boolean {
    return !!canBuildActivate || (!!view && view.check.kind !== "idle");
}

export const IDLE_PLATFORM_CHECK: PlatformCheck = { kind: "idle" };

const REFUSED_STATUSES: ReadonlySet<number> = new Set([400, 409, 422]);
const FORBIDDEN_STATUS = 403;

export function isPlatformRefusal(status: number | undefined): boolean {
    return status !== undefined && REFUSED_STATUSES.has(status);
}

function problemOf(op: BuildOperationResult): PlatformProblem {
    return { kind: op.kind, name: op.name, message: op.error?.message ?? "refused" };
}

export function classifyDryRun(response: ApplyBuildResponse): PlatformCheck {
    const failures = response.operations.filter(op => op.status === "failed");
    const problems = failures.filter(op => isPlatformRefusal(op.error?.httpStatus)).map(problemOf);
    const forbidden = failures.filter(op => op.error?.httpStatus === FORBIDDEN_STATUS).map(problemOf);
    if (problems.length > 0) return { kind: "refused", problems, forbidden };
    if (forbidden.length > 0) return { kind: "notAllowed", problems: forbidden };
    if (failures.length > 0) return { kind: "unavailable", message: failures[0].error?.message ?? "The platform could not check the build." };
    return { kind: "passed", warnings: saveWarnings(response.operations) };
}

export function classifyDryRunError(error: unknown): PlatformCheck {
    const status = (error as ApiError | undefined)?.status;
    const message = getErrorMessage(error, "The platform could not check the build.");
    const problem: PlatformProblem = { kind: null, name: null, message };
    if (status === FORBIDDEN_STATUS) return { kind: "notAllowed", problems: [problem] };
    if (isPlatformRefusal(status)) return { kind: "refused", problems: [problem], forbidden: [] };
    return { kind: "unavailable", message };
}

function describeProblem({ kind, name, message }: PlatformProblem): string {
    const subject = [kind, name].filter(Boolean).join(" ");
    return subject ? `- ${subject}: ${message}` : `- ${message}`;
}

export function platformCheckMessage(problems: PlatformProblem[]): string {
    return (
        "[automated] Platform check: the platform would refuse this build as it stands:\n\n" +
        problems.map(describeProblem).join("\n") +
        "\n\nPlease fix these in the affected configuration(s), then run `validate_build_manifest` with `full_validation: true` again."
    );
}

export const PLATFORM_CHECK_CHIP = chatChip("⟳ Platform check");
