// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { getSchemaType } from "@/features/connectors/utils";

import { Deprecated, EmptyState } from "@/common";
import type { OpenAPISpecFile, Parameter } from "@/types";

const getInText = (param: Parameter): string => {
    if (param.in === "header" || param.in === "cookie") {
        return `(${param.in})`;
    }
    return "";
};

export interface ParametersDisplayProps {
    parameters: Parameter[];
    spec: OpenAPISpecFile;
    label?: string;
    className?: string;
}

const ParametersDisplay: React.FC<ParametersDisplayProps> = ({ parameters, label, spec, className = "" }) => {
    if (!parameters || parameters.length === 0) {
        return (
            <div className={className}>
                <EmptyState title={`No ${label || "parameters"} is defined.`} variant="noImage" />
            </div>
        );
    }

    return (
        <table className={`min-w-full border text-sm ${className}`}>
            <thead>
                <tr className="bg-(--secondary-w10)">
                    <th className="w-[20%] border-b px-3 py-2 text-left font-semibold">Key</th>
                    <th className="w-[20%] border-b px-3 py-2 text-left font-semibold">Type</th>
                    <th className="w-[10%] border-b px-3 py-2 text-left font-semibold">Required</th>
                    <th className="w-[50%] border-b px-3 py-2 text-left font-semibold">Description</th>
                </tr>
            </thead>
            <tbody>
                {parameters.map(param => (
                    <tr key={param.name}>
                        <td className="border-b px-3 py-2">
                            <span className="flex items-center gap-2">
                                {param.name}
                                {param.deprecated && <Deprecated>Deprecated</Deprecated>}
                            </span>
                        </td>
                        <td className="border-b px-3 py-2">
                            <code className="rounded bg-(--secondary-w10) px-2 py-1 text-xs">
                                {getSchemaType(param.schema, spec) ?? "—"} {getInText(param)}
                            </code>
                        </td>
                        <td className="border-b px-3 py-2">
                            {param.required ? (
                                <span className="inline-flex items-center rounded-full bg-(--error-w10) px-2 py-1 text-xs font-medium text-(--error-w100)">Required</span>
                            ) : (
                                <span className="text-sm text-(--secondary-text-wMain)">Optional</span>
                            )}
                        </td>
                        <td className="border-b px-3 py-2 whitespace-pre-wrap">{param.description ?? "—"}</td>
                    </tr>
                ))}
            </tbody>
        </table>
    );
};

export { ParametersDisplay };
