// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const (
	jevModelPrefix           = "jev-"
	openRouterJevModelPrefix = "typesafe/jev-"
	imagesHeader             = "TypeSafe-Danger-Zone-Images"
	wireAssetTypeImage       = "image"
)

var jevImageBounds = visionimage.Bounds{MinSide: 256, MaxSide: 1568, MinPixels: 78_400, MaxBytes: 16 << 20}

var jevAssetFormats = map[string]string{"image/png": "png", "image/jpeg": "jpg"}

type wireAsset struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

func readsAssets(model string) bool {
	return strings.HasPrefix(model, jevModelPrefix)
}

func imagesRequestHeader(req decision.Request) http.Header {
	if len(req.Media) == 0 || !readsAssets(requestModel(req)) {
		return nil
	}
	return http.Header{imagesHeader: []string{"true"}}
}

func (c *Client) encodeJevState(state any, images []decision.Media) (json.RawMessage, map[string]wireAsset, error) {
	text, err := decision.StateText(state)
	if err != nil {
		return nil, nil, err
	}
	if len(text) > c.transport.MaxStateBytes {
		return nil, nil, &decision.StateTooLargeError{Size: len(text), Limit: c.transport.MaxStateBytes}
	}
	parts := make([]string, 0, len(images)+1)
	parts = append(parts, text)
	assets := make(map[string]wireAsset, len(images))
	for i, img := range images {
		asset, assetErr := jevAsset(img, imageLabel(img, i))
		if assetErr != nil {
			return nil, nil, assetErr
		}
		ref := idgen.New()
		assets[ref] = asset
		parts = append(parts, ref)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return nil, nil, fmt.Errorf("encode decision state: %w", err)
	}
	return encoded, assets, nil
}

func imageLabel(img decision.Media, index int) string {
	if img.Name != "" {
		return img.Name
	}
	return "images[" + strconv.Itoa(index) + "]"
}

func jevAsset(img decision.Media, label string) (wireAsset, error) {
	data, mimeType, err := visionimage.FitBounds(img.Data, img.MIMEType, jevImageBounds)
	if errors.Is(err, visionimage.ErrTooLarge) {
		return wireAsset{}, decision.ImageTooLargeToDownscale(label)
	}
	if err != nil {
		return wireAsset{}, fmt.Errorf("image %q cannot be decoded to fit Jev's limits: %w", label, err)
	}
	format, ok := jevAssetFormats[mimeType]
	if !ok {
		return wireAsset{}, fmt.Errorf("image %q fitted to %s, which Jev does not accept", label, mimeType)
	}
	return wireAsset{
		Type:       wireAssetTypeImage,
		Format:     format,
		DataBase64: base64.StdEncoding.EncodeToString(data),
	}, nil
}
