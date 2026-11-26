// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheckBig } from "lucide-react";
import type { FC } from "react";

import { NODE_BASE_STYLES, type NodeProps } from "../utils/types";

const EndNode: FC<NodeProps> = ({ node }) => {
    return (
        <div
            className={NODE_BASE_STYLES.PILL}
            style={{
                width: `${node.width}px`,
                height: `${node.height}px`,
            }}
        >
            <CircleCheckBig className="h-4 w-4" />
            <span className="text-sm font-semibold">{node.data.label}</span>
        </div>
    );
};

export default EndNode;
