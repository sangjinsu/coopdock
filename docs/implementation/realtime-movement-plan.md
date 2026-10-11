# Realtime Movement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go 서버에서 1~6명이 3D 이동 의도를 보내면 서버가 고정 tick으로 위치를 계산하고 실제 적용 ACK와 일관된 full snapshot을 전달한다.

**Architecture:** 룸 actor가 match·입력 FIFO·게임 계산·번호를 단독 소유한다. `internal/game`의 reference handler는 지속 방향과 서버 속도로 3D 위치를 계산한다. 연결 processor는 입력을 비동기 접수하고, 단일 writer는 중요 FIFO와 최신 snapshot 한 칸을 전송 순서 fence 아래 소비한다.

**Tech Stack:** `roomserver`, Go `1.26.0` 이상, `net/http`, `github.com/coder/websocket v1.8.15`, 기존 `golang.org/x/crypto v0.58.0`; 새 dependency 없음.

**Spec:** [룸·세션](../specs/02-rooms-and-sessions.md), [실시간 동기화](../specs/03-realtime-sync.md), [메시지·API](../specs/04-messages-and-api.md), [구현·설정](../specs/07-implementation-and-config.md), [검증](../specs/08-validation.md), [전체 설계](../go-room-server-design.md). 구현 기준 commit은 `189ba87257bc7efaae498c04c45f06beffe1d963`, branch는 `codex/room-entry`다. 사용자 추가 결정은 **Go 먼저, 3D x/y/z, 작업마다 local commit, Godot 제외**다. 아래 구체 수치·API·종료 payload는 기존 문서가 확정하지 않은 이번 구현 선택이다.

## Global Constraints

- 문서의 확정 범위: `Go 우선, 룸당 1~6명, 실시간 이동·협동, 향후 Godot 연결, 운영 대시보드`.
- 문서의 번호·정원 값: `숫자 6자리 문자열`, `허용 1~6명, 기본 6명 제안`. 기존 구현의 기본 6과 선행 0을 유지한다.
- 문서의 비밀번호 값: `4~64 Unicode 문자(code point)`, 대소문자 구분, 임의 trim·정규화 없음. password/hash/salt는 어떤 공개 snapshot·응답·로그에도 넣지 않는다.
- 문서의 초기 계산·전송 값: `30Hz tick / 15Hz snapshot 제안`. 실제 도착 지연·처리 성능 보장이 아니다.
- 문서의 cleanup 용량: `룸 정원 이상, 전용 용량`. 실제 최소값 6을 유지한다.
- `input_seq`는 세션 수명, `server_tick`은 match 수명, `snapshot_seq`는 룸 수명이다. 새 match에서 input_seq·snapshot_seq를 초기화하지 않는다.
- 요청 응답은 동일 op·request_id, 서버 이벤트는 `request_id:null`. 모든 `game.action`에도 비어 있지 않은 request_id가 필요하다. enqueue 성공을 적용 ACK로 내보내지 않는다.
- actor만 룸·게임 상태를 변경한다. Manager 잠금 순서 `Manager.mu → Session.mu` 유지; 잠금 아래 handler 호출·channel 대기·socket I/O 금지.
- PLAYING 신규 입장·재입장은 `ROOM_IN_PROGRESS`. host 퇴장 시 가장 먼저 입장한 잔여 참가자에게 승계; 마지막 퇴장은 룸 제거.
- guest 재연결은 새 세션이며 자동 복원 없음. 기존 generation·membershipToken·private 검증의 room ID 결합을 보존한다.
- 코드 주석은 영어, 설명은 한국어. 기존 공식 API 조사 `work/movement/official-api-research.md`(당시 로컬 조사 기록; 공개 저장소에 포함하지 않음)를 먼저 읽는다.
- 이번 결과는 free-space reference 이동이다. 지형·충돌·중력·구체 협동 목표·승패·Godot 표시/보간/예측·운영 API/audit를 임의로 추가하지 않는다.
- 사용자는 실행과 작업별 local commit을 승인했다. 계획 재승인 대기 없이 Task 1→2→3→4 순서로 실행하고 소유 파일만 stage한다. 다른 작업자의 변경을 되돌리지 않는다.

## Review Focus

1. **입력 burst와 잘못된 숫자:** null/누락/overflow/NaN/Inf를 거절하고 메시지 수로 이동 속도가 커지지 않는다. 소유 Task 1·3, 시험 `TestMovementBurstDoesNotAccelerate`, `TestDecodeMovementStrict`.
2. **계산 지연·FIFO 포화·ACK:** tick 완료 전 success ACK가 없고 포화/timeout/종료된 요청은 실패하며 다른 요청 수신이 ACK 대기로 멈추지 않는다. 소유 Task 2·3, 시험 `TestInputAckAfterTickAndOverflow`, `TestPipelinedActionsDoNotWaitForAck`.
3. **이전 membership·이전 match·새 판:** 옛 입력/cleanup이 재입장한 소속이나 새 판을 바꾸지 않고 session seq·room snapshot seq가 역행하지 않는다. 소유 Task 2, 시험 `TestMatchRestartAndStaleMembership` 및 기존 세 cleanup 회귀 시험.
4. **writer 경계·느린 수신자:** 이미 쓰기 시작한 snapshot 외에는 join/start/end 중요 묶음을 앞지르지 않고 최신 상태 교체가 ACK·일회성 이벤트를 지우지 않는다. 소유 Task 3, 시험 `TestOutboxFenceAtWriteBoundary`, `TestSnapshotReplacementPreservesReliable`.
5. **동시 상태 복사·퇴장·shutdown:** 보낸 snapshot이 나중 tick에 변하지 않고 PLAYING host 퇴장·hook 실패·ticker Stop 후 대기·빈방 종료에서 deadlock/바쁜 반복이 없다. 소유 Task 1·2, 시험 `TestStateCloneIsIndependent`, `TestPlayingLeaveAndTickerShutdown`.

---

## 이번 구현에서 구체화하는 계약

### 1. 방향·시간·공개 상태

