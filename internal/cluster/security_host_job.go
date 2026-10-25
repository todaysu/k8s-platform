package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultKubeBenchImage            = "docker.io/aquasec/kube-bench:v0.16.0"
	defaultHostScanConcurrency       = 5
	defaultSecurityPodStartupTimeout = 5 * time.Minute
	maxHostReportBytes               = 4 << 20
)

func (connector *KubernetesConnector) ConfigureHostSecurityScanner(image, amd64Image, arm64Image, namespace, pullSecret string, concurrency int) {
	if value := strings.TrimSpace(image); value != "" {
		connector.kubeBenchImage = value
	}
	connector.kubeBenchImageAMD64 = strings.TrimSpace(amd64Image)
	connector.kubeBenchImageARM64 = strings.TrimSpace(arm64Image)
	if value := strings.TrimSpace(namespace); value != "" {
		connector.securityScannerNamespace = value
	}
	connector.securityScannerPullSecret = strings.TrimSpace(pullSecret)
	if concurrency > 0 {
		connector.hostScanConcurrency = concurrency
	}
}

// SetSecurityPodStartupTimeout bounds how long a kube-bench Job may wait for its
// scan Pod to start. A non-positive value restores the default.
func (connector *KubernetesConnector) SetSecurityPodStartupTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultSecurityPodStartupTimeout
	}
	connector.securityPodStartupTimeout = timeout
}

type securityNodeTarget struct {
	name         string
	arch         string
	controlPlane bool
}

// RunSecurityHostBenchmark creates one short-lived Job per node. Each Job is
// pinned to its node and only reads host configuration through read-only
// mounts. hostPID and UID 0 are nevertheless high privileges, so this path is
// exposed as an explicit full scan rather than being hidden behind API mode.
func (connector *KubernetesConnector) RunSecurityHostBenchmark(ctx context.Context, parsed ParsedKubeconfig) ([]SecurityHostBenchmarkOutput, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	clientset, err := connector.clientsetWithTimeout(parsed, 30*time.Second)
	if err != nil {
		return nil, err
	}
	// Node discovery is paginated: a 200-node cluster must not be read as one
	// unbounded response. Only the name and architecture label are retained.
	type nodeTarget struct {
		name, arch   string
		controlPlane bool
	}
	targets := make([]nodeTarget, 0, 256)
	if err := visitNodes(ctx, clientset, func(node *corev1.Node) error {
		arch := node.Labels[corev1.LabelArchStable]
		if arch == "" {
			arch = node.Status.NodeInfo.Architecture
		}
		targets = append(targets, nodeTarget{name: node.Name, arch: arch, controlPlane: isControlPlaneNode(*node)})
		return nil
	}); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("集群中没有可扫描节点")
	}
	nodeNames := make([]string, len(targets))
	for index := range targets {
		nodeNames[index] = targets[index].name
	}
	reportSecurityProgress(ctx, SecurityScanProgress{Phase: "discovering_nodes", Message: fmt.Sprintf("已发现 %d 个节点，正在准备临时扫描 Job。", len(targets)), TotalNodes: len(nodeNames)})
	workers := connector.hostScanConcurrency
	if workers <= 0 {
		workers = defaultHostScanConcurrency
	}
	type result struct {
		output SecurityHostBenchmarkOutput
		err    error
	}
	jobs := make(chan securityNodeTarget)
	results := make(chan result, len(targets))
	var group sync.WaitGroup
	reportSecurityProgress(ctx, SecurityScanProgress{Phase: "creating_jobs", Message: "正在按节点架构选择镜像并创建临时 Job。", TotalNodes: len(targets)})
	for index := 0; index < workers && index < len(targets); index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for node := range jobs {
				data, scanErr := connector.runKubeBenchNodeJob(runCtx, clientset, node.name, node.arch, node.controlPlane)
				results <- result{output: SecurityHostBenchmarkOutput{Node: node.name, Data: data}, err: scanErr}
				if scanErr != nil {
					cancel()
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, target := range targets {
			select {
			case jobs <- securityNodeTarget{name: target.name, arch: target.arch, controlPlane: target.controlPlane}:
			case <-runCtx.Done():
				return
			}
		}
	}()
	go func() { group.Wait(); close(results) }()

	outputs := make([]SecurityHostBenchmarkOutput, 0, len(targets))
	var failures []string
	completed := 0
	for item := range results {
		if item.err != nil {
			failures = append(failures, item.output.Node+": "+safeSecurityError(item.err))
			continue
		}
		outputs = append(outputs, item.output)
		completed++
		reportSecurityProgress(ctx, SecurityScanProgress{Phase: "scanning_nodes", Message: fmt.Sprintf("已完成 %d/%d 个节点的 kube-bench 扫描。", completed, len(targets)), CompletedNodes: completed, TotalNodes: len(targets)})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		if len(failures) > 5 {
			failures = append(failures[:5], fmt.Sprintf("另有 %d 个节点失败", len(failures)-5))
		}
		return nil, errors.New(strings.Join(failures, "；"))
	}
	return outputs, nil
}

