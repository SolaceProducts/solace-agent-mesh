// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useThemeContext } from "@/providers/contexts";

import workflowDark from "@/assets/images/workflowDark.svg";
import workflowLight from "@/assets/images/workflowLight.svg";

interface WorkflowIllustrationProps {
    width?: number;
    height?: number;
}

export function WorkflowIllustration({ width, height }: WorkflowIllustrationProps) {
    const { currentTheme } = useThemeContext();

    return <img src={currentTheme === "dark" ? workflowDark : workflowLight} alt="Workflow illustration" width={width} height={height} className="h-auto w-full" />;
}
