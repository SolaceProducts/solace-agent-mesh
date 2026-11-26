// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

type Profile struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`

	SlideWidth  float64 `json:"slide_width"`
	SlideHeight float64 `json:"slide_height"`

	Colors      ThemeColors `json:"colors"`
	HeadingFont string      `json:"heading_font"`
	BodyFont    string      `json:"body_font"`
	LabelFont   string      `json:"label_font"`

	Assets     map[string]*ProfileAsset    `json:"assets"`
	Footer     ProfileFooter               `json:"footer"`
	LayoutMap  map[string]string           `json:"layout_map"`
	Categories map[string]*ProfileCategory `json:"categories"`
	CardStyle  CardStyle                   `json:"card_style"`
}

type CardStyle struct {
	Shadow         *bool    `json:"shadow"`
	CornerRadiusIn *float64 `json:"corner_radius_in"`
	Border         *bool    `json:"border"`
}

type ProfileAsset struct {
	DataBase64 string `json:"data_base64"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Format     string `json:"format"`

	Data []byte `json:"-"`
}

type ProfileFooter struct {
	Enabled         bool    `json:"enabled"`
	ShowSlideNumber bool    `json:"show_slide_number"`
	ShowFooterText  bool    `json:"show_footer_text"`
	ShowDate        bool    `json:"show_date"`
	FontSize        float64 `json:"font_size"`
	ColorOnLight    string  `json:"color_on_light"`
	ColorOnDark     string  `json:"color_on_dark"`

	SlideNumberPosition *Position `json:"slide_number_position"`
	FooterTextPosition  *Position `json:"footer_text_position"`
	SlideNumberStyle    string    `json:"slide_number_style"`
}

type Position struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type ProfileCategory struct {
	Background     string       `json:"background"`
	TitleColor     string       `json:"title_color"`
	BodyColor      string       `json:"body_color"`
	AccentBarColor *string      `json:"accent_bar_color"`
	KickerColor    string       `json:"kicker_color,omitempty"`
	Decorations    []Decoration `json:"decorations"`
}

type Decoration struct {
	Asset   string  `json:"asset"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Opacity *int    `json:"opacity"`
}

func LoadProfile(data []byte) (*Profile, error) {
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile JSON: %w", err)
	}

	if p.SlideWidth <= 0 || p.SlideHeight <= 0 {
		return nil, fmt.Errorf("profile must specify positive slide_width and slide_height")
	}

	for name, asset := range p.Assets {
		if asset.DataBase64 == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(asset.DataBase64)
		if err != nil {
			return nil, fmt.Errorf("decode asset %q: %w", name, err)
		}
		asset.Data = decoded
		asset.DataBase64 = ""
	}

	for catName, cat := range p.Categories {
		for _, deco := range cat.Decorations {
			if _, ok := p.Assets[deco.Asset]; !ok {
				return nil, fmt.Errorf("category %q references unknown asset %q", catName, deco.Asset)
			}
		}
	}

	return &p, nil
}

func LoadProfileFromFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile file: %w", err)
	}
	return LoadProfile(data)
}
