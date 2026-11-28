package update

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetMirrorsForTest() {
	mirrorsOnce = sync.Once{}
	mirrors = nil
}

func TestMirrorCandidates(t *testing.T) {
	t.Setenv("KOMARI_MIRRORS", "https://a.example/ https://b.example")
	resetMirrorsForTest()
	got := mirrorCandidates("https://github.com/o/r/releases/download/v/x.exe")
	want := []string{
		"https://github.com/o/r/releases/download/v/x.exe",
		"https://a.example/https://github.com/o/r/releases/download/v/x.exe",
		"https://b.example/https://github.com/o/r/releases/download/v/x.exe",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mirrorCandidates = %q, want %q", got, want)
	}

	api := "https://api.github.com/repos/o/r/releases?per_page=100&page=1"
	if c := mirrorCandidates(api); len(c) != 3 || c[1] != "https://a.example/"+api {
		t.Fatalf("api.github.com should be mirrored too, got %q", c)
	}

	// 自建 API 和测试服务器不走镜像
	for _, raw := range []string{"https://ghe.example.com/api/v3/repos/o/r", "http://127.0.0.1:1/x", "https://github.example.com/x"} {
		if c := mirrorCandidates(raw); len(c) != 1 || c[0] != raw {
			t.Fatalf("mirrorCandidates(%q) = %q, want only the original", raw, c)
		}
	}

	t.Setenv("KOMARI_MIRRORS", "-")
	resetMirrorsForTest()
	if c := mirrorCandidates("https://github.com/o/r/releases/download/v/x.exe"); len(c) != 1 {
		t.Fatalf(`KOMARI_MIRRORS="-" should disable mirrors, got %q`, c)
	}
}

func TestDoCandidatesFallsBackToMirror(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/direct" {
			if r.Header.Get("Authorization") == "" {
				t.Errorf("direct attempt must keep the token")
			}
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization leaked to mirror %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/direct", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := doCandidates(http.DefaultClient, req, []string{srv.URL + "/direct", srv.URL + "/mirror"}, 5*time.Second)
	if err != nil {
		t.Fatalf("doCandidates: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("mirror body must stay readable after the response is returned: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("body = %q", body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 || hits[0] != "/direct" || hits[1] != "/mirror" {
		t.Fatalf("unexpected attempt order: %q", hits)
	}
}

func TestDoCandidatesAllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doCandidates(http.DefaultClient, req, []string{srv.URL + "/x", srv.URL + "/y"}, time.Second); err == nil || !strings.Contains(err.Error(), "all 2 sources failed") {
		t.Fatalf("want an all-sources error, got %v", err)
	}
	// 只有一个候选时保持原来的报错形状，别把老测试和日志改掉
	_, err = doCandidates(http.DefaultClient, req, []string{srv.URL + "/x"}, time.Second)
	if err == nil || strings.Contains(err.Error(), "sources failed") || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("single candidate should keep the plain status error, got %v", err)
	}
}

// 直连 127.0.0.1:1 立刻被拒，所以两个测试都只会打到镜像候选上。
func mirrorTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/direct", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestDoCandidatesMirrorNotBoundByClientTimeout(t *testing.T) {
	// 响应头立刻返回，响应体比 client.Timeout 慢：镜像下载只该受 mirrorTimeout 约束
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
		_, _ = w.Write([]byte("slow"))
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	start := time.Now()
	resp, err := doCandidates(client, mirrorTestRequest(t), []string{"http://127.0.0.1:1/direct", srv.URL + "/slow"}, 5*time.Second)
	if err != nil {
		t.Fatalf("mirror body must not be cut off by the client timeout: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "slow" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	if time.Since(start) < 400*time.Millisecond {
		t.Fatalf("returned too early: %v", time.Since(start))
	}
}

func TestDoCandidatesMirrorMustRespondQuickly(t *testing.T) {
	old := mirrorResponseTimeout
	mirrorResponseTimeout = 100 * time.Millisecond
	defer func() { mirrorResponseTimeout = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mute" {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("fast"))
	}))
	defer srv.Close()
	start := time.Now()
	resp, err := doCandidates(http.DefaultClient, mirrorTestRequest(t),
		[]string{"http://127.0.0.1:1/direct", srv.URL + "/mute", srv.URL + "/fast"}, 10*time.Second)
	if err != nil {
		t.Fatalf("a mute mirror must be skipped, not waited for: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fast" {
		t.Fatalf("body = %q", body)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("mute mirror took %v, response timeout did not apply", elapsed)
	}
}
