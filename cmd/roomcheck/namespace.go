package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"

	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/room"
)

func checkerURL(base, gameID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(base, "?#") || !room.ValidGameID(gameID) {
		return "", errors.New("WebSocket base URL 또는 game ID가 잘못되었습니다.")
	}
	u.RawQuery = url.Values{"game_id": {gameID}}.Encode()
	return u.String(), nil
}
func expectedGame(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "default", nil
	}
	ids := values["game_id"]
	if len(values) != 1 || len(ids) != 1 || !room.ValidGameID(ids[0]) {
		return "", errors.New("잘못된 game query.")
	}
	return ids[0], nil
}
func snapshotBinding(raw json.RawMessage, expected string) error {
	var state struct {
		GameID string `json:"game_id"`
	}
	if json.Unmarshal(raw, &state) != nil || state.GameID != expected {
		return errors.New("snapshot game_id 바인딩 불일치.")
	}
	return nil
}
func (c *checkClient) validateBinding(r reply) error {
	if r.Error != nil {
		return nil
	}
	switch r.Op {
	case "room.state", "game.snapshot":
		return snapshotBinding(r.Payload, c.gameID)
	case "room.create", "room.join", "room.start", "game.started", "game.ended":
		var wrapper struct {
			Snapshot json.RawMessage `json:"snapshot"`
		}
		if json.Unmarshal(r.Payload, &wrapper) != nil {
			return errors.New("snapshot wrapper 오류.")
		}
		return snapshotBinding(wrapper.Snapshot, c.gameID)
	case "room.list":
		var wrapper struct {
			Rooms []json.RawMessage `json:"rooms"`
		}
		if json.Unmarshal(r.Payload, &wrapper) != nil || wrapper.Rooms == nil {
			return errors.New("룸 목록 오류.")
		}
		for _, raw := range wrapper.Rooms {
			if err := snapshotBinding(raw, c.gameID); err != nil {
				return err
			}
		}
	}
	return nil
}
func listedState(ctx context.Context, c *checkClient, roomID string) (room.Snapshot, error) {
	r, err := c.request(ctx, "room.list", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return room.Snapshot{}, err
	}
	var p struct {
		Rooms []room.Snapshot `json:"rooms"`
	}
	if json.Unmarshal(r.Payload, &p) != nil {
		return room.Snapshot{}, errors.New("namespace 목록 해석 실패.")
	}
	for _, v := range p.Rooms {
		if v.RoomID == roomID {
			return v, nil
		}
	}
	return room.Snapshot{}, errors.New("namespace 룸이 목록에서 사라짐.")
}
func assertRoomAbsent(ctx context.Context, c *checkClient, roomID string) error {
	r, err := c.request(ctx, "room.list", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return err
	}
	var p struct {
		Rooms []room.Snapshot `json:"rooms"`
	}
	if json.Unmarshal(r.Payload, &p) != nil {
		return errors.New("namespace 목록 해석 실패.")
	}
	for _, v := range p.Rooms {
		if v.RoomID == roomID {
			return errors.New("namespace 격리 또는 빈 룸 정리 실패.")
		}
	}
	return nil
}
func namespaceMoveStop(ctx context.Context, c *checkClient, roomID string) (room.Snapshot, error) {
	r, err := c.request(ctx, "room.start", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return room.Snapshot{}, err
	}
	var p struct {
		Snapshot room.Snapshot `json:"snapshot"`
	}
	if json.Unmarshal(r.Payload, &p) != nil || p.Snapshot.MatchID == nil {
		return room.Snapshot{}, errors.New("namespace 시작 match 없음.")
	}
	match := *p.Snapshot.MatchID
	for seq, direction := range []game.Vec3{{Z: 1}, {}} {
		r, err = c.request(ctx, "game.action", map[string]any{"match_id": match, "input_seq": seq + 1, "action": game.Action{Type: "move", Direction: direction}})
		if err = expect(r, err, ""); err != nil {
			return room.Snapshot{}, err
		}
		var ack room.ActionAck
		if json.Unmarshal(r.Payload, &ack) != nil || ack.InputSeq != uint64(seq+1) || ack.MatchID != match || ack.ServerTick == 0 {
			return room.Snapshot{}, errors.New("namespace action ACK 실패.")
		}
		if seq == 0 {
			for {
				state, e := listedState(ctx, c, roomID)
				if e != nil {
					return state, e
				}
				moving := false
				if state.GameState == nil {
					return state, errors.New("namespace 이동 상태 없음.")
				}
				for _, player := range state.GameState.Players {
					if player.SessionID == c.sessionID {
						moving = player.Position.Z > 0 && player.Velocity.Z == 5
					}
				}
				if moving {
					break
				}
				if ctx.Err() != nil {
					return state, ctx.Err()
				}
			}
		}
	}
	stopped, err := listedState(ctx, c, roomID)
	if err != nil {
		return stopped, err
	}
	if stopped.LastProcessedInputSeq[c.sessionID] != 2 || stopped.GameState == nil {
		return stopped, errors.New("namespace 정지 seq 누락.")
	}
	for _, player := range stopped.GameState.Players {
		if player.Velocity != (game.Vec3{}) {
			return stopped, errors.New("namespace 정지 속도 오류.")
		}
	}
	for {
		after, e := listedState(ctx, c, roomID)
		if e != nil {
			return after, e
		}
		if after.ServerTick > stopped.ServerTick+1 {
			if !reflect.DeepEqual(stopped.GameState, after.GameState) {
				return after, errors.New("namespace 정지 후 위치 변동.")
			}
			return after, nil
		}
		if ctx.Err() != nil {
			return after, ctx.Err()
		}
	}
}
func runNamespaceChecks(ctx context.Context, base, gameID, otherGameID string) error {
	if gameID == otherGameID {
		return errors.New("서로 다른 game ID 두 개가 필요합니다.")
	}
	endpoints := make([]string, 2)
	for i, id := range []string{gameID, otherGameID} {
		v, err := checkerURL(base, id)
		if err != nil {
			return err
		}
		endpoints[i] = v
	}
	var clients []*checkClient
	defer func() {
		for _, c := range clients {
			c.conn.CloseNow()
		}
	}()
	dial := func(index int) (*checkClient, error) {
		c, err := connect(ctx, endpoints[index])
		if err == nil {
			clients = append(clients, c)
		}
		return c, err
	}
	ah, err := dial(0)
	if err != nil {
		return err
	}
	ag, err := dial(0)
	if err != nil {
		return err
	}
	bh, err := dial(1)
	if err != nil {
		return err
	}
	bg, err := dial(1)
	if err != nil {
		return err
	}
	r, err := ah.request(ctx, "room.create", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return err
	}
	av, err := created(r)
	if err != nil {
		return err
	}
	if err = assertRoomAbsent(ctx, bh, av.RoomID); err != nil {
		return err
	}
	r, err = bh.request(ctx, "room.join", map[string]any{"room_number": av.RoomNumber})
	if err = expect(r, err, "ROOM_NOT_FOUND"); err != nil {
		return err
	}
	r, err = bh.request(ctx, "room.create", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return err
	}
	bv, err := created(r)
	if err != nil {
		return err
	}
	for _, pair := range []struct {
		c      *checkClient
		number string
	}{{ag, av.RoomNumber}, {bg, bv.RoomNumber}} {
		r, err = pair.c.request(ctx, "room.join", map[string]any{"room_number": pair.number})
		if err = expect(r, err, ""); err != nil {
			return err
		}
	}
	for _, op := range []string{"room.create", "room.join", "room.list", "room.start", "room.leave", "game.action"} {
		payload := map[string]any{"game_id": otherGameID}
		if op == "room.join" {
			payload["room_number"] = bv.RoomNumber
		}
		if op == "game.action" {
			payload["match_id"] = "fake"
			payload["input_seq"] = 1
			payload["action"] = game.Action{Type: "move"}
		}
		r, err = ah.request(ctx, op, payload)
		if err = expect(r, err, "INVALID_MESSAGE"); err != nil {
			return err
		}
	}
	if _, err = namespaceMoveStop(ctx, ah, av.RoomID); err != nil {
		return err
	}
	untouched, err := listedState(ctx, bh, bv.RoomID)
	if err != nil {
		return err
	}
	if untouched.Phase != "WAITING" || untouched.MatchID != nil || untouched.HostID != bh.sessionID || len(untouched.Members) != 2 {
		return errors.New("A lifecycle 이후 B waiting 상태 변동.")
	}
	stable, err := namespaceMoveStop(ctx, bh, bv.RoomID)
	if err != nil {
		return err
	}
	r, err = ah.request(ctx, "room.leave", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return err
	}
	successor, err := listedState(ctx, ag, av.RoomID)
	if err != nil {
		return err
	}
	if successor.HostID != ag.sessionID || len(successor.Members) != 1 {
		return errors.New("A host 승계 실패.")
	}
	r, err = ag.request(ctx, "room.leave", map[string]any{})
	if err = expect(r, err, ""); err != nil {
		return err
	}
	if err = assertRoomAbsent(ctx, ah, av.RoomID); err != nil {
		return err
	}
	remaining, err := listedState(ctx, bh, bv.RoomID)
	if err != nil {
		return err
	}
	if remaining.HostID != stable.HostID || remaining.Revision != stable.Revision || !reflect.DeepEqual(remaining.Members, stable.Members) || !reflect.DeepEqual(remaining.MatchID, stable.MatchID) || !reflect.DeepEqual(remaining.GameState, stable.GameState) {
		return errors.New("A cleanup 이후 B 상태 변동.")
	}
	for _, c := range []*checkClient{bh, bg} {
		r, err = c.request(ctx, "room.leave", map[string]any{})
		if err = expect(r, err, ""); err != nil {
			return err
		}
	}
	return assertRoomAbsent(ctx, bh, bv.RoomID)
}
