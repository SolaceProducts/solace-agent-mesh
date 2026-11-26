// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { XCircle } from "lucide-react";

import type { ApiError } from "@/api/client";
import { evaluationsApi } from "@/api/evaluations";

import { type ExampleProblem, exampleProblemsOf, exampleProblemsSummary } from "../utils";

export interface ExampleRefusal {
    readonly problems: ExampleProblem[];
    readonly workflowName: string;
    readonly target: string;
    readonly datasetId: string;
}

export function currentRefusal(refusal: ExampleRefusal | null, target: string, datasetId: string): ExampleRefusal | null {
    return refusal?.target === target && refusal.datasetId === datasetId ? refusal : null;
}

export async function exampleProblemsForError(err: unknown, datasetId: string): Promise<ExampleProblem[]> {
    const details = (err as ApiError | null | undefined)?.validationDetails;
    const aboutExamples = !!details && Object.keys(details).some(key => key.startsWith("examples["));
    const examples = aboutExamples && datasetId ? await evaluationsApi.getExamples(datasetId).catch(() => []) : [];
    return exampleProblemsOf(details, examples);
}

export function ExampleProblemsAlert({ refusal }: { readonly refusal: ExampleRefusal | null }) {
    if (!refusal) return null;
    const { problems, workflowName } = refusal;
    return (
        <div data-testid="exampleProblems" role="alert" className="mt-2 flex flex-col gap-1 text-xs">
            <span className="inline-flex items-start gap-1.5 text-(--secondary-text-wMain)">
                <XCircle className="mt-0.5 size-3.5 shrink-0 text-(--error-wMain)" />
                {exampleProblemsSummary(problems.length, workflowName)}
            </span>
            <ul className="flex flex-col gap-1 pl-5 text-(--secondary-text-wMain)">
                {problems.map(p => (
                    <li key={p.sequenceNumber}>
                        <span className="font-semibold text-(--primary-text-wMain)">Example {p.position}:</span> {p.problems.join(" ")}
                    </li>
                ))}
            </ul>
        </div>
    );
}
