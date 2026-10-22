package setup

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/k8s-platform/internal/auth"
	"example.com/k8s-platform/internal/store"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var adminUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)

type Config struct {
	Token         string
	Namespace     string
	RuntimeSecret string
	DefaultDBHost string
	DefaultDBName string
	SecureCookies bool
	SessionTTL    time.Duration
	Restart       func()
}

type Server struct {
	config  Config
	client  kubernetes.Interface
	dynamic dynamic.Interface
	restart func()

	mu       sync.Mutex
	failures map[string]setupFailure
	// startedAt bounds the lifetime of the failure table. Initialization is a
	// one-shot flow, so the table is discarded when the server has been up long
	// enough; a long-running instance must not accumulate caller addresses.
	startedAt time.Time

	installMu      sync.Mutex
	install        InstallStatus
	installRunning bool
}

type setupFailure struct {
	count int
	until time.Time
}

const (
	setupFailureWindow     = 15 * time.Minute
	setupFailuresPerSource = 20
	// setupFailureTableTTL caps how long a caller address is remembered.
	setupFailureTableTTL = 6 * time.Hour
	maxSetupFailures     = 512
)

// limited throttles repeated setup-token guesses. Every rejected attempt is
// recorded, so a distributed or flood attempt cannot escape accounting.
func (server *Server) limited(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	now := time.Now()
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.failures == nil {
		server.failures = map[string]setupFailure{}
		server.startedAt = now
	}
	if now.Sub(server.startedAt) > setupFailureTableTTL {
		server.failures = map[string]setupFailure{}
		server.startedAt = now
	}
	for key, value := range server.failures {
		if now.After(value.until) {
			delete(server.failures, key)
		}
	}
	entry := server.failures[host]
	if entry.count == 0 || now.After(entry.until) {
		entry = setupFailure{until: now.Add(setupFailureWindow)}
	}
	entry.count++
	server.failures[host] = entry
	if len(server.failures) > maxSetupFailures {
		// Evict the oldest window instead of rejecting every later caller.
		oldestKey, oldestUntil := "", time.Time{}
		for key, value := range server.failures {
			if oldestKey == "" || value.until.Before(oldestUntil) {
				oldestKey, oldestUntil = key, value.until
			}
		}
		delete(server.failures, oldestKey)
	}
	return entry.count > setupFailuresPerSource
}

type connectionRequest struct {
	Token    string `json:"token"`
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	TLS      string `json:"tls"`
}

type initializeRequest struct {
	connectionRequest
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
}

// placeholderTokens are the values shipped in deploy manifests. Accepting them
// would make the initialization token a publicly known string, letting anyone
// create the first admin account.
var placeholderTokens = []string{"REPLACE_WITH_SETUP_TOKEN", "CHANGE_ME", "changeme", "please-change-me"}

const minSetupTokenLength = 16

func validateSetupToken(token string) error {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return errors.New("setup token is required")
	}
	for _, placeholder := range placeholderTokens {
		if strings.EqualFold(trimmed, placeholder) {
			return errors.New("setup token still uses the example placeholder; replace it before deploying")
		}
	}
	if len(trimmed) < minSetupTokenLength {
		return fmt.Errorf("setup token must be at least %d characters", minSetupTokenLength)
	}
	return nil
}

