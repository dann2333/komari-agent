package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 内置的 GitHub 加速镜像。这些站点时好时坏, 所以一次多放几个,
// 哪个先通就记住哪个 (mirrorHit), 后面的请求直接从它开始。
var builtinMirrors = []string{
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

type fetcher struct {
	ghproxy   string   // 只用这个前缀
	mirrors   []string // 自定义 + 内置
	noMirror  bool
	mirrorHit string // 上一次成功的前缀
}

func newFetcher(cfg *config) *fetcher {
	f := &fetcher{ghproxy: strings.TrimSuffix(cfg.ghproxy, "/"), noMirror: cfg.noMirror}
	for _, m := range cfg.mirrors {
		f.mirrors = append(f.mirrors, strings.TrimSuffix(m, "/"))
	}
	f.mirrors = append(f.mirrors, builtinMirrors...)
	return f
}

// candidates 把一个 GitHub 地址展开成候选列表:
// 上次成功的镜像 -> 指定的前缀 -> 直连 -> 内置镜像
func (f *fetcher) candidates(url string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if f.mirrorHit != "" {
		add(f.mirrorHit + "/" + url)
	}
	if f.ghproxy != "" {
		add(f.ghproxy + "/" + url)
		add(url)
		return out
	}
	add(url)
	if f.noMirror {
		return out
	}
	for _, m := range f.mirrors {
		add(m + "/" + url)
	}
	return out
}

func (f *fetcher) noteHit(candidate, base string) {
	if candidate == base {
		f.mirrorHit = ""
		return
	}
	f.mirrorHit = strings.TrimSuffix(candidate, "/"+base)
}

func newRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "komari-switch")
	req.Header.Set("Accept", "application/vnd.github+json")
	return req, nil
}

// get 依次试每个候选地址, 返回第一份拿到的内容
func (f *fetcher) get(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	var lastErr error
	for _, candidate := range f.candidates(url) {
		req, err := newRequest(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || len(body) == 0 {
			lastErr = fmt.Errorf("%s: %s", candidate, resp.Status)
			continue
		}
		f.noteHit(candidate, url)
		return body, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate url for %s", url)
	}
	return nil, lastErr
}

// finalURL 跟完跳转后的最终地址。/releases/latest 会 302 到
// /releases/tag/<版本>, API 不通的时候靠它拿版本号。
func (f *fetcher) finalURL(url string, timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout}
	var lastErr error
	for _, candidate := range f.candidates(url) {
		req, err := newRequest(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Request == nil {
			lastErr = fmt.Errorf("%s: %s", candidate, resp.Status)
			continue
		}
		f.noteHit(candidate, url)
		return resp.Request.URL.String(), nil
	}
	return "", lastErr
}

// download 把二进制下到 dest。连上了却几乎不传数据的镜像会被 client 的
// 超时掐掉, 换下一个。
func (f *fetcher) download(url, dest string, log func(string, ...interface{})) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	var lastErr error
	for _, candidate := range f.candidates(url) {
		log("下载: %s", candidate)
		req, err := newRequest(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s: %s", candidate, resp.Status)
			continue
		}
		err = writeFileFrom(resp.Body, dest)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			os.Remove(dest)
			continue
		}
		// 镜像挂掉时经常回一个 HTML 错误页, 大小不为零但根本不是程序
		if !looksLikeBinary(dest) {
			log("这个地址返回的不是可执行文件, 换下一个")
			os.Remove(dest)
			lastErr = fmt.Errorf("%s: not an executable", candidate)
			continue
		}
		f.noteHit(candidate, url)
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate url for %s", url)
	}
	return lastErr
}

func writeFileFrom(r io.Reader, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// looksLikeBinary 看文件头的魔数, 别把网页装到 agent 的位置上去
func looksLikeBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	if n, _ := io.ReadFull(f, head); n < 4 {
		return false
	}
	switch {
	case head[0] == 0x7f && string(head[1:4]) == "ELF": // ELF
		return true
	case head[0] == 'M' && head[1] == 'Z': // PE
		return true
	}
	switch fmt.Sprintf("%x", head) { // Mach-O
	case "cafebabe", "cffaedfe", "cefaedfe", "feedface", "feedfacf":
		return true
	}
	return false
}

const snapshotPrefix = "Snapshot-"

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
}

// resolveVersion 查要装的版本号, 三级退让:
// GitHub API -> releases/latest 的跳转地址 -> releases.atom。
// 三条都不通时返回 latest 直下通道 (useLatestChannel), 那条路不需要版本号。
func resolveVersion(f *fetcher, repo, want string) (version string, useLatestChannel bool, err error) {
	switch want {
	case "snapshot":
		v := resolveSnapshot(f, repo)
		if v == "" {
			return "", false, fmt.Errorf("取不到 %s 的快照版 release", repo)
		}
		return v, false, nil
	case "latest", "auto":
		if v := resolveLatest(f, repo); v != "" {
			return v, false, nil
		}
		if want == "auto" {
			if v := resolveSnapshot(f, repo); v != "" {
				return v, false, nil
			}
		}
		return "latest", true, nil
	default:
		return want, false, nil
	}
}

func resolveLatest(f *fetcher, repo string) string {
	if body, err := f.get("https://api.github.com/repos/"+repo+"/releases/latest", 20*time.Second); err == nil {
		var rel releaseInfo
		if json.Unmarshal(body, &rel) == nil && rel.TagName != "" {
			return rel.TagName
		}
	}
	// API 不通就退一步, 从 /releases/latest 的跳转地址里抠版本号
	if eff, err := f.finalURL("https://github.com/"+repo+"/releases/latest", 20*time.Second); err == nil {
		if i := strings.Index(eff, "/releases/tag/"); i >= 0 {
			return strings.Trim(eff[i+len("/releases/tag/"):], "/")
		}
	}
	return ""
}

var atomTagRe = regexp.MustCompile(`releases/tag/(Snapshot-[0-9A-Za-z.\-]+)`)

func resolveSnapshot(f *fetcher, repo string) string {
	var tags []string
	if body, err := f.get("https://api.github.com/repos/"+repo+"/releases?per_page=100", 20*time.Second); err == nil {
		var rels []releaseInfo
		if json.Unmarshal(body, &rels) == nil {
			for _, r := range rels {
				if strings.HasPrefix(r.TagName, snapshotPrefix) {
					tags = append(tags, r.TagName)
				}
			}
		}
	}
	if len(tags) == 0 {
		// 快照是预发布, /releases/latest 看不到, 用 atom 订阅源兜底
		if body, err := f.get("https://github.com/"+repo+"/releases.atom", 20*time.Second); err == nil {
			for _, m := range atomTagRe.FindAllStringSubmatch(string(body), -1) {
				tags = append(tags, m[1])
			}
		}
	}
	if len(tags) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(tags)))
	return tags[0]
}

func downloadURL(repo, version, asset string, useLatestChannel bool) string {
	if useLatestChannel {
		return "https://github.com/" + repo + "/releases/latest/download/" + asset
	}
	return "https://github.com/" + repo + "/releases/download/" + version + "/" + asset
}
