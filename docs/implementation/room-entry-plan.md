# Room Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go 서버를 실행해 guest 연결, 공개·프라이빗 룸 생성, 6자리 번호 입장, 비밀번호 확인, 퇴장과 연결 정리를 검증한다.

**Architecture:** 단일 프로세스의 메모리 registry가 룸 ID와 번호를 관리하고 룸 actor만 참가자·방장 상태를 변경한다. 세션 예약과 최종 admission을 함께 확인하며 Argon2id 계산은 actor 밖의 유한 worker pool에서 수행한다. `net/http`와 WebSocket JSON transport는 룸 코어의 공개 API를 사용한다.

**Tech Stack:** Go, 표준 `net/http`·`crypto/rand`, 공식 문서로 확인한 `github.com/coder/websocket v1.8.15`, `golang.org/x/crypto v0.58.0`, 간접 dependency `golang.org/x/sys v0.49.0`. `go.mod`와 `go.sum`에 고정한다.

**Spec:** [룸·세션](../specs/02-rooms-and-sessions.md), [메시지·API](../specs/04-messages-and-api.md), [구현·설정](../specs/07-implementation-and-config.md), [검증](../specs/08-validation.md), [전체 설계](../go-room-server-design.md).

## Global Constraints

- CoopDock(코옵독)을 작업 프로젝트명으로 사용하며 구현 위치는 `/Users/sangjinsu/Documents/ChatGPT/coopdock`이다. Go module 이름 `roomserver`는 내부 module 식별자로 유지한다.
- local Go는 `1.27.2 darwin/arm64`로 확인했다. dependency의 최소 요구 버전에 따라 `go.mod`는 Go `1.26.0`을 사용한다.
- 룸당 정원 `1~6명`, 미지정 기본 `6`; 번호는 ASCII 숫자 `6자리` 문자열이며 선행 0을 유지한다.
- 기본 공개 여부는 `public`; `private`는 생성자 지정 비밀번호, public 생성에서 password 필드가 있으면 `INVALID_MESSAGE`다.
- 비밀번호는 `4~64 Unicode 문자(code point)`, 대소문자 구분, trim·정규화 없음. 누락과 빈 문자열을 구분하며 원문을 영구 저장하지 않는다.
- 한 세션은 한 룸에만 속한다. `JOINING`은 같은 세션의 중복 요청 차단이며 정원 예약이 아니다.
- 검증 결과는 실제 `room_id`와 세션 예약 generation에 묶는다. 취소·disconnect·Close 후 늦은 결과가 새 예약이나 재사용 번호를 변경하지 않는다.
- 룸 actor가 정원·상태와 참가자 추가를 함께 처리한다. host는 잔여 참가자 중 입장 순서로 승계하고 마지막 참가자 퇴장 시 룸을 제거한다.
- 비밀번호·hash·salt는 응답, snapshot, 공개 목록, 로그에 포함하지 않는다. `room.list`에서 private를 제외한다.
- 코드 주석은 영어, 문서와 사용자 안내는 한국어. dependency 계약은 공식 문서 확인 결과를 따른다.
- 이번 범위는 WAITING 룸 입장 기능이다. `room.start`, `game.action`, simulation, admin, 지속 audit, Godot 화면과 자동 재접속 복원은 구현하지 않는다.
- 사용자에게 이미 실행과 작업별 Git commit 승인을 받았다. 계획 재승인을 요청하지 않는다. 기존 설계 문서를 구현 완료 보고로 바꾸지 않는다.
- 각 작업 완료 후 소유한 파일을 명시적으로 stage한다. `git add .`를 사용하지 않고 다른 작업자의 변경을 되돌리지 않는다.

## Review Focus

