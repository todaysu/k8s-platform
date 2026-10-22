package setup

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	{"ingress", "创建 HTTPS Ingress"},
	{"runtime", "部署平台实例"},
	{"verify", "健康检查"},
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
	server.install = newInstallStatus()
	server.installRunning = true
	server.installMu.Unlock()
	go func() {
		defer func() {
			server.installMu.Lock()
			server.installRunning = false
			server.install.UpdatedAt = time.Now().UTC()
			server.installMu.Unlock()
		}()
		server.runInstall(request)
	}()
	return nil
}

func (server *Server) runInstall(request InstallRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	fail := func(step string, err error) {
		server.installMu.Lock()
		for index := range server.install.Steps {
			if server.install.Steps[index].Key == step {
				server.install.Steps[index].Status = "failed"
				server.install.Steps[index].Message = err.Error()
			}
		}
		server.install.State = "failed"
		server.install.Error = err.Error()
		server.install.UpdatedAt = time.Now().UTC()
		server.installMu.Unlock()
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
	ingressClass := strings.TrimSpace(request.IngressClass)
	if ingressClass == "" {
		for _, class := range preflight.IngressClasses {
			if class.Default {
				ingressClass = class.Name
				break
			}
		}
		if ingressClass == "" && len(preflight.IngressClasses) > 0 {
			ingressClass = preflight.IngressClasses[0].Name
		}
	}
	if ingressClass == "" {
		fail("preflight", fmt.Errorf("没有可用的 IngressClass"))
		return
	}
	foundClass := false
	for _, class := range preflight.IngressClasses {
		if class.Name == ingressClass {
			foundClass = true
			break
		}
	}
	if !foundClass {
		fail("preflight", fmt.Errorf("IngressClass %s 不存在", ingressClass))
		return
	}
	domain, err := ValidateInternalDomain(request.Domain)
	if err != nil {
		fail("preflight", err)
		return
	}
	step("preflight", "done", "集群能力检查通过")

	step("database", "running", "正在连接外部 MySQL、创建表结构和管理员")
	dsn, err := externalDatabaseDSN(request.ExternalDatabase)
	if err != nil {
		fail("database", err)
		return
	}
	if err := pingDatabase(ctx, dsn); err != nil {
		fail("database", fmt.Errorf("无法连接外部 MySQL：%w", err))
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

	step("ingress", "running", "正在创建自签 CA、TLS Secret 和 Ingress")
	if err := server.ApplyIngress(ctx, IngressPlan{Namespace: namespace, Host: domain, IngressClass: ingressClass}); err != nil {
		fail("ingress", err)
		return
	}
	caCertificate := ""
	if secret, err := server.client.CoreV1().Secrets(namespace).Get(ctx, platformCASecret, metav1.GetOptions{}); err == nil {
		caCertificate = string(secret.Data["ca.crt"])
	}
	step("ingress", "done", "HTTPS Ingress 已创建")

	step("runtime", "running", "正在部署 API、Collector 和 Worker")
	platformImage, err := server.resolvePlatformImage(ctx, request.PlatformImage)
	if err != nil {
		fail("runtime", err)
		return
	}
	runtimePlan := RuntimePlan{
		Namespace:         namespace,
		PlatformImage:     platformImage,
		ImagePullSecret:   strings.TrimSpace(request.PlatformImagePullSecret),
		APIReplicas:       request.APIReplicas,
		CollectorReplicas: request.CollectorReplicas,
		WorkerReplicas:    request.WorkerReplicas,
	}
	if runtimePlan.APIReplicas <= 0 {
		runtimePlan.APIReplicas = 1
	}
	if runtimePlan.CollectorReplicas <= 0 {
		runtimePlan.CollectorReplicas = 1
	}
	if runtimePlan.WorkerReplicas <= 0 {
		runtimePlan.WorkerReplicas = 1
	}
	if err := server.ApplyRuntime(ctx, runtimePlan); err != nil {
		fail("runtime", err)
		return
	}
	step("runtime", "done", "API/Collector/Worker 已提交")

	step("verify", "running", "正在等待平台 API 就绪")
	if err := server.WaitForDeployment(ctx, namespace, "ftc-platform", 8*time.Minute); err != nil {
		fail("verify", err)
		return
	}
	if err := waitForHTTPHealth(ctx, fmt.Sprintf("http://ftc-platform.%s.svc/api/v1/health", namespace), 3*time.Minute); err != nil {
		fail("verify", err)
		return
	}
	step("verify", "done", "平台 API 健康检查通过")

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
	server.install.URL = "https://" + domain
	server.install.CAURL = "/api/v1/setup/ca"
	server.install.CACert = caCertificate
	server.install.Hosts = hosts
	server.install.UpdatedAt = time.Now().UTC()
	server.installMu.Unlock()
	go server.revokeSetupPermissions()
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
	writeJSON(writer, http.StatusOK, server.installSnapshot())
}
