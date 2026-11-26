// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
)

const slate400 = "#94a3b8"

var chartThemeColors = map[string]string{
	"text":          slate400,
	"textBright":    "#cbd5e1",
	"grid":          "#1e293b",
	"surface":       "#0f1828",
	"accent":        "#00c895",
	"accentAlt":     "#0892ce",
	"success":       "#22c55e",
	"warn":          "#f59e0b",
	"danger":        "#ef4444",
	"slate":         slate400,
	"completed":     "#22c55e",
	"failed":        "#ef4444",
	"errored":       "#dc2626",
	"timeout":       "#f97316",
	"canceled":      slate400,
	"running":       "#f59e0b",
	"inProgress":    "#3b82f6",
	"stale":         "#a855f7",
	"unknownStatus": "#64748b",
}

func statusPieChartJSConfig(rows []StatusCountRow) map[string]any {
	total := 0
	for _, r := range rows {
		total += r.Count
	}
	labels := make([]string, len(rows))
	values := make([]int, len(rows))
	colors := make([]string, len(rows))
	for i, r := range rows {
		var share float64
		if total > 0 {
			share = 100 * float64(r.Count) / float64(total)
		}
		labels[i] = fmt.Sprintf("%s (%d, %.1f%%)", r.Status, r.Count, share)
		values[i] = r.Count
		colors[i] = chartStatusColor(r.Status)
	}
	return map[string]any{
		"type": "pie",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{
					"data":            values,
					"backgroundColor": colors,
					"borderColor":     chartThemeColors["surface"],
					"borderWidth":     2,
				},
			},
		},
		"options": darkChartOptions(map[string]any{
			"plugins": map[string]any{
				"legend": map[string]any{
					"position": "right",
					"labels": map[string]any{
						"color":    chartThemeColors["textBright"],
						"font":     map[string]any{"size": 12},
						"boxWidth": 12,
						"padding":  10,
					},
				},
				"tooltip": darkTooltipOpts(),
			},
		}),
	}
}

func durationHistogramChartJSConfig(buckets []DurationBucket) map[string]any {
	labels := make([]string, len(buckets))
	counts := make([]int, len(buckets))
	for i, b := range buckets {
		labels[i] = bucketLabel(b)
		counts[i] = b.Count
	}
	return map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{
					"label":           "Tasks",
					"data":            counts,
					"backgroundColor": chartThemeColors["accent"],
					"borderRadius":    2,
				},
			},
		},
		"options": darkChartOptions(map[string]any{
			"plugins": map[string]any{
				"legend":  map[string]any{"display": false},
				"tooltip": darkTooltipOpts(),
			},
			"scales": map[string]any{
				"x": darkAxisOpts(false),
				"y": darkAxisOpts(true),
			},
		}),
	}
}

func tokenTrendChartJSConfig(rows []TokenTrendRow) map[string]any {
	dates := make([]string, len(rows))
	input := make([]int64, len(rows))
	output := make([]int64, len(rows))
	cached := make([]int64, len(rows))
	for i, r := range rows {
		dates[i] = r.DateIso
		input[i] = r.Input
		output[i] = r.Output
		cached[i] = r.Cached
	}
	return map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": dates,
			"datasets": []map[string]any{
				{"label": "Input", "data": input, "backgroundColor": chartThemeColors["accent"], "stack": "tokens"},
				{"label": "Output", "data": output, "backgroundColor": chartThemeColors["accentAlt"], "stack": "tokens"},
				{"label": "Cached", "data": cached, "backgroundColor": chartThemeColors["slate"], "stack": "tokens"},
			},
		},
		"options": darkChartOptions(map[string]any{
			"plugins": map[string]any{
				"legend": map[string]any{
					"position": "bottom",
					"labels": map[string]any{
						"color":    chartThemeColors["textBright"],
						"boxWidth": 12,
						"padding":  10,
					},
				},
				"tooltip": darkTooltipOpts(),
			},
			"scales": map[string]any{
				"x": mergeMaps(darkAxisOpts(false), map[string]any{"stacked": true}),
				"y": mergeMaps(darkAxisOpts(true), map[string]any{"stacked": true, "beginAtZero": true}),
			},
		}),
	}
}

func taskCountTrendChartJSConfig(rows []TokenTrendRow) map[string]any {
	dates := make([]string, len(rows))
	tasks := make([]int, len(rows))
	for i, r := range rows {
		dates[i] = r.DateIso
		tasks[i] = r.TaskCount
	}
	return map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": dates,
			"datasets": []map[string]any{
				{
					"label":           "Tasks",
					"data":            tasks,
					"backgroundColor": chartThemeColors["warn"],
					"borderRadius":    2,
				},
			},
		},
		"options": darkChartOptions(map[string]any{
			"plugins": map[string]any{
				"legend":  map[string]any{"display": false},
				"tooltip": darkTooltipOpts(),
			},
			"scales": map[string]any{
				"x": darkAxisOpts(false),
				"y": mergeMaps(darkAxisOpts(true), map[string]any{"beginAtZero": true}),
			},
		}),
	}
}

func chartStatusColor(status string) string {
	switch status {
	case "completed":
		return chartThemeColors["completed"]
	case "failed":
		return chartThemeColors["failed"]
	case "error":
		return chartThemeColors["errored"]
	case "timeout":
		return chartThemeColors["timeout"]
	case "canceled", "cancelled":
		return chartThemeColors["canceled"]
	case "running":
		return chartThemeColors["running"]
	case "in_progress":
		return chartThemeColors["inProgress"]
	case "stale":
		return chartThemeColors["stale"]
	}
	return chartThemeColors["unknownStatus"]
}

func darkChartOptions(chartSpecific map[string]any) map[string]any {
	opts := map[string]any{
		"responsive":          true,
		"maintainAspectRatio": false,
		"animation":           map[string]any{"duration": 300},
		"color":               chartThemeColors["text"],
	}
	for k, v := range chartSpecific {
		opts[k] = v
	}
	return opts
}

func darkAxisOpts(beginAtZero bool) map[string]any {
	return map[string]any{
		"beginAtZero": beginAtZero,
		"ticks": map[string]any{
			"color": chartThemeColors["text"],
			"font":  map[string]any{"size": 11},
		},
		"grid": map[string]any{
			"color": chartThemeColors["grid"],
		},
	}
}

func darkTooltipOpts() map[string]any {
	return map[string]any{
		"backgroundColor": "#020617",
		"titleColor":      chartThemeColors["textBright"],
		"bodyColor":       chartThemeColors["text"],
		"borderColor":     chartThemeColors["grid"],
		"borderWidth":     1,
		"padding":         8,
	}
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
