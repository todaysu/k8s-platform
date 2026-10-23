package setup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"example.com/k8s-platform/internal/store"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExternalDatabase is an existing MySQL 8.0+ instance. The setup controller
// does not create the database or user; it only verifies connectivity, runs
// migrations and stores the DSN for the runtime.
type ExternalDatabase struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLS      string `json:"tls"`
}

type InstallRequest struct {
	Token                   string           `json:"token"`
	Domain                  string           `json:"domain"`
	NodePort                int32            `json:"node_port"`
	IngressClass            string           `json:"ingress_class"`
	ExternalDatabase        ExternalDatabase `json:"external_database"`
	AdminUsername           string           `json:"admin_username"`
	AdminPassword           string           `json:"admin_password"`
	PlatformImage           string           `json:"platform_image"`
	PlatformImagePullSecret string           `json:"platform_image_pull_secret"`
	APIReplicas             int32            `json:"api_replicas"`
	CollectorReplicas       int32            `json:"collector_replicas"`
	WorkerReplicas          int32            `json:"worker_replicas"`
}

type InstallStep struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type InstallStatus struct {
	State              string        `json:"state"`
	Steps              []InstallStep `json:"steps"`
	Error              string        `json:"error,omitempty"`
	URL                string        `json:"url,omitempty"`
	Domain             string        `json:"domain,omitempty"`
	NodePort           int32         `json:"node_port,omitempty"`
	AdminUsername      string        `json:"admin_username,omitempty"`
	CAURL              string        `json:"ca_url,omitempty"`
	CACert             string        `json:"ca_cert,omitempty"`
	Hosts              []string      `json:"hosts,omitempty"`
	PermissionsRevoked bool          `json:"permissions_revoked,omitempty"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

var installStepDefinitions = []struct {
	key   string
	label string
}{
	{"preflight", "环境预检"},
	{"database", "连接外部 MySQL 并初始化"},
	{"tls", "生成 HTTPS 证书"},
	{"runtime", "部署平台实例"},
	{"verify", "验证平台服务"},
}

func newInstallStatus() InstallStatus {
	steps := make([]InstallStep, 0, len(installStepDefinitions))
	for _, definition := range installStepDefinitions {
		steps = append(steps, InstallStep{Key: definition.key, Label: definition.label, Status: "pending"})
	}
	return InstallStatus{State: "running", Steps: steps, UpdatedAt: time.Now().UTC()}
}

func (server *Server) StartInstall(request InstallRequest) error {
	server.installMu.Lock()
	if server.installRunning {
		server.installMu.Unlock()
		return fmt.Errorf("安装正在进行中，请等待当前任务结束")
	}
	server.installRunning = true
	server.installMu.Unlock()
	release, lost, err := server.acquireInstallLease(context.Background())
	if err != nil {
		server.installMu.Lock()
		server.installRunning = false
		server.installMu.Unlock()
		return err
	}
	server.installMu.Lock()
	server.install = newInstallStatus()
	server.install.Domain = strings.ToLower(strings.TrimSpace(request.Domain))
	server.install.AdminUsername = request.AdminUsername
	if request.NodePort == 0 {
		server.install.NodePort = 30443
	} else {
		server.install.NodePort = request.NodePort
	}
	server.installMu.Unlock()
	if err := server.saveInstallState(context.Background()); err != nil {
		release()
		server.installMu.Lock()
		server.installRunning = false
		server.installMu.Unlock()
		return fmt.Errorf("保存安装进度失败：%w", err)
	}
	go func() {
		defer release()
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-lost:
				cancel()
			case <-done:
			}
		}()
		defer func() {
			server.installMu.Lock()
			server.installRunning = false
			server.install.UpdatedAt = time.Now().UTC()
			server.installMu.Unlock()
		}()
		server.runInstall(parent, request)
	}()
	return nil
}

func (server *Server) runInstall(parent context.Context, request InstallRequest) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Minute)
	defer cancel()
	var dsn string
	fail := func(step string, err error) {
		message := err.Error()
		for _, secret := range []string{request.ExternalDatabase.Password, request.AdminPassword, dsn} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[已隐藏]")
			}
		}
		server.installMu.Lock()
		for index := range server.install.Steps {
			if server.install.Steps[index].Key == step {
				server.install.Steps[index].Status = "failed"
				server.install.Steps[index].Message = message
			}
		}
		server.install.State = "failed"
		server.install.Error = message
		server.install.UpdatedAt = time.Now().UTC()
		server.installMu.Unlock()
		_ = server.saveInstallState(context.Background())
	}
	step := func(key, status, message string) {
		server.installMu.Lock()
		for index := range server.install.Steps {
			if server.install.Steps[index].Key == key {
				server.install.Steps[index].Status = status
				server.install.Steps[index].Message = message
			}
		}
		server.install.UpdatedAt = time.Now().UTC()
		server.installMu.Unlock()
		_ = server.saveInstallState(context.Background())
	}

	namespace := server.config.Namespace
	if namespace == "" {
		namespace = "ftc-platform"
	}
	step("preflight", "running", "正在检查集群能力")
	preflight := server.preflight(ctx)
	if len(preflight.Errors) > 0 {
		fail("preflight", fmt.Errorf("%s", strings.Join(preflight.Errors, "；")))
		return
	}
	domain, err := ValidateInternalDomain(request.Domain)
	if err != nil {
		fail("preflight", err)
		return
	}
	if request.NodePort == 0 {
		request.NodePort = 30443
	}
	if request.NodePort < 30000 || request.NodePort > 32767 {
		fail("preflight", fmt.Errorf("HTTPS NodePort 必须在 30000–32767 之间"))
		return
	}
	step("preflight", "done", "节点与访问配置检查通过")

	step("database", "running", "正在连接外部 MySQL、创建表结构和管理员")
	dsn, err = externalDatabaseDSN(request.ExternalDatabase)
	if err != nil {
		fail("database", err)
		return
	}
	if err := pingDatabase(ctx, dsn); err != nil {
		fail("database", fmt.Errorf("无法连接外部 MySQL：%w", err))
		return
	}
	if err := verifyExistingShardMode(ctx, dsn); err != nil {
		fail("database", err)
		return
	}
	adminHash, err := provisionAdmin(ctx, dsn, request.AdminUsername, request.AdminPassword, server.config.SecureCookies, server.config.SessionTTL)
	if err != nil {
		fail("database", err)
		return
	}
	if err := server.writeRuntimeSecret(ctx, dsn, request.AdminUsername, adminHash); err != nil {
		fail("database", err)
		return
	}
	step("database", "done", "外部 MySQL 已连接，表结构和管理员已就绪")

	step("tls", "running", "正在生成并保存自签证书")
	if _, err := server.EnsureTLS(ctx, platformCASecret, platformTLSCert, domain, nil); err != nil {
		fail("tls", err)
		return
	}
	ca, err := server.client.CoreV1().Secrets(namespace).Get(ctx, platformCASecret, metav1.GetOptions{})
	if err != nil {
		fail("tls", err)
		return
	}
	caCertificate := string(ca.Data["ca.crt"])
	step("tls", "done", "HTTPS 证书已保存")

	step("runtime", "running", "正在部署 API、Collector 和 Worker")
	platformImage, pullSecrets, err := server.ownImage(ctx)
	if err != nil {
		fail("runtime", err)
		return
	}
	runtimePlan := RuntimePlan{
		Namespace:         namespace,
		PlatformImage:     platformImage,
		ImagePullSecrets:  pullSecrets,
		Domain:            domain,
		NodePort:          request.NodePort,
		APIReplicas:       request.APIReplicas,
		CollectorReplicas: request.CollectorReplicas,
		WorkerReplicas:    request.WorkerReplicas,
	}
	if runtimePlan.APIReplicas <= 0 {
		runtimePlan.APIReplicas = 2
	}
	if runtimePlan.CollectorReplicas <= 0 {
		runtimePlan.CollectorReplicas = 2
	}
	if runtimePlan.WorkerReplicas <= 0 {
		runtimePlan.WorkerReplicas = 1
	}
	if err := server.ApplyRuntime(ctx, runtimePlan); err != nil {
		fail("runtime", err)
		return
	}
	step("runtime", "done", "API/Collector/Worker 已提交")

	step("verify", "running", "正在等待 API、采集器和任务实例就绪")
	for _, name := range []string{"ftc-platform", "ftc-platform-collector", "ftc-platform-worker"} {
		if err := server.WaitForDeployment(ctx, namespace, name, 8*time.Minute); err != nil {
			fail("verify", err)
			return
		}
	}
	if err := waitForShards(ctx, dsn, 16, 3*time.Minute); err != nil {
		fail("verify", err)
		return
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.Data["ca.crt"]) {
		fail("verify", fmt.Errorf("平台 CA 证书无效"))
		return
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: domain, MinVersion: tls.VersionTLS12}}}
	if err := waitForClientHealth(ctx, client, fmt.Sprintf("https://ftc-platform.%s.svc/api/v1/health", namespace), 3*time.Minute); err != nil {
		fail("verify", err)
		return
	}
	step("verify", "done", "平台服务与 HTTPS 验证通过")

	hosts := make([]string, 0)
	seen := map[string]bool{}
	for _, node := range preflight.Nodes {
		for _, address := range node.InternalIPs {
			if !seen[address] {
				seen[address] = true
				hosts = append(hosts, address)
			}
		}
	}
	server.installMu.Lock()
	server.install.State = "completed"
	server.install.URL = fmt.Sprintf("https://%s:%d", domain, request.NodePort)
	server.install.Domain = domain
	server.install.NodePort = request.NodePort
	server.install.CAURL = "/api/v1/setup/ca"
	server.install.CACert = caCertificate
	server.install.Hosts = hosts
	server.install.UpdatedAt = time.Now().UTC()
	server.installMu.Unlock()
	_ = server.saveInstallState(context.Background())

}

// revokeSetupPermissions removes the setup identity's bindings after the
// runtime has been verified. The setup Pod stays alive for the result page,
// but it can no longer mutate the cluster; the bindings are restored only by
// re-applying the bootstrap manifest.
func (server *Server) revokeSetupPermissions() {
	time.Sleep(30 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	namespace := server.config.Namespace
	if namespace == "" {
		namespace = "ftc-platform"
	}
	roleErr := server.client.RbacV1().RoleBindings(namespace).Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{})
	clusterErr := server.client.RbacV1().ClusterRoleBindings().Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{})
	if roleErr == nil && clusterErr == nil {
		server.installMu.Lock()
		server.install.PermissionsRevoked = true
		server.install.UpdatedAt = time.Now().UTC()
		server.installMu.Unlock()
	}
}

func (server *Server) installSnapshot() InstallStatus {
	server.installMu.Lock()
	defer server.installMu.Unlock()
	snapshot := server.install
	snapshot.Steps = append([]InstallStep(nil), server.install.Steps...)
	if snapshot.State == "" {
		snapshot.State = "idle"
	}
	return snapshot
}

func (server *Server) resolvePlatformImage(ctx context.Context, requested string) (string, error) {
	image := strings.TrimSpace(requested)
	if image != "" && !strings.Contains(image, "REPLACE_WITH") {
		return image, nil
	}
	if server.client == nil {
		return "", fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	config, err := server.client.CoreV1().ConfigMaps(server.config.Namespace).Get(ctx, "ftc-platform-install", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("读取安装 ConfigMap 失败：%w", err)
	}
	image = strings.TrimSpace(config.Data["PLATFORM_IMAGE"])
	if image == "" || strings.Contains(image, "REPLACE_WITH") {
		return "", fmt.Errorf("平台运行镜像未配置；请在向导中填写或在 ConfigMap 中设置 PLATFORM_IMAGE")
	}
	return image, nil
}

func (server *Server) ownImage(ctx context.Context) (string, []corev1.LocalObjectReference, error) {
	name := os.Getenv("POD_NAME")
	if name == "" {
		return "", nil, fmt.Errorf("无法读取初始化 Pod 名称")
	}
	pod, err := server.client.CoreV1().Pods(server.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("读取初始化 Pod 镜像失败：%w", err)
	}
	if len(pod.Spec.Containers) == 0 || pod.Spec.Containers[0].Image == "" || strings.Contains(pod.Spec.Containers[0].Image, "REPLACE_WITH") {
		return "", nil, fmt.Errorf("请在安装清单中配置平台镜像")
	}
	return pod.Spec.Containers[0].Image, pod.Spec.ImagePullSecrets, nil
}

func verifyExistingShardMode(ctx context.Context, dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='collection_shard_config'").Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil
	}
	var count int
	err = db.QueryRowContext(ctx, "SELECT shard_count FROM collection_shard_config WHERE id=1").Scan(&count)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取已有采集分片配置失败：%w", err)
	}
	if count != 16 {
		return fmt.Errorf("现有数据库采集分片数为 %d，安装配置要求 16；请先迁移数据库，安装器不会清空已有分片", count)
	}
	return nil
}

func waitForClientHealth(ctx context.Context, client *http.Client, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("HTTPS 健康检查超时：%v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func waitForShards(ctx context.Context, dsn string, expected int, timeout time.Duration) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	deadline := time.Now().Add(timeout)
	for {
		owned, err := store.CountActiveCollectionShards(ctx, db, false)
		if err == nil && owned == expected {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("采集分片未全部接管：已接管 %d/%d：%v", owned, expected, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func validAdminCredentials(username, password string) bool {
	return adminUsernamePattern.MatchString(username) && len(password) >= 12 && len(password) <= 72
}

func (server *Server) handleInstall(writer http.ResponseWriter, request *http.Request) {
	var payload InstallRequest
	if !decode(writer, request, &payload) {
		return
	}
	if !server.validToken(writer, request, payload.Token) {
		return
	}
	if !validAdminCredentials(payload.AdminUsername, payload.AdminPassword) {
		writeError(writer, http.StatusBadRequest, "INVALID_ADMIN_CREDENTIALS", "管理员用户名或密码格式无效。")
		return
	}
	if _, err := ValidateInternalDomain(payload.Domain); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_DOMAIN", err.Error())
		return
	}
	if _, err := externalDatabaseDSN(payload.ExternalDatabase); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_DATABASE", err.Error())
		return
	}
	server.installMu.Lock()
	running := server.installRunning
	server.installMu.Unlock()
	if !running {
		if err := server.loadInstallState(request.Context()); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "SETUP_STORAGE_FAILED", err.Error())
			return
		}
	}
	exists, err := server.runtimeSecretExists(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "SETUP_STORAGE_FAILED", "无法检查初始化状态。")
		return
	}
	if exists {
		server.installMu.Lock()
		canResume := server.install.State == "failed"
		server.installMu.Unlock()
		if !canResume {
			writeError(writer, http.StatusConflict, "ALREADY_INITIALIZED", "平台已经初始化，请勿重复安装。")
			return
		}
	}
	if err := server.StartInstall(payload); err != nil {
		writeError(writer, http.StatusConflict, "INSTALL_IN_PROGRESS", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, server.installSnapshot())
}

func (server *Server) handleInstallStatus(writer http.ResponseWriter, request *http.Request) {
	if !server.validToken(writer, request, request.Header.Get("X-Setup-Token")) {
		return
	}
	server.installMu.Lock()
	running := server.installRunning
	server.installMu.Unlock()
	if !running {
		_ = server.loadInstallState(request.Context())
	}
	writeJSON(writer, http.StatusOK, server.installSnapshot())
}

func (server *Server) handleFinish(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Token     string `json:"token"`
		Confirmed bool   `json:"confirmed"`
	}
	if !decode(writer, request, &payload) {
		return
	}
	if !server.validToken(writer, request, payload.Token) {
		return
	}
	if !payload.Confirmed {
		writeError(writer, http.StatusBadRequest, "CONFIRM_REQUIRED", "请先确认已能登录正式平台。")
		return
	}
	status := server.installSnapshot()
	if status.State != "completed" {
		writeError(writer, http.StatusConflict, "NOT_READY", "平台尚未通过安装验证。")
		return
	}
	if status.PermissionsRevoked {
		writeJSON(writer, http.StatusOK, status)
		return
	}
	server.installMu.Lock()
	server.install.PermissionsRevoked = true
	server.install.UpdatedAt = time.Now().UTC()
	server.installMu.Unlock()
	if err := server.saveInstallState(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "SAVE_FAILED", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, server.installSnapshot())
	go server.removeSetupResources()
}

func (server *Server) removeSetupResources() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	namespace := server.config.Namespace
	ignoreMissing := func(err error) bool { return err == nil || apierrors.IsNotFound(err) }
	// The response is sent before deleting this Pod's Deployment.
	if err := server.client.CoreV1().Services(namespace).Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{}); !ignoreMissing(err) {
		log.Printf("setup cleanup: service deletion failed: %v", err)
		return
	}
	if err := server.client.AppsV1().Deployments(namespace).Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{}); !ignoreMissing(err) {
		log.Printf("setup cleanup: deployment deletion failed: %v", err)
		return
	}
	if err := server.client.RbacV1().RoleBindings(namespace).Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{}); !ignoreMissing(err) {
		log.Printf("setup cleanup: role binding deletion failed: %v", err)
		return
	}
	if err := server.client.RbacV1().ClusterRoleBindings().Delete(ctx, "ftc-platform-setup", metav1.DeleteOptions{}); !ignoreMissing(err) {
		log.Printf("setup cleanup: cluster role binding deletion failed: %v", err)
	}
}
