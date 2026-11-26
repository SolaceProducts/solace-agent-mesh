// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package execbit

import (
	"bytes"
	"encoding/binary"
	"os"
)

var (
	elfMagic         = []byte{0x7f, 'E', 'L', 'F'}
	machO64LE        = []byte{0xCF, 0xFA, 0xED, 0xFE}
	machO64BE        = []byte{0xFE, 0xED, 0xFA, 0xCF}
	machO32LE        = []byte{0xCE, 0xFA, 0xED, 0xFE}
	machO32BE        = []byte{0xFE, 0xED, 0xFA, 0xCE}
	machOUniversal   = []byte{0xCA, 0xFE, 0xBA, 0xBE}
	machOUniversal64 = []byte{0xCA, 0xFE, 0xBA, 0xBF}
	windowsDOSHeader = []byte("MZ")
	shebang          = []byte("#!")
)

var nativeBinaryMagic = [][]byte{
	elfMagic,
	machO64LE, machO64BE,
	machO32LE, machO32BE,
	machOUniversal, machOUniversal64,
}

const headerBytes = 0x40

func LooksRunnable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	return LooksRunnableFile(f)
}

func LooksRunnableFile(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	header := make([]byte, headerBytes)
	n, err := f.ReadAt(header, 0)
	if n < 2 && err != nil {
		return false
	}
	header = header[:n]

	if looksRunnableHeader(header) {
		return true
	}
	return isWindowsPE(f, header)
}

func LooksRunnableBytes(content []byte) bool {
	header := content
	if len(header) > headerBytes {
		header = header[:headerBytes]
	}
	return looksRunnableHeader(header) || bytes.HasPrefix(header, windowsDOSHeader)
}

func looksRunnableHeader(header []byte) bool {
	if bytes.HasPrefix(header, shebang) {
		return true
	}
	for _, magic := range nativeBinaryMagic {
		if bytes.HasPrefix(header, magic) {
			return true
		}
	}
	return false
}

func isWindowsPE(f *os.File, header []byte) bool {
	if !bytes.HasPrefix(header, windowsDOSHeader) || len(header) < headerBytes {
		return false
	}
	signature := make([]byte, 4)
	offset := binary.LittleEndian.Uint32(header[0x3c:0x40])
	if _, err := f.ReadAt(signature, int64(offset)); err != nil {
		return false
	}
	return bytes.Equal(signature, []byte{'P', 'E', 0, 0})
}