- 좌표는 오른손/왼손 렌더링 규칙에 종속시키지 않는 `x/y/z` 축의 추상 world unit이다. 초기 참가자는 입장 순서 index `i`에 `(2*i,0,0)`으로 spawn하며 velocity는 0이다. 속도 상수는 초당 `5` world units다.
- action은 `{"type":"move","direction":{"x":1,"y":0,"z":0}}`. 각 축은 필수 numeric scalar, finite, `[-1,1]`. 길이가 1을 넘으면 단위 벡터로 정규화하고 1 이하면 크기를 보존한다. `(0,0,0)`은 정지다. 클라이언트 position/speed/dt/timestamp 또는 알려지지 않은 필드는 거절한다.
- move는 지속 방향 갱신이다. 입력이 안 와도 마지막 승인 방향을 유지한다. 동일 tick의 입력을 FIFO로 모두 `Apply`한 뒤 `Tick(1/float64(TickHz))`을 한 번 호출한다. 입력마다 별도 dt를 적분하지 않는다. 앞선 방향이 같은 tick의 뒤 방향으로 바뀌더라도 각 setter는 실행되며 각각 ACK한다. ACK는 이동 거리 보장이 아니라 방향 갱신이 해당 계산 단계에 포함됐다는 뜻이다.
- 공개 `game_state`는 `{"players":[{"session_id":"...","position":{"x":0,"y":0,"z":0},"velocity":{"x":0,"y":0,"z":0}}]}`. players 순서는 살아 있는 참가자의 입장 순서다. 타입은 값 벡터와 값 player slice이며 `any` 상태를 공유하지 않는다.
- 독립 tick/snapshot ticker는 PLAYING 시작 시 생성하고 종료 시 Stop 후 select 채널을 nil로 만든다. 수신한 tick마다 고정 dt를 한 번만 진행한다. 놓친 timer tick을 보충하지 않는다. 부하 중 simulation은 wall time보다 느려질 수 있다. elapsed를 큰 dt로 바꾸거나 무제한 catch-up하지 않는다.
- ticker가 전달한 예정 시각을 현재 시각으로 쓰지 않는다. deadline 판정은 실제 처리 시점의 monotonic `time.Now()`를 사용한다. 기존 password TTL용 `cfg.now`와 simulation clock을 혼용하지 않는다.

### 2. 입력·번호·실제 ACK

- `input_seq`는 JSON 정수 `1..9007199254740991`이며 gap을 허용한다. typed uint64로 읽고 safe-integer 상한을 별도로 검사한다. 0·음수·소수·지수 표기·string·null은 거절한다.
- session에는 **성공 적용 완료 번호** `lastProcessedInputSeq`만 유지한다. actor는 입력 접수와 적용 직전에 membershipToken·room ID·match·형식·순서를 확인한다. 같은 batch의 앞 입력 적용을 포함해 `seq <= lastProcessedInputSeq`면 거절한다. 거부·timeout·미적용 취소는 번호를 소비하지 않는다. 따라서 실패한 번호는 이후 유효한 요청에 재사용할 수 있지만 이미 더 큰 번호가 적용됐으면 역순으로 거절된다.
- 같은 session에서 아직 미적용인 번호의 중복/역순은 큐에 남아 있을 수 있으나 실제 tick 검증에서 정확히 한 번만 적용된다. request_id 중복은 idempotency 기능이 아니며 seq가 중복 적용을 막는다.
- 입력용 inbox+actor pending FIFO의 **합계**가 `InputQueue`를 넘지 않게 shared credit를 쓴다. Manager는 credit을 nonblocking 확보하고 입력 채널에 넣는다. actor는 complete/reject/cleanup 시 한 번만 반환한다. cleanup 전용 큐와 일반 command 큐를 입력 큐가 차용하지 않는다. SubmitInput의 registry/session/token 확인과 nonblocking credit 확보·enqueue는 Manager.mu→Session.mu 잠금 안에서 원자적으로 한다(대기하지 않는다). actor 최종 종료는 같은 Manager.mu 아래 registry를 먼저 제거해 새 enqueue를 차단한 뒤 잔여 receipt를 실패/반환 처리한다. done과 send가 동시에 ready여서 죽은 actor에 요청이 남는 경합을 막는다.
- tick 시작 시 input inbox의 현재 길이만큼을 pending FIFO 뒤에 옮긴 다음 최대 `InputsPerTick`개만 처리한다. 따라서 수동 tick 전에 SubmitInput이 반환한 입력이 actor select 순서 때문에 다음 tick으로 밀리지 않는다. 입력 수신은 actor가 계속 받을 수 있지만 credit 총합으로 유한하다. cleanup 우선 확인 후 이미 ready인 tick을 먼저 처리해 일반 명령/입력 flood가 계산을 무한 연기하지 않도록 한다.
- `SubmitInput`의 nil 반환은 접수 사실일 뿐이다. deferred 결과는 session reliable `Event{Op:"game.action",RequestID:...,Ack:...,Err:...}`로 전달한다. processor는 입력별 goroutine이나 ACK 대기 없이 다음 ingress를 처리한다.
- 각 입력 deadline은 제출 시 `min(caller deadline, now+room.RequestTimeout)`을 복사한다. 제출 후 caller context 자체를 보관하지 않는다. transport의 접수 함수 반환 후 cancel은 이미 접수된 동작을 즉시 취소하지 않는다. 연결 종료는 session/membership 검증으로 취소한다. tick 직전 deadline이 지나면 `REQUEST_TIMEOUT`이다.
- 한 tick은 유효 입력을 순서대로 Apply → 게임 Tick → `server_tick++` → 성공한 입력들의 session seq 확정 → 해당 tick 번호로 ACK 순서다. Apply 성공 후보의 seq 순서는 batch-local high-water로 검사하며 Tick 실패 전에는 session 번호를 확정하지 않는다. Tick error면 후보들을 `GAME_ERROR`로 실패시키고 게임을 종료한다.
- ACK payload는 `{"match_id":"...","input_seq":42,"server_tick":1}`. 같은 tick의 여러 ACK에는 같은 tick이 올 수 있다. 정상 종료를 결정한 마지막 성공 tick도 ACK들을 먼저 reliable FIFO에 넣고 game events·ended 순서를 따른다.
- 오류 추가: `NOT_HOST`(기존 문서명), `GAME_NOT_STARTED`, `STALE_MATCH`, `INVALID_INPUT_SEQ`, `INPUT_CANCELED`, `GAME_ERROR`. 형식/방향 오류는 `INVALID_MESSAGE`, 입력 포화는 `SERVER_BUSY`. 오류 message는 영어 고정 문자열이며 payload 전문을 출력하지 않는다.

### 3. match 수명·snapshot 캡처

| 시점 | phase / match / tick / 게임 | seq·revision·snapshot 규칙 |
|---|---|---|
| 최초 WAITING | WAITING / null / 0 / null | 참가자별 값은 session 성공 seq(새 session은 0); 기존 metadata 변경마다 revision 증가 |
| room.start | PLAYING / 서버 새 ID / 0 / 새 handler state | 현재 host만 허용, 인원 1도 가능. revision 1 증가, 참가자 session seq 유지 |
| 성공 tick | PLAYING / 동일 ID / 완료 tick+1 / 계산 결과 | 위치·방향·처리 seq 등 공개 상태가 변했으면 revision을 tick당 1 증가; idle tick은 revision 유지 |
| handler 정상 종료 | WAITING / null / 0 / null | 이전 match ID·마지막 tick은 ended payload에 보관. revision 1 증가, 참가자 session seq 유지, pending 입력은 INPUT_CANCELED |
| PLAYING 일부 퇴장 | 잔여 match 유지 | handler.OnLeave로 player 제거, host 승계, 해당 token 입력 취소. hook error면 membership은 제거한 채 GAME_ERROR 종료 |
| 마지막 퇴장/Close | CLOSED 후 registry 제거 | 모든 pending/inbox credit 정리, ticker Stop, done close. 빈방 초기 unpublished actor는 tick 때문에 조기 제거하지 않음 |

