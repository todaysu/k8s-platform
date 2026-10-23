package arthasdist

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Source struct {
	URL     string
	Version string
	SHA256  string
	Timeout time.Duration
}

// Loader keeps one validated override per handler. All target Pods receive the
// same verified bytes, without depending on outbound access from the container.
type Loader struct {
	once   sync.Once
	gate   chan struct{}
	key    string
	bundle Bundle
}

func (loader *Loader) Load(ctx context.Context, source Source) (Bundle, error) {
	if strings.TrimSpace(source.URL) == "" {
		return Builtin()
	}
	if !ValidVersion(source.Version) {
		return Bundle{}, fmt.Errorf("Arthas 更新包版本无效，请填写例如 4.3.5 的版本号")
	}
	parsed, err := url.Parse(source.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return Bundle{}, fmt.Errorf("Arthas 更新包需要不含账号密码的 HTTP/HTTPS 地址")
	}
	checksum := strings.ToLower(source.SHA256)
	if checksum != "" && (len(checksum) != 64 || strings.IndexFunc(checksum, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) >= 0) {
		return Bundle{}, fmt.Errorf("Arthas 更新包 SHA-256 无效")
	}
	if source.Timeout <= 0 || source.Timeout > 10*time.Minute {
		source.Timeout = time.Minute
	}
	key := source.URL + "\n" + source.Version + "\n" + checksum
	requestCtx, cancel := context.WithTimeout(ctx, source.Timeout)
	defer cancel()
	loader.once.Do(func() { loader.gate = make(chan struct{}, 1) })
	select {
	case loader.gate <- struct{}{}:
		defer func() { <-loader.gate }()
	case <-requestCtx.Done():
		return Bundle{}, fmt.Errorf("Arthas 更新包下载等待超时或已取消")
	}
	if loader.key == key {
		return loader.bundle, nil
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, source.URL, nil)
	if err != nil {
		return Bundle{}, fmt.Errorf("创建 Arthas 更新包下载请求失败")
	}
	client := &http.Client{Timeout: source.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return Bundle{}, fmt.Errorf("Arthas 更新包下载超时或已取消")
		}
		return Bundle{}, fmt.Errorf("无法下载 Arthas 更新包，请检查离线地址及平台网络")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Bundle{}, fmt.Errorf("Arthas 更新包下载失败，HTTP %d（不跟随重定向）", response.StatusCode)
	}
	if response.ContentLength > maxArchiveSize {
		return Bundle{}, fmt.Errorf("Arthas 离线包超过 128 MiB 上限")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxArchiveSize+1))
	if err != nil {
		return Bundle{}, fmt.Errorf("读取 Arthas 更新包失败")
	}
	if len(content) > maxArchiveSize {
		return Bundle{}, fmt.Errorf("Arthas 离线包超过 128 MiB 上限")
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(content))
	if checksum != "" && checksum != actual {
		return Bundle{}, fmt.Errorf("Arthas 更新包 SHA-256 不匹配")
	}
	payload, err := archivePayload(content)
	if err != nil {
		return Bundle{}, err
	}
	result := Bundle{Version: source.Version, Home: "/tmp/ftc-arthas-" + source.Version + "-" + actual[:12], Payload: payload}
	loader.key, loader.bundle = key, result
	return result, nil
}
