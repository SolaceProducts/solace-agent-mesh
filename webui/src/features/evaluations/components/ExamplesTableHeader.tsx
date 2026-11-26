// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { HelpCircle } from "lucide-react";

import { TableHead, TableHeader, TableRow } from "@/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

interface ExamplesTableHeaderProps {
    readonly withActionsColumn?: boolean;
    readonly withInputData?: boolean;
}

function LabelWithHint({ label, hint }: { readonly label: string; readonly hint: string }) {
    return (
        <span className="inline-flex items-center gap-1">
            {label}
            <Tooltip>
                <TooltipTrigger asChild>
                    <HelpCircle className="size-3.5 text-(--secondary-text-wMain)" />
                </TooltipTrigger>
                <TooltipContent>{hint}</TooltipContent>
            </Tooltip>
        </span>
    );
}

export function ExamplesTableHeader({ withActionsColumn = false, withInputData = false }: ExamplesTableHeaderProps) {
    return (
        <TableHeader>
            <TableRow>
                <TableHead className="w-10 pl-4 font-semibold">#</TableHead>
                <TableHead className="w-[30%] font-semibold">
                    {withInputData ? (
                        <LabelWithHint label="Prompt or Input" hint="A prompt for an agent, or the input data a workflow receives, with optional instructions for the LLM judge. Each example needs a prompt or input data." />
                    ) : (
                        <>
                            Prompt <span className="text-(--brand-wMain)">*</span>
                        </>
                    )}
                </TableHead>
                <TableHead className="w-[30%] font-semibold">
                    <LabelWithHint label="Expected Response" hint="The ideal agent response (optional). Used by evaluators to compare against actual output." />
                </TableHead>
                <TableHead className="w-[150px] min-w-[120px] font-semibold">
                    <LabelWithHint label="Input File" hint="One optional file delivered to the agent with the prompt, the same way a chat attachment is." />
                </TableHead>
                <TableHead className="w-[150px] min-w-[120px] font-semibold">
                    <LabelWithHint label="Expected File" hint="The reference evaluators grade against. Text only, and never sent to the agent." />
                </TableHead>
                {withActionsColumn && <TableHead className="w-20" />}
            </TableRow>
        </TableHeader>
    );
}
