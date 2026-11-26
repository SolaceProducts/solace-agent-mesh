// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink, X } from "lucide-react";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { Button } from "@/ui/button";

import { cn } from "@/utils";

export interface OnboardingBannerProps {
    storageKey: string;
    header: string;
    description: string;
    learnMoreText: string;
    learnMoreUrl: string;
    className?: string;
}

export const OnboardingBanner = ({ storageKey, header, description, learnMoreText, learnMoreUrl, className }: OnboardingBannerProps) => {
    const [isDismissed, setIsDismissed] = useLocalStorage(storageKey, false);

    const handleDismiss = () => {
        setIsDismissed(true);
    };

    if (isDismissed) {
        return null;
    }

    return (
        <div className={cn("relative rounded-lg border border-(--learning-w20) bg-(--learning-w10) p-4", className)}>
            <Button variant="ghost" size="icon" onClick={handleDismiss} tooltip="Close" className="absolute top-2 right-2">
                <X size={16} />
            </Button>

            <div className="pr-8">
                <p className="text-sm">
                    <span className="font-semibold">{header}</span> {description}
                </p>

                <Button variant="link" onClick={() => window.open(learnMoreUrl, "_blank")} className="mt-1 flex h-auto !p-0 text-left leading-snug whitespace-normal">
                    <span>{learnMoreText}</span>
                    <ExternalLink size={14} className="shrink-0" />
                </Button>
            </div>
        </div>
    );
};