- `capture()`는 actor 안에서 현재 state·tick·seq map을 함께 복사하며 **full 상태를 새로 캡처할 때마다** room `snapshot_seq`를 1 증가시킨다. create/join/start/end/list/room.state/주기 snapshot 모두 해당한다. 하나의 capture를 여러 수신자 또는 response+event에 재사용하면 같은 seq다. snapshot 생성은 revision을 바꾸지 않는다.
- 시작/종료 시 room snapshot sequence는 유지한다. room.state의 metadata는 revision으로 판단하고 주기 game.snapshot의 match/seq 필터와 분리한다.
- `room.start` 요청 payload는 `{}`, 성공 payload는 `{"snapshot":fullState}`. `game.started` 이벤트 payload도 기존 spec대로 `{"snapshot":fullState}` wrapper다. requester에는 response → game.started(snapshot) → room.state 순서다. 다른 멤버에는 game.started → room.state 순서다.
- handler는 `Tick` 또는 `OnLeave` outcome의 비어 있지 않은 `EndReason`으로 종료한다. reference handler는 자동 종료 조건이 없다. 테스트 handler로 정상 종료/다음 match를 검증한다. 새 client `room.end` op나 임의 제한 시간·승패 규칙을 추가하지 않는다.
- `game.ended` payload는 이번 선택으로 `{"match_id":"old","reason":"handler reason","server_tick":lastCompletedTick,"snapshot":waitingFullState}`이며 이후 room.state도 같은 waitingFullState를 쓴다.
- handler 생성·Start 실패는 `GAME_ERROR`이며 WAITING을 유지한다. OnLeave는 membership 제거와 잠금 해제 후 호출한다. hook 오류가 이미 끊긴 참가자를 복원하면 안 된다.

### 4. 전송 경계와 immutable 값

- core `Session.Events()`는 reliable `Event` 단일 FIFO다. room.state, started, ended, game.event, action 완료를 같은 경로로 보낸다. 주기 snapshot은 별도 최신 한 칸 + wake 채널로 넘긴다. reliable full이면 기존처럼 해당 session을 닫고 actor가 직접 cleanup한다.
- lifecycle core Event 하나에 전환 snapshot을 동봉한다. transport가 started 또는 ended Event를 해당 wire event+room.state **원자 batch**로 전개한다. 두 core 채널에 start와 room.state를 나눠 재정렬시키지 않는다.
- peer outbox는 mutex 아래 bounded reliable FIFO와 latest snapshot 한 칸을 함께 관리한다. writer가 다음 메시지를 고르는 순간 같은 lock으로 reliable 우선 pop 후 latest pop을 한다. 임의 select의 랜덤 선택을 우선순위라고 취급하지 않는다.
- `enqueueBatch(clearLatest=true, messages...)`는 queue 여유를 전체 batch에 대해 확인하고 이전 latest를 폐기한 뒤 중요 메시지들을 연속 등록한다. 여유 없으면 부분 등록하지 않고 연결을 닫는다. create/join/start/end/leave 전환에 사용한다.
- snapshot을 pop했어도 아직 실제 Write를 시작하지 않았다면 transition epoch를 재확인한다. epoch가 변했거나 reliable이 생겼으면 snapshot을 폐기/되돌려 reliable을 먼저 보낸다. writer의 **write 시작 확정**은 outbox lock 아래 이 재검증을 통과해 in-flight로 표시하는 순간이다. 이 짧은 선형화점 이후의 이전 snapshot은 단일 writer에서 끝난 다음 transition을 보낸다. socket I/O 동안 outbox lock을 잡지 않는다.
- processor는 membership token, room ID, active match를 먼저 검사한다. game.started 처리 전 새 match snapshot은 저장/전송하지 않는다. ended 처리 뒤 옛 match snapshot도 저장하지 않는다. 중요 초기 snapshot에는 주기 snapshot의 오래된 seq 필터를 적용하지 않는다.
- self start는 Manager.Start 결과로 response+started+room.state를 원자 등록한다. core의 동일 started Event는 `(membershipToken,matchID,startRevision)`으로 중복 제거한다. 일반 room.state의 revision 기준 갱신과 lifecycle event 중복 기준을 혼합해 paired room.state를 지우지 않는다.
- action 완료 응답은 room을 이미 떠났더라도 RequestID가 있고 연결이 살아 있으면 전달한다. room/match 필터는 성공/실패 응답에 적용하지 않는다. 이미 완료된 입력 ACK가 leave 응답보다 늦게 와도 같은 request_id와 적용 당시 match/tick으로 상관관계가 유지된다.
- `Snapshot.GameState`를 `*game.State`로 바꾸고 State.Clone에서 players slice까지 복제한다. session latest와 reliable payload마다 snapshot.clone을 사용한다. marshal은 writer에서 하며 actor mutable map/slice가 외부에 새지 않는다.

## 공통 실행·검증 환경

모든 명령은 repo 루트에서 실행한다. cache 경로는 이 작업 공간에 고정한다.

```bash
export GOCACHE="$PWD/work/go-build"
export GOMODCACHE="$PWD/work/go-mod"
export GOTMPDIR="$PWD/work/tmp"
export GOTOOLCHAIN=local
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
```

각 worker의 보고서는 `.superpowers/sdd/realtime-movement-plan/task-N-report.md`에 범위, 변경 파일, RED 명령/실제 실패 이유, GREEN 명령/exit code, full-suite 결과, commit SHA, 잔여 제약을 적는다. 상태는 `DONE`, `DONE_WITH_CONCERNS`, `NEEDS_CONTEXT`, `BLOCKED` 중 하나다. 의도된 RED를 완료 시 남은 실패로 보고하지 않는다. 미실행을 PASS로 쓰지 않는다. `NEEDS_CONTEXT`/`BLOCKED`면 구체적으로 빠진 계약/명령/오류를 보고하고 다른 파일로 우회 수정하지 않는다.

## Task 1: 테스트 가능한 reference 3D GameHandler

**독립 산출물:** 네트워크 없이 3D 방향·속도·spawn·복사를 검증하는 game package.

**Files:** Create `internal/game/handler.go`, `internal/game/movement.go`, `internal/game/movement_test.go`.

