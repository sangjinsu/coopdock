# CoopDock - Go 룸 입장 서버

Go **1.26 이상**이 필요합니다. `roomserver`는 내부 module 이름입니다. 이 서버는 하나의 프로세스에서 guest 연결, 룸 생성, ASCII 6자리 번호 입장, private 비밀번호 확인, 공개 목록, 퇴장과 연결 정리를 제공합니다.

```bash
cd /Users/sangjinsu/Documents/ChatGPT/coopdock
go run ./cmd/server
```

설계 문서는 [전체 설계](docs/go-room-server-design.md)와 [기능별 스펙](docs/specs/README.md)을 참조합니다.

기본 주소는 `127.0.0.1:8080`입니다. 다른 주소가 필요하면 `HTTP_ADDR=127.0.0.1:9090 go run ./cmd/server`처럼 지정합니다.

| HTTP 경로 | 동작 |
|---|---|
| GET `/healthz` | 프로세스 상태: 200 |
| GET `/readyz` | 신규 수용 중 200, 종료 시작 이후 503 |
| GET `/ws` | WebSocket guest 연결 |

연결마다 새 guest ID를 발급합니다. 클라이언트는 자기 `session_id`를 요청에 지정할 수 없습니다. 한 연결은 동시에 한 룸에만 속하며 생성자는 자동 입장하고 host가 됩니다. 룸 정원은 1~6명, 미지정 기본값은 6명입니다. host 연결이 끊어지거나 퇴장하면 먼저 입장한 잔여 참가자가 host를 이어받습니다. 마지막 참가자가 나가면 룸은 삭제됩니다.

모든 데이터는 메모리에 있습니다. 재시작하면 룸·비밀번호 자료·membership이 사라집니다. 재연결은 새 guest이므로 private 비밀번호를 다시 확인합니다. 룸의 visibility와 비밀번호는 수명 동안 고정입니다.

## 실제 WebSocket 메시지

모든 envelope에는 `version`, `op`, `request_id`, `payload`, `error`가 명시됩니다. 요청 `request_id`는 비어 있지 않은 문자열이며 최대 128 UTF-8 bytes입니다. 같은 ID를 다시 보내도 중복 실행이 방지되지 않습니다. 응답은 같은 `op`와 `request_id`를 사용하고, 서버 이벤트의 `request_id`는 `null`입니다.

연결 직후 서버 이벤트:

```json
{"version":1,"op":"session.guest","request_id":null,"payload":{"session_id":"server-issued-id"},"error":null}
```

공개 룸 생성 요청과 번호 입장 요청:

```json
{"version":1,"op":"room.create","request_id":"create-public","payload":{"capacity":6,"visibility":"public"},"error":null}
{"version":1,"op":"room.join","request_id":"join-public","payload":{"room_number":"004271"},"error":null}
```

생성 및 입장 성공 응답의 `payload.snapshot`은 전체 공개 상태입니다. 아래 ID·번호는 형식 예시입니다.

```json
{"version":1,"op":"room.create","request_id":"create-public","payload":{"snapshot":{"room_id":"room-id","room_number":"004271","visibility":"public","capacity":6,"match_id":null,"phase":"WAITING","host_id":"guest-id","members":[{"session_id":"guest-id"}],"revision":1,"game_state":null,"server_tick":0,"snapshot_seq":0,"last_processed_input_seq":{}}},"error":null}
```

이 응답과 같은 revision의 초기 `room.state`를 processor에서 바로 이어 송신 FIFO에 넣고 이후 상태 이벤트를 전달합니다. core가 보내는 같은 revision의 초기 중복은 제외합니다. `room.state.payload`는 위 `snapshot` 내부 구조이며 `request_id`는 `null`입니다. 퇴장 후 재입장할 때 이전 membership의 오래된 이벤트는 새 입장 초기 상태보다 앞서 전달하지 않습니다.

Private 룸 생성 및 비밀번호 확인 순서:

```json
{"version":1,"op":"room.create","request_id":"create-private","payload":{"capacity":6,"visibility":"private","password":"coop2026"},"error":null}
{"version":1,"op":"room.join","request_id":"join-1","payload":{"room_number":"004271"},"error":null}
{"version":1,"op":"room.join","request_id":"join-1","payload":null,"error":{"code":"PASSWORD_REQUIRED","message":"Room password is required."}}
{"version":1,"op":"room.join","request_id":"join-2","payload":{"room_number":"004271","password":"wrong2026"},"error":null}
{"version":1,"op":"room.join","request_id":"join-2","payload":null,"error":{"code":"PASSWORD_INVALID","message":"Room password is invalid."}}
{"version":1,"op":"room.join","request_id":"join-3","payload":{"room_number":"004271","password":"coop2026"},"error":null}
```

정답이라도 정원이 찼으면 `ROOM_FULL`입니다. 비밀번호는 **4~64 Unicode code point**, 대소문자 구분이며 공백 제거와 Unicode 정규화를 하지 않습니다. `" 가 나 "`의 공백도 비밀번호의 일부입니다. 누락과 `""`는 다릅니다. public 생성에 `password` 필드가 있으면 빈 값이어도 거절합니다. private 생성의 누락·잘못된 길이와 join에 명시된 잘못된 타입·길이는 `INVALID_MESSAGE`입니다. 서버는 Argon2id hash와 무작위 salt만 룸에 보관하며 원문을 룸에 저장하지 않습니다. 원문·hash·salt는 응답·이벤트·목록·로그에 포함하지 않습니다. 요청 payload를 로그에 남기지 않습니다.

공개 목록과 퇴장:

```json
{"version":1,"op":"room.list","request_id":"list","payload":{},"error":null}
{"version":1,"op":"room.list","request_id":"list","payload":{"rooms":[]},"error":null}
{"version":1,"op":"room.leave","request_id":"leave","payload":{},"error":null}
{"version":1,"op":"room.leave","request_id":"leave","payload":{},"error":null}
```

`rooms`에는 public만 포함하며 각 항목은 snapshot 구조입니다. 번호는 ASCII 숫자 6자리 문자열이라 `"004271"`의 앞 0을 유지합니다. 전각 숫자·숫자 타입·5/7자리 번호를 거절합니다. `capacity`와 `visibility`의 기본값은 필드를 생략했을 때만 적용합니다. 명시된 `0`, 소수, `null`, 빈 visibility는 거절합니다.

JSON text frame만 받습니다. malformed/trailing JSON, 중복·미정의 필드, 잘못된 버전·필드 타입, 필수 필드 누락, `payload:null`·배열, 잘못된 UTF-8과 독립 UTF-16 surrogate escape를 `INVALID_MESSAGE`로 거절합니다. 유효한 high/low surrogate pair, 실제 U+FFFD 문자와 escaped backslash는 원래 문자열 값으로 보존합니다. malformed JSON에서 요청 정보를 읽을 수 없으면 오류 응답의 `op`는 빈 문자열이고 `request_id`는 `null`입니다. frame이 크기 제한을 넘으면 연결을 종료합니다.

## 오류 코드

| code | 의미 |
|---|---|
| `INVALID_MESSAGE` | 형식·값·미지원 op 오류 |
| `ROOM_FULL` | 정원 초과 |
| `ROOM_NOT_FOUND` | 최초 조회 시 해당 번호 없음 |
| `ROOM_CLOSED` | 진행 중 admission의 대상 룸이 닫힘 |
| `ROOM_IN_PROGRESS` | 게임 중 입장 거절용 코어 계약; 현재 서버는 WAITING만 제공 |
| `ALREADY_IN_ROOM` | 이미 참가 중이거나 admission 진행 중 |
| `NOT_IN_ROOM` | 퇴장할 membership 없음 |
| `PASSWORD_REQUIRED` | private 입장에 비밀번호 필요 |
| `PASSWORD_INVALID` | private 비밀번호 불일치 |
| `RATE_LIMITED` | 실패 시도 제한 중; TTL 만료 후 재시도 |
| `SERVER_BUSY` | 세션·룸·actor·hash queue 등 처리 상한 도달 |
| `SERVER_CLOSING` | 서버 종료 중 |
| `SESSION_CLOSED` | 종료된 세션 |
| `REQUEST_TIMEOUT` | 요청 취소 또는 제한 시간 초과 |