func (connector *KubernetesConnector) runKubeBenchNodeJob(ctx context.Context, clientset *kubernetes.Clientset, node, arch string, controlPlane bool) ([]byte, error) {
	namespace := connector.securityScannerNamespace
	if namespace == "" {
		namespace = "kube-system"
	}
	name := "ftc-host-scan-" + shortSecurityHash(node+time.Now().UTC().String())
	labels := map[string]string{
		"app.kubernetes.io/managed-by":   "ftc-platform",
		"ftc.cmbchina.com/security-scan": "host",
		"ftc.cmbchina.com/scan-id":       name,
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		policy := metav1.DeletePropagationBackground
		_ = clientset.BatchV1().Jobs(namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{PropagationPolicy: &policy})
	}
	defer cleanup()

	backoff := int32(0)
	deadline := int64(600)
	ttl := int32(120)
	runAsUser := int64(0)
	readOnly := true
	privileged := true
	automountToken := false
	pullSecrets := []corev1.LocalObjectReference{}
	if connector.securityScannerPullSecret != "" {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: connector.securityScannerPullSecret})
	}
	image := connector.kubeBenchImageForArch(arch)
	directory := corev1.HostPathDirectory
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: batchv1.JobSpec{BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline, TTLSecondsAfterFinished: &ttl, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				NodeName: node, HostPID: true, RestartPolicy: corev1.RestartPolicyNever, ImagePullSecrets: pullSecrets, AutomountServiceAccountToken: &automountToken,
				SecurityContext: &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				Tolerations:     []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				Containers: []corev1.Container{{
					Name: "kube-bench", Image: image, ImagePullPolicy: corev1.PullIfNotPresent,
					Args:            kubeBenchArgs(ctx, controlPlane),
					SecurityContext: &corev1.SecurityContext{RunAsUser: &runAsUser, Privileged: &privileged, ReadOnlyRootFilesystem: &readOnly},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "host-etc", MountPath: "/etc", ReadOnly: true},
						{Name: "host-var", MountPath: "/var", ReadOnly: true},
						{Name: "host-systemd", MountPath: "/lib/systemd", ReadOnly: true},
						{Name: "host-bin", MountPath: "/usr/local/mount-from-host/bin", ReadOnly: true},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "host-etc", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/etc", Type: &directory}}},
					{Name: "host-var", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var", Type: &directory}}},
					{Name: "host-systemd", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/lib/systemd", Type: &directory}}},
					{Name: "host-bin", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/usr/bin", Type: &directory}}},
				},
			},
		}},
	}
	if _, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return nil, mapConnectionError(err)
	}
	startupTimeout := connector.securityPodStartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = defaultSecurityPodStartupTimeout
	}
	reportSecurityProgress(ctx, SecurityScanProgress{Phase: "creating_jobs", Message: fmt.Sprintf("节点 %s：临时 Job 已创建，正在等待扫描 Pod 启动（超时 %s）。", node, startupTimeout.Round(time.Second))})
	return waitForSecurityJob(ctx, clientset, namespace, name, node, "kube-bench", maxHostReportBytes, startupTimeout)
}

func kubeBenchArgs(ctx context.Context, controlPlane bool) []string {
	targets := "node"
	if controlPlane {
		targets = "master,node,etcd,policies"
	}
	args := []string{"run", "--json", "--targets", targets}
	if benchmark := securityBenchmarkFromContext(ctx); benchmark != "" {
		args = append(args, "--benchmark", benchmark)
	}
	return args
}

func isControlPlaneNode(node corev1.Node) bool {
	for label := range node.Labels {
		if label == "node-role.kubernetes.io/control-plane" || label == "node-role.kubernetes.io/master" {
			return true
		}
	}
	return false
}

