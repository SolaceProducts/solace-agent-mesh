// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function CollapsePanelIcon({ className }: Readonly<{ className?: string }>) {
    return (
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none" className={className} aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg">
            <path
                fillRule="evenodd"
                clipRule="evenodd"
                d="M0 17.5C0 17.7761 0.2239 18 0.5 18H1.5C1.7761 18 2 17.7761 2 17.5V0.5C2 0.2239 1.7761 0 1.5 0H0.5C0.2239 0 0 0.2239 0 0.5V17.5ZM17.6 7.9998C17.8 7.9998 18 8.1998 18 8.3998V9.5998C18 9.7998 17.8 9.9998 17.6 9.9998H7.75L10.95 13.4498C11.15 13.6498 11.15 13.8498 10.95 14.0498L10.15 14.8498C9.95 15.0498 9.75 15.0498 9.55 14.8498L4.15 9.2998C3.95 9.0998 3.95 8.8998 4.15 8.6998L9.494 3.1558C9.675 2.9748 9.886 2.9478 10.094 3.1558L10.894 3.9558C11.094 4.1558 11.094 4.3558 10.894 4.5558L7.65 7.9998H17.6Z"
                fill="currentColor"
            />
        </svg>
    );
}
