package ws

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// writeWait 是单次写入的上限。连接变成半开之后, 内核发送缓冲一旦填满,
// 不设期限的写会一直卡着, 连带把重连也卡死。
const writeWait = 10 * time.Second

type SafeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex // 只保护写: gorilla 同一时刻只允许一个写者

	pongWait time.Duration
	heard    atomic.Bool // 收到过对端的 pong
}

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{conn: conn}
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return sc.conn.WriteMessage(messageType, data)
}

func (sc *SafeConn) WriteJSON(v interface{}) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return sc.conn.WriteJSON(v)
}

// WritePing 发一个心跳 ping。控制帧走 WriteControl, 它可以和其它写并发,
// 不用抢写锁, 某个写卡住的时候心跳也照样发得出去 (或者照样报错)。
func (sc *SafeConn) WritePing() error {
	return sc.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
}

// Close 不拿写锁: gorilla 允许 Close 和其它方法并发。之前这里也加了锁,
// 某个写卡在死连接上时, 主循环想关掉它重连就会跟着一起卡住。
func (sc *SafeConn) Close() error {
	return sc.conn.Close()
}

// EnableHeartbeat 让连接能发现"对端早就没了"。
//
// 之前只发 ping、从来不看回应: 连接一旦半开 (NAT 映射过期、中间设备掉线、
// 面板所在机器断电), 读会永远阻塞, 写进内核缓冲区也不报错, agent 以为自己
// 在线, 面板那边早就判了离线, 只能重启进程。
//
// 装上之后, 收到第一个 pong 起就要求 wait 之内必须再收到 pong 或任何消息,
// 否则读超时、连接作废、主循环重连。第一个 pong 之前不计时: 个别反代不转发
// pong, 那种环境下退回原来的行为, 而不是每隔 wait 误判一次掉线。
// 必须在开始读之前调用。
func (sc *SafeConn) EnableHeartbeat(wait time.Duration) {
	sc.pongWait = wait
	sc.conn.SetPongHandler(func(string) error {
		sc.heard.Store(true)
		return sc.conn.SetReadDeadline(time.Now().Add(wait))
	})
}

func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	messageType, data, err := sc.conn.ReadMessage()
	if err == nil && sc.heard.Load() {
		sc.conn.SetReadDeadline(time.Now().Add(sc.pongWait))
	}
	return messageType, data, err
}

func (sc *SafeConn) ReadJSON(v interface{}) error {
	return sc.conn.ReadJSON(v)
}

func (sc *SafeConn) SetReadDeadline(t time.Time) error {
	return sc.conn.SetReadDeadline(t)
}

func (sc *SafeConn) GetConn() *websocket.Conn {
	return sc.conn
}