1. 동시에 온 마지막 자리 요청과 같은 세션의 두 룸 요청: 정원과 한 세션 한 룸을 모두 지킨다(Task 1).
2. 취소된 private 검증, 번호 재사용, 늦은 결과: 예전 generation·room_id의 결과가 새 입장을 만들지 않는다(Task 1).
3. public에 빈 password 필드, 잘못된 JSON 타입, Unicode·공백 비밀번호: 필드 존재·문자 수·원문 비교 계약을 지킨다(Task 1·2).
4. hash 포화·TTL 만료·느린 연결: 오류가 구분되고 제한 해제 후 정상 입장이 가능하며 정리가 막히지 않는다(Task 1·2).
5. 응답·room.state 순서, disconnect·shutdown: 초기 snapshot보다 후속 상태가 먼저 전달되지 않고 제한 시간 안에 소속과 작업을 정리한다(Task 2).

---

## 파일과 책임

| 파일 | 책임 |
|---|---|
| `go.mod`, `go.sum` | 공식 문서로 확인한 dependency와 Go 요구 버전 |
| `internal/room/types.go`, `errors.go` | transport가 사용하는 옵션·snapshot·오류 계약 |
| `internal/room/manager.go`, `actor.go` | 번호 registry, admission, 룸 actor, host·정원·정리 |
| `internal/room/session.go` | 연결 상태, 예약 generation, 현재 membership; room 공개 API 뒤에 유지 |
| `internal/room/password.go`, `limits.go` | Argon2id worker·queue, TTL 실패 제한 |
| `internal/room/manager_test.go`, `password_test.go` | 상태·취소·포화·동시성 시험 |
| `internal/protocol/message.go`, `message_test.go` | version 1 envelope와 엄격한 입력 검사 |
| `internal/transport/server.go`, `server_test.go` | `/ws`, health, 준비 상태, 단일 reader/writer, shutdown |
| `internal/config/config.go`, `config_test.go` | 환경변수와 실제 상한·timeout 검사 |
| `cmd/server/main.go`, `README.md` | 실행 프로그램, 설정·프로토콜·검증 안내 |

위 경로는 모두 프로젝트 root 기준이다. 파일 분할을 바꿀 때 producer·consumer API를 함께 갱신하고 중복 session 패키지를 만들지 않는다.

## 로컬 설정 제안과 확정 기준

아래는 구현 시작값 제안이며 수용량·성능 보장이 아니다. 실제 선택값은 구현 worker가 공식 API와 시험 결과를 확인해 README에 기록한다.

| 항목 | 제안값 | 확인할 동작 |
|---|---:|---|
| 전체 룸 / 세션 상한 | 128 / 768 | 초과는 `SERVER_BUSY`; 시험으로 규모 보장을 주장하지 않음 |
| 번호 충돌 재시도 | 64 | registry 잠금 안에서 예약; 소진 시 `SERVER_BUSY` |
| hash worker / 대기 queue | 2 / 8 | 무제한 goroutine·대기 없음; 포화 `SERVER_BUSY` |
| 실패 TTL / IP·session·room 횟수 | 1분 / 20·5·30 | 만료 후 재시도 가능; 전체 룸 영구 잠금 없음 |
| 제한 key 상한 | 4096 | 만료 key 정리; 상한 도달 시 무제한 저장하지 않음 |
| actor 명령 / 정리 queue | 64 / 6 이상 | 취소된 연결 context와 독립한 membership 정리 |
| WS message / 중요 송신 queue | 16KiB / 64 | 초과 입력 거부·느린 연결 종료 |
| 요청 / shutdown timeout | 5초 / 10초 | 취소와 늦은 결과 정리, 종료 상한 |
| 공개 목록 상한 | 100 | private 제외, 응답량 상한 |

공식 자료 확인 후 초기 Argon2id 기준은 memory `19MiB(19456KiB)`, time `2`, parallelism `1`, 무작위 salt `16bytes`, hash `32bytes`로 채택한다. 성능을 보장하는 값은 아니며 이미 진행 중인 `IDKey` 계산이 context 취소로 즉시 끝난다고 가정하지 않는다. IP는 `RemoteAddr`를 사용하며 신뢰 proxy 설정 없이 `X-Forwarded-For`를 신뢰하지 않는다. WS Origin은 local 실행에 필요한 명시 목록만 허용하며 외부 운영은 WSS를 사용한다.

