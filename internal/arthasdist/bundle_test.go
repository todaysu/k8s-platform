package arthasdist

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestPinnedBundleIncludesOfflineCoreAndBothLinuxArchitectures(t *testing.T) {
	payload, err := Payload()
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found[h.Name] = true
	}
	for _, file := range []string{"arthas-core.jar", "arthas-agent.jar", "arthas-spy.jar", "arthas-boot.jar", "async-profiler/libasyncProfiler-linux-arm64.so", "async-profiler/libasyncProfiler-linux-x64.so", "lib/libArthasJniLibrary-aarch64.so", "lib/libArthasJniLibrary-x64.so"} {
		if !found[file] {
			t.Fatalf("missing %s", file)
		}
	}
}

// A bare ".." cleans to itself, so a path.Clean-only check would let it
// through. These cases must all be rejected before extraction.
func TestSafeEntryNameRejectsTraversal(t *testing.T) {
	unsafe := []string{
		"..", ".", "/", "/etc/passwd", "../evil", "a/../../evil",
		"..\\evil", "C:\\evil", "a/b/../../../c", "", "a\x00b",
		"./relative", "trailing/",
	}
	for _, name := range unsafe {
		if safeEntryName(name) {
			t.Fatalf("must reject %q", name)
		}
	}
	safe := []string{"bin/arthas.jar", "lib/a.jar", "arthas", "a/b/c.txt", "conf/application.properties"}
	for _, name := range safe {
		if !safeEntryName(name) {
			t.Fatalf("must accept %q", name)
		}
	}
}

func TestPayloadIsBoundedAndValid(t *testing.T) {
	payload, err := Payload()
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 {
		t.Fatal("payload is empty")
	}
	if len(payload) > maxTotalBytes {
		t.Fatalf("payload %d exceeds the extraction limit", len(payload))
	}
}
