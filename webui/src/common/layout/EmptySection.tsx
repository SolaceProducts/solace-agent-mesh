// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";

interface EmptySectionProps {
    message?: string;
    action?: string;
    onClick?: () => void;
    className?: string;
}

export const EmptySection = ({ message = "", action, onClick, className }: EmptySectionProps) => {
    return (
        <div className={`flex h-[100px] flex-col items-center justify-center gap-2 rounded-sm bg-(--secondary-w10) ${className}`}>
            {message && <span className="text-(--secondary-text-wMain)">{message}</span>}
            {action && onClick && (
                <Button variant="outline" onClick={onClick}>
                    {action}
                </Button>
            )}
        </div>
    );
};