func (connector *KubernetesConnector) kubeBenchImageForArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x86_64":
		if connector.kubeBenchImageAMD64 != "" {
			return connector.kubeBenchImageAMD64
		}
	case "arm64", "aarch64":
		if connector.kubeBenchImageARM64 != "" {
			return connector.kubeBenchImageARM64
		}
	}
	if connector.kubeBenchImage != "" {
		return connector.kubeBenchImage
	}
	return defaultKubeBenchImage
}

func waitForSecurityJob(ctx context.Context, clientset kubernetes.Interface, namespace, name, node, container string, limit int64, startupTimeout time.Duration) ([]byte, error) {
	if startupTimeout <= 0 {
		startupTimeout = defaultSecurityPodStartupTimeout
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	startupTimer := time.NewTimer(startupTimeout)
	defer startupTimer.Stop()
	startupDone := false
	stopStartupTimer := func() {
		if startupDone {
			return
		}
		startupDone = true
		if !startupTimer.Stop() {
			select {
			case <-startupTimer.C:
			default:
			}
		}
	}
	podRunningReported := false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-startupTimer.C:
			reason := "扫描 Pod 尚未创建"
			pods, listErr := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
			if listErr != nil {
				reason = mapConnectionError(listErr).Error()
			} else if len(pods.Items) > 0 {
				reason = securityPodStartupState(&pods.Items[0])
			}
			return nil, fmt.Errorf("Job %s 的扫描 Pod 在 %s 内未能启动：%s。请检查 kube-bench 镜像拉取、节点调度、hostPath 挂载和 Pod Security/SCC 策略", name, startupTimeout.Round(time.Second), reason)
		case <-ticker.C:
			current, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, mapConnectionError(err)
			}
			if current.Status.Succeeded > 0 {
				stopStartupTimer()
				reportSecurityProgress(ctx, SecurityScanProgress{Phase: "collecting_report", Message: fmt.Sprintf("节点 %s：扫描完成，正在读取报告并回收临时资源。", node)})
				return readNamedSecurityJobLogs(ctx, clientset, namespace, name, container, limit)
			}
			if current.Status.Failed > 0 {
				stopStartupTimer()
				logs, _ := readNamedSecurityJobLogs(ctx, clientset, namespace, name, container, limit)
				return nil, fmt.Errorf("Job %s 执行失败: %s", name, safeScanFailure(logs))
			}
			pods, listErr := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
			if listErr == nil {
				for i := range pods.Items {
					if reason := securityPodStartupFailure(&pods.Items[i]); reason != "" {
						stopStartupTimer()
						return nil, fmt.Errorf("Job %s 启动失败: %s", name, reason)
					}
					if !podRunningReported && (pods.Items[i].Status.Phase == corev1.PodRunning || pods.Items[i].Status.Phase == corev1.PodSucceeded) {
						podRunningReported = true
						stopStartupTimer()
						if pods.Items[i].Status.Phase == corev1.PodRunning {
							reportSecurityProgress(ctx, SecurityScanProgress{Phase: "scanning_nodes", Message: fmt.Sprintf("节点 %s：扫描 Pod 已启动，正在执行 kube-bench。", node)})
						}
					}
				}
			}
		}
	}
}

func securityPodStartupState(pod *corev1.Pod) string {
	parts := make([]string, 0, 3+len(pod.Status.ContainerStatuses))
	phase := strings.TrimSpace(string(pod.Status.Phase))
	if phase == "" {
		phase = "Unknown"
	}
	parts = append(parts, "阶段 "+phase)
	if reason := strings.TrimSpace(pod.Status.Reason); reason != "" {
		parts = append(parts, "原因 "+reason)
	}
	if message := truncateSecurityMessage(pod.Status.Message, 160); message != "" {
		parts = append(parts, "消息 "+message)
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting == nil {
			continue
		}
		waiting := status.State.Waiting
		detail := fmt.Sprintf("容器 %s 等待中 %s", status.Name, waiting.Reason)
		if message := truncateSecurityMessage(waiting.Message, 160); message != "" {
			detail += " - " + message
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "；")
}

func truncateSecurityMessage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}

