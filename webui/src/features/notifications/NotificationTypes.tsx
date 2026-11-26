// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import type { ApiError } from "@/api/client";
import type { NotificationMatrix, NotificationMatrixCategory, NotificationMatrixCell, NotificationMatrixChannel } from "@/api/notifications";
import { useNotificationMatrix, useUpdateNotificationMatrix } from "@/api/notifications";

import { useChatContext } from "@/providers/contexts";

import { GoogleChatIcon, SlackIcon, TeamsIcon } from "@/features/entrypoints/GatewayTypeChip";

import { Button, Spinner, Switch, Tabs, TabsList, TabsTrigger } from "@/ui";

import { serverReason } from "./serverReason";

const CHANNEL_LABELS: Record<string, string> = {
    web: "In-App",
    slack: "Slack",
    teams: "Teams",
    google_chat: "Google Chat",
    email: "Email",
    webhook: "Webhook",
    connector: "Connector",
};

const LOAD_FAILED_MESSAGE = "Couldn't load your notification settings.";
const REFRESH_FAILED_MESSAGE = "Couldn't refresh your notification settings.";
const SAVE_FAILED_MESSAGE = "Couldn't save your notification settings. Try again.";
const SAVE_TIMED_OUT_MESSAGE = "Saving took too long, so your change may not have saved. Check the switches and try again.";
const SAVE_UNREACHABLE_MESSAGE = "Couldn't reach the service. Check your connection and try again.";
const LOGO_SIZE = "14";

interface Section {
    group: string;
    categories: NotificationMatrixCategory[];
}

function channelLabel(channel: NotificationMatrixChannel): string {
    const label = CHANNEL_LABELS[channel.channelType] ?? channel.channelType;
    return channel.destination ? `${label} (${channel.destination})` : label;
}

function channelKeyOf(channelType: string, destinationKey: string): string {
    return `${channelType}\u0000${destinationKey}`;
}

function channelKey(channel: NotificationMatrixChannel): string {
    return channelKeyOf(channel.channelType, channel.destinationKey);
}

function cellKey(category: string, channelType: string, destinationKey: string): string {
    return `${category}\u0000${channelKeyOf(channelType, destinationKey)}`;
}

function saveErrorMessage(failure: Error): string {
    if (failure.name === "TimeoutError") {
        return SAVE_TIMED_OUT_MESSAGE;
    }
    if (failure instanceof TypeError) {
        return SAVE_UNREACHABLE_MESSAGE;
    }
    return serverReason(failure) ?? SAVE_FAILED_MESSAGE;
}

function loadErrorMessage(error: ApiError | null): string {
    return (error?.status && serverReason(error)) || LOAD_FAILED_MESSAGE;
}

function sectionsOf(categories: NotificationMatrixCategory[]): Section[] {
    const sections: Section[] = [];
    for (const category of categories) {
        const group = category.group ?? "";
        const last = sections.at(-1);
        if (last?.group === group) {
            last.categories.push(category);
        } else {
            sections.push({ group, categories: [category] });
        }
    }
    return sections;
}

const CHANNEL_LOGOS: Record<string, typeof SlackIcon> = {
    slack: SlackIcon,
    teams: TeamsIcon,
    google_chat: GoogleChatIcon,
};

function ChannelLogo({ channelType }: Readonly<{ channelType: string }>) {
    const Logo = CHANNEL_LOGOS[channelType];
    return Logo ? <Logo size={LOGO_SIZE} /> : null;
}

interface ChannelSwitcherProps {
    channels: NotificationMatrixChannel[];
    selected: string;
    onSelect: (key: string) => void;
    counts: Map<string, number>;
    total: number;
}

function ChannelSwitcher({ channels, selected, onSelect, counts, total }: Readonly<ChannelSwitcherProps>) {
    return (
        <Tabs value={selected} onValueChange={onSelect}>
            <TabsList aria-label="Channel">
                {channels.map(channel => {
                    const key = channelKey(channel);
                    const on = counts.get(key) ?? 0;
                    return (
                        <TabsTrigger key={key} value={key} aria-label={`${channelLabel(channel)}, ${on} of ${total} on`}>
                            <ChannelLogo channelType={channel.channelType} />
                            {channelLabel(channel)}
                            <span className="text-xs font-normal text-(--secondary-text-wMain)">
                                {on}/{total}
                            </span>
                        </TabsTrigger>
                    );
                })}
            </TabsList>
        </Tabs>
    );
}

