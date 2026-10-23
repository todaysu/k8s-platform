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
	"regexp"
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

// Bundle is a complete offline distribution ready to transfer to a container.
// Home is content-addressed so changing an offline package cannot reuse old jars.
type Bundle struct {
	Version string
	Home    string
	Payload []byte
	BuiltIn bool
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[.-][A-Za-z0-9]+)*$`)

func ValidVersion(version string) bool {
	return len(version) <= 64 && versionPattern.MatchString(version)
}

func Builtin() (Bundle, error) {
	content, err := Payload()
	return Bundle{Version: Version, Home: Home, Payload: content, BuiltIn: true}, err
}

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
		payload, payloadErr = archivePayload(distribution)
	})
	return payload, payloadErr
}

func archivePayload(distribution []byte) ([]byte, error) {
	if len(distribution) > maxArchiveSize {
		return nil, fmt.Errorf("Arthas 离线包超过 128 MiB 上限")
	}
	files, err := zip.NewReader(bytes.NewReader(distribution), int64(len(distribution)))
	if err != nil {
		return nil, fmt.Errorf("Arthas 离线包必须为完整的 ZIP 分发包: %w", err)
	}
	if len(files.File) > 10000 {
		return nil, fmt.Errorf("Arthas 离线包文件数量超过上限")
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	var total int64
	found := map[string]bool{}
	nonempty := map[string]bool{}
	for _, file := range files.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !safeEntryName(file.Name) || !file.Mode().IsRegular() || found[file.Name] {
			return nil, fmt.Errorf("Arthas 离线包含非法或重复路径")
		}
		found[file.Name] = true
		size := int64(file.UncompressedSize64)
		if size < 0 || size > maxEntryBytes || total+size > maxTotalBytes {
			return nil, fmt.Errorf("Arthas 离线包超过解压大小上限")
		}
		total += size
		nonempty[file.Name] = size > 0
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		err = tw.WriteHeader(&tar.Header{Name: file.Name, Mode: 0600, Size: size})
		if err == nil {
			// Bound the copy as well: a lying header cannot inflate past
			// the limit, and a short entry is caught by the size check.
			_, err = io.Copy(tw, io.LimitReader(reader, maxEntryBytes))
		}
		reader.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	for _, required := range []string{"arthas-boot.jar", "arthas-core.jar", "arthas-agent.jar", "arthas-spy.jar"} {
		if !nonempty[required] {
			return nil, fmt.Errorf("Arthas 完整离线包缺少 %s", required)
		}
	}
	return buffer.Bytes(), nil
}
