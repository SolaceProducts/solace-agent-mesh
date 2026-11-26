// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Plug } from "lucide-react";

interface ConnectorIconProps {
    icon?: string;
    name: string;
    size?: "sm" | "md";
}

const sizeConfig = {
    sm: {
        container: "h-8 w-8",
        margin: "mr-2",
        plugIcon: "h-7 w-7",
        image: "h-7 w-7",
        text: "text-sm",
    },
    md: {
        container: "h-12 w-12",
        margin: "mr-4",
        plugIcon: "h-10 w-10",
        image: "h-10 w-10",
        text: "text-lg",
    },
};

export const ConnectorIcon = ({ icon, name, size = "md" }: ConnectorIconProps) => {
    const [imageError, setImageError] = useState(false);
    const config = sizeConfig[size];

    useEffect(() => {
        setImageError(false);
    }, [icon]);

    const renderIcon = () => {
        if (icon === "plug") {
            return (
                <div className={`${config.margin} flex ${config.container} items-center justify-center rounded-xs bg-[#F3F4F6]`}>
                    <Plug className={`${config.plugIcon} text-green-600`} />
                </div>
            );
        }

        if (icon && !imageError) {
            return (
                <div className={`${config.margin} flex ${config.container} items-center justify-center rounded-xs bg-[#F3F4F6]`}>
                    <img src={icon} alt={name} className={`${config.image} object-contain`} onError={() => setImageError(true)} />
                </div>
            );
        }

        return (
            <div className={`flex ${config.container} items-center justify-center rounded-full bg-(--secondary-w10)`}>
                <span className={`text-(--secondary-text-wMain) ${config.text} font-semibold`}>{name.charAt(0).toUpperCase()}</span>
            </div>
        );
    };

    return <div className="flex items-center justify-center">{renderIcon()}</div>;
};