func New(config Config) (*Server, error) {
	if err := validateSetupToken(config.Token); err != nil {
		return nil, err
	}
	if config.Namespace == "" {
		config.Namespace = os.Getenv("POD_NAMESPACE")
	}
	if config.RuntimeSecret == "" {
		config.RuntimeSecret = "ftc-platform-runtime"
	}
	if config.DefaultDBHost == "" {
		config.DefaultDBHost = "platform-mysql.ftc-platform.svc"
	}
	if config.DefaultDBName == "" {
		config.DefaultDBName = "ftc_platform"
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 8 * time.Hour
	}
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("setup mode requires running inside Kubernetes: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	restart := config.Restart
	if restart == nil {
		restart = func() {
			time.Sleep(2 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// Environment variables are fixed for the lifetime of a Pod. New
			// Pods are required so the runtime Secret becomes visible.
			pods, err := client.CoreV1().Pods(config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=ftc-platform"})
			if err != nil {
				log.Printf("setup: list platform pods: %v", err)
			} else {
				for _, pod := range pods.Items {
					if err := client.CoreV1().Pods(config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
						log.Printf("setup: delete pod %s: %v", pod.Name, err)
					}
				}
			}
			if podName := os.Getenv("POD_NAME"); podName != "" {
				_ = client.CoreV1().Pods(config.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
			}
		}
	}
	return &Server{config: config, client: client, dynamic: dynamicClient, restart: restart}, nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/health":
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/setup/status":
		server.status(writer)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/setup/preflight":
		server.handlePreflight(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/setup/install":
		server.handleInstall(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/setup/install":
		server.handleInstallStatus(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/setup/ca":
		server.handleCA(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/setup/test":
		server.test(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/setup/initialize":
		server.initialize(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "SETUP_ROUTE_NOT_FOUND", "未知的初始化接口。")
	}
}

func (server *Server) status(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"setup_required":   true,
		"default_host":     server.config.DefaultDBHost,
		"default_database": server.config.DefaultDBName,
		"default_port":     "3306",
		"default_user":     "ftc",
	})
}

func (server *Server) handlePreflight(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Token string `json:"token"`
	}
	if !decode(writer, request, &payload) {
		return
	}
	if !server.validToken(writer, request, payload.Token) {
		return
	}
	writeJSON(writer, http.StatusOK, server.preflightWithTimeout(request.Context()))
}

func (server *Server) test(writer http.ResponseWriter, request *http.Request) {
	var payload connectionRequest
	if !decode(writer, request, &payload) {
		return
	}
	if !server.validToken(writer, request, payload.Token) {
		return
	}
	dsn, ok := server.connectionDSN(writer, payload)
	if !ok {
		return
	}
	if err := pingDatabase(request.Context(), dsn); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "DATABASE_CONNECTION_FAILED", "无法连接数据库，请检查地址、账号、密码和网络。")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (server *Server) initialize(writer http.ResponseWriter, request *http.Request) {
	var payload initializeRequest
	if !decode(writer, request, &payload) {
		return
	}
	if !server.validToken(writer, request, payload.Token) {
		return
	}
	if !adminUsernamePattern.MatchString(payload.AdminUsername) || len(payload.AdminPassword) < 12 || len(payload.AdminPassword) > 72 {
		writeError(writer, http.StatusBadRequest, "INVALID_ADMIN_CREDENTIALS", "管理员用户名或密码格式无效。")
		return
	}
	exists, err := server.runtimeSecretExists(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "SETUP_STORAGE_FAILED", "无法检查初始化状态。")
		return
	}
	if exists {
		writeError(writer, http.StatusConflict, "ALREADY_INITIALIZED", "平台已经初始化，请等待 Pod 重启后登录。")
		return
	}
	dsn, ok := server.connectionDSN(writer, connectionRequest(payload.connectionRequest))
	if !ok {
		return
	}
	if err := pingDatabase(request.Context(), dsn); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "DATABASE_CONNECTION_FAILED", "无法连接数据库，请检查地址、账号、密码和网络。")
		return
	}
	database, err := store.OpenMySQL(dsn)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "DATABASE_MIGRATION_FAILED", "数据库连接成功，但初始化表结构失败；请确认账号具有建表权限。")
		return
	}
	defer database.Close()
	var userCount int
	if err = database.SQLDB().QueryRowContext(request.Context(), `SELECT COUNT(*) FROM auth_users`).Scan(&userCount); err != nil {
		writeError(writer, http.StatusInternalServerError, "SETUP_STORAGE_FAILED", "无法检查管理员账号状态。")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(payload.AdminPassword), 12)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "AUTH_FAILED", "管理员密码处理失败。")
		return
	}
	adminHash := string(hash)
	if userCount > 0 {
		// Recover a retry where the database was initialized but the runtime
		// Secret write failed: allow the same admin credentials to finish.
		var existing string
		if err := database.SQLDB().QueryRowContext(request.Context(), `SELECT password_hash FROM auth_users WHERE username=?`, payload.AdminUsername).Scan(&existing); err != nil || bcrypt.CompareHashAndPassword([]byte(existing), []byte(payload.AdminPassword)) != nil {
			writeError(writer, http.StatusConflict, "ALREADY_INITIALIZED", "数据库中已经存在管理员账号，请恢复平台运行配置后登录。")
			return
		}
		adminHash = existing
	} else {
		authentication, err := auth.NewWithDB(database.SQLDB(), "", payload.AdminUsername, adminHash, server.config.SecureCookies, server.config.SessionTTL)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "AUTH_FAILED", "管理员账号创建失败。")
			return
		}
		_ = authentication.Close()
	}
	if err := server.writeRuntimeSecret(request.Context(), dsn, payload.AdminUsername, adminHash); err != nil {
		writeError(writer, http.StatusInternalServerError, "SETUP_STORAGE_FAILED", "数据库已初始化，但保存连接配置失败；请检查平台 Secret 权限。")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"restarting": true})
	go server.restart()
}

