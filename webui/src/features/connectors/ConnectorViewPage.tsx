// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { Pencil } from "lucide-react";

import { findConnectorBadge, useCapabilities } from "@/api/capabilities";
import { useConnectorDetail } from "@/api/connectors";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageFooter } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";

import { EmptyState, Header, PageLayout } from "@/common";

import { ConnectorAgents } from "./ConnectorAgents";
import { ConnectorIcon } from "./ConnectorIcon";
import { ConnectorView } from "./ConnectorView";

export const ConnectorViewPage = () => {
    const { handleEditConnector, navigate, useLoaderData, getConnectorSchema } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: capabilities } = useCapabilities();
    const { connectorId } = useLoaderData();

    const [currentTab, setCurrentTab] = useState<"configuration" | "agentUsage">("configuration");

    const { data: connectorData, isLoading, error } = useConnectorDetail(connectorId);
    const connector = connectorData ?? null;
    const loadError = error ? getErrorMessage(error, "Failed to load connector.") : null;

    const handleBack = () => {
        navigate(`/connectors`);
    };

    const tabs = useMemo(() => {
        return [
            { id: "configuration", label: "Configuration", isActive: currentTab === "configuration", onClick: () => setCurrentTab("configuration") },
            { id: "agents", label: `Agent Usage (${connector?.agentCount ?? 0})`, isActive: currentTab === "agentUsage", onClick: () => setCurrentTab("agentUsage") },
        ];
    }, [currentTab, connector]);

    const headerButtons = useMemo(() => {
        return currentTab === "configuration" && userHasCapability(CAPABILITIES.updateConnectors)
            ? [
                  <Button
                      key="edit"
                      variant="ghost"
                      onClick={() => {
                          if (connector) handleEditConnector(connector);
                      }}
                      data-testid="editConnectorButton"
                      title="Edit Connector"
                      disabled={!connector}
                  >
                      <Pencil />
                      Edit
                  </Button>,
              ]
            : undefined;
    }, [connector, currentTab, handleEditConnector, userHasCapability, CAPABILITIES]);

    const titleText = connector ? connector.name : "N/A";
    const schemaInfo = connector ? getConnectorSchema(connector.type, connector.subtype) : null;
    const lifecycleBadge = findConnectorBadge(capabilities, connector?.type ?? "", schemaInfo?.experimental ?? false, connector?.subtype);

    const titleWithIcon = (
        <div className="flex min-w-0 items-center gap-3">
            {schemaInfo?.icon && <ConnectorIcon icon={schemaInfo.icon} name={schemaInfo.name} size="sm" />}
            <span className="min-w-0 truncate">{titleText}</span>
            {lifecycleBadge && <LifecycleBadge>{lifecycleBadge}</LifecycleBadge>}
        </div>
    );

    let body;
    if (!connectorId) {
        body = <EmptyState variant="error" title="Connector Not Found" subtitle="No connector was specified." buttons={[{ text: "Go To Connectors", variant: "default", onClick: () => navigate("/connectors") }]} />;
    } else if (isLoading) {
        body = <EmptyState variant="loading" title="Loading Connector..." />;
    } else if (loadError) {
        body = <EmptyState variant="error" title="Failed to Load Connector" subtitle={loadError} buttons={[{ text: "Go To Connectors", variant: "default", onClick: () => navigate("/connectors") }]} />;
    } else if (connector) {
        body = (
            <>
                {currentTab === "configuration" && <ConnectorView connector={connector} />}
                {currentTab === "agentUsage" && <ConnectorAgents connector={connector} />}
            </>
        );
    }

    return (
        <PageLayout className="min-[896px]:min-w-4xl">
            <Header title={titleWithIcon} breadcrumbs={[{ label: "Connectors", onClick: () => navigate("/connectors") }, { label: titleText }]} buttons={headerButtons} tabs={tabs} />

            {body}

            <PageFooter>
                <Button data-testid="closeButton" variant="outline" title="Close" onClick={handleBack}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
};
