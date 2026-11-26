// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";

import { copyToClipboard } from "@/utils/clipboard";

import { Button, type ButtonProps } from "@/ui/button";

import { cn } from "@/utils";

const COPIED_RESET_MS = 2000;

export type CopyButtonProps = Omit<ButtonProps, "children" | "onClick" | "tooltip"> & {
    value: string;
    label?: string;
    copiedLabel?: string;
};

export function CopyButton({ value, label = "Copy", copiedLabel = "Copied", variant = "ghost", size = "icon", type = "button", className, ...props }: CopyButtonProps) {
    const [copied, setCopied] = useState(false);

    useEffect(() => {
        if (!copied) return;
        const id = globalThis.setTimeout(() => setCopied(false), COPIED_RESET_MS);
        return () => globalThis.clearTimeout(id);
    }, [copied]);

    const handleCopy = useCallback(async () => {
        if (await copyToClipboard(value)) setCopied(true);
    }, [value]);

    return (
        <Button variant={variant} size={size} type={type} tooltip={copied ? copiedLabel : label} onClick={handleCopy} className={cn("size-6 rounded-sm [&_svg]:size-3.5", className)} {...props}>
            {copied ? <Check className="text-(--success-wMain)" /> : <Copy />}
        </Button>
    );
}
