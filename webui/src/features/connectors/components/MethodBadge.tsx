// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const getMethodBadgeClassName = (method: string): string => {
    const baseClassName = "inline-flex items-center rounded-[30px] px-2 py-0.5 text-xs font-medium leading-[14px]";

    switch (method.toUpperCase()) {
        case "GET":
            return `${baseClassName} bg-blue-600 text-white`;
        case "POST":
            return `${baseClassName} bg-green-600 text-white`;
        case "PUT":
            return `${baseClassName} bg-orange-600 text-white`;
        case "PATCH":
            return `${baseClassName} bg-yellow-600 text-white`;
        case "DELETE":
            return `${baseClassName} bg-red-600 text-white`;
        case "HEAD":
            return `${baseClassName} bg-purple-600 text-white`;
        case "MCP":
            return `${baseClassName} bg-(--primary-wMain) text-(--primary-text-w10)`;
        case "EVENT_MESH":
            return `${baseClassName} bg-(--brand-w10) text-(--brand-w100)`;
        default:
            return `${baseClassName} bg-gray-600 text-white`;
    }
};

export { getMethodBadgeClassName };
