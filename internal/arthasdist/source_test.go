package arthasdist

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testArchive(t *testing.T, extra string, mode os.FileMode) []byte {
	t.Helper()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	for _, name := range []string{"arthas-boot.jar", "arthas-core.jar", "arthas-agent.jar", "arthas-spy.jar"} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("test jar contents")); err != nil {
			t.Fatal(err)
		}
	}
	if extra != "" {
		header := &zip.FileHeader{Name: extra}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("extra")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func TestLoaderDefaultsToEmbeddedBundle(t *testing.T) {
	bundle, err := (&Loader{}).Load(context.Background(), Source{})
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.BuiltIn || bundle.Version != Version || bundle.Home != Home || len(bundle.Payload) == 0 {
		t.Fatalf("expected embedded complete distribution: %#v", bundle)
	}
}

func TestLoaderVerifiesAndCachesOfflineOverride(t *testing.T) {
	content := testArchive(t, "", 0)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { requests++; _, _ = writer.Write(content) }))
	defer server.Close()
	source := Source{URL: server.URL + "/arthas.zip", Version: "4.4.0", SHA256: fmt.Sprintf("%x", sha256.Sum256(content)), Timeout: time.Second}
	loader := &Loader{}
	first, err := loader.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if first.BuiltIn || first.Version != "4.4.0" || !strings.Contains(first.Home, source.SHA256[:12]) || len(first.Payload) == 0 || second.Home != first.Home || requests != 1 {
		t.Fatalf("override was not verified and cached: home=%s requests=%d", first.Home, requests)
	}
	source.SHA256 = strings.Repeat("0", 64)
	if _, err := loader.Load(context.Background(), source); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum mismatch accepted: %v", err)
	}
}

func TestLoaderRejectsRedirectWithoutFetchingDestination(t *testing.T) {
	var destinationRequests int
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationRequests++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer server.Close()
	_, err := (&Loader{}).Load(context.Background(), Source{URL: server.URL, Version: Version})
	if err == nil || destinationRequests != 0 {
		t.Fatalf("redirect must be rejected, requests=%d error=%v", destinationRequests, err)
	}
}

func TestLoaderRejectsInvalidAndUnsafePackages(t *testing.T) {
	for _, item := range []struct {
		name    string
		content []byte
	}{
		{"not a zip", []byte("<html>error</html>")},
		{"path traversal", testArchive(t, "../escape.jar", 0600)},
		{"symlink", testArchive(t, "lib/escape.jar", os.ModeSymlink|0600)},
		{"duplicate jar", testArchive(t, "arthas-core.jar", 0600)},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(item.content) }))
			defer server.Close()
			if _, err := (&Loader{}).Load(context.Background(), Source{URL: server.URL, Version: Version}); err == nil {
				t.Fatal("invalid package accepted")
			}
		})
	}
}

func TestLoaderRejectsInvalidSourceAndOversizeResponse(t *testing.T) {
	for _, source := range []Source{
		{URL: "file:///etc/passwd", Version: Version},
		{URL: "https://user:password@example.com/arthas.zip", Version: Version},
		{URL: "https://example.com/a.zip#fragment", Version: Version},
		{URL: "https://example.com/a.zip", Version: "4.3.5;touch /tmp/unsafe"},
		{URL: "https://example.com/a.zip", Version: Version, SHA256: "x"},
	} {
		if _, err := (&Loader{}).Load(context.Background(), source); err == nil {
			t.Fatalf("invalid source accepted: %#v", source)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", fmt.Sprint(maxArchiveSize+1))
	}))
	defer server.Close()
	if _, err := (&Loader{}).Load(context.Background(), Source{URL: server.URL, Version: Version}); err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Fatalf("oversize accepted: %v", err)
	}
}

func TestLoaderHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	defer server.Close()
	_, err := (&Loader{}).Load(context.Background(), Source{URL: server.URL, Version: Version, Timeout: 20 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected bounded download: %v", err)
	}
}
