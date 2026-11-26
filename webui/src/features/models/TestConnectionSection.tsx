// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";

import { modelsApi } from "@/api/models";

import { MessageBanner } from "@/common";
import { Button } from "@/ui";

import type { ModelFormData } from "./modelProviderUtils";
import { buildModelPayload, buildTestPayload, formModelType } from "./modelProviderUtils";

const CONNECTION_TEST_SUCCESS_MESSAGE = "Connection test successful";
const CONNECTION_TEST_FAILURE_FALLBACK = "The connection test failed.";

interface TestConnectionSectionProps {
    getFormData: () => ModelFormData;
    getDirtyFields?: () => Partial<Record<string, boolean>>;
    isNew: boolean;
    modelId?: string;
    disabled?: boolean;
}

export const TestConnectionSection = ({ getFormData, getDirtyFields, isNew, modelId, disabled }: TestConnectionSectionProps) => {
    const [isTesting, setIsTesting] = useState(false);
    const [result, setResult] = useState<{ success: boolean; message: string } | null>(null);
    const resultRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (result && resultRef.current) {
            resultRef.current.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
    }, [result]);

    const handleTestConnection = useCallback(async () => {
        setIsTesting(true);
        setResult(null);

        try {
            const formData = getFormData();
            const dirtyFields = getDirtyFields?.();
            const payload = buildModelPayload(formData, dirtyFields);
            const testPayload = buildTestPayload(payload, !isNew ? modelId : undefined, formModelType(formData));

            const response = await modelsApi.testConnection(testPayload);
            setResult(response);
        } catch (error) {
            const message = error instanceof Error ? error.message : "An unknown error occurred while testing the connection.";
            setResult({ success: false, message });
        } finally {
            setIsTesting(false);
        }
    }, [getFormData, getDirtyFields, isNew, modelId]);

    return (
        <div className="pt-4">
            <div className="flex items-center gap-3">
                <Button type="button" variant="outline" onClick={handleTestConnection} disabled={isTesting || disabled} tooltip={disabled ? "Required configuration is missing" : undefined} tooltipSide="top" testid="test-connection-button">
                    Test Connection
                </Button>
                {isTesting && (
                    <div className="flex items-center gap-2 text-sm text-(--secondary-text-wMain)">
                        <Loader2 className="size-4 animate-spin" />
                        <span>Testing connection...</span>
                    </div>
                )}
            </div>

            {result && (
                <div ref={resultRef} className="mt-3">
                    <MessageBanner variant={result.success ? "success" : "error"} message={result.success ? CONNECTION_TEST_SUCCESS_MESSAGE : result.message || CONNECTION_TEST_FAILURE_FALLBACK} dismissible onDismiss={() => setResult(null)} />
                </div>
            )}
        </div>
    );
};
