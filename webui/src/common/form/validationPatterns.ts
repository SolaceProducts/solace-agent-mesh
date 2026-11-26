// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

// eslint-disable-next-line no-control-regex -- intentional: validates input has no control characters
export const SAFE_NAME_PATTERN = /^[^\x00-\x1F\x7F]+$/;
export const SAFE_NAME_DESCRIPTION = "Name cannot contain control characters, newlines, or tabs.";

// eslint-disable-next-line no-control-regex -- intentional: validates input has no control characters
export const SAFE_TEXT_PATTERN = /^[^\x00-\x08\x0B\x0C\x0E-\x1F\x7F]+$/;
export const SAFE_TEXT_DESCRIPTION = "Value cannot contain control characters such as a backspace or vertical tab.";
