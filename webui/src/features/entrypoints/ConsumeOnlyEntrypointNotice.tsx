// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useChatContext } from "@/providers/contexts";

import { EmptyState, Header, PageLayout } from "@/common";

interface ConsumeOnlyEntrypointNoticeProps {
    gatewayName: string;
}

export const ConsumeOnlyEntrypointNotice = ({ gatewayName }: ConsumeOnlyEntrypointNoticeProps) => {
    const { navigate } = useChatContext();

    return (
        <PageLayout>
            <Header title={gatewayName} breadcrumbs={[{ label: "Entrypoints", onClick: () => navigate("/entrypoints") }, { label: gatewayName }]} />
            <div data-testid="consumeOnlyEntrypointNotice" className="h-full w-full">
                <EmptyState
                    variant="error"
                    title="Entrypoint managed from the command line"
                    buttons={[
                        {
                            text: "Go To Entrypoints",
                            variant: "default",
                            onClick: () => navigate("/entrypoints"),
                        },
                    ]}
                />
            </div>
        </PageLayout>
    );
};