이 환경에서 worker·검증 명령은 다음 cache 설정을 사용한다. 사용자 README의 일반 실행은 표준 Go 명령으로 안내해도 된다.

```bash
mkdir -p work/go-build work/go-mod work/tmp
export GOCACHE="$PWD/work/go-build"
export GOMODCACHE="$PWD/work/go-mod"
export GOTMPDIR="$PWD/work/tmp"
export GOTOOLCHAIN=local
cd /Users/sangjinsu/Documents/ChatGPT/coopdock
```

## Task 1: 룸 코어와 안전한 private 입장

**Files:** 위 표의 `go.mod`, `go.sum`, `internal/room/*.go`와 관련 test.

Task 1은 실제 import하는 `golang.org/x/crypto v0.58.0`과 간접 dependency `golang.org/x/sys v0.49.0`만 추가하고 `go mod tidy`로 module 파일을 정리한다. 아직 사용하지 않는 WebSocket dependency는 Task 2가 추가한다.

**Interfaces — producer 계약:** transport가 내부 mutex·actor·비밀번호 자료에 접근하지 않도록 다음 API를 시작점으로 사용한다. 구현 직전 core·transport worker가 함께 최종 signature를 확인하고 이 블록을 실제 코드와 일치시킨다.

```go
package room

type CreateOptions struct {
    Capacity int
    Visibility string
    Password *string
}
type JoinOptions struct {
    RoomNumber string
    Password *string
}
type Member struct { SessionID string `json:"session_id"` }
type Snapshot struct {
    RoomID string `json:"room_id"`
    RoomNumber string `json:"room_number"`
    Visibility string `json:"visibility"`
    Capacity int `json:"capacity"`
    MatchID *string `json:"match_id"`
    Phase string `json:"phase"`
    HostID string `json:"host_id"`
    Members []Member `json:"members"`
    Revision uint64 `json:"revision"`
    GameState any `json:"game_state"`
    ServerTick uint64 `json:"server_tick"`
    SnapshotSeq uint64 `json:"snapshot_seq"`
    LastProcessedInputSeq map[string]uint64 `json:"last_processed_input_seq"`
}
type Error struct { Code string; Message string }

func DefaultConfig() Config
func NewManager(Config) (*Manager, error)
func (m *Manager) Connect(peerID string) (*Session, error)
func (s *Session) ID() string
func (s *Session) Done() <-chan struct{}
func (s *Session) Events() <-chan Snapshot
func (m *Manager) Create(context.Context, *Session, CreateOptions) (Snapshot, error)
func (m *Manager) Join(context.Context, *Session, JoinOptions) (Snapshot, error)
func (m *Manager) Leave(context.Context, *Session) error
func (m *Manager) Disconnect(*Session)
func (m *Manager) ListPublic(context.Context) ([]Snapshot, error)
func (m *Manager) Ready() bool
func (m *Manager) Close(context.Context) error
func CodeOf(error) string
```

`Config`, `Manager`, `Session`은 이 작업의 해당 파일에서 정의한다. `Config`에는 위 상한·timeout과 Argon2id 설정을 둔다. `Session`의 변경 가능한 필드는 비공개다. `Events`는 유한 중요 이벤트 FIFO이고 `Done`은 세션 종료를 알린다. 포화 시 session closing·cleanup을 신호로 보내되 actor는 socket IO나 자신의 queue에 넣는 작업으로 막히지 않는다. `Disconnect`는 반복 호출해도 안전하며 늦은 결과를 먼저 무효화한다. `Close`는 신규 admission을 닫고 세션·actor·hash 작업을 context 상한까지 정리한다.

- [x] **Step 1: 실패할 코어 시험을 먼저 작성한다.** 다음 공개 정원 시험을 `manager_test.go`에 추가하고 private·Unicode·host 승계·빈 룸 삭제 시험을 같은 API로 작성한다.

