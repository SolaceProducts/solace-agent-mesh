// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function stripReportMetadataSections(markdownContent: string): string {
    if (!markdownContent) {
        return markdownContent;
    }

    const referencesPattern = /\n---\n\n## References\b/i;
    const referencesMatch = markdownContent.match(referencesPattern);

    if (referencesMatch && referencesMatch.index !== undefined) {
        return markdownContent.substring(0, referencesMatch.index).trim();
    }

    const simpleReferencesPattern = /\n## References\b/i;
    const simpleReferencesMatch = markdownContent.match(simpleReferencesPattern);

    if (simpleReferencesMatch && simpleReferencesMatch.index !== undefined) {
        return markdownContent.substring(0, simpleReferencesMatch.index).trim();
    }

    const methodologyPattern = /\n## Research Methodology\b/i;
    const methodologyMatch = markdownContent.match(methodologyPattern);

    if (methodologyMatch && methodologyMatch.index !== undefined) {
        return markdownContent.substring(0, methodologyMatch.index).trim();
    }

    return markdownContent;
}

export function isDeepResearchReportFilename(filename: string): boolean {
    if (!filename) {
        return false;
    }

    return filename.toLowerCase().endsWith("_deep_research_report.md");
}
