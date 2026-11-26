// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import yaml from "js-yaml";
import { Code, Eye } from "lucide-react";

import { JSONViewer } from "@/common/JSONViewer";

import { Button } from "@/ui/button";

import { type BaseRendererProps, TextRenderer } from ".";

interface StructuredDataRendererProps extends BaseRendererProps {
    rendererType: "json" | "yaml";
}

export const StructuredDataRenderer: React.FC<StructuredDataRendererProps> = ({ content, rendererType, setRenderError, isStreaming }) => {
    const [showRawTextView, setShowRawTextView] = useState(false);

    const [rawData, parsedData, parseError] = useMemo(() => {
        if (isStreaming) {
            return [content, null, null];
        }
        try {
            if (rendererType === "yaml") {
                const parsedYaml = yaml.load(content);
                return [content, parsedYaml, null];
            } else if (rendererType === "json") {
                const parsedJson = JSON.parse(content);
                return [JSON.stringify(parsedJson, null, 2), parsedJson, null];
            }

            throw new Error(`Unsupported renderer type: ${rendererType}`);
        } catch (e) {
            console.error(`Error parsing ${rendererType === "yaml" ? "YAML" : "JSON"} for panel:`, e);
            return [content, null, (e as Error).message];
        }
    }, [content, rendererType, isStreaming]);

    useEffect(() => {
        if (!parseError) {
            setRenderError(null);
            return;
        }
        const errorType = rendererType === "yaml" ? "YAML" : "JSON";
        setRenderError(`${errorType} parsing failed: ${parseError}`);
    }, [parseError, rendererType, setRenderError]);

    const effectiveShowRawText = parseError || isStreaming ? true : showRawTextView;

    return (
        <div className="relative flex h-full flex-col overflow-hidden bg-(--background-w10)">
            {!parseError && !isStreaming && (
                <div className="absolute top-4 right-4 z-10">
                    <Button onClick={() => setShowRawTextView(!showRawTextView)} title={showRawTextView ? "Show Structured View" : "Show Raw Text"}>
                        {showRawTextView ? (
                            <>
                                <Eye /> Structured
                            </>
                        ) : (
                            <>
                                <Code /> Raw Text
                            </>
                        )}
                    </Button>
                </div>
            )}
            {parseError && (
                <div className="border-b bg-(--warning-w10) px-4 py-2 text-xs text-(--warning-wMain)">
                    {rendererType === "yaml" ? "YAML" : "JSON"} parse failed: {parseError}. Showing raw content.
                </div>
            )}
            <div className="flex min-h-0 flex-col">
                <div className="flex-1 overflow-auto">{effectiveShowRawText ? <TextRenderer content={rawData} setRenderError={setRenderError} /> : <JSONViewer data={parsedData} maxDepth={4} className="min-h-16 border-none p-2" />}</div>
            </div>
        </div>
    );
};