연결 수용이 불가능하면 WebSocket upgrade 전 HTTP 503을 반환할 수 있습니다. 종료·read/write 실패·유한 queue 포화 시에는 연결을 종료하므로 마지막 오류 frame 수신을 보장하지 않습니다.

## 설정

환경변수는 시작할 때 검증합니다. 값이 잘못되면 해당 변수 이름과 고정된 오류만 출력하고 종료하며 원문 값을 출력하지 않습니다. 아래 상한은 메모리·대기 작업 제한이며 수용 규모나 성능 보장이 아닙니다.

| 변수 | 기본값 | 허용 범위·형식 |
|---|---:|---|
| `HTTP_ADDR` | `127.0.0.1:8080` | 비어 있지 않은 host와 port 1~65535 |
| `WS_ORIGINS` | 추가 목록 없음 | 쉼표로 나눈 명시 host 또는 host:port; wildcard·scheme·공백 금지 |
| `ROOM_MAX_ROOMS` | 128 | 1~4096 |
| `ROOM_MAX_SESSIONS` | 768 | 1~24576 |
| `ROOM_NUMBER_RETRIES` | 64 | 1~1024 |
| `ROOM_ACTOR_QUEUE` | 64 | 1~4096 |
| `ROOM_CLEANUP_QUEUE` | 6 | 6~4096 |
| `ROOM_EVENT_QUEUE` | 64 | 1~4096 |
| `ROOM_LIST_LIMIT` | 100 | 1~1000 |
| `ROOM_HASH_WORKERS` | 2 | 1~16 |
| `ROOM_HASH_QUEUE` | 8 | 1~256 |
| `ROOM_FAILURE_TTL` | 1m | 0 초과~1h, Go duration |
| `ROOM_IP_FAILURE_LIMIT` | 20 | 1~10000 |
| `ROOM_SESSION_FAILURE_LIMIT` | 5 | 1~10000 |
| `ROOM_ROOM_FAILURE_LIMIT` | 30 | 1~10000 |
| `ROOM_MAX_FAILURE_KEYS` | 4096 | 3~65536 |
| `ROOM_ARGON_MEMORY_KIB` | 19456 | 19456~65536 KiB |
| `ROOM_REQUEST_TIMEOUT` | 5s | 0 초과~1m |
| `ROOM_SHUTDOWN_TIMEOUT` | 10s | 0 초과~1m |
| `WS_MESSAGE_BYTES` | 16384 | 256~1048576 bytes |
| `WS_INGRESS_QUEUE` | 16 | 1~4096 |
| `WS_OUTBOUND_QUEUE` | 64 | 1~4096 |
| `WS_READ_TIMEOUT` | 2m | 0 초과~24h, 다음 frame 수신 제한 |
| `WS_WRITE_TIMEOUT` | 5s | 0 초과~1m, frame 송신 제한 |
| `WS_REQUEST_TIMEOUT` | 5s | 0 초과~1m |
| `WS_SHUTDOWN_TIMEOUT` | 10s | 0 초과~1m |

Argon2id의 고정값은 time=2, parallelism=1, salt=16bytes, hash=32bytes입니다. 실행 중인 Argon 계산은 취소로 즉시 중단되지 않습니다. worker는 계산이 끝날 때까지 유지하고 늦은 결과는 membership 변경에 사용하지 않습니다. room/transport 요청 timeout 중 먼저 도달한 제한이 적용됩니다.

