package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientapi "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type KubectlResult struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

var kubectlSlots = make(chan struct{}, 4)
var kubectlVerbs = strings.Fields("get describe logs top explain rollout scale autoscale delete annotate label patch apply create replace set cordon uncordon drain taint wait diff api-resources api-versions version cluster-info")
var kubectlValues = strings.Fields("-n --namespace -l --selector --field-selector -o --output -f --filename --replicas --timeout --request-timeout --since --since-time --tail --limit-bytes -c --container --max-log-requests --type -p --patch --field-manager --dry-run --grace-period --revision --resource-version --subresource --for --image --min --max --cpu-percent --chunk-size --sort-by --cascade --prune-allowlist")
var kubectlBooleans = strings.Fields("-A --client --help -h --all-namespaces --all --all-containers --previous -p --timestamps --follow -f --watch -w --show-labels --show-kind --ignore-not-found --force --now --wait --overwrite --recursive -R --server-side --force-conflicts --prune --record --validate --ignore-daemonsets --delete-emptydir-data --disable-eviction --use-protocol-buffers --show-managed-fields --no-headers --namespaced --verbs --cached --containers --sum --allow-missing-template-keys --local")

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func ValidateKubectlArgs(args []string, stdin string) error {
	if len(args) == 0 || len(args) > 128 || !contains(kubectlVerbs, args[0]) || len(stdin) > 1024*1024 {
		return fmt.Errorf("unsupported command")
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("invalid argument")
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		parts := strings.SplitN(arg, "=", 2)
		flag := parts[0]
		// -f is follow for logs, filename for resource operations.
		takesValue := contains(kubectlValues, flag) && !(args[0] == "logs" && (flag == "-f" || flag == "-p"))
		if !takesValue && !contains(kubectlBooleans, flag) {
			return fmt.Errorf("unsupported flag: %s", flag)
		}
		if !takesValue {
			if len(parts) == 2 && parts[1] != "true" && parts[1] != "false" && !(flag == "--validate" && (parts[1] == "strict" || parts[1] == "warn" || parts[1] == "ignore")) {
				return fmt.Errorf("invalid flag value")
			}
			continue
		}
		var value string
		if len(parts) == 2 {
			value = parts[1]
		} else {
			i++
			if i >= len(args) {
				return fmt.Errorf("missing flag value")
			}
			value = args[i]
		}
		if len(value) > 8192 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid flag value")
		}
		if (flag == "-f" || flag == "--filename") && (value != "-" || strings.TrimSpace(stdin) == "") {
			return fmt.Errorf("use -f - with YAML input")
		}
		if (flag == "-o" || flag == "--output") && !(contains(strings.Fields("json yaml wide name"), value) || strings.HasPrefix(value, "jsonpath=") || strings.HasPrefix(value, "jsonpath-as-json=") || strings.HasPrefix(value, "custom-columns=") || strings.HasPrefix(value, "go-template=")) {
			return fmt.Errorf("unsupported output format")
		}
	}
	return nil
}
func (service *OnboardingService) RunKubectl(ctx context.Context, id string, args []string, stdin string) (KubectlResult, error) {
	if err := ValidateKubectlArgs(args, stdin); err != nil {
		return KubectlResult{}, err
	}
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return KubectlResult{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return KubectlResult{}, err
	}
	select {
	case kubectlSlots <- struct{}{}:
		defer func() { <-kubectlSlots }()
	default:
		return KubectlResult{}, fmt.Errorf("terminal busy")
	}
	return executeKubectl(ctx, parsed, args, stdin)
}

type cappedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := 1024*1024 - w.buf.Len()
	if len(p) > remaining {
		w.truncated = true
		p = p[:remaining]
	}
	w.buf.Write(p)
	return n, nil
}
func executeKubectl(ctx context.Context, parsed ParsedKubeconfig, args []string, stdin string) (KubectlResult, error) {
	binary, err := exec.LookPath("kubectl")
	if err != nil {
		return KubectlResult{}, fmt.Errorf("kubectl is not installed")
	}
	dir, err := os.MkdirTemp("", "kubescope-kubectl-")
	if err != nil {
		return KubectlResult{}, err
	}
	defer os.RemoveAll(dir)
	cfg := clientapi.NewConfig()
	cfg.Clusters["target"] = &clientapi.Cluster{Server: parsed.APIServer, CertificateAuthorityData: parsed.CAData}
	cfg.AuthInfos["session"] = &clientapi.AuthInfo{Token: parsed.Token, ClientCertificateData: parsed.ClientCertificateData, ClientKeyData: parsed.ClientKeyData}
	cfg.Contexts["target"] = &clientapi.Context{Cluster: "target", AuthInfo: "session"}
	cfg.CurrentContext = "target"
	content, err := clientcmd.Write(*cfg)
	if err != nil {
		return KubectlResult{}, err
	}
	if err = os.WriteFile(dir+"/config", content, 0600); err != nil {
		return KubectlResult{}, err
	}
	child, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, binary, append([]string{"--kubeconfig", dir + "/config", "--cache-dir", dir + "/cache"}, args...)...)
	// No local shell, user plugins, exec credentials or inherited kubeconfig are used.
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "LANG=C.UTF-8"}
	cmd.Stdin = strings.NewReader(stdin)
	output := &cappedOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		code = 1
		if exit, ok := runErr.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	if child.Err() != nil {
		code = 124
		io.WriteString(output, "\nCommand stopped or exceeded 60 seconds.\n")
	}
	text := output.buf.String()
	// kubectl output can include a temporary config path, but never expose credentials.
	text = strings.ReplaceAll(text, dir, "[session]")
	if parsed.Token != "" {
		text = strings.ReplaceAll(text, parsed.Token, "[redacted]")
	}
	return KubectlResult{Output: text, ExitCode: code, Truncated: output.truncated}, nil
}
