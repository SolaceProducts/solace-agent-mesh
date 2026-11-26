// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type CSSProperties } from "react";

export type ErrorStateAction = { kind: "button"; label: string; onClick: () => void } | { kind: "link"; label: string; href: string; onClick?: (event: React.MouseEvent<HTMLAnchorElement>) => void };

interface ErrorStateProps {
    title?: string;
    message: string;
    actions: ErrorStateAction[];
}

const containerStyle: CSSProperties = {
    minHeight: "100dvh",
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: "1rem",
    padding: "2rem",
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
    color: "var(--primary-text-wMain, #273749)",
    textAlign: "center",
};

const titleStyle: CSSProperties = { fontSize: "1.25rem", fontWeight: 600, margin: 0 };
const messageStyle: CSSProperties = { maxWidth: "28rem", margin: 0, color: "var(--secondary-text-wMain, #647481)" };
const actionsStyle: CSSProperties = { display: "flex", gap: "0.75rem", marginTop: "0.5rem" };

const buttonStyle: CSSProperties = {
    padding: "0.5rem 1rem",
    borderRadius: "var(--radius-sm, 0.375rem)",
    background: "var(--primary-wMain, #015B82)",
    color: "var(--primary-text-w10, #FFFFFF)",
    border: "none",
    cursor: "pointer",
    fontWeight: 500,
};

const linkStyle: CSSProperties = {
    padding: "0.5rem 1rem",
    borderRadius: "var(--radius-sm, 0.375rem)",
    border: "1px solid var(--primary-wMain, #015B82)",
    color: "var(--primary-wMain, #015B82)",
    textDecoration: "none",
    fontWeight: 500,
};

function ErrorState({ title = "Something Went Wrong", message, actions }: Readonly<ErrorStateProps>) {
    return (
        <div style={containerStyle} role="alert">
            <h1 style={titleStyle}>{title}</h1>
            <p style={messageStyle}>{message}</p>
            <div style={actionsStyle}>
                {actions.map(action =>
                    action.kind === "button" ? (
                        <button key={action.label} type="button" style={buttonStyle} onClick={action.onClick}>
                            {action.label}
                        </button>
                    ) : (
                        <a key={action.label} href={action.href} style={linkStyle} onClick={action.onClick}>
                            {action.label}
                        </a>
                    )
                )}
            </div>
        </div>
    );
}

export default ErrorState;
