// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { ErrorLabel } from "../status/ErrorLabel";

import { PageLabel } from "./PageLabel";

export interface FormFieldLayoutItemProps {
    label: React.ReactNode;
    required?: boolean;
    error?: { message?: string };
    warning?: string;
    helpText?: React.ReactNode;
    statusIndicator?: React.ReactNode;
    messageId?: string;
    children: React.ReactNode;
}

export const FormFieldLayoutItem = ({ label, required = false, error, warning, helpText, statusIndicator, messageId, children }: FormFieldLayoutItemProps) => (
    <div className="grid grid-rows-[auto_1fr] gap-2">
        <div className="flex items-start justify-between gap-2">
            <PageLabel required={required}>{label}</PageLabel>
            {statusIndicator}
        </div>
        <div className="flex flex-col gap-1">
            {children}
            {(error || warning || helpText) && (
                <div className="mt-1" id={messageId}>
                    {error && <ErrorLabel message={error.message} />}
                    {warning && <div className="text-xs text-(--warning-wMain)">{warning}</div>}
                    {helpText && <div className="text-xs text-(--secondary-text-wMain)">{helpText}</div>}
                </div>
            )}
        </div>
    </div>
);