func securityPodStartupFailure(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting == nil {
			continue
		}
		waiting := status.State.Waiting
		switch waiting.Reason {
		case "ErrImagePull", "ImagePullBackOff", "ErrImageNeverPull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
			message := strings.TrimSpace(waiting.Message)
			if len(message) > 240 {
				message = message[:240]
			}
			if message == "" {
				return waiting.Reason
			}
			return waiting.Reason + " - " + message
		}
	}
	return ""
}

func readNamedSecurityJobLogs(ctx context.Context, clientset kubernetes.Interface, namespace, jobName, container string, limit int64) ([]byte, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil {
		return nil, mapConnectionError(err)
	}
	if len(pods.Items) == 0 {
		return nil, errors.New("扫描 Pod 不存在")
	}
	stream, err := clientset.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: container}).Stream(ctx)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("扫描报告超过大小限制")
	}
	return data, nil
}

func safeScanFailure(value []byte) string {
	text := strings.TrimSpace(string(value))
	if len(text) > 300 {
		text = text[:300]
	}
	if text == "" {
		return "扫描容器未返回日志"
	}
	return text
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func (connector *KubernetesConnector) CleanupSecurityScanResources(ctx context.Context, parsed ParsedKubeconfig) error {
	clientset, err := connector.clientsetWithTimeout(parsed, 30*time.Second)
	if err != nil {
		return err
	}
	selector := "app.kubernetes.io/managed-by=ftc-platform,ftc.cmbchina.com/security-scan"
	namespace := connector.securityScannerNamespace
	if namespace == "" {
		namespace = "kube-system"
	}
	policy := metav1.DeletePropagationBackground
	_ = clientset.BatchV1().Jobs(namespace).DeleteCollection(ctx, metav1.DeleteOptions{PropagationPolicy: &policy}, metav1.ListOptions{LabelSelector: selector})
	accounts, _ := clientset.CoreV1().ServiceAccounts(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	for i := range accounts.Items {
		_ = clientset.CoreV1().ServiceAccounts(namespace).Delete(ctx, accounts.Items[i].Name, metav1.DeleteOptions{})
	}
	roles, _ := clientset.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{LabelSelector: selector})
	for i := range roles.Items {
		_ = clientset.RbacV1().ClusterRoles().Delete(ctx, roles.Items[i].Name, metav1.DeleteOptions{})
	}
	bindings, _ := clientset.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{LabelSelector: selector})
	for i := range bindings.Items {
		_ = clientset.RbacV1().ClusterRoleBindings().Delete(ctx, bindings.Items[i].Name, metav1.DeleteOptions{})
	}
	return nil
}

func shortSecurityHash(value string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return fmt.Sprintf("%x", hash.Sum64())[:12]
}

var kubeBenchControlPattern = regexp.MustCompile(`^\d+(?:\.\d+)+$`)

type kubeBenchAggregate struct {
	id, text, remediation string
	states                map[string]bool
	nodes                 map[string]struct{}
}

// kube-bench may print a human-readable warning before the JSON document, for
// example when it cannot auto-detect the Kubernetes version through kubectl
// (the scan Job deliberately does not mount a service-account token). Extract
// only the JSON object so the warning does not turn a successful scan into a
// parse failure.
func kubeBenchJSONPayload(data []byte) []byte {
	start := bytes.Index(data, []byte(`{"Controls"`))
	if start < 0 {
		start = bytes.IndexByte(data, '{')
	}
	end := bytes.LastIndexByte(data, '}')
	if start < 0 || end <= start {
		return nil
	}
	return data[start : end+1]
}