```go
func TestRoomCapacity(t *testing.T) {
    ctx := context.Background()
    m, err := NewManager(DefaultConfig())
    if err != nil { t.Fatal(err) }
    defer m.Close(ctx)
    host, err := m.Connect("127.0.0.1")
    if err != nil { t.Fatal(err) }
    created, err := m.Create(ctx, host, CreateOptions{Capacity: 1, Visibility: "public"})
    if err != nil { t.Fatal(err) }
    guest, err := m.Connect("127.0.0.2")
    if err != nil { t.Fatal(err) }
    _, err = m.Join(ctx, guest, JoinOptions{RoomNumber: created.RoomNumber})
    if CodeOf(err) != "ROOM_FULL" { t.Fatalf("got %v", err) }
}
```

각 subtest의 입력과 기대값은 다음과 같다: capacity `0`은 기본 6, `-1/7`은 `INVALID_MESSAGE`; public password `nil`은 성공하고 `&""`·`&"abcd"`는 `INVALID_MESSAGE`; private password 누락·3·65문자는 `INVALID_MESSAGE`; 4·64문자와 `" 가 나 "`는 원문대로 성공; private 목록 제외; 잘못된 6자리 형식은 `INVALID_MESSAGE`, 없는 번호는 `ROOM_NOT_FOUND`, 비밀번호 누락은 `PASSWORD_REQUIRED`, 다른 값은 `PASSWORD_INVALID`. 별도 연결의 host 퇴장 후 첫 잔여 참가자 승계와 마지막 퇴장 후 `ROOM_NOT_FOUND`를 확인한다.

- [x] **Step 2: 위 시험을 실행해 미구현으로 실패함을 확인한다.** module 부재로 실행 불가이면 공식 문서로 확인한 `go.mod`만 먼저 만들고 다시 실행한다.

```bash
go test ./internal/room -run TestRoomCapacity -count=1
```

기대 결과: `NewManager` 등 아직 없는 구현 때문에 FAIL. dependency 버전·Go 요구 버전은 조사 결과를 사용한다.

- [x] **Step 3: 최소 코어를 구현한다.** registry에서 `crypto/rand`로 6자리 후보를 만들고 중복 검사·예약을 함께 수행한다. 예약 → actor 명령 → 입장 확정을 구현하며 public create·join, leave·disconnect·Close가 같은 membership 정리 경로를 사용한다. `Error`에 고정된 code/message만 넣고 요청·비밀번호를 문자열화하지 않는다.

```go
// Preserve absent and empty password as different inputs.
if opts.Visibility == "public" && opts.Password != nil {
    return Snapshot{}, &Error{Code: "INVALID_MESSAGE", Message: "Public rooms cannot have a password."}
}
// Admission rechecks the captured room ID and reservation generation.
// The actor owns capacity checks, membership updates and host succession.
```

- [x] **Step 4: private 계산·취소·상한 시험을 먼저 작성하고 실패를 확인한다.** test용 hash barrier·clock·번호 생성기를 package 비공개 경계에 주입해 실제 Argon 계산 시간에 의존하지 않는 interleaving을 만든다. `password_test.go`는 실제 Argon2id 생성·검증과 salt 차이도 확인한다.

