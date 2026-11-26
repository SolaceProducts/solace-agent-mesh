// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MessageBanner } from "@/common/banners";

import { Button } from "@/ui/button";

interface NoAssignableRolesBannerProps {
    message: string;
    actionLabel: string;
    onAction: () => void;
}

export function NoAssignableRolesBanner({ message, actionLabel, onAction }: Readonly<NoAssignableRolesBannerProps>) {
    return (
        <MessageBanner
            variant="info"
            message={
                <div className="flex w-full items-center justify-between gap-2">
                    <span>{message}</span>
                    <Button variant="outline" onClick={onAction}>
                        {actionLabel}
                    </Button>
                </div>
            }
        />
    );
}
