// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MoreHorizontal } from "lucide-react";

import type { UserChannel } from "@/api/notifications";
import { useNotificationChannels, useTurnOffChannel, useTurnOnChannel } from "@/api/notifications";

import { GoogleChatIcon, SlackIcon, TeamsIcon } from "@/features/entrypoints/GatewayTypeChip";

import { MessageBanner } from "@/common";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Spinner } from "@/ui";

import { serverReason } from "./serverReason";

const CHANNEL_LABELS: Record<string, string> = {
    slack: "Slack",
    teams: "Teams",
    google_chat: "Google Chat",
};

const HTTP_BAD_REQUEST = 400;
const HTTP_FORBIDDEN = 403;
const HTTP_BAD_GATEWAY = 502;
const HTTP_SERVICE_UNAVAILABLE = 503;

const PERMISSION_MESSAGE = "You don't have permission to do this.";
const ENABLE_FAILED_MESSAGE = "Couldn't enable the application. Try again.";
const REFRESH_FAILED_MESSAGE = "Couldn't refresh your external applications.";
const LOGO_SIZE = "24";

function channelLabel(channelType: string): string {
    return CHANNEL_LABELS[channelType] ?? channelType;
}

function errorStatus(error: unknown): number | undefined {
    return (error as { status?: number } | null)?.status;
}

function enableErrorMessage(error: unknown, label: string): string {
    const status = errorStatus(error);
    const reason = serverReason(error);
    if (status === HTTP_FORBIDDEN) {
        return PERMISSION_MESSAGE;
    }
    if (status === HTTP_BAD_REQUEST) {
        return reason || `Unable to enable notifications because Agent Mesh has not received a message from this user in ${label}. Send a message to the Agent Mesh application in ${label} first.`;
    }
    if (status === HTTP_BAD_GATEWAY || status === HTTP_SERVICE_UNAVAILABLE) {
        return reason || ENABLE_FAILED_MESSAGE;
    }
    return ENABLE_FAILED_MESSAGE;
}

function disableErrorMessage(error: unknown, label: string): string {
    return errorStatus(error) === HTTP_FORBIDDEN ? PERMISSION_MESSAGE : `Couldn't disable ${label}. Try again.`;
}

const APPLICATION_LOGOS: Record<string, typeof SlackIcon> = {
    slack: SlackIcon,
    teams: TeamsIcon,
    google_chat: GoogleChatIcon,
};

function ApplicationLogo({ channelType }: Readonly<{ channelType: string }>) {
    const Logo = APPLICATION_LOGOS[channelType];
    return Logo ? <Logo size={LOGO_SIZE} /> : null;
}

interface ApplicationCardProps {
    channel: UserChannel;
    busy: boolean;
    enabling: boolean;
    onEnable: (channelType: string) => void;
    onDisable: (channelType: string) => void;
}

function ApplicationCard({ channel, busy, enabling, onEnable, onDisable }: Readonly<ApplicationCardProps>) {
    const label = channelLabel(channel.channelType);
    return (
        <div className="flex items-center justify-between gap-3 rounded-md border p-4">
            <div className="flex items-center gap-3">
                <ApplicationLogo channelType={channel.channelType} />
                <div className="flex flex-col">
                    <span className="text-sm font-semibold">{label}</span>
                    <span className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                        <span aria-hidden="true" className={channel.confirmed ? "size-2 rounded-full bg-(--success-text-wMain)" : "size-2 rounded-full bg-(--secondary-w40)"} />
                        {channel.confirmed ? "Enabled" : "Not enabled"}
                    </span>
                </div>
            </div>
            {channel.confirmed ? (
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="size-8" aria-label={`${label} actions`}>
                            <MoreHorizontal className="size-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                        <DropdownMenuItem disabled={busy} onClick={() => onDisable(channel.channelType)}>
                            Disable
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            ) : (
                <Button variant="outline" size="sm" disabled={busy} onClick={() => onEnable(channel.channelType)} aria-label={`${enabling ? "Enabling…" : "Enable"} ${label}`}>
                    {enabling ? "Enabling…" : "Enable"}
                </Button>
            )}
        </div>
    );
}

export function ExternalApplications() {
    const { data, isLoading, isError, refetch } = useNotificationChannels();
    const enable = useTurnOnChannel();
    const disable = useTurnOffChannel();
    const channels = data?.data ?? [];

    const enabledNote = (channelType: string): string | undefined => {
        const channel = channels.find(c => c.channelType === channelType);
        if (enable.variables !== channelType || !enable.data?.confirmed || !channel?.confirmed) {
            return undefined;
        }
        return `${channelLabel(channelType)} is enabled. Agent Mesh may send a short message there. If notifications do not arrive, check that the Agent Mesh application can message this user.`;
    };

    let content;
    if (isLoading) {
        content = (
            <div className="flex items-center justify-center py-8">
                <Spinner variant="muted" />
            </div>
        );
    } else if (isError && !data) {
        content = (
            <div className="flex flex-col items-start gap-2">
                <p className="text-sm text-(--error-text-wMain)">Couldn&apos;t load your external applications.</p>
                <Button variant="outline" size="sm" onClick={() => refetch()}>
                    Retry
                </Button>
            </div>
        );
    } else if (channels.length === 0) {
        content = <p className="text-sm text-(--secondary-text-wMain)">No external applications are available yet. Ask an administrator to set up a Slack, Microsoft Teams or Google Chat entry point that sends notifications.</p>;
    } else {
        content = (
            <>
                <div className="grid gap-3 sm:grid-cols-2">
                    {channels.map(channel => (
                        <ApplicationCard
                            key={channel.channelType}
                            channel={channel}
                            busy={enable.isPending || disable.isPending}
                            enabling={enable.isPending && enable.variables === channel.channelType}
                            onEnable={enable.mutate}
                            onDisable={disable.mutate}
                        />
                    ))}
                </div>
                {isError && <MessageBanner variant="error" message={REFRESH_FAILED_MESSAGE} />}
                {enable.isError && <MessageBanner variant="error" message={enableErrorMessage(enable.error, channelLabel(String(enable.variables)))} dismissible onDismiss={enable.reset} />}
                {disable.isError && <MessageBanner variant="error" message={disableErrorMessage(disable.error, channelLabel(String(disable.variables)))} dismissible onDismiss={disable.reset} />}
                {channels.map(channel => {
                    const note = enabledNote(channel.channelType);
                    return note ? <MessageBanner key={channel.channelType} variant="info" message={note} dismissible onDismiss={enable.reset} /> : null;
                })}
            </>
        );
    }

    return (
        <section className="flex flex-col gap-3">
            <h3 className="text-sm font-semibold text-(--primary-text-wMain)">External Application Notifications</h3>
            <p className="text-sm text-(--secondary-text-wMain)">Enable an external application to receive notifications there, then choose which notification types it receives above.</p>
            {content}
        </section>
    );
}