| 시험 이름 | 강제한 순서와 기대 결과 |
|---|---|
| `TestLastSeatConcurrentJoin` | 5명 룸에 두 연결 동시 join → 성공 1, `ROOM_FULL` 1, 인원 6 |
| `TestOneSessionConcurrentRooms` | 한 연결이 두 룸 동시 join → 소속 1개, 다른 요청 `ALREADY_IN_ROOM` |
| `TestCanceledHashCannotJoin` | private hash 정지 → timeout 또는 disconnect → hash 재개 → 참가자·예약 없음 |
| `TestOldGenerationCannotReplaceNewJoin` | 이전 검증 취소 → 새 룸 입장 → 이전 결과 재개 → 새 소속 유지 |
| `TestNumberReuseBindsRoomID` | 이전 룸 검증 정지 → 빈 룸 제거·번호 재사용 → 재개 → 새 룸 입장 없음 |
| `TestCloseRejectsPendingAdmission` | 검증 정지 → Close admission 차단 → 재개 → 생성/입장 확정 없음 |
| `TestHashQueueFull` | 모든 worker·queue 차지 → 추가 요청 `SERVER_BUSY` → 해제 후 정상 입장 |
| `TestFailureLimitsExpire` | IP·session·room key별 한계 → `RATE_LIMITED` → clock TTL 경과 → 정상 입장 |
| `TestCreateCancellationReleasesNumber` | private 생성 취소 → 번호·미게시 룸·예약 제거 → 후속 생성 성공 |
| `TestSnapshotsContainNoSecrets` | create/join/list/event JSON에 password·hash·salt 필드와 원문 없음 |

```bash
go test ./internal/room -count=1
```

기대 결과: 아직 처리하지 않은 interleaving 시험 FAIL. 취소 시 actor cleanup에는 취소된 request context를 전달하지 않는다.

- [x] **Step 5: 검증 결과의 room_id·generation binding, worker 상한, 실패 TTL과 cleanup을 구현한 뒤 검증한다.** 이미 실행 중인 Argon 계산은 worker 안에서 끝나며 결과 수신 시 폐기한다. 세션은 정리가 끝나기 전 새 요청을 확정할 수 없다. snapshot은 깊은 복사로 반환한다.

```bash
go test ./internal/room -count=1
go test -race ./internal/room -count=1
go vet ./internal/room
```

기대 결과: PASS, race·vet 오류 없음. 공식 dependency 출처와 실제 Go 요구 버전을 기록하고 `gofmt`를 적용한다.

- [x] **Step 6: 독립 검토 후 작업 파일만 commit한다.** core worker가 ownership을 가진 Go 파일과 module 파일만 stage한다. parent는 producer API와 시험 근거를 확인한다.

```bash
git add go.mod go.sum internal/room
git commit -m "feat: add room creation and password admission core"
```

## Task 2: 실제 WebSocket 서버와 실행 안내

**Files:** `internal/protocol/`, `internal/transport/`, `internal/config/`, `cmd/server/main.go`, `README.md`, WebSocket dependency 추가에 필요한 `go.mod`, `go.sum`; 필요하면 실제 검증용 `cmd/roomcheck/main.go`를 포함한다.

Task 1 커밋 이후 Task 2가 module 파일 소유권을 이어받아 실제 import하는 `github.com/coder/websocket v1.8.15`를 추가하고 `go mod tidy`로 정리한다. Task 1이 고정한 crypto dependency를 유지하며 변경한 module 파일도 Task 2 커밋에 명시 stage한다.

**Interfaces — consumer 계약:** Task 1의 `room.NewManager`, `Connect`, `Create`, `Join`, `Leave`, `Disconnect`, `ListPublic`, `Ready`, `Close`, `Session.ID/Events`, `Snapshot`, `CodeOf`만 사용한다. 네트워크 내부 구현을 코어에 전달하지 않는다.

```go
package transport
func New(manager *room.Manager, cfg Config) (*Server, error)
func (s *Server) Handler() http.Handler
func (s *Server) Close(context.Context) error
```

`transport.Config`는 허용 Origin, message·송신 queue 한계와 read/write/request timeout을 정의한다. `config.Load()`는 환경변수를 검증해 room·transport config와 HTTP listen 주소를 반환한다. HTTP 처리 전에 실패하면 고정된 원인만 로그로 출력하고 종료한다.

- [x] **Step 1: JSON과 실제 WebSocket 실패 시험을 먼저 작성한다.** `message_test.go`에 필드 타입·누락·미지원 op 시험을, `server_test.go`에 `httptest.NewServer`와 공식 websocket dial API를 사용한 통합 시험을 추가한다.