**Interfaces — consumes:** 표준 `math`만; room/transport import 없음.

**Interfaces — produces (Task 2가 정확히 소비):**

```go
package game
const MoveSpeed = 5.0
const SpawnSpacing = 2.0
type Vec3 struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
    Z float64 `json:"z"`
}
type Action struct {
    Type string `json:"type"`
    Direction Vec3 `json:"direction"`
}
type Player struct {
    SessionID string `json:"session_id"`
    Position Vec3 `json:"position"`
    Velocity Vec3 `json:"velocity"`
}
type State struct { Players []Player `json:"players"` }
func (s State) Clone() State
type Event struct { Type, SessionID string }
type Outcome struct { Changed bool; Events []Event; EndReason string }
type GameHandler interface {
    Start(sessionIDs []string) error
    Validate(action Action) error
    Apply(sessionID string, action Action) error
    Tick(dtSeconds float64) (Outcome, error)
    OnLeave(sessionID string) (Outcome, error)
    Snapshot() State
}
func NewMovement() GameHandler
```

모든 wire 필드에 위 계약의 snake_case JSON tag를 붙인다. handler는 room error에 의존하지 않고 고정 영어 error를 반환하며 room이 wire code로 매핑한다. Apply 실패 시 state 변경 없음. Start는 입력 순서대로 players를 만들며 수명은 match 하나다. Tick의 dt는 양의 finite만 허용한다.

- [ ] **Step 1: RED 시험을 작성한다.** 최소 아래 두 시험과 zero-stop·analog magnitude·OnLeave 제거·copy 독립을 추가한다.

```go
func TestMovementBurstDoesNotAccelerate(t *testing.T) {
    h := NewMovement()
    if err := h.Start([]string{"a", "b"}); err != nil { t.Fatal(err) }
    for i := 0; i < 100; i++ {
        if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 1, Y: 1, Z: 1}}); err != nil { t.Fatal(err) }
    }
    if _, err := h.Tick(1.0 / 30); err != nil { t.Fatal(err) }
    p := h.Snapshot().Players[0]
    distance := math.Hypot(math.Hypot(p.Position.X, p.Position.Y), p.Position.Z)
    if math.Abs(distance-MoveSpeed/30) > 1e-12 { t.Fatalf("distance=%g", distance) }
    if h.Snapshot().Players[1].Position.X != SpawnSpacing { t.Fatal("spawn changed") }
}
func TestDirectionRejectsInvalid(t *testing.T) {
    h := NewMovement()
    for _, x := range []float64{1.01, -1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
        if h.Validate(Action{Type: "move", Direction: Vec3{X: x}}) == nil { t.Fatalf("accepted %v", x) }
    }
    if h.Validate(Action{Type: "teleport"}) == nil { t.Fatal("unknown action accepted") }
}
```

- [ ] **Step 2: RED 확인.** `go test ./internal/game -count=1`을 실행해 새 symbol/package 미구현 실패를 기록한다.
- [ ] **Step 3: 최소 구현.** 범위 검사 후 `n := math.Hypot(math.Hypot(x,y),z)`; n>1일 때만 세 축을 n으로 나눈다. Apply는 velocity를 `direction*MoveSpeed`로 갱신하고, Tick은 `position += velocity*dt`를 한 번 수행한다. Snapshot과 Clone 모두 players backing array를 분리한다. Apply로 velocity가 바뀐 사실을 dirty flag로 보존해 정지 입력도 다음 Tick의 Outcome.Changed에 포함한다.

```go
out := State{Players: append([]Player{}, s.Players...)}
// Return a detached copy so callers cannot mutate the handler.
return out
```

- [ ] **Step 4: GREEN 및 회귀.** `go test ./internal/game -count=1`, `go test ./... -count=1`. `TestStateCloneIsIndependent`는 반환 players를 바꾼 뒤 handler와 다른 snapshot이 변하지 않는지 검사한다. 방향을 한 번 입력하고 연속 두 tick을 실행해 지속 이동을, 0 방향 뒤에는 위치가 유지되는지 검사한다.
- [ ] **Step 5: 검토 후 local commit.** `git add internal/game/handler.go internal/game/movement.go internal/game/movement_test.go` 뒤 `git commit -m "feat: add authoritative 3d movement handler"`. task-1-report에 실제 결과와 SHA를 적는다.

## Task 2: room actor lifecycle·FIFO·고정 tick·snapshot core

**독립 산출물:** 수동 clock을 쓰는 core 시험으로 start→입력→완료 ACK→snapshot→leave/end/restart/close를 검증한다. 기존 서버 테스트 전체가 이 commit에서도 컴파일·통과해야 한다.

**Files:** Modify `internal/room/types.go`, `session.go`, `manager.go`, `actor.go`, `errors.go`, `manager_test.go`; Create `internal/room/realtime.go`, `realtime_test.go`. **호환 seam만 Modify** `internal/transport/server.go`: 기존 Events 소비를 Event.Snapshot으로 풀고 `Op=="room.state"`만 기존 경로에 넘긴다. 새 WS op dispatch와 outbox는 Task 3 소유다. root 승인으로 `internal/transport/server_test.go`의 단일 `Events().Snapshot` 호환 seam도 Task 2 소유에 포함한다.

**Interfaces — consumes:** Task 1의 `game.GameHandler`, `game.NewMovement()`, `game.Action`, `game.State.Clone()`, `game.Outcome` 및 기존 `Create`, `Join`, `Leave`, `Disconnect`, session identity/cleanup token.

**Interfaces — produces (Task 3·4가 정확히 소비):**

```go
const MaxInputSeq uint64 = 9007199254740991
type Input struct { MatchID string; InputSeq uint64; Action game.Action }
type ActionAck struct {
    MatchID string `json:"match_id"`
    InputSeq uint64 `json:"input_seq"`
    ServerTick uint64 `json:"server_tick"`
}
type Ended struct {
    MatchID string `json:"match_id"`
    Reason string `json:"reason"`
    ServerTick uint64 `json:"server_tick"`
}
type GameEvent struct {
    EventID string `json:"event_id"`
    MatchID string `json:"match_id"`
    ServerTick uint64 `json:"server_tick"`
    Type string `json:"type"`
    SessionID string `json:"session_id"`
}
type Event struct {
    Op string
    Snapshot Snapshot
    MembershipToken uint64
    RequestID string
    Ack *ActionAck
    Err error
    Ended *Ended
    GameEvent *GameEvent
}
func (m *Manager) Start(ctx context.Context, s *Session) (Snapshot, error)
func (m *Manager) SubmitInput(ctx context.Context, s *Session, requestID string, in Input) error
func (s *Session) Events() <-chan Event
func (s *Session) SnapshotReady() <-chan struct{}
func (s *Session) TakeSnapshot() (Snapshot, bool)
func (s *Session) Membership() (roomID string, token uint64)
```

