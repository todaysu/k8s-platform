package setup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"example.com/k8s-platform/internal/auth"
	"example.com/k8s-platform/internal/store"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ErrAdminConflict = errors.New("admin account already exists with different credentials")
	applicationName  = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
)

// provisionAdmin runs the schema migrations and creates the first admin. A
// retry with the same credentials is accepted so a failed Secret write can be
// recovered without wiping the database.
func provisionAdmin(ctx context.Context, dsn, username, password string, secureCookies bool, sessionTTL time.Duration) (string, error) {
	database, err := store.OpenMySQL(dsn)
	if err != nil {
		return "", fmt.Errorf("初始化数据库表结构失败：%w", err)
	}
	defer func() { _ = database.Close() }()
	var userCount int
	if err := database.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_users`).Scan(&userCount); err != nil {
		return "", fmt.Errorf("检查管理员账号失败：%w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("生成管理员密码失败：%w", err)
	}
	adminHash := string(hash)
	if userCount > 0 {
		var existing string
		if err := database.SQLDB().QueryRowContext(ctx, `SELECT password_hash FROM auth_users WHERE username=?`, username).Scan(&existing); err != nil || bcrypt.CompareHashAndPassword([]byte(existing), []byte(password)) != nil {
			return "", ErrAdminConflict
		}
		return existing, nil
	}
	authentication, err := auth.NewWithDB(database.SQLDB(), "", username, adminHash, secureCookies, sessionTTL)
	if err != nil {
		return "", fmt.Errorf("创建管理员账号失败：%w", err)
	}
	_ = authentication.Close()
	return adminHash, nil
}

// ensureApplicationDatabase creates the platform database and application user
// through HAProxy. It returns the DSN used by the runtime Deployments.
func (server *Server) ensureApplicationDatabase(ctx context.Context, plan MySQLPlan, appUser, appPassword, databaseName string) (string, error) {
	if server.client == nil {
		return "", fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	if !applicationName.MatchString(appUser) {
		return "", fmt.Errorf("应用数据库用户名只能包含字母、数字和下划线")
	}
	if !applicationName.MatchString(databaseName) {
		return "", fmt.Errorf("应用数据库名只能包含字母、数字和下划线")
	}
	if plan.Namespace == "" {
		plan.Namespace = "ftc-platform"
	}
	if plan.SecretName == "" {
		plan.SecretName = pxcSecretName
	}
	secret, err := server.client.CoreV1().Secrets(plan.Namespace).Get(ctx, plan.SecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("PXC 密码 Secret %s 不存在", plan.SecretName)
	}
	if err != nil {
		return "", fmt.Errorf("读取 PXC 密码 Secret 失败：%w", err)
	}
	rootPassword := string(secret.Data["root"])
	operatorPassword := string(secret.Data["operator"])
	credentials := []struct {
		user     string
		password string
	}{{"root", rootPassword}, {"operator", operatorPassword}}
	var adminDatabase *sql.DB
	var adminUser string
	for _, credential := range credentials {
		if credential.password == "" {
			continue
		}
		config := mysql.NewConfig()
		config.User = credential.user
		config.Passwd = credential.password
		config.Net = "tcp"
		config.Addr = fmt.Sprintf("%s-haproxy.%s.svc:3306", plan.Name, plan.Namespace)
		config.ParseTime = true
		config.Loc = time.UTC
		config.Params = map[string]string{"charset": "utf8mb4"}
		candidate, openErr := sql.Open("mysql", config.FormatDSN())
		if openErr != nil {
			continue
		}
		pingContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		pingErr := candidate.PingContext(pingContext)
		cancel()
		if pingErr == nil {
			adminDatabase = candidate
			adminUser = credential.user
			break
		}
		_ = candidate.Close()
	}
	if adminDatabase == nil {
		return "", fmt.Errorf("无法通过 HAProxy 连接 PXC 管理账号（已尝试 root/operator）；请检查 PXC 状态和 Secret 权限")
	}
	defer adminDatabase.Close()
	quotedDatabase := "`" + databaseName + "`"
	quotedUser := "'" + appUser + "'@'%'"
	quotedPassword := "'" + strings.ReplaceAll(appPassword, "'", "''") + "'"
	statements := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", quotedDatabase),
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s", quotedUser, quotedPassword),
		fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s", quotedUser, quotedPassword),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO %s", quotedDatabase, quotedUser),
	}
	for _, statement := range statements {
		if _, err := adminDatabase.ExecContext(ctx, statement); err != nil {
			return "", fmt.Errorf("使用 %s 创建应用数据库或账号失败：%w", adminUser, err)
		}
	}

	appConfig := mysql.NewConfig()
	appConfig.User = appUser
	appConfig.Passwd = appPassword
	appConfig.Net = "tcp"
	appConfig.Addr = fmt.Sprintf("%s-haproxy.%s.svc:3306", plan.Name, plan.Namespace)
	appConfig.DBName = databaseName
	appConfig.ParseTime = true
	appConfig.Loc = time.UTC
	appConfig.Params = map[string]string{"charset": "utf8mb4"}
	return appConfig.FormatDSN(), nil
}

func (server *Server) WaitForDeployment(ctx context.Context, namespace, name string, timeout time.Duration) error {
	if server.client == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	if namespace == "" {
		namespace = "ftc-platform"
	}
	deadline := time.Now().Add(timeout)
	for {
		deployment, err := server.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && deployment.Spec.Replicas != nil {
			desired := *deployment.Spec.Replicas
			ready := deployment.Status.ReadyReplicas
			updated := deployment.Status.UpdatedReplicas
			if ready >= desired && updated >= desired && deployment.Status.ObservedGeneration >= deployment.Generation {
				return nil
			}
		} else if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("读取 Deployment %s 状态失败：%w", name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 Deployment %s 就绪超时（%s）", name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func waitForHTTPHealth(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
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
			return fmt.Errorf("等待平台健康检查超时（%s）", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
