// roomcheck asserts room entry against an already running WebSocket server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/coder/websocket"
	"os"
	"roomserver/internal/protocol"
	"roomserver/internal/room"
	"time"
)

type reply struct {
	Version int             `json:"version"`
	Op      string          `json:"op"`
	ID      *string         `json:"request_id"`
	Payload json.RawMessage `json:"payload"`
	Error   *protocol.Error `json:"error"`
}
type checkClient struct {
	conn     *websocket.Conn
	sequence int
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:8080/ws", "검증할 WebSocket URL")
	runs := flag.Int("race-runs", 3, "마지막 자리 동시 입장 반복 횟수 (1~100)")
	timeout := flag.Duration("timeout", 30*time.Second, "전체 검증 제한 시간")
	flag.Parse()
	if *runs < 1 || *runs > 100 || *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "검증 옵션이 잘못되었습니다.")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if e := runChecks(ctx, *url, *runs); e != nil {
		fmt.Fprintln(os.Stderr, "검증 실패:", e)
		os.Exit(1)
	}
	fmt.Printf("PASS: public/private 6인·7번째 거절, private 비밀번호 순서·목록 제외, 마지막 자리 race %d회\n", *runs)
}
func connect(ctx context.Context, url string) (*checkClient, error) {
	c, _, e := websocket.Dial(ctx, url, nil)
	if e != nil {
		return nil, errors.New("WebSocket 연결 실패.")
	}
	v := &checkClient{conn: c}
	r, e := v.read(ctx)
	if e != nil || r.Op != "session.guest" || r.ID != nil || r.Error != nil {
		c.CloseNow()
		return nil, errors.New("guest 발급 검증 실패.")
	}
	var p struct {
		ID string `json:"session_id"`
	}
	if json.Unmarshal(r.Payload, &p) != nil || p.ID == "" {
		c.CloseNow()
		return nil, errors.New("guest ID 없음.")
	}
	return v, nil
}
func (c *checkClient) read(ctx context.Context) (reply, error) {
	var r reply
	_, b, e := c.conn.Read(ctx)
	if e != nil {
		return r, errors.New("WebSocket 수신 실패.")
	}
	if json.Unmarshal(b, &r) != nil || r.Version != 1 {
		return r, errors.New("응답 envelope 오류.")
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	if len(fields) != 5 {
		return r, errors.New("응답 envelope 필드 오류.")
	}
	for _, k := range []string{"password", "hash", "salt"} {
		if hasKey(r.Payload, k) {
			return r, errors.New("비밀정보 필드 노출.")
		}
	}
	return r, nil
}
func hasKey(raw json.RawMessage, key string) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(x any) bool {
		switch m := x.(type) {
		case map[string]any:
			for k, v := range m {
				if k == key || walk(v) {
					return true
				}
			}
		case []any:
			for _, v := range m {
				if walk(v) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}
func (c *checkClient) request(ctx context.Context, op string, p any) (reply, error) {
	c.sequence++
	id := fmt.Sprintf("check-%d", c.sequence)
	b, e := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: &id, Payload: p})
	if e != nil {
		return reply{}, e
	}
	if e = c.conn.Write(ctx, websocket.MessageText, b); e != nil {
		return reply{}, errors.New("WebSocket 송신 실패.")
	}
	for {
		r, e := c.read(ctx)
		if e != nil {
			return r, e
		}
		if r.ID != nil {
			if *r.ID != id || r.Op != op {
				return r, errors.New("요청 응답 상관관계 오류.")
			}
			return r, nil
		}
		if r.Op != "room.state" {
			return r, errors.New("예상하지 않은 이벤트.")
		}
	}
}
func expect(r reply, e error, code string) error {
	if e != nil {
		return e
	}
	if code == "" {
		if r.Error != nil {
			return fmt.Errorf("성공 대신 %s.", r.Error.Code)
		}
	} else if r.Error == nil || r.Error.Code != code {
		return fmt.Errorf("%s 오류 검증 실패.", code)
	}
	return nil
}
func created(r reply) (room.Snapshot, error) {
	var p struct {
		Snapshot room.Snapshot `json:"snapshot"`
	}
	e := json.Unmarshal(r.Payload, &p)
	v := p.Snapshot
	if e != nil || len(v.RoomNumber) != 6 || v.Phase != "WAITING" || v.Capacity != 6 || len(v.Members) != 1 {
		return v, errors.New("생성 snapshot 검증 실패.")
	}
	return v, nil
}
func runChecks(ctx context.Context, url string, runs int) error {
	var clients []*checkClient
	defer func() {
		for _, c := range clients {
			c.conn.CloseNow()
		}
	}()
	dial := func() (*checkClient, error) {
		c, e := connect(ctx, url)
		if e == nil {
			clients = append(clients, c)
		}
		return c, e
	}
	for _, visibility := range []string{"public", "private"} {
		h, e := dial()
		if e != nil {
			return e
		}
		opts := map[string]any{"visibility": visibility}
		if visibility == "private" {
			opts["password"] = "demo pass"
		}
		r, e := h.request(ctx, "room.create", opts)
		if e = expect(r, e, ""); e != nil {
			return e
		}
		v, e := created(r)
		if e != nil {
			return e
		}
		g, e := dial()
		if e != nil {
			return e
		}
		join := map[string]any{"room_number": v.RoomNumber}
		if visibility == "private" {
			r, e = g.request(ctx, "room.join", join)
			if e = expect(r, e, "PASSWORD_REQUIRED"); e != nil {
				return e
			}
			join["password"] = "wrong pass"
			r, e = g.request(ctx, "room.join", join)
			if e = expect(r, e, "PASSWORD_INVALID"); e != nil {
				return e
			}
			join["password"] = "demo pass"
		}
		for i := 0; i < 6; i++ {
			if i > 0 {
				g, e = dial()
				if e != nil {
					return e
				}
			}
			r, e = g.request(ctx, "room.join", join)
			code := ""
			if i == 5 {
				code = "ROOM_FULL"
			}
			if e = expect(r, e, code); e != nil {
				return e
			}
			if i < 5 {
				var p struct {
					Snapshot room.Snapshot `json:"snapshot"`
				}
				if json.Unmarshal(r.Payload, &p) != nil || len(p.Snapshot.Members) != i+2 {
					return errors.New("입장 인원 검증 실패.")
				}
			}
		}
		r, e = h.request(ctx, "room.list", map[string]any{})
		if e = expect(r, e, ""); e != nil {
			return e
		}
		var listed struct {
			Rooms []room.Snapshot `json:"rooms"`
		}
		if json.Unmarshal(r.Payload, &listed) != nil {
			return errors.New("목록 해석 실패.")
		}
		found := false
		for _, rv := range listed.Rooms {
			if rv.Visibility != "public" {
				return errors.New("private 목록 노출.")
			}
			if rv.RoomID == v.RoomID {
				found = true
			}
		}
		if found != (visibility == "public") {
			return errors.New("public/private 목록 검증 실패.")
		}
	}
	for i := 0; i < runs; i++ {
		if e := checkRace(ctx, url); e != nil {
			return e
		}
	}
	return nil
}
func checkRace(ctx context.Context, url string) error {
	var clients []*checkClient
	defer func() {
		for _, c := range clients {
			c.conn.CloseNow()
		}
	}()
	dial := func() (*checkClient, error) {
		c, e := connect(ctx, url)
		if e == nil {
			clients = append(clients, c)
		}
		return c, e
	}
	h, e := dial()
	if e != nil {
		return e
	}
	r, e := h.request(ctx, "room.create", map[string]any{})
	if e = expect(r, e, ""); e != nil {
		return e
	}
	v, e := created(r)
	if e != nil {
		return e
	}
	join := map[string]any{"room_number": v.RoomNumber}
	for i := 0; i < 4; i++ {
		g, e := dial()
		if e != nil {
			return e
		}
		r, e = g.request(ctx, "room.join", join)
		if e = expect(r, e, ""); e != nil {
			return e
		}
	}
	a, e := dial()
	if e != nil {
		return e
	}
	b, e := dial()
	if e != nil {
		return e
	}
	type result struct {
		r reply
		e error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, c := range []*checkClient{a, b} {
		go func(c *checkClient) { <-start; r, e := c.request(ctx, "room.join", join); results <- result{r, e} }(c)
	}
	close(start)
	success, full := 0, 0
	for i := 0; i < 2; i++ {
		res := <-results
		if res.e != nil {
			return res.e
		}
		if res.r.Error == nil {
			success++
			var p struct {
				Snapshot room.Snapshot `json:"snapshot"`
			}
			if json.Unmarshal(res.r.Payload, &p) != nil || len(p.Snapshot.Members) != 6 {
				return errors.New("race 인원 검증 실패.")
			}
		} else if res.r.Error.Code == "ROOM_FULL" {
			full++
		} else {
			return errors.New("race 오류 검증 실패.")
		}
	}
	if success != 1 || full != 1 {
		return errors.New("마지막 자리 race 검증 실패.")
	}
	return nil
}
