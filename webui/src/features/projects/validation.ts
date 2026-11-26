// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useConfigContext } from "@/providers/contexts";

export const DEFAULT_MAX_NAME_LENGTH = 255;
export const DEFAULT_MAX_DESCRIPTION_LENGTH = 1000;
export const DEFAULT_MAX_FILE_DESCRIPTION_LENGTH = 1000;
export const DEFAULT_MAX_INSTRUCTIONS_LENGTH = 32000;

export const PROJECT_NAME_REQUIRED_ERROR = "Project name is required";

export interface ProjectFieldLimits {
    maxNameLength: number;
    maxDescriptionLength: number;
}

const usableLimit = (advertised: number | undefined, fallback: number): number => (advertised && advertised > 0 ? advertised : fallback);

export const useProjectFieldLimits = (): ProjectFieldLimits => {
    const { validationLimits } = useConfigContext();
    return {
        maxNameLength: usableLimit(validationLimits?.projectNameMax, DEFAULT_MAX_NAME_LENGTH),
        maxDescriptionLength: usableLimit(validationLimits?.projectDescriptionMax, DEFAULT_MAX_DESCRIPTION_LENGTH),
    };
};

export const validateProjectName = (name: string): string | null => (name.trim().length === 0 ? PROJECT_NAME_REQUIRED_ERROR : null);

const submittedText = (value: string): string => value.replaceAll("\r\n", "\n").trim();

export const projectTextLength = (value: string): number => [...submittedText(value)].length;

export const isProjectTextOverLimit = (value: string, maxLength: number): boolean => projectTextLength(value) > maxLength;
