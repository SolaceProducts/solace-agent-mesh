// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { useToolsetBuildTarget } from "@/api/toolsets";

import { Button } from "@/ui/button";

import { copyToClipboard } from "@/utils";

export interface ResolvedBuildTarget {
    targetOS: string;
    targetArch: string;
    targetPython: string;
    targetLabel: string;
    isError: boolean;
}

export function useResolvedBuildTarget(): ResolvedBuildTarget {
    const { data, isLoading, isError } = useToolsetBuildTarget();
    const detected = data?.source === "str-init" ? data : undefined;
    let targetLabel = "could not detect";
    if (isLoading) {
        targetLabel = "detecting…";
    } else if (detected) {
        targetLabel = `${detected.os} / ${detected.arch}`;
        if (detected.pythonVersion) {
            targetLabel += ` · Python ${detected.pythonVersion}`;
        }
    }
    return {
        targetOS: detected?.os ?? "<os>",
        targetArch: detected?.arch ?? "<arch>",
        targetPython: detected?.pythonVersion ?? "",
        targetLabel,
        isError: isError || (data !== undefined && !detected),
    };
}

export function CommandLine({ command, testid }: { readonly command: string; readonly testid?: string }) {
    const [copied, setCopied] = useState(false);

    const handleCopy = async () => {
        if (await copyToClipboard(command)) {
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
        }
    };

    return (
        <div className="relative">
            <code data-testid={testid} className="block rounded-md border border-(--secondary-w20) bg-(--background-w10) py-2 pr-10 pl-3 font-mono text-xs whitespace-pre-wrap text-(--primary-text-wMain)">
                {command}
            </code>
            <Button type="button" variant="ghost" size="sm" onClick={handleCopy} title={copied ? "Copied" : "Copy command"} aria-label="Copy command" className="absolute top-1 right-1 size-7 p-0">
                {copied ? <Check className="size-4 text-(--success-wMain)" /> : <Copy className="size-4 text-(--secondary-text-w50)" />}
            </Button>
        </div>
    );
}
