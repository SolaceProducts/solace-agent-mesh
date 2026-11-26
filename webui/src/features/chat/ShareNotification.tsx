// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { formatRelativeTime } from "@/utils/format";

type ShareType = "user-specific" | "domain-restricted" | "authenticated" | "public";

interface ShareNotificationProps {
    sharedBy: string;
    shareType: ShareType;
    sharedWith?: string;
    sharedAt: number;
}

export function ShareNotification({ sharedBy, shareType, sharedWith, sharedAt }: Readonly<ShareNotificationProps>) {
    const getShareMessage = () => {
        switch (shareType) {
            case "user-specific":
                return (
                    <>
                        <span className="font-medium">{sharedBy}</span> shared this chat with <span className="font-medium">{sharedWith}</span>
                    </>
                );
            case "domain-restricted":
                return (
                    <>
                        <span className="font-medium">{sharedBy}</span> shared this chat with <span className="font-medium">your domain</span>
                    </>
                );
            case "authenticated":
                return (
                    <>
                        <span className="font-medium">{sharedBy}</span> shared this chat with <span className="font-medium">authenticated users</span>
                    </>
                );
            case "public":
                return (
                    <>
                        <span className="font-medium">{sharedBy}</span> created a <span className="font-medium">public link</span> for this chat
                    </>
                );
        }
    };

    return (
        <div className="mx-auto my-4 max-w-3xl rounded-lg px-4 py-3 text-center text-sm text-(--secondary-text-wMain)">
            <p className="mt-0.5 text-xs text-(--secondary-text-w50)">{formatRelativeTime(new Date(sharedAt).toISOString())}</p>
            <p>{getShareMessage()}</p>
        </div>
    );
}