`Snapshot.GameState`는 기존 `game_state` JSON tag를 유지한 `*game.State`로 바꾼다. Config에 `TickHz, SnapshotHz, InputQueue, InputsPerTick int`, `HandlerFactory func() game.GameHandler`를 추가한다. 기본값은 `30,15,256,64,game.NewMovement`. nil factory는 NewManager에서 default로 채운다. 검증은 `1<=TickHz<=120`, `1<=SnapshotHz<=TickHz`, `1<=InputQueue<=4096`, `1<=InputsPerTick<=InputQueue`. 비공개 `newTicker func(time.Duration) (<-chan time.Time, func())`를 추가하며 nil이면 time.NewTicker의 C/Stop wrapper를 사용한다. `Membership()`은 Session.mu 아래 복사만 반환한다.

- [ ] **Step 1: 핵심 RED를 작성한다.** 아래 시험은 기존 room 테스트의 `setup`, `connect`, `create` helper를 쓴다. awaitGameEvent helper는 실제로 정의한다.

```go
func awaitGameEvent(t *testing.T, s *Session, op string) Event {
    t.Helper()
    timeout := time.NewTimer(2*time.Second)
    defer timeout.Stop()
    for {
        select {
        case e := <-s.Events():
            if e.Op == op { return e }
        case <-timeout.C: t.Fatalf("missing %s", op)
        }
    }
}
func TestInputAckAfterTickAndOverflow(t *testing.T) {
    ticks := make(chan time.Time, 1)
    snapshots := make(chan time.Time, 1)
    cfg := DefaultConfig()
    cfg.InputQueue, cfg.InputsPerTick = 1, 1
    cfg.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
        if d == time.Second/time.Duration(cfg.TickHz) { return ticks, func() {} }
        return snapshots, func() {}
    }
    m := setup(t, cfg)
    h := connect(t, m, "host")
    create(t, m, h, CreateOptions{})
    initial, err := m.Start(context.Background(), h)
    if err != nil { t.Fatal(err) }
    awaitGameEvent(t, h, "game.started")
    in := Input{MatchID: *initial.MatchID, InputSeq: 1, Action: game.Action{Type: "move", Direction: game.Vec3{Z: 1}}}
    if err := m.SubmitInput(context.Background(), h, "a", in); err != nil { t.Fatal(err) }
    in.InputSeq = 2
    if got := CodeOf(m.SubmitInput(context.Background(), h, "b", in)); got != "SERVER_BUSY" { t.Fatal(got) }
    select {
    case e := <-h.Events(): if e.Op == "game.action" { t.Fatal("ACK before tick") }
    default:
    }
    ticks <- time.Now()
    e := awaitGameEvent(t, h, "game.action")
    if e.Err != nil || e.Ack == nil || e.Ack.InputSeq != 1 || e.Ack.ServerTick != 1 { t.Fatalf("bad completion: %+v", e) }
    snapshots <- time.Now()
    select { case <-h.SnapshotReady(): case <-time.After(2*time.Second): t.Fatal("missing snapshot") }
    state, ok := h.TakeSnapshot()
    if !ok || state.ServerTick != 1 || state.LastProcessedInputSeq[h.ID()] != 1 { t.Fatal("inconsistent capture") }
    if math.Abs(state.GameState.Players[0].Position.Z-game.MoveSpeed/30) > 1e-12 { t.Fatal("wrong displacement") }
}
```

- [ ] **Step 2: RED 확인.** `go test ./internal/room -run 'TestInputAckAfterTickAndOverflow' -count=1`. missing API로 실패를 기록한다. imports는 context/math/time/game 및 기존 testing을 명시한다.
- [ ] **Step 3: core를 구현한다.** `realtime.go`에 match 상태·입력 receipt/deadline·credit·tick/끝 처리·snapshot clone을 모은다. actor loop에서 cleanup→ready tick→일반 select 순서를 유지하고 start의 admission gate 재검사·PLAYING join 거절을 실제 commit 지점에 추가한다. 새 handler 생성·Start는 잠금 밖에서 준비하고 실제 PLAYING commit 바로 전에 gate/session/token/host/phase를 다시 검사한다. handler를 잠금 아래 호출하지 않는다. Start는 caller deadline을 actor commit 직전 검사하고 commit 이후 결과가 준비됐다면 단순 timeout으로 실패를 뒤집지 않는다.

```go
// A successful enqueue is not an acknowledgement of application.
select {
case r.inputSlots <- struct{}{}:
    // The receipt holds the room ID, membership token, deadline and request ID.
case <-ctx.Done():
    return ctx.Err()
default:
    return failure("SERVER_BUSY")
}
```

위 slot 확보 후 enqueue 실패/room.done/context 취소 시 반드시 반환한다. actor pending으로 옮길 때 반환하지 않는다. stop 시 nil ticker channel과 done을 사용하며 입력/event 채널을 외부 sender와 경쟁해 닫지 않는다. reliable overflow 때 actor 자신의 cleanup completion을 기다리지 않고 직접 remove한다.

- [ ] **Step 4: lifecycle RED→GREEN을 추가한다.** 아래 명시된 test handler와 action script를 사용한다.

```go
type endingHandler struct { game.GameHandler; ticks int }
func (h *endingHandler) Tick(dt float64) (game.Outcome, error) {
    out, err := h.GameHandler.Tick(dt)
    h.ticks++
    if h.ticks == 2 { out.EndReason = "test_complete" }
    return out, err
}
```

| 시험 이름 | 입력·barrier와 필수 assertion |
|---|---|
| `TestMatchRestartAndStaleMembership` | factory로 endingHandler 주입; seq 5를 tick1 적용, tick2 정상 종료. WAITING null match/tick0/map5 확인. restart는 다른 match/tick0/seq5/이전보다 큰 snapshot_seq. 옛 match seq6 거절 후 새 match seq6 성공, seq5 거절. 옛 membership receipt를 보류한 뒤 leave/rejoin/start하고 재개하여 새 player 불변 확인 |
| `TestRejectedSequenceIsNotConsumed` | malformed direction seq9 및 stale match seq9 실패 뒤 유효 seq8 성공. 다음 seq7 실패; seq10·11을 같은 tick에 FIFO 적용해 ACK 두 개/tick 동일/최종 방향 11 확인 |
| `TestInputDeadlineAndEndCancel` | tick 전 deadline 만료 입력은 REQUEST_TIMEOUT, seq 미소비. budget 1에서 두 번째 입력이 pending인 채 handler 종료하면 INPUT_CANCELED; success ACK 없음; 모든 credit 반환 |
| `TestPlayingLeaveAndTickerShutdown` | host+member start, host leave→member host/PLAYING/player 제거. 마지막 leave→room 없음/Stop 정확히 각1회/Close 반환. OnLeave 오류 handler도 membership을 복원하지 않고 WAITING 전환. stopped ticker 채널은 닫지 않고 남겨 둔다 |
| `TestNoCatchUpAndAtomicSnapshot` | tick 예정 timestamp를 오래된 값으로 한 번 보내도 tick1/이동5/30. snapshot을 얻고 별도 tick 후 첫 snapshot·seq map·players가 바뀌지 않음 |
| `TestOneShotEventOrder` | test handler가 Tick outcome에 Event{Type:"pulse",SessionID:host} 1개 생성; ACK→game.event 순서, event_id 비어 있지 않음·중복 없음; 다음 snapshot 교체가 reliable event를 지우지 않음 |