Origin 검사는 기본으로 same host와 Origin이 없는 native 연결을 허용합니다. 외부 브라우저가 필요하면 `WS_ORIGINS=localhost:9000,example.com`처럼 추가합니다. `InsecureSkipVerify`는 켜지 않습니다. 제한에 쓰는 IP는 `RemoteAddr`에서 얻으며 `X-Forwarded-For`를 신뢰하지 않습니다. 외부 운영에서는 WSS 연결을 구성해야 합니다.

## 종료와 검증

`SIGINT` 또는 `SIGTERM`을 받으면 transport 신규 수용을 닫고 기존 WS를 cancel/close한 뒤 룸 세션·actor·hash worker를 정리하고 HTTP shutdown을 수행합니다. `http.Server.Shutdown`만으로 WS 정리가 되지 않으므로 transport가 직접 WS를 종료합니다. HTTP header timeout은 5s, idle timeout은 60s입니다.

`transport.Server.Close(ctx)`는 caller/config deadline이면 오류를 반환하면서 정리를 계속합니다. pending Accept와 연결 goroutine도 기다립니다. 정상 종료는 모든 정리가 끝난 뒤 반환합니다. 아직 끝나지 않은 Argon 계산 때문에 설정 시간을 넘길 수 있으며, 현재 실행 프로그램은 종료 timeout 오류가 반환되면 nonzero로 종료합니다. 운영 supervisor의 강제 종료 grace는 이 특성을 고려해야 합니다.

실제 실행 프로세스를 검사하려면 서버를 켜 둔 뒤 다른 터미널에서 다음을 실행합니다. 검증 CLI는 임시 룸을 만들고 검사 후 모든 WS를 닫습니다. assertion 실패나 연결 실패는 exit 1입니다.

```bash
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -race-runs 3 -timeout 30s
```

검사 범위는 public/private 실제 6 WS 입장, 7번째 `ROOM_FULL`, 비밀번호 누락→오류→정답, private 목록 제외, 5명 룸의 마지막 자리 두 연결 동시 입장입니다. `-race-runs`는 1~100, `-timeout`은 0 초과 Go duration입니다. 이 검사는 부하·게임 simulation 검증이 아닙니다.

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o bin/roomserver ./cmd/server
go build -o bin/roomcheck ./cmd/roomcheck
```

시험은 실제 `httptest` HTTP/WebSocket과 실제 룸 코어를 사용합니다. 정원·동시 입장·순서·재입장·host 승계·Origin·strict frame·read limit·timeout·reader 취소·queue 포화·종료 경합을 검사합니다. transport의 지연 검사는 package 내부 barrier로 수행하고 hash 진행 중 취소·늦은 결과는 코어의 별도 barrier 시험이 검사합니다.

`room.start`, `game.action`, game simulation, admin HTTP 경로, 지속 audit, Godot client·게임 화면, 자동 재접속 복원은 현재 미구현입니다. 미지원 WS op는 `INVALID_MESSAGE`, admin 경로는 HTTP 404입니다. Godot은 후속 custom WebSocket + JSON client를 구현해야 합니다.

## 고정한 dependency와 공식 자료

- [`github.com/coder/websocket v1.8.15`](https://pkg.go.dev/github.com/coder/websocket): Accept/Read/Write/CloseNow/Origin 계약. [공식 release](https://github.com/coder/websocket/releases/tag/v1.8.15), Go 최소 1.23.
- [`golang.org/x/crypto v0.58.0`](https://pkg.go.dev/golang.org/x/crypto/argon2): Argon2id, Go 최소 1.26.
- [`golang.org/x/sys v0.49.0`](https://pkg.go.dev/golang.org/x/sys): crypto의 간접 dependency.

`go.mod`는 Go 1.26.0과 위 버전을 고정하고 실제 import 후 `go mod tidy`로 정리했습니다.
