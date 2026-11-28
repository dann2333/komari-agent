package update

// 内置的 GitHub 加速镜像：直连失败后按顺序尝试，和 install.sh / install.ps1 用同一份名单。
// 这些站点时好时坏，所以一次多放几个；环境变量 KOMARI_MIRRORS（空格分隔）可以整体替换，
// 设为 "-" 关闭。只改写 github.com / api.github.com 的地址，自建 API（--update-api-url）
// 不受影响。走镜像下载的二进制仍然按 GitHub API 给出的 digest 校验，镜像被投毒也装不上。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var githubMirrors = []string{
	"https://ghfast.top",
	"https://gh-proxy.com",
	"https://cdn.gh-proxy.com",
	"https://edgeone.gh-proxy.com",
	"https://hk.gh-proxy.com",
	"https://ghproxy.net",
	"https://ghproxy.cc",
	"https://hub.gitmirror.com",
	"https://github.moeyy.xyz",
	"https://gh.llkk.cc",
	"https://gh.ddlc.top",
}

var (
	mirrorsOnce sync.Once
	mirrors     []string
)

func activeMirrors() []string {
	mirrorsOnce.Do(func() {
		mirrors = githubMirrors
		if raw := strings.TrimSpace(os.Getenv("KOMARI_MIRRORS")); raw != "" {
			if raw == "-" {
				mirrors = nil
			} else {
				mirrors = strings.Fields(raw)
			}
		}
	})
	return mirrors
}

func isGitHubURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return u.Host == "github.com" || u.Host == "api.github.com"
}

// mirrorCandidates 把地址展开成"直连 + 各个镜像"的候选列表，非 GitHub 地址原样返回。
func mirrorCandidates(rawURL string) []string {
	if !isGitHubURL(rawURL) {
		return []string{rawURL}
	}
	out := []string{rawURL}
	for _, m := range activeMirrors() {
		m = strings.TrimSuffix(strings.TrimSpace(m), "/")
		if m != "" {
			out = append(out, m+"/"+rawURL)
		}
	}
	return out
}

// mirrorResponseTimeout 是镜像"开始应答"（返回响应头）的时限。挂掉的站点要么连不上，
// 要么连上后不吭声，后者不该占满整个下载时限。测试会把它调短。
var mirrorResponseTimeout = 20 * time.Second

// doViaMirrors 按候选顺序发请求，返回第一个 2xx 响应。直连沿用 client 自身的超时；
// 镜像请求只受 mirrorTimeout 约束（连接 + 应答 + 读完响应体），并且必须在
// mirrorResponseTimeout 内开始应答，免得挂掉的站点把两小时一次的检查拖上几分钟。
// Authorization 只发给直连地址，不转发给镜像。
func doViaMirrors(client *http.Client, req *http.Request, mirrorTimeout time.Duration) (*http.Response, error) {
	return doCandidates(client, req, mirrorCandidates(req.URL.String()), mirrorTimeout)
}

func doCandidates(client *http.Client, req *http.Request, candidates []string, mirrorTimeout time.Duration) (*http.Response, error) {
	var lastErr error
	for _, raw := range candidates {
		r := req
		if raw == req.URL.String() {
			resp, err := client.Do(r)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				return resp, nil
			}
			lastErr = closeWithError(resp)
			continue
		}

		u, err := url.Parse(raw)
		if err != nil {
			lastErr = err
			continue
		}
		ctx, cancel := context.WithTimeout(req.Context(), mirrorTimeout)
		respond := time.AfterFunc(mirrorResponseTimeout, cancel)
		r = req.Clone(ctx)
		r.URL = u
		r.Host = ""
		r.Header = req.Header.Clone()
		r.Header.Del("Authorization")
		resp, err := withoutClientTimeout(client).Do(r)
		respond.Stop()
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			resp.Body = &cancelOnBodyClose{resp.Body, cancel}
			return resp, nil
		}
		cancel()
		lastErr = closeWithError(resp)
	}
	if lastErr == nil {
		lastErr = io.EOF
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("all %d sources failed, last error: %w", len(candidates), lastErr)
	}
	return nil, lastErr
}

// withoutClientTimeout 去掉 client 自带的总超时（它连响应体一起算，会抢在 mirrorTimeout
// 之前把下载掐掉），让镜像请求只受本次 ctx 约束。Transport 等仍与原 client 共用。
func withoutClientTimeout(c *http.Client) *http.Client {
	if c.Timeout == 0 {
		return c
	}
	mc := *c
	mc.Timeout = 0
	return &mc
}

func closeWithError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// cancelOnBodyClose 让镜像请求的 context 随响应体关闭一起释放。
type cancelOnBodyClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnBodyClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