RED는 각 새 시험을 구현 전에 실행하고 GREEN은 구현 후 같은 명령으로 확인한다. 기존 `TestTimeoutAfterCommitCleansBeforeRejoin`, `TestCanceledCommittedJoinCannotRemoveRejoin`, `TestRequestDeadlineDuringHashCannotReplaceRejoin`, `TestEventOverflowCleansActorWithoutDeadlock`를 유지한다. 기존 `[]Snapshot{v, <-h.Events()}`는 Event.Snapshot으로 갱신한다.

- [ ] **Step 5: 호환 adapter와 전체 검증.** 현재 transport Events case에서 `event.Op!="room.state"`면 넘기고 `state:=event.Snapshot`을 기존 logic에 사용한다. `go test ./internal/room -count=1`, `go test -race ./internal/room -count=1`, `go test ./... -count=1`을 실행한다. Task 2는 아직 새 wire op를 제공하지 않는다는 범위를 보고한다.
- [ ] **Step 6: 검토 후 local commit.** 위 Files에 열거된 파일만 `git add`하고 `git commit -m "feat: run movement matches in room actors"`. task-2-report에 결과·SHA·adapter 경계를 남긴다.

## Task 3: strict WebSocket 계약·비동기 ACK·두 송신 경로

**독립 산출물:** 실제 WS로 start/move/stop과 reliable/lifecycle 순서를 검증한다. 신규 config 값은 아직 기본값으로 실행할 수 있다.

**Files:** Modify `internal/protocol/message.go`, `message_test.go`, `internal/transport/server.go`, `server_test.go`; Create `internal/transport/outbox.go`, `outbox_test.go`, `movement_test.go`.

**Interfaces — consumes:** Task 2 공개 API 전체 및 Task 1 `game.Action`. SubmitInput에 성공 응답을 덧붙이지 않으며 Events 완료만 Response로 변환한다. SnapshotReady→TakeSnapshot은 최종 상태 하나만 읽는다.

**Interfaces — produces:** 기존 `Decode([]byte) (Request,error)`의 Request에 `Input room.Input` 추가. `room.start`는 `{}`로 decode. `transport.New`·`Handler`·`Close` 공개 signature는 유지. private outbox API는 아래와 같다.

```go
type outbox struct {
    mu sync.Mutex
    reliableQueue []protocol.Envelope
    latest *protocol.Envelope
    epoch uint64
    wake chan struct{}
    capacity int
}
func newOutbox(capacity int) *outbox
func (o *outbox) reliable(clearLatest bool, messages ...protocol.Envelope) bool
func (o *outbox) snapshot(v protocol.Envelope)
func (o *outbox) next(ctx context.Context) (protocol.Envelope, uint64, bool)
func (o *outbox) beginSnapshotWrite(epoch uint64) bool
```

`next`는 reliable이면 epoch=0, snapshot이면 현재 epoch>0을 반환한다. beginSnapshotWrite는 epoch 일치+현재 reliable 없음 조건을 lock 아래 검사해 in-flight를 확정한다. snapshot 선택 후 beforeWrite hook은 beginSnapshotWrite **전**에 호출해 fence 경합을 시험한다. false면 해당 snapshot을 폐기하고 next로 돌아간다. reliable 메시지는 hook 뒤 그대로 단일 writer에서 보낸다. wake 채널은 용량1 알림이며 queue 길이와 독립이다. close는 peer ctx 취소로 깨우고 mailbox 자체를 닫지 않는다.

- [ ] **Step 1: strict JSON RED.** 기존 envelope·duplicate-key·Unicode 검사를 그대로 사용하며 nested object마다 exact keys와 required presence를 검사한다.

```go
func TestDecodeMovementStrict(t *testing.T) {
    valid := `{"version":1,"op":"game.action","request_id":"a","payload":{"match_id":"m","input_seq":1,"action":{"type":"move","direction":{"x":1,"y":0,"z":0}}},"error":null}`
    if _, err := Decode([]byte(valid)); err != nil { t.Fatal(err) }
    for _, patch := range [][2]string{
        {`"z":0`, `"z":null`}, {`"z":0`, `"q":0`},
        {`"x":1`, `"x":1e400`}, {`"x":1`, `"x":1.1`},
        {`"input_seq":1`, `"input_seq":9007199254740992`},
        {`"input_seq":1`, `"input_seq":1.5`}, {`"input_seq":1`, `"input_seq":null`},
        {`"request_id":"a"`, `"request_id":null`},
    } {
        bad := strings.Replace(valid, patch[0], patch[1], 1)
        if _, err := Decode([]byte(bad)); err == nil { t.Fatalf("accepted %s", bad) }
    }
}
```

추가 table: 모든 필수 필드 누락/null/type mismatch, seq 0/-1/1e0/최댓값, y/z overflow, duplicate nested key, unknown position/dt, start payload null/extra, old unknown-op fixture를 `room.unknown`으로 교체. `go test ./internal/protocol -run TestDecodeMovementStrict -count=1`로 RED 기록 후 구현한다.

- [ ] **Step 2: writer fence RED→GREEN.** 아래 outbox 시험을 먼저 쓴 뒤 mutex 기반 두 lane을 구현한다.

```go
func TestOutboxFenceAtWriteBoundary(t *testing.T) {
    box := newOutbox(8)
    box.snapshot(protocol.Event("game.snapshot", map[string]int{"n":1}))
    picked, epoch, ok := box.next(context.Background())
    if !ok || picked.Op != "game.snapshot" || epoch == 0 { t.Fatal("not a snapshot") }
    if !box.reliable(true, protocol.Event("game.ended", struct{}{}), protocol.Event("room.state", struct{}{})) { t.Fatal("enqueue failed") }
    if box.beginSnapshotWrite(epoch) { t.Fatal("old snapshot crossed fence") }
    for _, want := range []string{"game.ended", "room.state"} {
        got, e, ok := box.next(context.Background())
        if !ok || e != 0 || got.Op != want { t.Fatal("reliable order") }
    }
}
```

