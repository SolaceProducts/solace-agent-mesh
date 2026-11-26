// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ConnectorDisplayField } from "../ConnectorDisplayField";
import { EVENT_MESH_SECTION_TITLE } from "../eventMeshStyles";
import { EVENT_MESH_DELIVERY_MODE_OPTIONS, EVENT_MESH_PAYLOAD_FORMAT_OPTIONS, EVENT_MESH_REPLY_MODE_OPTIONS, type EventMeshOperation } from "../eventMeshUtils";

export function EventMeshAdvancedSettingsSummary({ operation }: Readonly<{ operation: EventMeshOperation }>) {
    return (
        <>
            <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Advanced Settings</div>
            <div className="grid gap-6 md:grid-cols-2">
                <ConnectorDisplayField label="Payload Schema" value={operation.payload_format} fieldType="select" options={EVENT_MESH_PAYLOAD_FORMAT_OPTIONS} fieldName="payload_format" />
                <ConnectorDisplayField label="Delivery Mode" value={String(operation.qos ?? 1)} fieldType="select" options={EVENT_MESH_DELIVERY_MODE_OPTIONS} fieldName="delivery_mode" />
                {operation.wait_for_response && <ConnectorDisplayField label="Timeout (ms)" value={operation.request_expiry_ms ?? 60000} fieldName="timeout" />}
                {operation.wait_for_response && <ConnectorDisplayField label="Reply Delivery" value={operation.reply_mode ?? "temp_queue"} fieldType="select" options={EVENT_MESH_REPLY_MODE_OPTIONS} fieldName="reply_mode" />}
            </div>
        </>
    );
}
