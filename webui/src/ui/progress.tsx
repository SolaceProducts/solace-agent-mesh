// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

interface ProgressProps {
    value?: number;
    className?: string;
    indicatorClassName?: string;
}

export const Progress: React.FC<ProgressProps> = ({ value = 0, className = "", indicatorClassName = "bg-(--primary-wMain)" }) => {
    const clampedValue = Math.min(100, Math.max(0, value));

    return (
        <div className={`relative h-2 w-full overflow-hidden rounded-full bg-(--secondary-w20) ${className}`}>
            <div className={`h-full transition-all duration-300 ease-in-out ${indicatorClassName}`} style={{ width: `${clampedValue}%` }} />
        </div>
    );
};