```json
{"version":1,"op":"room.create","request_id":"create","payload":{"capacity":6,"visibility":"public"},"error":null}
```
```json
{"version":1,"op":"room.join","request_id":"join","payload":{"room_number":"004271"},"error":null}
```
```json
{"version":1,"op":"room.leave","request_id":"leave","payload":{},"error":null}
```
```json
{"version":1,"op":"room.list","request_id":"list","payload":{},"error":null}
```

각 시험은 다음 입력을 보낸다: malformed JSON, 한 frame의 JSON 두 개, version `2`, request_id 누락·잘못된 타입, payload `null`·잘못된 타입, capacity `0`·`1.5`, visibility `""`, password 숫자, room_number 숫자·전각 숫자·5/7자리, public password `""`. 모두 `INVALID_MESSAGE`로 처리하고 크기 초과·허용되지 않은 Origin은 연결 또는 frame을 거부한다. capacity·visibility 기본값은 필드 누락 때만 적용한다.

- [x] **Step 2: 미구현 상태에서 시험이 실패함을 확인한다.**

```bash
go test ./internal/protocol ./internal/transport ./internal/config -count=1
```

- [x] **Step 3: guest 발급과 요청·응답을 구현한다.** 접속 직후 `session.guest` 이벤트를 보내고 실제 connection의 Session을 사용한다. 요청마다 같은 `op`·`request_id`로 응답하며 create/join 성공 payload는 `{"snapshot":...}`, list 성공은 `{"rooms":[...]}`, leave 성공은 `{}`다. `room.state` 이벤트의 request_id는 null이다. 초기 게임 필드는 match_id·game_state null, tick 0, 적용 입력 map 빈 값으로 보낸다.

```json
{"version":1,"op":"session.guest","request_id":null,"payload":{"session_id":"server-issued-id"},"error":null}
```
```json
{"version":1,"op":"room.join","request_id":"join","payload":null,"error":{"code":"PASSWORD_REQUIRED","message":"Room password is required."}}
```

단일 reader와 writer를 사용하되 비밀번호 계산을 비동기 요청 worker로 넘긴다. reader의 ingress enqueue는 nonblocking이며 검증 대기 중에도 disconnect를 감지해 Session을 취소할 수 있어야 한다. 중요 FIFO 포화 또는 Session.Done 시 연결을 종료하고 Disconnect를 호출한다. 새 참가자의 초기 성공 응답을 먼저 enqueue한 뒤 같은 revision의 초기 이벤트와 이후 room.state를 전달하는 연결별 fence를 두며 actor를 네트워크 쓰기로 막지 않는다. `room.start`, `game.action`, admin 경로는 README에 미제공으로 명시하고 요청은 `INVALID_MESSAGE`로 거절한다.

- [x] **Step 4: 서버 실행과 종료, 실제 연결 검증을 먼저 시험한다.** health는 200, ready는 수용 중 200·Close 시작 후 503. 종료는 admission 차단 → WS close/cancel → membership·actor·worker 정리 → HTTP shutdown 순서로 수행한다.

| 시험 이름 | 관측할 결과 |
|---|---|
| `TestSixClientsJoinAndSeventhRejected` | 실제 6 WS 연결로 public/private 입장, 7번째 `ROOM_FULL` |
| `TestLastSeatWSRace` | 실제 두 WS가 마지막 자리 동시 요청, 성공 한 명 |
| `TestPrivatePasswordRoundTrip` | PASSWORD_REQUIRED → 틀린 값 PASSWORD_INVALID → 정답 성공; private 목록 제외 |
| `TestInitialResponseBeforeRoomState` | 요청 성공 snapshot이 해당 연결의 후속 room.state보다 먼저 도착 |
| `TestDisconnectHostAndEmptyRoom` | host 연결 종료 후 승계 이벤트; 마지막 연결 종료 후 목록 제거 |
| `TestWireAndLogsExcludeSecrets` | 송신 frame·captured log에 원문·password/hash/salt 자료 없음 |
| `TestRequestTimeoutAndShutdown` | private 계산 지연·Close 경합 시 입장 없음; 연결·작업 정리 완료 |
| `TestSlowWriterCleanup` | 읽지 않는 연결의 중요 queue 포화 시 disconnect, 정상 연결은 계속 동작 |

