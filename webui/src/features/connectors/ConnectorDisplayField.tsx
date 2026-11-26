// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { formatDisplayFieldValue } from "@/utils/displayFieldValue";
import { downloadAuthenticatedFile } from "@/utils/download";
import { isWailsRuntime, openExternalLink } from "@/utils/openExternalLink";

interface ConnectorDisplayFieldProps {
    label: string;
    value: any;
    fieldType?: string;
    options?: Array<{ value: string; label: string }>;
    description?: string;
    className?: string;
    fieldName?: string;
}

const isHttpUrl = (value: string): boolean => {
    try {
        const url = new URL(value);
        return url.protocol === "http:" || url.protocol === "https:";
    } catch {
        return false;
    }
};

const isConnectorSpecResourceUrl = (value: string): boolean => {
    try {
        return /\/api\/v[12]\/platform\/connectors\/[^/]+\/resource$/.test(new URL(value).pathname);
    } catch {
        return false;
    }
};

const specDownloadFilename = (label: string): string => {
    const base = label.trim().replace(/\s+/g, "-").toLowerCase() || "connector";
    return `${base}-spec.yaml`;
};

const handleLinkClick = (url: string, label: string): void => {
    if (isConnectorSpecResourceUrl(url) && !isWailsRuntime()) {
        void downloadAuthenticatedFile(url, specDownloadFilename(label));
        return;
    }
    openExternalLink(url);
};

export const ConnectorDisplayField: React.FC<ConnectorDisplayFieldProps> = ({ label, value, fieldType = "text", options, className, fieldName }) => {
    const displayValue = formatDisplayFieldValue(value, fieldType, options);
    const isNotSet = displayValue === "—";
    const testId = fieldName ? `connector-review-${fieldName.replace(/\./g, "-")}` : undefined;

    return (
        <PageLabelWithValue className={"flex gap-2" + (className ? ` ${className}` : "")}>
            <PageLabel>{label}</PageLabel>
            <div className={`${isNotSet ? "text-(--secondary-text-wMain)" : ""} ${fieldType === "textarea" ? "whitespace-pre-wrap" : "break-all"}`} data-testid={testId}>
                {isHttpUrl(displayValue) ? (
                    <a
                        href={displayValue}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-(--primary-wMain) hover:underline"
                        onClick={e => {
                            e.preventDefault();
                            handleLinkClick(displayValue, label);
                        }}
                    >
                        {displayValue}
                    </a>
                ) : (
                    displayValue
                )}
            </div>
        </PageLabelWithValue>
    );
};
