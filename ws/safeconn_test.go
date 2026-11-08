package ws

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dial 起一个 WebSocket 服务端 (行为由 serve 决定) 并连上去
func dial(t *testing.T, serve func(*websocket.Conn)) *SafeConn {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		serve(c)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sc := NewSafeConn(c)
	t.Cleanup(func() { sc.Close() })
	return sc
}

func readResult(sc *SafeConn) <-chan error {
	ch := make(chan error, 1)
	go func() {
		for {
			if _, _, err := sc.ReadMessage(); err != nil {
				ch <- err
				return
			}
		}
	}()
	return ch
}

// 对端回过一次 pong 之后就再也不出声 (半开连接): 读必须超时返回, 而不是永远卡住
func TestHeartbeatDetectsSilentPeer(t *testing.T) {
	answered := make(chan struct{})
	sc := dial(t, func(c *websocket.Conn) {
		c.SetPingHandler(func(data string) error {
			err := c.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
			close(answered)
			return err
		})
		c.ReadMessage() // 处理第一个 ping
	})
	sc.EnableHeartbeat(300 * time.Millisecond)
	done := readResult(sc)
	if err := sc.WritePing(); err != nil {
		t.Fatal(err)
	}
	<-answered

	select {
	case err := <-done:
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("expected a read timeout, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dead connection was never noticed")
	}
}

// 从来不回 pong、也不发任何数据: 不能再一直当成在线。反代读超时经常是 600 秒,
// 之前要等第一次 pong 才计时, 这段时间面板收不到上报。
func TestSilentPeerTimesOutWithoutPong(t *testing.T) {
	sc := dial(t, func(c *websocket.Conn) {
		c.SetPingHandler(func(string) error { return nil })
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	sc.EnableHeartbeat(200 * time.Millisecond)
	done := readResult(sc)
	if err := sc.WritePing(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("expected a read timeout, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("silent connection was never noticed")
	}
}

// 反代把 pong 吃掉, 但数据帧还在 (面板对带 id 的上报有回复): 不能拆连接。
func TestInboundDataKeepsConnectionAlive(t *testing.T) {
	sc := dial(t, func(c *websocket.Conn) {
		c.SetPingHandler(func(string) error { return nil })
		go func() {
			ticker := time.NewTicker(80 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				if err := c.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)); err != nil {
					return
				}
			}
		}()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	sc.EnableHeartbeat(300 * time.Millisecond)
	done := readResult(sc)
	select {
	case err := <-done:
		t.Fatalf("connection dropped although data is still arriving: %v", err)
	case <-time.After(time.Second):
	}
}

// 某个写卡在死连接上时 (这里直接占住写锁模拟), Close 不能跟着卡住,
// 否则主循环想关掉旧连接重连也会一起卡死
func TestCloseDoesNotWaitForStuckWriter(t *testing.T) {
	sc := dial(t, func(c *websocket.Conn) { c.ReadMessage() })
	sc.mu.Lock()
	defer sc.mu.Unlock()

	closed := make(chan struct{})
	go func() {
		sc.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a stuck writer")
	}
}