interface TypeRowProps {
    category: NotificationMatrixCategory;
    channel: NotificationMatrixChannel;
    cell: NotificationMatrixCell | undefined;
    onToggle: (cell: NotificationMatrixCell, enabled: boolean) => void;
}

function TypeRow({ category, channel, cell, onToggle }: Readonly<TypeRowProps>) {
    const lockedNote = cell?.locked ? `${category.label} always reach your in-app inbox.` : undefined;
    return (
        <div className="flex items-center justify-between gap-4 border-b py-3">
            <div className="flex min-w-0 flex-col">
                <span className="text-sm">{category.label}</span>
                {category.description && <span className="text-xs text-(--secondary-text-wMain)">{category.description}</span>}
            </div>
            {cell && (
                <span className="inline-flex shrink-0" title={lockedNote}>
                    <Switch checked={cell.enabled} disabled={cell.locked} onCheckedChange={enabled => onToggle(cell, enabled)} aria-label={`${category.label}: ${channelLabel(channel)}`} />
                </span>
            )}
        </div>
    );
}

function countsByChannel(matrix: NotificationMatrix): Map<string, number> {
    const counts = new Map<string, number>();
    for (const cell of matrix.cells) {
        if (cell.enabled) {
            const key = channelKeyOf(cell.channelType, cell.destinationKey);
            counts.set(key, (counts.get(key) ?? 0) + 1);
        }
    }
    return counts;
}

export function NotificationTypes() {
    const { data: matrix, isLoading, isError, error, refetch } = useNotificationMatrix();
    const { addNotification } = useChatContext();
    const [saveError, setSaveError] = useState<string | null>(null);
    const update = useUpdateNotificationMatrix({
        onSaveError: failure => {
            const message = saveErrorMessage(failure);
            setSaveError(message);
            addNotification?.(message, "warning");
        },
    });
    const [selected, setSelected] = useState("");

    if (isLoading) {
        return (
            <div className="flex items-center justify-center py-8">
                <Spinner variant="muted" />
            </div>
        );
    }
    if (!matrix) {
        return (
            <div className="flex flex-col items-start gap-2">
                <p className="text-sm text-(--error-text-wMain)">{loadErrorMessage(error)}</p>
                <Button variant="outline" size="sm" onClick={() => refetch()}>
                    Retry
                </Button>
            </div>
        );
    }

    const channel = matrix.channels.find(c => channelKey(c) === selected) ?? matrix.channels[0];
    const cells = new Map(matrix.cells.map(c => [cellKey(c.category, c.channelType, c.destinationKey), c]));
    const toggle = (cell: NotificationMatrixCell, enabled: boolean) => {
        if (!update.isPending) {
            setSaveError(null);
        }
        update.mutate([{ category: cell.category, channelType: cell.channelType, destinationKey: cell.destinationKey, enabled }]);
    };
    const sections = sectionsOf(matrix.categories);

    return (
        <div className="flex flex-col gap-5">
            {saveError && (
                <p role="alert" className="text-sm text-(--error-text-wMain)">
                    {saveError}
                </p>
            )}
            {matrix.channels.length > 1 && <ChannelSwitcher channels={matrix.channels} selected={channelKey(channel)} onSelect={setSelected} counts={countsByChannel(matrix)} total={matrix.categories.length} />}
            {sections.map(section => (
                <section key={section.group} aria-label={section.group} className="flex flex-col">
                    <h4 className="text-sm font-semibold text-(--primary-text-wMain)">{section.group}</h4>
                    {section.categories.map(category => (
                        <TypeRow key={category.category} category={category} channel={channel} cell={cells.get(cellKey(category.category, channel.channelType, channel.destinationKey))} onToggle={toggle} />
                    ))}
                </section>
            ))}
            {isError && <p className="text-sm text-(--error-text-wMain)">{REFRESH_FAILED_MESSAGE}</p>}
        </div>
    );
}
