// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const PageLabel = ({ children, required, className = "", htmlFor }: { children: React.ReactNode; required?: boolean; className?: string; htmlFor?: string }) => {
    const content = (
        <>
            {children}
            {required && <span className="ml-1 text-(--brand-wMain)">*</span>}
        </>
    );
    const classes = `text-sm font-medium text-(--secondary-text-wMain) ${className}`;
    return htmlFor ? (
        <label htmlFor={htmlFor} className={classes}>
            {content}
        </label>
    ) : (
        <div className={classes}>{content}</div>
    );
};
