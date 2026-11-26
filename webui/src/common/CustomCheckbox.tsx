// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

interface CustomCheckboxProps {
    id?: string;
    checked: boolean;
    indeterminate?: boolean;
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
    onClick?: (e: React.MouseEvent<HTMLInputElement>) => void;
    disabled?: boolean;
    className?: string;
    ariaLabel?: string;
}

export const CustomCheckbox = ({ id, checked, indeterminate = false, onChange, onClick, disabled = false, className = "", ariaLabel }: Readonly<CustomCheckboxProps>) => {
    const markColor = disabled ? "var(--secondary-text-w50)" : "var(--brand-wMain)";
    return (
        <div className={`relative h-6 w-6 flex-shrink-0 ${className}`}>
            <input
                id={id}
                type="checkbox"
                aria-label={ariaLabel}
                checked={checked}
                onChange={onChange}
                onClick={onClick}
                disabled={disabled}
                className={`peer h-6 w-6 appearance-none rounded border focus-visible:ring-2 focus-visible:ring-(--brand-wMain) focus-visible:ring-offset-2 focus-visible:outline-none ${disabled ? "cursor-not-allowed bg-(--secondary-w10)" : "cursor-pointer bg-(--background-w10)"}`}
            />
            {indeterminate ? (
                <svg className="pointer-events-none absolute top-1/2 left-1/2 h-[24px] w-[24px] -translate-x-1/2 -translate-y-1/2" fill="none" stroke={markColor} strokeWidth="3" viewBox="0 0 24 24">
                    <line x1="6" y1="12" x2="18" y2="12" />
                </svg>
            ) : (
                checked && (
                    <svg className="pointer-events-none absolute top-1/2 left-1/2 h-[24px] w-[24px] -translate-x-1/2 -translate-y-1/2" fill="none" stroke={markColor} strokeWidth="3" viewBox="0 0 24 24">
                        <polyline points="20 6 9 17 4 12" />
                    </svg>
                )
            )}
        </div>
    );
};
