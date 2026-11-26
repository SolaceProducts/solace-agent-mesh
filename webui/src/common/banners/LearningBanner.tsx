// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { FileCode2Icon, X } from "lucide-react";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { Button } from "@/ui/button";

interface LearningBannerProps {
    title: string;
    storageKey: string;
    children: ReactNode;
    showIcon?: boolean;
}

export function LearningBanner({ title, storageKey, children, showIcon = true }: LearningBannerProps) {
    const [isDismissed, setIsDismissed] = useLocalStorage(storageKey, false);

    if (isDismissed) return null;

    return (
        <div className="mb-6 rounded-[4px] border border-(--learning-w20) bg-(--learning-w10) p-4 text-(--primary-text-wMain)">
            <div className="mb-2 flex items-start justify-between gap-3">
                <div className="flex min-w-0 items-center gap-3">
                    {showIcon && <FileCode2Icon className="size-5 shrink-0 text-(--secondary-text-wMain)" />}
                    <h3 className="text-base font-medium">{title}</h3>
                </div>
                <Button variant="ghost" size="sm" onClick={() => setIsDismissed(true)} className="-mt-1 -mr-1 h-6 w-6 shrink-0 p-0 hover:bg-(--learning-w20)" title="Dismiss">
                    <X className="size-4" />
                </Button>
            </div>
            <div className="text-sm">{children}</div>
        </div>
    );
}