func parseKubeBenchReports(outputs []SecurityHostBenchmarkOutput) (SecurityBaselineReport, error) {
	controls := map[string]*kubeBenchAggregate{}
	for _, output := range outputs {
		payload := kubeBenchJSONPayload(output.Data)
		if len(payload) == 0 {
			return SecurityBaselineReport{}, fmt.Errorf("节点 %s 未返回 kube-bench JSON 报告", output.Node)
		}
		var root any
		if err := json.Unmarshal(payload, &root); err != nil {
			return SecurityBaselineReport{}, fmt.Errorf("节点 %s 返回无效 kube-bench JSON: %v", output.Node, err)
		}
		walkKubeBench(root, output.Node, controls)
	}
	if len(controls) == 0 {
		return SecurityBaselineReport{}, errors.New("kube-bench 报告中没有控制项")
	}
	report := SecurityBaselineReport{Mode: "job-kube-bench", ScannedAt: time.Now().UTC(), Coverage: SecurityBaselineCoverage{Included: []string{"节点文件与目录权限", "kubelet 启动参数与配置", "控制面 API Server、etcd、scheduler 与 controller-manager 参数（组件所在节点可访问时）"}, NotCovered: []string{"托管控制面或未暴露宿主机配置的节点"}}}
	ids := make([]string, 0, len(controls))
	for id := range controls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		item := controls[id]
		status := "PASS"
		severity := "low"
		if item.states["fail"] {
			status, severity = "FAIL", "high"
		} else if item.states["warn"] || item.states["info"] {
			status, severity = "WARN", "medium"
		}
		nodes := sortedSecurityKeys(item.nodes)
		affected := len(nodes)
		if len(nodes) > securityFindingSampleLimit {
			nodes = nodes[:securityFindingSampleLimit]
			report.TruncatedSamples = true
		}
		title := item.text
		if title == "" {
			title = "CIS Kubernetes 检查 " + id
		}
		remediation := item.remediation
		if remediation == "" {
			remediation = "依据 CIS Kubernetes Benchmark 和集群发行版文档完成整改，并通过变更流程验证。"
		}
		report.Findings = append(report.Findings, SecurityBaselineFinding{ControlID: "CIS-" + id, Status: status, Severity: severity, Category: "CIS 节点基线", Title: title, Affected: affected, Resources: nodes, Evidence: fmt.Sprintf("kube-bench 在 %d 个节点返回 %s。", affected, status), Remediation: remediation})
	}
	recomputeSecuritySummary(&report)
	return report, nil
}

func walkKubeBench(value any, node string, controls map[string]*kubeBenchAggregate) {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			walkKubeBench(child, node, controls)
		}
	case map[string]any:
		id := firstString(item, "test_number", "id", "testNumber")
		state := strings.ToLower(firstString(item, "status", "state"))
		if kubeBenchControlPattern.MatchString(id) && state != "" {
			entry := controls[id]
			if entry == nil {
				entry = &kubeBenchAggregate{id: id, states: map[string]bool{}, nodes: map[string]struct{}{}}
				controls[id] = entry
			}
			if entry.text == "" {
				entry.text = firstString(item, "test_desc", "text", "description")
			}
			if entry.remediation == "" {
				entry.remediation = firstString(item, "remediation")
			}
			entry.states[state] = true
			if state != "pass" {
				entry.nodes["Node/"+node] = struct{}{}
			}
		}
		for _, child := range item {
			walkKubeBench(child, node, controls)
		}
	}
}

func mergeSecurityReports(reports ...SecurityBaselineReport) SecurityBaselineReport {
	merged := SecurityBaselineReport{Mode: "job-full-cis", ScannedAt: time.Now().UTC()}
	for _, report := range reports {
		merged.Namespaces = max(merged.Namespaces, report.Namespaces)
		merged.Pods = max(merged.Pods, report.Pods)
		merged.TruncatedSamples = merged.TruncatedSamples || report.TruncatedSamples
		merged.Coverage.Included = append(merged.Coverage.Included, report.Coverage.Included...)
		merged.Coverage.NotCovered = append(merged.Coverage.NotCovered, report.Coverage.NotCovered...)
		merged.Findings = append(merged.Findings, report.Findings...)
	}
	merged.Coverage.Included = uniqueSecurityStrings(merged.Coverage.Included)
	merged.Coverage.NotCovered = uniqueSecurityStrings(merged.Coverage.NotCovered)
	recomputeSecuritySummary(&merged)
	return merged
}

func recomputeSecuritySummary(report *SecurityBaselineReport) {
	report.Summary = SecurityBaselineSummary{}
	for _, finding := range report.Findings {
		report.Summary.Controls++
		switch finding.Status {
		case "PASS":
			report.Summary.Passed++
		case "FAIL":
			report.Summary.Failed++
		case "WHITELISTED":
			// A whitelisted control is an accepted pass. The whitelist count is
			// kept as a subset so the UI can show both "effective pass" and
			// "explicitly exempted".
			report.Summary.Passed++
			report.Summary.Whitelisted++
		default:
			report.Summary.Warnings++
		}
	}
	if report.Summary.Controls > 0 {
		report.Summary.Score = report.Summary.Passed * 100 / report.Summary.Controls
	}
}

func uniqueSecurityStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok || value == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
