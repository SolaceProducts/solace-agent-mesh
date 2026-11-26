// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import "encoding/binary"

const (
	mp4BoxHeaderBytes       = 8
	mp4LargeSizeBytes       = 8
	mp4BoxSizeToEnd         = 0
	mp4BoxSizeIsLarge       = 1
	mp4MaxBoxesWalkedInMoov = 4096
	mp4HandlerTypeOffset    = 8
	mp4HandlerTypeBytes     = 4
	mp4SoundHandler         = "soun"
	mp4VideoHandler         = "vide"
	mp4FourCCBytes          = 4
	mp4BoxTypeStartOffset   = 4
	mp4MovieBox             = "moov"
)

var mp4HandlerPathBelowMoov = []string{"trak", "mdia", "hdlr"}

type mp4Box struct {
	boxType string
	payload []byte
	size    uint64
}

func nextMP4Box(data []byte) (mp4Box, bool) {
	if len(data) < mp4BoxHeaderBytes {
		return mp4Box{}, false
	}
	size := uint64(binary.BigEndian.Uint32(data))
	header := uint64(mp4BoxHeaderBytes)
	switch size {
	case mp4BoxSizeToEnd:
		size = uint64(len(data))
	case mp4BoxSizeIsLarge:
		if len(data) < mp4BoxHeaderBytes+mp4LargeSizeBytes {
			return mp4Box{}, false
		}
		size = binary.BigEndian.Uint64(data[mp4BoxHeaderBytes:])
		header += mp4LargeSizeBytes
	}
	if size < header || size > uint64(len(data)) {
		return mp4Box{}, false
	}
	boxType := string(data[mp4BoxTypeStartOffset : mp4BoxTypeStartOffset+mp4FourCCBytes])
	return mp4Box{boxType: boxType, payload: data[header:size:size], size: size}, true
}

func audioOnlyMP4(data []byte) bool {
	for len(data) > 0 {
		box, ok := nextMP4Box(data)
		if !ok {
			return false
		}
		if box.boxType == mp4MovieBox {
			return movieHasSoundAndNoVideo(box.payload)
		}
		data = data[box.size:]
	}
	return false
}

func movieHasSoundAndNoVideo(moov []byte) bool {
	walk := mp4HandlerWalk{budget: mp4MaxBoxesWalkedInMoov, complete: true}
	walk.walk(moov, mp4HandlerPathBelowMoov)
	return walk.complete && walk.hasSound && !walk.hasVideo
}

type mp4HandlerWalk struct {
	budget             int
	complete           bool
	hasSound, hasVideo bool
}

func (w *mp4HandlerWalk) walk(data []byte, path []string) {
	for len(data) > 0 && w.complete {
		if w.budget == 0 {
			w.complete = false
			return
		}
		w.budget--
		box, ok := nextMP4Box(data)
		if !ok {
			w.complete = false
			return
		}
		if box.boxType == path[0] {
			if len(path) == 1 {
				w.readHandler(box.payload)
			} else {
				w.walk(box.payload, path[1:])
			}
		}
		data = data[box.size:]
	}
}

func (w *mp4HandlerWalk) readHandler(hdlr []byte) {
	if len(hdlr) < mp4HandlerTypeOffset+mp4HandlerTypeBytes {
		return
	}
	switch string(hdlr[mp4HandlerTypeOffset : mp4HandlerTypeOffset+mp4HandlerTypeBytes]) {
	case mp4SoundHandler:
		w.hasSound = true
	case mp4VideoHandler:
		w.hasVideo = true
	}
}
