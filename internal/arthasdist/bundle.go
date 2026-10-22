// Package arthasdist keeps the pinned, complete upstream distribution in the binary.
package arthasdist

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
)

const Version = "4.3.5"
const SHA256 = "2f75ea954ead5d02c4a743ecd783fd92f546a44ef3aee437ea5066eb30ad07bf"
const Home = "/tmp/ftc-arthas-4.3.5-2f75ea954ead"

// Decompression limits. The bundle is checksum-verified at startup, so these
// are defence in depth: if the archive is ever replaced or the checksum
// constant is regenerated from an untrusted source, a high-ratio entry cannot
// exhaust memory here.
const (
	maxEntryBytes  = 128 << 20 // one file inside the archive
	maxTotalBytes  = 512 << 20 // everything the archive expands to
	maxArchiveSize = 128 << 20 // the embedded zip itself
)

//go:embed arthas-4.3.5-bin.zip
var distribution []byte
var once sync.Once
var payload []byte
var payloadErr error

// safeEntryName rejects any archive entry that could escape the extraction
// directory once the target container runs tar. path.Clean alone is not
// enough: a bare ".." cleans to itself and "." has the same shape, and Windows
// separators would survive a slash-only check.
func safeEntryName(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) {
		return false
	}
	if strings.Contains(name, `\`) {
		return false
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return false
	}
	cleaned := path.Clean(name)
	if cleaned != name {
		return false
	}
	// "..", ".", "/" and any path that still climbs out after cleaning.
	if cleaned == "." || cleaned == ".." || cleaned == "/" {
		return false
	}
	for _, element := range strings.Split(cleaned, "/") {
		if element == ".." || element == "." || element == "" {
			return false
		}
	}
	return true
}

// Payload returns a tar.gz stream for tar-equipped containers; unzip and internet
// access in the target container are not required. Only trusted bundled files enter it.
func Payload() ([]byte, error) {
	once.Do(func() {
		if fmt.Sprintf("%x", sha256.Sum256(distribution)) != SHA256 {
			payloadErr = fmt.Errorf("Arthas bundle checksum mismatch")
			return
		}
		if len(distribution) > maxArchiveSize {
			payloadErr = fmt.Errorf("Arthas bundle is larger than the supported limit")
			return
		}
		files, err := zip.NewReader(bytes.NewReader(distribution), int64(len(distribution)))
		if err != nil {
			payloadErr = err
			return
		}
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tw := tar.NewWriter(gz)
		var total int64
		for _, file := range files.File {
			if file.FileInfo().IsDir() {
				continue
			}
			if !safeEntryName(file.Name) {
				payloadErr = fmt.Errorf("invalid bundled path")
				return
			}
			size := int64(file.UncompressedSize64)
			if size < 0 || size > maxEntryBytes || total+size > maxTotalBytes {
				payloadErr = fmt.Errorf("Arthas bundle exceeds the extraction limit")
				return
			}
			total += size
			reader, err := file.Open()
			if err != nil {
				payloadErr = err
				return
			}
			err = tw.WriteHeader(&tar.Header{Name: file.Name, Mode: 0600, Size: size})
			if err == nil {
				// Bound the copy as well: a lying header cannot inflate past
				// the limit, and a short entry is caught by the size check.
				_, err = io.Copy(tw, io.LimitReader(reader, maxEntryBytes))
			}
			reader.Close()
			if err != nil {
				payloadErr = err
				return
			}
		}
		if err := tw.Close(); err != nil {
			payloadErr = err
			return
		}
		if err := gz.Close(); err != nil {
			payloadErr = err
			return
		}
		payload = buffer.Bytes()
	})
	return payload, payloadErr
}
