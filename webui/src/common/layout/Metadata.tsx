// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { formatActor } from "@/utils/format";

import { CopyButton } from "../buttons/CopyButton";

interface MetadataProps {
    metadata: {
        id?: string;
        createdBy?: string;
        createdAt?: string;
        updatedBy?: string;
        updatedAt?: string;
    };
    scope?: string;
    className?: string;
}

export const Metadata = ({ metadata, scope, className }: MetadataProps) => {
    return (
        <div className={`mt-8 grid auto-rows-[21px] grid-cols-[auto_1fr] items-center gap-x-2 border-t py-8 text-xs text-(--secondary-text-wMain) ${className}`}>
            <div>Created By:</div>
            <div className="truncate">{formatActor(metadata.createdBy) ?? "-"}</div>
            <div>Created:</div>
            <div className="truncate">{metadata.createdAt ? new Date(metadata.createdAt).toLocaleString() : "N/A"}</div>
            <div>Last Updated By:</div>
            <div className="truncate">{formatActor(metadata.updatedBy) ?? "-"}</div>
            <div>Last Updated:</div>
            <div className="truncate">{metadata.updatedAt ? new Date(metadata.updatedAt).toLocaleString() : "N/A"}</div>
            <div>ID:</div>
            <div className="truncate">{metadata.id ?? "-"}</div>
            {scope ? (
                <>
                    <div>Invoke Scope:</div>
                    <div className="flex min-w-0 items-center gap-1">
                        <span className="truncate">{scope}</span>
                        <CopyButton value={scope} label="Copy invoke scope" className="shrink-0" />
                    </div>
                </>
            ) : null}
        </div>
    );
};