```bash
go test ./internal/transport -count=1
```

- [x] **Step 5: 통합 시험을 통과시키고 runnable README를 작성한다.** README에는 필요한 Go 버전, `go run ./cmd/server`, 실제 설정 key·기본값·상한, `/healthz`·`/readyz`·`/ws`, public/private JSON 순서, SIGINT/SIGTERM 종료, 메모리 저장·새 guest 정책을 넣는다. 실제 검증 CLI가 필요하면 public/private 6명 입장과 마지막 자리 race를 반복 가능한 명령으로 제공하고 assertion 실패 시 nonzero 종료한다. simulation·admin·Godot 게임 화면은 현재 미구현으로 안내한다.

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o bin/roomserver ./cmd/server
```

기대 결과: 모두 PASS. 실제 프로세스를 띄워 HTTP 준비 확인, 6 WS 입장·7번째 거절, private 비밀번호 순서, timeout·SIGTERM 종료를 실행한다. 이 검증은 부하·수용 규모 측정이 아니다. 실제 결과와 환경을 README 또는 parent 최종 보고에 구분해 기록한다.

- [x] **Step 6: 독립 architect 검증 후 명시 stage하고 commit한다.** stage 전 diff에 실제 dependency·설정값·API와 문서가 일치하는지 확인하고 다른 worker 파일은 stage하지 않는다.

```bash
git add go.mod go.sum internal/protocol internal/transport internal/config cmd/server README.md
git commit -m "feat: expose room entry over websocket with runnable server"
```

검증 CLI를 만든 경우 그 경로도 명시 stage한다. 최종 사용자 보고는 구현 범위, commit SHA, 실제 검증 근거, 실행 명령과 미구현 범위를 분리해서 안내한다.

## 계획 자체 점검

확정된 첫 구현 범위는 Task 1의 룸·예약·비밀번호와 Task 2의 실제 연결·정리·실행 안내에 대응한다. 미래 게임·운영 항목은 Global Constraints의 후속 범위다. 위 인터페이스는 구현 worker가 공유하는 계약 제안이며 실제 API·설정 확정 후 문서와 코드를 일치시킨다. 실제 시험 통과나 서버 구현 완료를 이 계획만으로 주장하지 않는다.

구현 review에서는 admission gate → session lock 순서를 통일하고 해당 Manager가 발급한 Session인지 검사한다. actor 자신의 이벤트 포화는 직접 퇴장 처리해 self-enqueue 교착을 피하고 mailbox는 닫지 않은 채 Done으로 종료한다. 취소된 hash의 worker permit은 실제 계산 return까지 유지한다. private create는 hash 완료 후 번호 확보·최종 admission을 수행해 미게시 번호 예약 정리를 단순화할 수 있다.

## 작업 완료 기록

- Task 1: `6e5671264226e457aadc7732437b81b259db0b9a` — 룸 코어·private 입장 구현, RED/GREEN·전체 test/race/vet 및 독립 검토 APPROVED 후 commit. 근거: `.superpowers/sdd/room-entry-plan/task-1-report.md`, `task-1-review.md`.
- Task 2: `7869e9ee587229d8351f0abbb448cc5300092b8d` — 실제 WebSocket·실행 안내 구현, RED/GREEN·전체 test/race/vet/build와 실제 CLI·SIGTERM 실행 근거, 독립 Architect의 스펙·품질 APPROVED 후 commit. 근거: `.superpowers/sdd/room-entry-plan/task-2-report.md`, `task-2-review.md`.

위 체크는 각 Task의 구현·독립 검토·commit 완료를 표시한다. 전체 branch Architect 검증과 fresh final QA는 별도 후속 단계이며, 게임 simulation·운영 기능·Godot 클라이언트·운영 부하와 성능 측정 완료를 의미하지 않는다.
