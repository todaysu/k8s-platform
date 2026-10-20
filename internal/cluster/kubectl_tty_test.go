package cluster

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTTYArgs(t *testing.T) {
	for _, args := range [][]string{{"exec", "-it", "web", "-n", "myapp", "--", "sh"}, {"logs", "-f", "web"}, {"get", "pods", "-w"}} {
		if err := ValidateTTYArgs(args); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, args := range [][]string{{"exec", "--kubeconfig=/tmp/config", "web", "--", "sh"}, {"config", "view"}, {"get", "pods", "--token=secret"}, {"exec", "web"}} {
		if ValidateTTYArgs(args) == nil {
			t.Fatal("accepted", args)
		}
	}
}
func TestTTYStreamsAndAcceptsInput(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf 'stream-ready\\n'\nread answer\nprintf 'reply:%s\\n' \"$answer\"\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := executeKubectlTTY(ctx, ParsedKubeconfig{}, []string{"version"}, strings.NewReader("hello\n"), &out, make(chan TerminalSize))
	if err != nil || !strings.Contains(out.String(), "reply:hello") {
		t.Fatal(err, out.String())
	}
}
