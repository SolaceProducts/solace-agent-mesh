// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";

import { MessageBanner } from "@/common";

interface EntrypointSetupBannerProps {
    product: string;
    generatedLine: string;
    docsUrl: string;
    testId: string;
    className?: string;
}

export function EntrypointSetupBanner({ product, generatedLine, docsUrl, testId, className = "" }: Readonly<EntrypointSetupBannerProps>) {
    const message = (
        <div className="flex w-full items-center justify-between">
            <div className="flex flex-col gap-1">
                <span>To complete your setup, configure your integration in {product}.</span>
                <span className="font-bold">{generatedLine}</span>
            </div>
            <a href={docsUrl} target="_blank" rel="noopener noreferrer" className="ml-4 flex shrink-0 items-center gap-1 font-semibold text-(--primary-wMain) hover:underline">
                Learn more <ExternalLink className="size-4" />
            </a>
        </div>
    );

    return <MessageBanner variant="info" message={message} className={className} data-testid={testId} />;
}
