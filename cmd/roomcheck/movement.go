package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"reflect"
	"roomserver/internal/game"
	"roomserver/internal/protocol"
	"roomserver/internal/room"
	"sync"
	"time"
)

const movementClientCount = 6
const movementCaptureLimit = 128

type movementMessage struct {
	client int
	reply  reply
}
type captureKey struct {
	match                    string
	revision, tick, sequence uint64
}
type movementCapture struct {
	states [movementClientCount]room.Snapshot
	seen   [movementClientCount]bool
}
type movementReceiver struct {
	messages chan movementMessage
	failure  chan error
	captures map[captureKey]*movementCapture
	order    []captureKey
	match    string
}

func compareMovementSnapshots(states []room.Snapshot) error {
	if len(states) == 0 {
		return errors.New("비교할 snapshot 없음.")
	}
	for _, state := range states[1:] {
		if !reflect.DeepEqual(states[0], state) {
			return errors.New("동일 capture의 공개 상태 불일치.")
		}
	}
	return nil
}

// Every connection has one reader after synchronous room entry has finished.
func (r *movementReceiver) start(ctx context.Context, clients []*checkClient, wg *sync.WaitGroup) {
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *checkClient) {
			defer wg.Done()
			fail := func(err error) {
				select {
				case r.failure <- err:
				default:
				}
			}
			for {
				v, err := c.read(ctx)
				if err != nil {
					if ctx.Err() == nil {
						fail(err)
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				select {
				case r.messages <- movementMessage{i, v}:
				default:
					fail(errors.New("이동 수신 채널 포화 (128)."))
					return
				}
			}
		}(i, c)
	}
}
func (r *movementReceiver) next(ctx context.Context) (movementMessage, error) {
	select {
	case err := <-r.failure:
		return movementMessage{}, err
	default:
	}
	select {
	case <-ctx.Done():
		return movementMessage{}, ctx.Err()
	case err := <-r.failure:
		return movementMessage{}, err
	case message := <-r.messages:
		v := message.reply
		if v.ID == nil {
			switch v.Op {
			case "room.state", "game.snapshot", "game.started":
				var state room.Snapshot
				payload := v.Payload
				if v.Op == "game.started" {
					var started struct {
						Snapshot json.RawMessage `json:"snapshot"`
					}
					if json.Unmarshal(payload, &started) != nil || len(started.Snapshot) == 0 {
						return message, errors.New("시작 snapshot wrapper 없음.")
					}
					payload = started.Snapshot
				}
				if json.Unmarshal(payload, &state) != nil {
					return message, errors.New("이동 snapshot 해석 실패.")
				}
				if state.MatchID != nil && *state.MatchID == r.match {
					key := captureKey{*state.MatchID, state.Revision, state.ServerTick, state.SnapshotSeq}
					capture := r.captures[key]
					if capture == nil {
						if len(r.order) == movementCaptureLimit {
							delete(r.captures, r.order[0])
							r.order = r.order[1:]
						}
						capture = &movementCapture{}
						r.captures[key] = capture
						r.order = append(r.order, key)
					}
					if capture.seen[message.client] && !reflect.DeepEqual(capture.states[message.client], state) {
						return message, errors.New("같은 연결의 동일 capture 불일치.")
					}
					capture.states[message.client] = state
					capture.seen[message.client] = true
				}
			case "game.event":
			default:
				return message, fmt.Errorf("예상하지 않은 이동 이벤트: %s.", v.Op)
			}
		}
		return message, nil
	}
}
func (c *checkClient) send(ctx context.Context, op string, p any) (string, error) {
	c.sequence++
	id := fmt.Sprintf("check-%d", c.sequence)
	data, err := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: &id, Payload: p})
	if err == nil {
		err = c.conn.Write(ctx, websocket.MessageText, data)
	}
	return id, err
}
func (r *movementReceiver) response(ctx context.Context, client int, op, id string) (reply, error) {
	for {
		message, err := r.next(ctx)
		if err != nil {
			return reply{}, err
		}
		v := message.reply
		if v.ID != nil {
			if message.client != client || v.Op != op || *v.ID != id {
				return v, errors.New("이동 응답 상관관계 오류.")
			}
			return v, expect(v, nil, "")
		}
	}
}
func (r *movementReceiver) snapshot(ctx context.Context, afterSeq, minTick uint64) (room.Snapshot, error) {
	for {
		for _, key := range r.order {
			if key.sequence <= afterSeq || key.tick < minTick {
				continue
			}
			capture := r.captures[key]
			complete := true
			for _, seen := range capture.seen {
				complete = complete && seen
			}
			if !complete {
				continue
			}
			if err := compareMovementSnapshots(capture.states[:]); err != nil {
				return room.Snapshot{}, err
			}
			return capture.states[0], nil
		}
		message, err := r.next(ctx)
		if err != nil {
			return room.Snapshot{}, err
		}
		if message.reply.ID != nil {
			return room.Snapshot{}, errors.New("snapshot 대기 중 예상하지 않은 응답.")
		}
	}
}
func (r *movementReceiver) action(ctx context.Context, clients []*checkClient, index int, seq uint64, direction game.Vec3) (room.ActionAck, error) {
	id, err := clients[index].send(ctx, "game.action", map[string]any{"match_id": r.match, "input_seq": seq, "action": game.Action{Type: "move", Direction: direction}})
	if err != nil {
		return room.ActionAck{}, err
	}
	v, err := r.response(ctx, index, "game.action", id)
	if err != nil {
		return room.ActionAck{}, err
	}
	var ack room.ActionAck
	if json.Unmarshal(v.Payload, &ack) != nil || ack.MatchID != r.match || ack.InputSeq != seq || ack.ServerTick == 0 {
		return ack, errors.New("실제 적용 ACK 검증 실패.")
	}
	return ack, nil
}
func movementPlayer(state room.Snapshot, id string) (game.Player, error) {
	if state.Phase != "PLAYING" || state.GameState == nil || len(state.GameState.Players) != movementClientCount || len(state.Members) != movementClientCount || len(state.LastProcessedInputSeq) != movementClientCount {
		return game.Player{}, errors.New("6인 PLAYING 공개 상태 검증 실패.")
	}
	for _, player := range state.GameState.Players {
		if player.SessionID == id {
			return player, nil
		}
	}
	return game.Player{}, errors.New("이동 참가자 없음.")
}
func runMovementChecks(ctx context.Context, url string) error {
	var clients []*checkClient
	readCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		for _, client := range clients {
			client.conn.CloseNow()
		}
		wg.Wait()
	}()
	for i := 0; i < movementClientCount; i++ {
		c, err := connect(ctx, url)
		if err != nil {
			return err
		}
		clients = append(clients, c)
	}
	v, err := clients[0].request(ctx, "room.create", map[string]any{})
	if err = expect(v, err, ""); err != nil {
		return err
	}
	initial, err := created(v)
	if err != nil {
		return err
	}
	for _, client := range clients[1:] {
		v, err = client.request(ctx, "room.join", map[string]any{"room_number": initial.RoomNumber})
		if err = expect(v, err, ""); err != nil {
			return err
		}
	}
	r := movementReceiver{messages: make(chan movementMessage, movementCaptureLimit), failure: make(chan error, 1), captures: make(map[captureKey]*movementCapture)}
	r.start(readCtx, clients, &wg)
	id, err := clients[0].send(ctx, "room.start", map[string]any{})
	if err != nil {
		return err
	}
	v, err = r.response(ctx, 0, "room.start", id)
	if err != nil {
		return err
	}
	var started struct {
		Snapshot room.Snapshot `json:"snapshot"`
	}
	if json.Unmarshal(v.Payload, &started) != nil || started.Snapshot.MatchID == nil || *started.Snapshot.MatchID == "" {
		return errors.New("시작 match 없음.")
	}
	r.match = *started.Snapshot.MatchID
	// Periodic captures prove all six readers see the same state, even if start events arrived before the response.
	state, err := r.snapshot(ctx, started.Snapshot.SnapshotSeq, 1)
	if err != nil {
		return err
	}
	observedAt, firstTick, firstSeq := time.Now(), state.ServerTick, state.SnapshotSeq
	for index, client := range clients {
		origin, err := movementPlayer(state, client.sessionID)
		if err != nil {
			return err
		}
		if origin.Position != (game.Vec3{X: float64(index) * 2}) || origin.Velocity != (game.Vec3{}) {
			return errors.New("초기 spawn 검증 실패.")
		}
		move, err := r.action(ctx, clients, index, 1, game.Vec3{Z: 1})
		if err != nil {
			return err
		}
		previous := origin
		for sample := 0; sample < 2; sample++ {
			state, err = r.snapshot(ctx, state.SnapshotSeq, move.ServerTick)
			if err != nil {
				return err
			}
			player, err := movementPlayer(state, client.sessionID)
			if err != nil {
				return err
			}
			if state.LastProcessedInputSeq[client.sessionID] != 1 || player.Velocity != (game.Vec3{Z: 5}) || player.Position.X != origin.Position.X || player.Position.Y != origin.Position.Y || player.Position.Z <= previous.Position.Z {
				return errors.New("양의 Z 이동·불변 X/Y·속도 5·seq1 검증 실패.")
			}
			previous = player
		}
		stop, err := r.action(ctx, clients, index, 2, game.Vec3{})
		if err != nil {
			return err
		}
		if stop.ServerTick <= move.ServerTick {
			return errors.New("정지 ACK tick 순서 오류.")
		}
		state, err = r.snapshot(ctx, state.SnapshotSeq, stop.ServerTick)
		if err != nil {
			return err
		}
		stopped, err := movementPlayer(state, client.sessionID)
		if err != nil {
			return err
		}
		if state.LastProcessedInputSeq[client.sessionID] != 2 || stopped.Velocity != (game.Vec3{}) || stopped.Position.X != origin.Position.X || stopped.Position.Y != origin.Position.Y || stopped.Position.Z < previous.Position.Z {
			return errors.New("정지·seq2 검증 실패.")
		}
		for sample := 0; sample < 2; sample++ {
			state, err = r.snapshot(ctx, state.SnapshotSeq, state.ServerTick+1)
			if err != nil {
				return err
			}
			player, err := movementPlayer(state, client.sessionID)
			if err != nil {
				return err
			}
			if player != stopped || state.LastProcessedInputSeq[client.sessionID] != 2 {
				return errors.New("정지 뒤 위치 유지 검증 실패.")
			}
		}
		fmt.Printf("이동 client %d: move ACK tick=%d, stop ACK tick=%d, capture=%d, z=%g → %g\n", index+1, move.ServerTick, stop.ServerTick, state.SnapshotSeq, origin.Position.Z, stopped.Position.Z)
	}
	fmt.Printf("수신 관측: elapsed=%s, tick delta=%d, capture seq delta=%d (목표 Hz 성능 assertion 아님)\n", time.Since(observedAt).Round(time.Millisecond), state.ServerTick-firstTick, state.SnapshotSeq-firstSeq)
	select {
	case err := <-r.failure:
		return err
	default:
	}
	for index, client := range clients {
		id, err = client.send(ctx, "room.leave", map[string]any{})
		if err != nil {
			return err
		}
		if _, err = r.response(ctx, index, "room.leave", id); err != nil {
			return err
		}
	}
	// Verify registry cleanup after all six membership leave acknowledgements.
	id, err = clients[0].send(ctx, "room.list", map[string]any{})
	if err != nil {
		return err
	}
	v, err = r.response(ctx, 0, "room.list", id)
	if err != nil {
		return err
	}
	var listed struct {
		Rooms []room.Snapshot `json:"rooms"`
	}
	if json.Unmarshal(v.Payload, &listed) != nil {
		return errors.New("정리 후 목록 해석 실패.")
	}
	for _, state := range listed.Rooms {
		if state.RoomID == initial.RoomID {
			return errors.New("이동 검사 빈 룸이 남음.")
		}
	}
	select {
	case err := <-r.failure:
		return err
	default:
	}
	fmt.Println("이동 membership 6개 퇴장 ACK 및 빈 룸 제거 확인.")
	return nil
}
