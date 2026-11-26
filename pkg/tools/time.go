// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const pythonIsoformatLayout = "2006-01-02T15:04:05.000000-07:00"

type GetCurrentDatetimeTool struct {
	artifactToolBase
	tool.ReadOnlyTool
}

func (t *GetCurrentDatetimeTool) Name() string { return "get_current_time" }

func (t *GetCurrentDatetimeTool) Description() string {
	return "Gets the current date and time in the user's local timezone. " +
		"Returns comprehensive time information including formatted timestamps, " +
		"timezone details, and date components. Use this tool when you need to " +
		"know what time it is for the user."
}

func (t *GetCurrentDatetimeTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{},
	}
}

func (t *GetCurrentDatetimeTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "current_time", typ: "string",
			description: "Current time in ISO 8601 format."},
		resultField{name: "timezone", typ: "string",
			description: "IANA timezone name the time is expressed in."},
		resultField{name: "timezone_offset", typ: "string",
			description: "Numeric UTC offset, e.g. \"-04:00\"."},
		resultField{name: "timezone_abbreviation", typ: "string",
			description: "Short timezone label, e.g. \"EDT\"."},
		resultField{name: "formatted_time", typ: "string",
			description: "Human-readable rendering of the current time."},
		resultField{name: "timestamp", typ: "integer",
			description: "Unix epoch seconds."},
		resultField{name: "date", typ: "string",
			description: "Date portion only."},
		resultField{name: "time", typ: "string",
			description: "Time-of-day portion only."},
		resultField{name: "day_of_week", typ: "string",
			description: "Weekday name."},
	)
}

func (t *GetCurrentDatetimeTool) Execute(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	tzName := tool.GetUserTimezone(toolCtx)

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Default().With("logger_name", "get_current_time").WarnContext(ctx, "get_current_time: invalid timezone, falling back to UTC",
			"timezone", tzName, "error", err)
		tzName = tool.DefaultUserTimezone
		loc = time.UTC
	}

	now := time.Now().In(loc)
	offsetStr := formatTimezoneOffset(now)
	abbrev, _ := now.Zone()

	isoFormat := now.Format(pythonIsoformatLayout)
	formattedTime := fmt.Sprintf("%s %s", now.Format("2006-01-02 15:04:05"), abbrev)
	dateOnly := now.Format("2006-01-02")
	timeOnly := now.Format("15:04:05")
	dayOfWeek := now.Weekday().String()
	timestamp := now.Unix()

	return tool.OK(
		fmt.Sprintf("Current time in %s: %s", tzName, formattedTime),
		tool.WithData(map[string]any{
			"current_time":          isoFormat,
			"timezone":              tzName,
			"timezone_offset":       offsetStr,
			"timezone_abbreviation": abbrev,
			"formatted_time":        formattedTime,
			"timestamp":             timestamp,
			"date":                  dateOnly,
			"time":                  timeOnly,
			"day_of_week":           dayOfWeek,
		}),
	), nil
}

func formatTimezoneOffset(t time.Time) string {
	_, offsetSec := t.Zone()
	sign := "+"
	if offsetSec < 0 {
		sign = "-"
		offsetSec = -offsetSec
	}
	hours := offsetSec / 3600
	mins := (offsetSec % 3600) / 60
	return fmt.Sprintf("%s%02d:%02d", sign, hours, mins)
}
