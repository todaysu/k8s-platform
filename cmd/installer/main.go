package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	source, err := os.ReadFile("deploy/install.yaml")
	if err != nil {
		fatal(err)
	}
	if strings.Count(string(source), "REPLACE_WITH_SETUP_TOKEN") != 1 || strings.Count(string(source), "REPLACE_WITH_PLATFORM_IMAGE") != 1 {
		fatal(fmt.Errorf("安装模板占位符数量不正确"))
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		fatal(err)
	}
	output := strings.Replace(string(source), "REPLACE_WITH_SETUP_TOKEN", base64.RawURLEncoding.EncodeToString(key), 1)
	path := filepath.Join("data", "delivery", "install.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fatal(err)
	}
	if _, err = os.Stat(path); err == nil {
		fatal(fmt.Errorf("%s 已存在；不会覆盖包含初始化凭证的清单", path))
	} else if !os.IsNotExist(err) {
		fatal(err)
	}
	if err = os.WriteFile(path, []byte(output), 0600); err != nil {
		fatal(err)
	}
	fmt.Println("已生成 data/delivery/install.yaml；修改唯一的 REPLACE_WITH_PLATFORM_IMAGE 后执行 kubectl apply。初始化凭证位于该私有文件的 setup-token 字段。")
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
