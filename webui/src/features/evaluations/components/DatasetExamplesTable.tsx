// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import type { EvalExampleArtifact } from "@/types/eval";

import { formatInputData } from "../utils";

import { ExampleArtifactField } from "./ExampleArtifactField";

export interface DatasetExampleRow {
    id: string;
    prompt: string;
    inputData?: Record<string, unknown> | null;
    expectedResponse?: string | null;
    inputArtifact?: EvalExampleArtifact | null;
    expectedArtifact?: EvalExampleArtifact | null;
}

interface DatasetExamplesTableProps {
    readonly rows: DatasetExampleRow[];
    readonly showArtifacts?: boolean;
    readonly showInputData?: boolean;
}

export function DatasetExamplesTable({ rows, showArtifacts = false, showInputData = false }: DatasetExamplesTableProps) {
    const contentWidth = showArtifacts ? "w-[30%]" : "w-1/2";

    return (
        <div className="rounded-xs border">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className={cn("border-r text-xs font-semibold", contentWidth)}>{showInputData ? "Input data" : "Prompt"}</TableHead>
                        <TableHead className={cn("text-xs font-semibold", contentWidth)}>Response</TableHead>
                        {showArtifacts && (
                            <>
                                <TableHead className="w-1/5 min-w-[90px] border-l text-xs font-semibold">Input File</TableHead>
                                <TableHead className="w-1/5 min-w-[90px] border-l text-xs font-semibold">Expected File</TableHead>
                            </>
                        )}
                    </TableRow>
                </TableHeader>
                <TableBody borderLastRow>
                    {rows.map(ex => (
                        <TableRow key={ex.id} className="align-top">
                            <TableCell className="max-w-0 border-r border-b py-3">
                                <span className="text-sm break-words whitespace-pre-wrap">{ex.prompt}</span>
                                {ex.inputData && <span className="block font-mono text-xs break-all whitespace-pre-wrap text-(--secondary-text-wMain)">{formatInputData(ex.inputData)}</span>}
                            </TableCell>
                            <TableCell className="max-w-0 border-b py-3">
                                <span className="text-sm break-words whitespace-pre-wrap text-(--secondary-text-wMain)">{ex.expectedResponse ?? ""}</span>
                            </TableCell>
                            {showArtifacts && (
                                <>
                                    <TableCell className="max-w-0 min-w-[90px] border-b border-l py-3">
                                        <ExampleArtifactField artifact={ex.inputArtifact} />
                                    </TableCell>
                                    <TableCell className="max-w-0 min-w-[90px] border-b border-l py-3">
                                        <ExampleArtifactField artifact={ex.expectedArtifact} subject="expected file" />
                                    </TableCell>
                                </>
                            )}
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
        </div>
    );
}