`TestSnapshotReplacementPreservesReliable`에서는 snapshot100개를 게시해 마지막1개만 남기고 ACK와 game.event 2개가 먼저 모두 나오는지 검사한다. 작은 reliable capacity에서 batch 일부만 등록되는지, reliable queue 포화 시 peer stop/cleanup이 되는지도 검사한다. `go test ./internal/transport -run 'TestOutbox|TestSnapshotReplacement' -count=1` RED/GREEN 기록.

- [ ] **Step 3: processor·writer 통합.** 새 outbox를 peer에 설치하고 room.start/create/join/leave 전환은 원자 reliable batch로 등록한다. game.action은 SubmitInput 호출 뒤 e!=nil만 즉시 Response로 보내며 nil이면 다음 ingress로 돌아간다. core Event 완료는 원래 RequestID를 되살려 `protocol.Response(protocol.Request{Op:"game.action",ID:&id},event.Ack,event.Err)`로 보낸다. 별도 per-input goroutine/pending request map은 만들지 않는다.

```go
case "game.action":
    e = p.server.manager.SubmitInput(ctx, p.session, *r.ID, r.Input)
    if e == nil {
        cancel()
        continue
    }
```

실제 코드에서는 afterRequest test hook과 cancellation 확인을 공통 경로에서 유지하도록 dispatch 결과 `deferred bool`로 분리해도 된다. 초기 응답과 stale-membership filter를 기존 테스트와 함께 보존한다. 단일 reader·writer 수를 늘리지 않는다.

- [ ] **Step 4: 실제 WebSocket RED→GREEN.** 기존 setup/client/send/read helper로 다음 시험을 만들고 해당 regex를 구현 전후 실행한다.

```go
func TestWebSocketMovement(t *testing.T) {
    _, _, _, dial := setup(t, DefaultConfig())
    c := dial()
    c.success("room.create", map[string]any{})
    started := snapshot(t, c.success("room.start", map[string]any{}))
    c.send("game.action", map[string]any{"match_id": *started.MatchID, "input_seq":1,
        "action":map[string]any{"type":"move", "direction":map[string]int{"x":0,"y":0,"z":1}}})
    ack := c.response("game.action")
    var a room.ActionAck
    if ack.Error != nil || json.Unmarshal(ack.Payload, &a) != nil || a.ServerTick == 0 || a.InputSeq != 1 { t.Fatal("invalid ACK") }
    for {
        f := c.read()
        if f.Op != "game.snapshot" { continue }
        var state room.Snapshot
        if err := json.Unmarshal(f.Payload, &state); err != nil { t.Fatal(err) }
        if state.LastProcessedInputSeq[c.id] < 1 { continue }
        if state.ServerTick < a.ServerTick || state.GameState.Players[0].Position.Z <= 0 { t.Fatal("input not reflected") }
        break
    }
}
```

| 추가 시험 | 강제 상황과 결과 |
|---|---|
| `TestPipelinedActionsDoNotWaitForAck` | beforeProcess/afterRequest barrier로 첫 Submit 완료와 두 번째 입력 수신 확인; 두 request를 연속 보내고 첫 ACK 기다리기 전 두 번째 dispatch 진행. 같은 요청 1·2의 ACK를 ID별 수신하며 FIFO seq 확인 |
| `TestInitialAndLifecycleFences` | beforeWrite에서 snapshot을 선택 직후 정지; start/end event batch를 등록하고 재개해 old unsent snapshot 폐기. 이미 beginSnapshotWrite를 지난 경우 old snapshot→event→room.state 순서 확인. 최초 join response 전에 새 snapshot이 없음 |
| `TestPlayingAdmissionAndLeave` | 실제 두 WS start 후 올바른 private password의 join 거절. host disconnect 후 남은 player와 host ID 일치, 게임 계속 진행 |
| `TestSlowSnapshotReaderIsolation` | beforeWrite로 한 peer의 snapshot 정지, 많은 snapshot은 한 칸 교체되어 연결 유지. reliable 큐를 채우면 해당 peer close/cleanup; 다른 룸은 입력 ACK와 tick 진행 |
| `TestDeferredErrorSurvivesLeaveFilter` | 입력 접수 후 같은 연결 leave; action 실패 또는 이미 완료된 성공 ACK가 room filter로 사라지지 않으며 request_id 동일 |

end 통합 시험은 `room.Config.HandlerFactory`에 test ending handler를 넣어 `room.NewManager`+`New`+`httptest.NewServer`를 만드는 fixture로 수행한다. production `room.end`를 만들어 테스트 편의를 확보하지 않는다.

- [ ] **Step 5: 전체 GREEN.** `go test ./internal/protocol ./internal/transport -count=1`, `go test -race ./internal/transport -count=1`, `go test ./... -count=1`. 기존 initial/pipelined admission, ingress overflow, slow writer, shutdown deadline, accept race 시험도 그대로 통과해야 한다.
- [ ] **Step 6: 검토 후 local commit.** 위 Files만 stage하고 `git commit -m "feat: synchronize movement over websocket"`; task-3-report에 실제 wire 순서·검증·SHA를 남긴다.

## Task 4: 설정·6클라이언트 Go 검증·실행 문서

**독립 산출물:** 조정 가능한 실행 서버와 반복 가능한 외부 WS CLI가 같은 match/tick 공개 상태 및 실제 이동을 확인한다.

**Files:** Modify `internal/config/config.go`, `config_test.go`, `cmd/roomcheck/main.go`, `README.md`, `docs/specs/03-realtime-sync.md`, `04-messages-and-api.md`, `07-implementation-and-config.md`, `08-validation.md`; Create `cmd/roomcheck/movement.go`, `movement_test.go`. **root 승인 추가 Modify:** `docs/specs/README.md`, `01-overview.md`, `docs/go-room-server-design.md`의 상태 문구와 이 실행계획 `docs/implementation/realtime-movement-plan.md`의 ownership 기록. **Task3 I1 root 승인 추가 Modify:** `internal/transport/server.go`, `internal/transport/movement_test.go`의 두 game.started 송신 경로를 기존 spec의 `payload.snapshot` wrapper로 일치시키고 관련 시험만 갱신한다. ordering/dedup/fence/core API는 유지한다.

**Interfaces — consumes:** Task 2 Config 필드와 Snapshot/GameState/ActionAck 타입, Task 3의 공개 JSON 계약.

