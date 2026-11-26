// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type JSX, useCallback, useRef, useState } from "react";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import { ErrorDialog } from "./ErrorDialog";

interface ErrorInfo {
    title: string;
    error: string;
    onRetry?: () => void;
    retryLabel?: string;
    traceId?: string;
    errorSource?: ProductAnalyticsErrorSource;
}

interface UseErrorDialogReturn {
    ErrorDialog: () => JSX.Element;
    setError: (errorInfo: ErrorInfo | null) => void;
}

export function useErrorDialog(): UseErrorDialogReturn {
    const [error, setError] = useState<ErrorInfo | null>(null);

    const latestError = useRef(error);
    latestError.current = error;

    const handleOpenChange = useCallback((open: boolean) => {
        if (!open) {
            setError(null);
        }
    }, []);

    const ErrorDialogComponent = useCallback(() => {
        const current = latestError.current;
        return (
            <ErrorDialog
                title={current?.title || "Error"}
                error={current?.error || "An error occurred."}
                open={current !== null}
                onOpenChange={handleOpenChange}
                onRetry={current?.onRetry}
                retryLabel={current?.retryLabel}
                traceId={current?.traceId}
                errorSource={current?.errorSource}
            />
        );
    }, [handleOpenChange]);

    return {
        ErrorDialog: ErrorDialogComponent,
        setError,
    };
}
