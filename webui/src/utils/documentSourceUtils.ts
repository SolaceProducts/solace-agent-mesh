// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RAGSearchResult, RAGSource } from "@/types";

export interface LocationCitation {
    sortKey: number;
    locationLabel: string;
    citationCount: number;
    citations: RAGSource[];
}

export interface GroupedDocument {
    filename: string;
    totalCitations: number;
    locations: LocationCitation[];
    fileExtension: string;
}

export const getFileExtension = (filename: string): string => {
    const match = filename.match(/\.([^.]+)$/);
    return match ? match[1].toLowerCase() : "file";
};

export const formatLocationLabel = (locationString: string): string => {
    if (!locationString) return locationString;

    if (locationString.startsWith("Lines ")) {
        return locationString.replace(/Lines (\d+)_(\d+)/g, "Lines $1-$2");
    }

    return locationString;
};

export const parseSortKey = (locationString: string | undefined): number => {
    if (!locationString) return 0;

    const match = locationString.match(/^Pages?\s*(\d+)/i);
    if (match) {
        return parseInt(match[1], 10);
    }

    return 0;
};

export const groupDocumentSources = (ragData: RAGSearchResult[]): GroupedDocument[] => {
    const documentSearchResults = ragData.filter(r => r.searchType === "document_search");

    const documentMap = new Map<string, { sources: RAGSource[]; filename: string }>();

    documentSearchResults.forEach(result => {
        result.sources.forEach(source => {
            const filename = source.filename || source.fileId || "Unknown";

            if (!documentMap.has(filename)) {
                documentMap.set(filename, { sources: [], filename });
            }
            documentMap.get(filename)!.sources.push(source);
        });
    });

    const groupedDocuments: GroupedDocument[] = [];

    documentMap.forEach(({ sources, filename }) => {
        const locationMap = new Map<string, RAGSource[]>();

        sources.forEach(source => {
            const locationString = source.metadata?.primary_location || source.metadata?.location_range || "Unknown";

            if (!locationMap.has(locationString)) {
                locationMap.set(locationString, []);
            }
            locationMap.get(locationString)!.push(source);
        });

        const locations: LocationCitation[] = Array.from(locationMap.entries())
            .map(([locationString, citations]) => {
                const sortKey = parseSortKey(locationString);
                return {
                    sortKey,
                    locationLabel: formatLocationLabel(locationString),
                    citationCount: citations.length,
                    citations,
                };
            })
            .sort((a, b) => a.sortKey - b.sortKey);

        groupedDocuments.push({
            filename,
            totalCitations: sources.length,
            locations,
            fileExtension: getFileExtension(filename),
        });
    });

    return groupedDocuments.sort((a, b) => a.filename.localeCompare(b.filename));
};
