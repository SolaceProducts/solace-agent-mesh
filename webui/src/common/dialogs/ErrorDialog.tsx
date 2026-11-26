// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect } from "react";
import { CircleX } from "lucide-react";

import { classifyErrorCategory } from "@/common/product-analytics/errorCategory";
import { report } from "@/common/product-analytics/report";
import { currentRoutePattern } from "@/common/product-analytics/routePattern";
import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

interface ErrorDialogProps {
    title: string;
    error: string | ReactNode;
    open: boolean;
    onOpenChange: (open: boolean) => void;

    subtitle?: string;
    errorDetails?: string;
    onRetry?: () => void;
    retryLabel?: string;
    traceId?: string;
    errorSource?: ProductAnalyticsErrorSource;
}

function useTrackErrorDisplayed(open: boolean, error: string | ReactNode, errorSource?: ProductAnalyticsErrorSource, errorDetails?: string, traceId?: string) {
    useEffect(() => {
        if (!open) return;
        const text = [typeof error === "string" ? error : "", errorDetails ?? ""].join(" ");
        report("error:displayed", {
            errorCategory: classifyErrorCategory(text),
            ...(errorSource ? { errorSource } : {}),
            page: currentRoutePattern(),
            ...(traceId ? { traceId } : {}),
        });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open]);
}

export const ErrorDialog: React.FC<ErrorDialogProps> = ({ title, subtitle, error, errorDetails, open, onOpenChange, onRetry, retryLabel = "Retry", traceId, errorSource }) => {
    useTrackErrorDisplayed(open, error, errorSource, errorDetails, traceId);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle className="flex min-w-0 flex-row gap-1 break-words">{title}</DialogTitle>
                    <DialogDescription>{subtitle}</DialogDescription>
                </DialogHeader>
                <div className="flex flex-col gap-4">
                    <div className="flex flex-row items-center gap-2">
                        <CircleX className="h-6 w-6 shrink-0 self-start text-(--error-wMain)" />
                        <div className="min-w-0 break-words">{error}</div>
                    </div>
                    {errorDetails && <div className="min-w-0 break-words">{errorDetails}</div>}
                </div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" testid="closeButton" title="Close">
                            Close
                        </Button>
                    </DialogClose>
                    {onRetry && (
                        <Button
                            testid="retryButton"
                            title={retryLabel}
                            onClick={() => {
                                onOpenChange(false);
                                onRetry();
                            }}
                        >
                            {retryLabel}
                        </Button>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