func (server *Server) validToken(writer http.ResponseWriter, request *http.Request, token string) bool {
	if subtle.ConstantTimeCompare([]byte(token), []byte(server.config.Token)) != 1 {
		if server.limited(request) {
			writeError(writer, http.StatusTooManyRequests, "SETUP_RATE_LIMITED", "尝试过于频繁，请稍后再试。")
			return false
		}
		writeError(writer, http.StatusForbidden, "SETUP_TOKEN_INVALID", "初始化凭证无效。")
		return false
	}
	return true
}

func (server *Server) connectionDSN(writer http.ResponseWriter, payload connectionRequest) (string, bool) {
	if payload.Mode != "" && payload.Mode != "in-cluster" && payload.Mode != "external" {
		writeError(writer, http.StatusBadRequest, "DATABASE_MODE_INVALID", "数据库部署方式无效。")
		return "", false
	}
	host := strings.TrimSpace(payload.Host)
	if host == "" {
		if payload.Mode == "external" {
			writeError(writer, http.StatusBadRequest, "DATABASE_HOST_REQUIRED", "请输入数据库地址。")
			return "", false
		}
		host = server.config.DefaultDBHost
	}
	databaseName := strings.TrimSpace(payload.Database)
	if databaseName == "" {
		databaseName = server.config.DefaultDBName
	}
	user := strings.TrimSpace(payload.User)
	if user == "" {
		user = "ftc"
	}
	if payload.Password == "" {
		writeError(writer, http.StatusBadRequest, "DATABASE_PASSWORD_REQUIRED", "请输入数据库密码。")
		return "", false
	}
	port := strings.TrimSpace(payload.Port)
	if port == "" {
		port = "3306"
	}
	if _, err := strconv.Atoi(port); err != nil {
		writeError(writer, http.StatusBadRequest, "DATABASE_PORT_INVALID", "数据库端口无效。")
		return "", false
	}
	databaseConfig := mysql.NewConfig()
	databaseConfig.User = user
	databaseConfig.Passwd = payload.Password
	databaseConfig.Net = "tcp"
	databaseConfig.Addr = net.JoinHostPort(host, port)
	databaseConfig.DBName = databaseName
	databaseConfig.ParseTime = true
	databaseConfig.Loc = time.UTC
	databaseConfig.Params = map[string]string{"charset": "utf8mb4"}
	if payload.TLS != "" {
		databaseConfig.TLSConfig = payload.TLS
	}
	return databaseConfig.FormatDSN(), true
}

func pingDatabase(ctx context.Context, dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(timeout)
}

func (server *Server) runtimeSecretExists(ctx context.Context) (bool, error) {
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := server.client.CoreV1().Secrets(server.config.Namespace).Get(timeout, server.runtimeSecret(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (server *Server) writeRuntimeSecret(ctx context.Context, dsn, username, hash string) error {
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data := map[string][]byte{
		"dsn":                 []byte(dsn),
		"admin-username":      []byte(username),
		"admin-password-hash": []byte(hash),
	}
	secrets := server.client.CoreV1().Secrets(server.config.Namespace)
	secret, err := secrets.Get(timeout, server.runtimeSecret(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: server.runtimeSecret(), Namespace: server.config.Namespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       data,
		}
		_, err = secrets.Create(timeout, secret, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	secret.Data = data
	_, err = secrets.Update(timeout, secret, metav1.UpdateOptions{})
	return err
}

func writeJSON(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"data": data, "error": nil})
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"data": nil, "error": map[string]string{"code": code, "message": message}})
}

func decode(writer http.ResponseWriter, request *http.Request, value any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", "初始化请求格式无效。")
		return false
	}
	return true
}
