// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Plus } from "lucide-react";

import { findGatewayBadge, useCapabilities } from "@/api/capabilities";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/ui/dialog";
import { LifecycleBadge } from "@/ui/lifecycleBadge";

import { EmptyState } from "@/common";

import { EmailIcon, EventMeshIcon, GoogleChatIcon, SlackIcon, TeamsIcon, WebhookIcon, WhatsAppIcon } from "./GatewayTypeChip";

const gatewayIconComponents: Record<string, React.ReactNode> = {
    event_mesh: <EventMeshIcon />,
    slack: <SlackIcon />,
    teams: <TeamsIcon />,
    email: <EmailIcon />,
    whatsapp: <WhatsAppIcon />,
    google_chat: <GoogleChatIcon />,
    webhook: <WebhookIcon />,
};

interface GatewaysAvailableDialogProps {
    triggerButton?: React.ReactNode;
}

export const GatewaysAvailableDialog = ({ triggerButton }: GatewaysAvailableDialogProps) => {
    const { navigate, gatewaySchemas, gatewaySchemasRefetch } = useChatContext();
    const { data: capabilities } = useCapabilities();

    const handleSelect = (type: string) => {
        navigate(`/entrypoints/new/edit?type=${type}`);
    };

    return (
        <Dialog onOpenChange={open => open && gatewaySchemasRefetch()}>
            <DialogTrigger asChild>
                {triggerButton ?? (
                    <Button title="Create Entrypoint" testid="createGateway" variant="outline">
                        <Plus />
                        Create Entrypoint
                    </Button>
                )}
            </DialogTrigger>
            <DialogContent className="max-w-2xl">
                <DialogHeader>
                    <DialogTitle>Available Entrypoint Types</DialogTitle>
                    <DialogDescription>Choose from the following entrypoint types to connect external systems to your agent mesh.</DialogDescription>
                </DialogHeader>

                <div className="flex min-h-[400px] flex-col pt-4">
                    {!gatewaySchemas || gatewaySchemas.length === 0 ? (
                        <div className="flex flex-1 items-center justify-center">
                            <EmptyState title="No entrypoint types available" subtitle="Contact your administrator to add entrypoint types." variant="noImage" />
                        </div>
                    ) : (
                        <div className="mb-4 flex max-h-[60vh] flex-col gap-2 overflow-y-auto pr-2">
                            {gatewaySchemas.map(schema => {
                                const badge = findGatewayBadge(capabilities, schema.type);
                                return (
                                    <div
                                        key={schema.type}
                                        onClick={() => handleSelect(schema.type)}
                                        data-testid={`gatewayTypeCard-${schema.type}`}
                                        className="grid min-h-[90px] shrink-0 cursor-pointer grid-cols-[100px_1fr] items-center rounded-sm border p-4 shadow-sm hover:bg-(--primary-w10)"
                                    >
                                        <div className="flex items-center justify-center">
                                            {gatewayIconComponents[schema.type] ? (
                                                gatewayIconComponents[schema.type]
                                            ) : (
                                                <div className="flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10) text-lg">
                                                    <div className="font-semibold text-(--secondary-text-wMain)">{schema.name.charAt(0).toUpperCase()}</div>
                                                </div>
                                            )}
                                        </div>
                                        <div className="flex flex-col gap-1">
                                            <div className="flex items-center gap-2">
                                                <div className="font-semibold">{schema.name}</div>
                                                {badge && <LifecycleBadge>{badge}</LifecycleBadge>}
                                            </div>
                                            {schema.description && <div className="text-sm text-(--secondary-text-wMain)">{schema.description}</div>}
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" testid="closeAvailableGatewaysDialog" title="Close">
                            Close
                        </Button>
                    </DialogClose>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