**Interfaces — produces:** 기존 `config.Load() (Config,error)` 유지. 환경 변수 `ROOM_TICK_HZ=30`(1..120), `ROOM_SNAPSHOT_HZ=15`(1..TickHz), `ROOM_INPUT_QUEUE=256`(1..4096), `ROOM_INPUTS_PER_TICK=64`(1..InputQueue). `func runMovementChecks(ctx context.Context,url string) error`; CLI flag `-movement`(bool, 기본 true). 기존 entry 검증 이후 movement 검증을 실행하며 `-movement=false`는 entry만 실행한다. 기존 roomcheck client의 guest ID를 `sessionID string`으로 보관한다. movement helper는 별도 reader 한 개/client와 bounded 수신 채널을 사용하며 기존 synchronous request와 동시 Read하지 않는다.

- [ ] **Step 1: 설정 및 checker RED.** 아래 cross-field 설정 시험과 실제 httptest WS integration 시험을 작성한다.

```go
func TestMovementRateConfig(t *testing.T) {
    t.Setenv("ROOM_TICK_HZ", "20")
    t.Setenv("ROOM_SNAPSHOT_HZ", "21")
    if _, err := Load(); err == nil { t.Fatal("snapshot exceeds tick") }
    t.Setenv("ROOM_SNAPSHOT_HZ", "10")
    got, err := Load()
    if err != nil || got.Room.TickHz != 20 || got.Room.SnapshotHz != 10 { t.Fatal("rates ignored", err) }
}
```

`TestMovementChecker`는 기존 main_test의 httptest 서버에 runMovementChecks를 호출해 nil, 도달 불가 URL에는 error를 검사한다. 거짓 PASS 방지를 위해 checker의 pure 비교 helper를 정의한다: `func compareMovementSnapshots(states []room.Snapshot) error`; 서로 다른 match/tick/revision/players position을 입력하면 error인 table을 작성한다. 설정은 empty/0/-1/121, snapshot>tick, budget>queue도 거절한다.

- [ ] **Step 2: RED 명령.** `go test ./internal/config ./cmd/roomcheck -run 'TestMovement' -count=1`. 미구현/미적용 설정으로 실패를 기록한다.
- [ ] **Step 3: 설정·외부 검사 구현.** Config.Load와 room.NewManager가 같은 범위를 검사한다. 6개 WS를 WAITING에 입장시킨 후 host가 시작한다. 모든 client reader를 활성화하고 session마다 move 1→ACK→여러 snapshot→stop 2→ACK를 검사한다. 동일 `(match_id,revision,server_tick,snapshot_seq)`의 full snapshot 하나가 6명 모두에게 도착할 때 players 상태·member 순서·seq map을 비교한다. stop tick은 velocity 0이므로 이동하지 않는다. 서버 tick Hz는 시작 상태에 없는 값이므로 CLI는 기본값을 추측해 이동 거리 공식을 강제하지 않는다. 아래 별도 검증 전략을 따른다.

**속도 검증 전략:** core 테스트는 정확한 5/Hz 거리를 검증한다. 외부 CLI는 move 후 양의 z 이동·x/y 불변·속도 z=5, stop 후 velocity0·추가 snapshot에서 위치 불변, 다른 5명도 해당 player와 같은 상태를 보는지 확인한다. CLI에 서버 tickHz를 임의로 가정하지 않는다. elapsed 단위 성능은 수신 주체의 time.Now와 tick/seq 차이로 관측하되 목표 Hz와 정확히 같다는 hard assertion을 하지 않는다. reader 채널 overflow는 검사 실패이며 무한 snapshot map을 두지 않는다(최근 128개의 capture key만 보관).

- [ ] **Step 4: README·spec의 실제 구현 경계를 갱신한다.** 3D JSON, start와 ACK, 지속 방향/stop, 새 오류/설정, 정상 종료가 handler 경로라는 점, no-catch-up slowdown을 적는다. 기존 설계 문서를 성능 보고서로 바꾸지 않고 구현 링크를 붙인다. Godot·충돌·목표/승패·UI 지연은 미구현/미측정으로 구분한다. runnable 명령은 아래와 같다.

```bash
go run ./cmd/server
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -movement -timeout 30s
```

- [ ] **Step 5: 최종 GREEN과 실제 프로세스 검증.** `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`을 실행한다. 별도 server 프로세스에 roomcheck를 실행하고 종료 후 프로세스·membership 정리를 확인한다. 지연/jitter/TCP 손실 주입을 하지 않았다면 그 사실을 보고한다. deterministic queue/ticker 시험을 실제 네트워크 손실 시험이라고 부르지 않는다. 실제 6 WS 테스트와 Godot QA를 구분한다.
- [ ] **Step 6: 검토 후 local commit.** 위 Files만 stage하고 `git commit -m "feat: validate realtime movement with six clients"`; task-4-report에 모든 명령·실제 결과·SHA·미측정 범위를 적는다.

## 선택 이유와 비용

- typed State는 snapshot 복사를 검증하기 쉽고 현재 공개 3D reference에 충분하다. 다른 게임에서 숨겨진 정보나 다른 state schema가 필요하면 handler 계약을 별도 설계로 확장한다. 범용 mutable `any`를 지금 도입해 복사 책임을 흐리지 않는다.
- 고정 dt 한 단계와 지속 방향은 구현·서버 권위·spam 방지가 단순하다. 대신 tick drop 중 simulation 시간이 늦어지므로 실제 부하 측정 후 bounded catch-up 필요성을 판단한다.
- 별도 입력 credit/FIFO와 reliable/latest mailbox는 bounded 메모리·cleanup 우선·전송 fence를 명시한다. 큐 관리와 테스트가 추가되지만 입력당 goroutine이나 무한 ACK 대기 map보다 수명 상한이 명확하다.
- 네 task로 나눈 이유는 handler, actor, wire ordering, 외부 실행 검증이 각자 독립적인 거절/승인 기준을 갖기 때문이다. Task 2의 transport 수정은 Events 타입 호환만으로 한정해 중간 commit도 기존 실행 경로를 유지한다.
- reference에 정상 종료 트리거를 추가하지 않으면 실제 free-space 실행은 마지막 퇴장까지 유지된다. 정상 종료·다음 match 계약은 교체 가능한 test handler로 검증하므로 임의 게임 규칙을 만들 필요가 없다.

## 전체 완료 근거와 handoff

각 Task는 RED→GREEN→전체 기존 suite→독립 검토→local commit 순서다. 순차 worker는 앞 task API를 소비하며 공유 파일은 단계별로 소유권을 넘긴다. root의 최종 Architect 검토는 숫자 검증, 실제 ACK 시점, writer 선형화점, membership 재사용, ticker 종료를 실제 diff와 fresh test 증거로 확인한다. 승인되지 않은 문제는 수정 후 해당 시험과 영향받는 전체 suite를 다시 실행한다.

완료 보고는 구현된 Go 동작, local commit 목록, 실제 검증 명령과 결과, Godot/게임별 규칙/부하·지연 미측정 범위를 포함한다. 이 계획 자체는 구현 또는 테스트 통과 증거가 아니다.
