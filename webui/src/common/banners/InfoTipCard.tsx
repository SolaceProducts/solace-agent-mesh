// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";
import { ExternalLink, X } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

export const InfoTipCard = ({
    storageKey,
    header,
    description,
    learnMoreText,
    learnMoreUrl,
    illustration,
    className,
}: {
    storageKey: string;
    header: string;
    description: string;
    learnMoreText?: string;
    learnMoreUrl?: string;
    illustration?: string;
    className?: string;
}) => {
    const [dismissed, setDismissed] = useState<boolean>(() => {
        if (typeof window === "undefined") return false;
        return window.localStorage.getItem(storageKey) === "true";
    });

    const handleDismiss = useCallback(() => {
        window.localStorage.setItem(storageKey, "true");
        setDismissed(true);
    }, [storageKey]);

    if (dismissed) return null;

    return (
        <div className={cn("relative rounded-lg border border-(--learning-w20) bg-(--learning-w10) p-4", className)}>
            <Button variant="ghost" size="icon" aria-label="Close" onClick={handleDismiss} className="absolute top-2 right-2 size-7">
                <X className="size-4" />
            </Button>
            <div className="flex items-center gap-4 pr-8">
                {illustration && <img src={illustration} alt="" aria-hidden className="w-20 shrink-0" />}
                <div>
                    <h4 className="text-sm font-semibold">{header}</h4>
                    <p className="mt-1 text-sm">{description}</p>
                    {learnMoreText && learnMoreUrl && (
                        <a href={learnMoreUrl} target="_blank" rel="noopener noreferrer" className="mt-2 inline-flex items-center gap-1 text-sm font-semibold text-(--primary-wMain) hover:underline">
                            {learnMoreText}
                            <ExternalLink className="size-3.5" aria-hidden />
                        </a>
                    )}
                </div>
            </div>
        </div>
    );
};
