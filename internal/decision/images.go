// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"fmt"
	"net/http"
	"slices"
)

const (
	DefaultMaxImages     = 5
	DefaultMaxImageBytes = 20 << 20
	MaxImagesPerRequest  = 128
	MaxImageBytesLimit   = 64 << 20
)

var imageMIMETypes = []string{"image/png", "image/jpeg", "image/webp"}

func ImageMIMETypes() []string { return slices.Clone(imageMIMETypes) }

func SniffImage(data []byte) (string, bool) {
	mimeType := http.DetectContentType(data)
	return mimeType, slices.Contains(imageMIMETypes, mimeType)
}

type ImagesTooLargeError struct {
	reason     imagesTooLargeReason
	image      string
	count      int
	countLimit int
	bytes      int64
	byteLimit  int64
}

type imagesTooLargeReason int

const (
	tooManyImages imagesTooLargeReason = iota + 1
	imagesOverBudget
	imageFileTooLarge
	imageTooLargeToDownscale
)

func TooManyImages(count, limit int) *ImagesTooLargeError {
	return &ImagesTooLargeError{reason: tooManyImages, count: count, countLimit: limit}
}

func ImagesOverBudget(bytes, byteLimit int64) *ImagesTooLargeError {
	return &ImagesTooLargeError{reason: imagesOverBudget, bytes: bytes, byteLimit: byteLimit}
}

func ImageFileTooLarge(name string, bytes, byteLimit int64) *ImagesTooLargeError {
	return &ImagesTooLargeError{reason: imageFileTooLarge, image: name, bytes: bytes, byteLimit: byteLimit}
}

func ImageTooLargeToDownscale(name string) *ImagesTooLargeError {
	return &ImagesTooLargeError{reason: imageTooLargeToDownscale, image: name}
}

func (e *ImagesTooLargeError) Error() string {
	switch e.reason {
	case tooManyImages:
		return fmt.Sprintf("decision sends %d images; the limit is %d (set max_images, or send fewer)", e.count, e.countLimit)
	case imagesOverBudget:
		return fmt.Sprintf("decision images are %d bytes after downscaling; the limit is %d (set max_image_bytes, or send smaller images)", e.bytes, e.byteLimit)
	case imageFileTooLarge:
		return fmt.Sprintf("image %q is %d bytes; the limit is %d (set max_image_bytes, or send a smaller image)", e.image, e.bytes, e.byteLimit)
	case imageTooLargeToDownscale:
		return fmt.Sprintf("image %q is too large to downscale safely; send a smaller image", e.image)
	default:
		return "decision images are over a limit"
	}
}
