package cluster

import (
	"context"
	"fmt"
	"github.com/creack/pty"
	"io"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientapi "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// ValidateTTYArgs keeps local configuration/files inaccessible. Arguments after
// exec's -- belong to the remote container, never to a local shell.
func ValidateTTYArgs(args []string) error {
	if len(args) > 0 && args[0] == "exec" {
		split := -1
		for i, a := range args {
			if a == "--" {
				split = i
				break
			}
		}
		if split < 2 || split == len(args)-1 {
			return fmt.Errorf("exec requires -- command")
		}
		safe := []string{"get"}
		for i := 1; i < split; i++ {
			a := args[i]
			if contains([]string{"-i", "-t", "-it", "-ti", "--stdin", "--tty"}, a) {
				continue
			}
			safe = append(safe, a)
		}
		if err := ValidateKubectlArgs(safe, ""); err != nil {
			return err
		}
		for _, a := range args[split+1:] {
			if len(a) > 8192 || strings.ContainsRune(a, 0) {
				return fmt.Errorf("invalid remote argument")
			}
		}
		return nil
	}
	return ValidateKubectlArgs(args, "tty")
}

func (service *OnboardingService) RunKubectlTTY(ctx context.Context, id string, args []string, input io.Reader, output io.Writer, sizes <-chan TerminalSize) error {
	if err := ValidateTTYArgs(args); err != nil {
		return err
	}
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return err
	}
	select {
	case kubectlSlots <- struct{}{}:
		defer func() { <-kubectlSlots }()
	default:
		return fmt.Errorf("terminal busy")
	}
	return executeKubectlTTY(ctx, parsed, args, input, output, sizes)
}
func executeKubectlTTY(ctx context.Context, parsed ParsedKubeconfig, args []string, input io.Reader, output io.Writer, sizes <-chan TerminalSize) error {
	binary, err := exec.LookPath("kubectl")
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "kubescope-tty-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfg := clientapi.NewConfig()
	cfg.Clusters["target"] = &clientapi.Cluster{Server: parsed.APIServer, CertificateAuthorityData: parsed.CAData}
	cfg.AuthInfos["session"] = &clientapi.AuthInfo{Token: parsed.Token, ClientCertificateData: parsed.ClientCertificateData, ClientKeyData: parsed.ClientKeyData}
	cfg.Contexts["target"] = &clientapi.Context{Cluster: "target", AuthInfo: "session"}
	cfg.CurrentContext = "target"
	content, err := clientcmd.Write(*cfg)
	if err != nil {
		return err
	}
	if err = os.WriteFile(dir+"/config", content, 0600); err != nil {
		return err
	}
	cmd := exec.Command(binary, append([]string{"--kubeconfig", dir + "/config", "--cache-dir", dir + "/cache"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TERM=xterm-256color", "LANG=C.UTF-8"}
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return err
	}
	defer terminal.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = terminal.Close()
		case <-done:
		}
	}()
	go func() { _, _ = io.Copy(terminal, input) }()
	go func() {
		for {
			select {
			case size, ok := <-sizes:
				if !ok {
					return
				}
				_ = pty.Setsize(terminal, &pty.Winsize{Cols: size.Width, Rows: size.Height})
			case <-done:
				return
			}
		}
	}()
	_, _ = io.Copy(output, terminal)
	// EOF/EIO means the PTY ended; a failed network write must also reap the child.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return cmd.Wait()
}
