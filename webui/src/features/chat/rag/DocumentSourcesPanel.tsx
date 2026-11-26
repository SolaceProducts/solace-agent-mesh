// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { Link2, Search } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { groupDocumentSources } from "@/utils/documentSourceUtils";

import { Accordion } from "@/ui/accordion";

import { EmptyState } from "@/common";
import type { RAGSearchResult } from "@/types";

import { DocumentSourceCard } from "./DocumentSourceCard";

export interface DocumentSourcesPanelProps {
    ragData: RAGSearchResult[] | null;
    enabled: boolean;
    readOnly?: boolean;
}

export const DocumentSourcesPanel: React.FC<DocumentSourcesPanelProps> = ({ ragData, enabled, readOnly = false }) => {
    const { expandedDocumentFilename, setExpandedDocumentFilename } = useChatContext();
    const [expandedValues, setExpandedValues] = useState<string[]>([]);

    const groupedDocuments = useMemo(() => {
        if (!ragData || ragData.length === 0) return [];
        return groupDocumentSources(ragData);
    }, [ragData]);

    useEffect(() => {
        if (!expandedDocumentFilename) return;

        const idx = groupedDocuments.findIndex(doc => doc.filename === expandedDocumentFilename);
        if (idx >= 0) {
            setExpandedValues(prev => {
                const value = `document-${idx}`;
                if (prev.includes(value)) return prev;
                return [...prev, value];
            });
        }

        requestAnimationFrame(() => {
            setExpandedDocumentFilename(null);
        });
    }, [expandedDocumentFilename, groupedDocuments, setExpandedDocumentFilename]);

    if (!enabled) {
        return <EmptyState title="Document Sources" subtitle="Source visibility is disabled in settings" variant="noImage" image={<Link2 className="h-12 w-12 opacity-50" />} />;
    }

    if (!ragData || ragData.length === 0 || groupedDocuments.length === 0) {
        return (
            <EmptyState
                title="Document Sources"
                subtitle={
                    <>
                        <span>No document sources available yet</span>
                        <br />
                        <span className="text-xs">Document citations will appear here after a document search</span>
                    </>
                }
                variant="noImage"
                image={<Search className="h-12 w-12 opacity-50" />}
            />
        );
    }

    const totalCitations = groupedDocuments.reduce((sum, doc) => sum + doc.totalCitations, 0);

    return (
        <div className="flex h-full flex-col overflow-hidden">
            <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
                <div className="mb-4">
                    <h3 className="text-sm font-semibold text-(--secondary-text-wMain)">
                        {groupedDocuments.length} Document
                        {groupedDocuments.length !== 1 ? "s" : ""} | {totalCitations} Citation{totalCitations !== 1 ? "s" : ""}
                    </h3>
                </div>

                <Accordion type="multiple" value={expandedValues} onValueChange={setExpandedValues} className="space-y-2">
                    {groupedDocuments.map((document, idx) => (
                        <DocumentSourceCard key={document.filename} document={document} sourceIndex={idx} readOnly={readOnly} />
                    ))}
                </Accordion>
            </div>
        </div>
    );
};
