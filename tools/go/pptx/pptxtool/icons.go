// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"embed"
	"strings"
	"unicode"
)

//go:embed icons/*.png
var iconsFS embed.FS

const iconAttribution = "Icon glyphs derived from Bootstrap Icons v1.13.1 (https://github.com/twbs/icons), " +
	"licensed under the MIT License, Copyright (c) 2019-2024 The Bootstrap Authors. " +
	"Recolored and rasterized to PNG; no other modification."

var iconAliases = map[string]string{
	"agent":         "robot",
	"ai":            "robot",
	"bot":           "robot",
	"assistant":     "robot",
	"conversation":  "chat",
	"message":       "chat",
	"speech":        "chat",
	"data":          "database",
	"storage":       "database",
	"support":       "headset",
	"helpdesk":      "headset",
	"research":      "search",
	"find":          "search",
	"discover":      "search",
	"builder":       "magic-wand",
	"build":         "magic-wand",
	"create":        "magic-wand",
	"connector":     "plug",
	"integration":   "plug",
	"observability": "chart-line",
	"analytics":     "chart-line",
	"metrics":       "chart-line",
	"chart":         "chart-line",
	"security":      "shield",
	"governance":    "shield",
	"privacy":       "lock",
	"flow":          "workflow",
	"pipeline":      "workflow",
	"hierarchy":     "sitemap",
	"mesh":          "sitemap",
	"team":          "people",
	"group":         "people",
	"skill":         "book",
	"docs":          "book",
	"knowledge":     "book",
	"tool":          "wrench",
	"config":        "gears",
	"settings":      "gears",
	"deploy":        "rocket",
	"launch":        "rocket",
	"assembly":      "workflow",
	"evaluate":      "flask",
	"eval":          "flask",
	"test":          "flask",
	"legal":         "gavel",
	"finance":       "calculator",
	"engineering":   "gears",
	"bizops":        "briefcase",
	"business":      "briefcase",
	"sales":         "chart-line",
	"growth":        "sprout",
	"goal":          "trophy",
	"win":           "trophy",
	"maturity":      "trophy",
	"code":          "code",
	"developer":     "code",
	"compose":       "puzzle",
	"assemble":      "puzzle",
	"assembler":     "puzzle",
	"scale":         "expand",
	"grow":          "sprout",
	"next":          "arrow-right",
	"step":          "chevron-right",
	"check":         "check-circle",
	"done":          "check-circle",
	"compass":       "compass",
	"orient":        "compass",
	"navigate":      "compass",
	"product":       "box",
	"package":       "box",
	"shipping":      "box",
	"target":        "target",
	"focus":         "target",
	"objective":     "target",
	"idea":          "lightbulb",
	"insight":       "lightbulb",
	"innovation":    "lightbulb",
	"watch":         "eye",
	"fast":          "bolt",
	"speed":         "bolt",
}

func lookupIcon(name string) []byte {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil
	}
	if canonical, ok := iconAliases[key]; ok {
		key = canonical
	}
	data, err := iconsFS.ReadFile("icons/" + key + ".png")
	if err != nil {
		return nil
	}
	return data
}

func resolveGlyph(item GridItem) []byte {
	if item.Icon != nil {
		if g := lookupIcon(*item.Icon); g != nil {
			return g
		}
	}
	texts := []string{item.Title}
	if item.Description != nil {
		texts = append(texts, *item.Description)
	}
	if name := inferIconName(texts...); name != "" {
		return lookupIcon(name)
	}
	return nil
}

func inferIconName(texts ...string) string {
	for _, text := range texts {
		for _, word := range strings.FieldsFunc(text, func(r rune) bool {
			return !unicode.IsLetter(r)
		}) {
			if len(word) < 2 {
				continue
			}
			if lookupIcon(word) != nil {
				return word
			}
			if singular, ok := strings.CutSuffix(strings.ToLower(word), "s"); ok && len(singular) >= 3 && lookupIcon(singular) != nil {
				return singular
			}
		}
	}
	return ""
}

func monogram(title string) string {
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	return ""
}
