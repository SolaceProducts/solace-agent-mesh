// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { type TaskFE } from "@/types/chat";

import EventCard from "./EventCard";

interface EventDetailsPanelProps {
    selectedTask: TaskFE | null;
}

const EventDetailsPanel: React.FC<EventDetailsPanelProps> = ({ selectedTask }) => {
    if (!selectedTask) {
        return (
            <div className="flex h-full items-center justify-center">
                <p className="text-(--secondary-text-wMain)">Select a task from the list to view its events.</p>
            </div>
        );
    }

    if (selectedTask.events.length === 0) {
        return (
            <div className="flex h-full items-center justify-center">
                <p className="text-(--secondary-text-wMain)">No events recorded for this task yet.</p>
            </div>
        );
    }

    return (
        <>
            <div className="mb-6">
                <h2 className="text-lg font-semibold">Events</h2>
                <p className="mt-1 text-sm text-(--secondary-text-wMain)">View events related to the selected task.</p>
            </div>
            <div className="space-y-3">
                {selectedTask.events.map((event, index) => (
                    <EventCard key={`${event.solace_topic}-${event.timestamp}-${index}`} event={event} />
                ))}
            </div>
        </>
    );
};

export default EventDetailsPanel;
