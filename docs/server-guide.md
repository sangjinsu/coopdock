# 서버 사용·운영 가이드

Go **1.26 이상**이 필요합니다. `roomserver`는 내부 module 이름입니다. 이 서버는 하나의 프로세스에서 guest 연결, 룸 생성, ASCII 6자리 번호 입장, private 비밀번호 확인, 공개 목록, 퇴장과 연결 정리, host의 게임 시작과 서버 권위 3D 이동을 제공합니다.

```bash
cd coopdock
go run ./cmd/server
```

처음 실행한다면 [빠른 시작](getting-started.md)을, 목표 기능과 후속 설계는 [기능별 스펙](specs/README.md)과 [전체 설계](go-room-server-design.md)를 참조하세요. 게임 처리와 운영 조회의 내부 경로는 [현재 서버 구조](architecture.md)에 정리했습니다. 문서 전체는 [문서 길잡이](README.md)에서 찾을 수 있습니다.

기본 주소는 `127.0.0.1:8080`입니다. 다른 주소가 필요하면 `HTTP_ADDR=127.0.0.1:9090 go run ./cmd/server`처럼 지정합니다.

하나의 서버에서 여러 게임의 룸을 분리하려면 시작할 때 `GAME_IDS=default,game-a,game-b`를 설정하고 각각 `ws://127.0.0.1:8080/ws?game_id=game-a`처럼 접속합니다. `GAME_IDS`는 1~32개의 고유 ID를 쉼표로 나눈 allowlist이며 ID는 ASCII `[a-z0-9][a-z0-9_-]{0,63}`입니다. 설정 항목 양끝 공백은 제거하지만 빈 항목·중복·잘못된 ID는 시작을 거절합니다. 미설정 기본값은 `default`입니다. bare `/ws`는 등록 목록이 정확히 `default` 하나일 때만 호환됩니다. 여러 게임 또는 단일 named game 구성에서는 `game_id`를 반드시 명시합니다.

연결 query는 `game_id` 하나만 허용하고 raw query는 최대 1024 bytes입니다. 누락(위 호환 예외 제외)·빈 값·중복·잘못된 ID·미정의 key·malformed query는 upgrade 전에 HTTP 400 `Invalid game selection.`, 형식은 맞지만 미등록 ID는 404 `Game was not found.`입니다. 이 단계는 WS JSON 오류를 보내지 않습니다. 연결의 게임은 guest 발급 시 고정되며 변경하려면 연결을 끊고 새 guest로 접속합니다. room 요청 payload에 `game_id`를 추가하면 `INVALID_MESSAGE`입니다.

create/list/join은 연결된 게임만 대상으로 하며 public 목록에서도 다른 게임의 룸을 숨깁니다. 번호는 게임 안에서만 고유하여 서로 다른 게임에서 같은 6자리 번호가 존재할 수 있습니다. 다른 게임에만 있는 번호는 private 비밀번호 유무와 관계없이 `ROOM_NOT_FOUND`입니다. 같은 번호의 자기 게임 룸이 있으면 그 룸을 대상으로 기존 입장 검사를 수행합니다. `game_id`는 공개 namespace이며 인증 증명이 아닙니다. 누구나 등록된 ID로 새 연결을 선택할 수 있고, 게임별 인증·인가는 별도 정책이 필요합니다.

등록된 모든 게임은 현재 공통 reference movement handler를 사용합니다. namespace 분리는 게임마다 다른 rules·action/state schema·지형·승패를 등록하는 기능이 아닙니다. 게임별 rules 지원은 후속 설계입니다. room/session 상한·hash worker/queue·IP 실패 한도는 전체 게임이 공유하며 게임별 quota와 CPU/성능 격리를 보장하지 않습니다.

| HTTP 경로 | 동작 |
|---|---|
| GET `/healthz` | 프로세스 상태: 200 |
| GET `/readyz` | 신규 수용 중 200, 종료 시작 이후 503 |
| GET `/ws` | WebSocket guest 연결 |
| GET/HEAD `/admin`, `/admin/` | Basic 인증된 운영 HTML |
| GET/HEAD `/admin/assets/dashboard.css`, `/admin/assets/dashboard.js` | 같은 인증으로 보호된 embed 자산 |
| GET/HEAD `/admin/api/status`, `/admin/api/rooms` | 게시된 운영 cache 조회 |

연결마다 새 guest ID를 발급합니다. 클라이언트는 자기 `session_id`를 요청에 지정할 수 없습니다. 한 연결은 동시에 한 룸에만 속하며 생성자는 자동 입장하고 host가 됩니다. 룸 정원은 1~6명, 미지정 기본값은 6명입니다. host 연결이 끊어지거나 퇴장하면 먼저 입장한 잔여 참가자가 host를 이어받습니다. 마지막 참가자가 나가면 룸은 삭제됩니다.

모든 데이터는 메모리에 있습니다. 재시작하면 룸·비밀번호 자료·membership이 사라집니다. 재연결은 새 guest이므로 private 비밀번호를 다시 확인합니다. 룸의 visibility와 비밀번호는 수명 동안 고정입니다.

## 운영 대시보드 켜기

운영 인증은 게임 guest·private 룸 비밀번호와 별개입니다. 아래 예제는 Bash에서 token을 화면과 shell history에 남기지 않고 입력받습니다. macOS 기본 zsh에서는 먼저 `bash`를 실행하세요.

```bash
read -r -s ADMIN_VIEWER_TOKEN
export ADMIN_VIEWER_TOKEN
go run ./cmd/server
```

입력값은 **32~256 bytes, 각 byte가 `0x21..0x7e`인 공백 없는 printable ASCII**여야 합니다. 빈 값 또는 미설정이면 모든 admin 경로가 404이며 collector와 runtime sampler를 만들지 않습니다. 잘못된 비어 있지 않은 값은 `Invalid ADMIN_VIEWER_TOKEN.`으로 시작을 거절합니다. 설정한 뒤 [로컬 대시보드](http://127.0.0.1:8080/admin)를 열고 browser의 HTTP Basic challenge에 username `viewer`, password로 token을 입력합니다. token을 URL·로그·응답 예제에 넣지 마세요.

미인증·잘못된 인증은 401과 Basic challenge입니다. 서버는 매 요청을 검사하고 HTML·CSS·JS도 인증 뒤 제공합니다. 알려진 조회 경로의 GET/HEAD 외 method는 405(`Allow: GET, HEAD`), viewer의 kick/close/drain POST는 403이며 실행하지 않습니다. 상세·commands와 그 외 미구현 경로는 인증 후 404입니다. admin 응답에는 오류까지 `Cache-Control: no-store`, self CSP, `nosniff`, frame `DENY`를 적용하며 외부 CDN·font·image는 사용하지 않습니다. `WS_ORIGINS`는 게임 WebSocket Origin 설정이며 운영 인증 정책이 아닙니다.

HTTP Basic은 암호화가 아닙니다. 외부 운영은 기존 reverse proxy에서 HTTPS로 종료하고 Go 서버의 loopback upstream으로 전달하는 구성으로 사용하세요. 게임 연결에는 WSS와 WebSocket upgrade 전달이 필요합니다. 이번 조회 MVP는 proxy·TLS·배포 설정을 제공하거나 변경하지 않습니다. token을 로그에서 제외하고 proxy access log에도 Authorization을 기록하지 마세요.

Basic 자격증명은 browser가 native cache에 보관할 수 있어 앱이 logout을 보장하지 않습니다. `다시 인증`은 현재 페이지를 reload하며 자격증명 cache 삭제를 보장하지 않습니다. token 폐기·변경은 환경변수를 바꾸고 서버를 다시 시작해 처리하며 재시작은 메모리 룸을 잃습니다.

### 화면과 자료의 의미

- `RUNNING`은 서버 실행 상태이고 `ready`는 신규 게임 수용 gate입니다. `STOPPING`은 종료 진행입니다. readiness true는 여유 정원·낮은 지연·모든 자료의 최신 상태를 보장하지 않습니다. DRAINING과 admin drain은 후속입니다.
- `active_connections`는 룸 밖을 포함한 live game 연결 수, `room_member_count`는 실제 룸 membership 합계입니다. 룸 수와 WAITING/PLAYING 룸 수를 따로 표시합니다.
- 룸 목록은 public/private 모두 표시하고 `game_id` 열로 게임을 구분합니다. 게임 `room.list`는 연결된 게임의 public-only를 유지합니다. 번호는 ASCII 6자리 **문자열**로 앞 0을 유지하며 내부 `room_id`, host ID, nullable match ID를 구분합니다. WAITING의 `match_id`는 `null`입니다.
- heap은 `HeapAlloc` bytes를 MiB로 표시하며 OS RSS가 아닙니다. goroutine은 Go 실행 작업 수이며 플레이어 수가 아닙니다. CPU·RTT·클라이언트 표시지연은 API `null`, 화면 `미측정`입니다.

room actor summary·background collector·runtime sampler는 각각 1초 주기입니다. HTTP는 cache만 읽으며 actor/manager lock·callback·runtime sampling을 실행하지 않습니다. actor summary 게시에는 게임 `snapshot()`을 재사용하지 않으며 revision·snapshot_seq를 증가시키지 않습니다. admin이 꺼져도 actor의 1초 summary heartbeat는 유지합니다. collector/runtime loop는 0개지만 이 고정 게시 비용은 남으며 최대 룸 수는 `ROOM_MAX_ROOMS`로 제한합니다. 게시·runtime sampling의 실제 게임 부하 영향은 미측정입니다.

자동 갱신은 두 API 조회 batch **완료 후 5초**, batch timeout은 5초입니다. 겹치는 조회를 만들지 않습니다. 자동 갱신 해제는 다음 자동 조회를 중단하고 진행 중 조회는 완료하며, hidden tab·pagehide는 진행 중 요청도 취소합니다. BFCache의 persisted pageshow 복귀에서는 표시 age와 lifecycle을 복구하며 visible·auto on·인증 유효 조건에서만 다시 조회합니다. 일반 초기 pageshow는 조회를 중복하지 않습니다. BFCache 적격성은 browser가 결정합니다.

`updated_at`은 응답 생성 시각이 아닙니다. status와 rooms envelope에서는 cache 게시 시각, 각 row에서는 actor summary 게시 시각이며 UTC RFC3339 문자열입니다. `started_at`은 서버 instance 시작 시각, `uptime_seconds`는 경과시간입니다. `generation`은 cache 게시 세대이며 게임 revision이 아닙니다. HTTP가 계산한 `age_ms`가 5000 이상이면 `stale=true`입니다. UI는 수신 뒤 monotonic 경과시간을 더해 표시하므로 수동 mode에서도 오래된 자료를 최신으로 고정하지 않습니다. cache가 최신이어도 개별 actor row는 stale일 수 있습니다.

일반 조회 실패는 API별 마지막 성공 자료·필터·시각을 유지하고 오류를 표시합니다. 최초 실패는 `—`를 유지하며 성공한 0과 구별합니다. 401/403에서는 자료를 지우고 polling을 멈춥니다. 서버를 종료한 뒤의 polling 실패는 서버 종료 때문일 수 있으며 게임 입력 실패나 drain 명령 실패를 뜻하지 않습니다.

### 룸 검색과 API

UI는 100개씩 표시하고 이전/다음, 게임 ID exact match·번호 앞자리·phase·visibility 필터를 제공합니다. 서버는 **전체 cache에 필터를 먼저 적용한 뒤** `(game_id, room_number, room_id)` 오름차순으로 정렬하고 pagination합니다. 빈 서버와 필터 결과 없음도 구별합니다.

| `/admin/api/rooms` query | 기본값 / 허용값 |
|---|---|
| `limit` | 100 / ASCII 정수 1~100 |
| `offset` | 0 / ASCII 정수 0~4096 |
| `number_prefix` | 빈 문자열 / ASCII 숫자 0~6자리 |
| `phase` | `all` / `all`, `WAITING`, `PLAYING` |
| `visibility` | `all` / `all`, `public`, `private` |
| `game_id` | 생략 시 전체 / 위 ID 문법의 정확한 값; 빈 값 금지 |

알 수 없는 key·중복 key·잘못된 값·malformed query는 400입니다. 문법이 맞는 미등록 `game_id` 필터는 200과 빈 결과를 반환합니다. `total`은 필터와 관계없이 모든 게임의 룸 수, `matched_total`은 모든 필터 일치 수, `rooms`는 현재 페이지입니다. status의 연결·membership·룸 counter도 서버 전체 합계입니다. admin은 인증된 전체 운영 조회이며 게임별 tenant 권한을 제공하지 않습니다. 결과가 없으면 배열 `[]`이며 `null`이 아닙니다. 정확한 JSON 필드는 [메시지와 API](specs/04-messages-and-api.md#현재-운영-json-계약)에 있습니다. summary/cache/API/HTML/로그에는 password·hash·salt·운영 token·전체 member ID 목록·game_state를 넣지 않습니다.

## 실제 WebSocket 메시지

모든 envelope에는 `version`, `op`, `request_id`, `payload`, `error`가 명시됩니다. 요청의 `version`은 `1`, `error`는 `null`이며 `op`는 최대 64 UTF-8 bytes입니다. 요청 `request_id`는 비어 있지 않은 문자열이며 최대 128 UTF-8 bytes입니다. 같은 ID를 다시 보내도 중복 실행이 방지되지 않습니다. 응답은 같은 `op`와 `request_id`를 사용하고, 서버 이벤트의 `request_id`는 `null`입니다.

연결 직후 서버 이벤트:

```json
{"version":1,"op":"session.guest","request_id":null,"payload":{"session_id":"server-issued-id","game_id":"game-a"},"error":null}
```

공개 룸 생성 요청과 번호 입장 요청:

```json
{"version":1,"op":"room.create","request_id":"create-public","payload":{"capacity":6,"visibility":"public"},"error":null}
{"version":1,"op":"room.join","request_id":"join-public","payload":{"room_number":"004271"},"error":null}
```

생성 및 입장 성공 응답의 `payload.snapshot`은 전체 공개 상태입니다. 아래 ID·번호는 형식 예시입니다.

```json
{"version":1,"op":"room.create","request_id":"create-public","payload":{"snapshot":{"game_id":"game-a","room_id":"room-id","room_number":"004271","visibility":"public","capacity":6,"match_id":null,"phase":"WAITING","host_id":"guest-id","members":[{"session_id":"guest-id"}],"revision":1,"game_state":null,"server_tick":0,"snapshot_seq":1,"last_processed_input_seq":{"guest-id":0}}},"error":null}
```

이 응답과 같은 revision의 초기 `room.state`를 processor에서 바로 이어 송신 FIFO에 넣고 이후 상태 이벤트를 전달합니다. core가 보내는 같은 revision의 초기 중복은 제외합니다. full 상태를 새로 캡처할 때마다 `snapshot_seq`가 증가하며 하나의 capture를 response/event에 재사용하면 같은 번호입니다. `room.state.payload`는 위 `snapshot` 내부 구조이며 `request_id`는 `null`입니다. 퇴장 후 재입장할 때 이전 membership의 오래된 이벤트는 새 입장 초기 상태보다 앞서 전달하지 않습니다.

`session.guest.payload.game_id`와 모든 full snapshot의 `game_id`는 **version 1 응답 schema의 추가 필드**입니다. create/join/start 응답의 snapshot, room.state/game.snapshot, game.started/game.ended 내부 snapshot, room.list의 각 항목에 포함됩니다. envelope와 기존 request payload·action ACK·leave ACK·game.event는 유지합니다. bare URL 호환만으로 strict 구버전 client가 호환되지는 않습니다. guest/snapshot key 집합을 엄격히 검사하는 client는 서버와 함께 업데이트해야 하며 모든 구버전 client의 호환을 보장하지 않습니다. 갱신된 Godot adapter는 요청한 게임과 guest·list·snapshot의 게임 일치를 검증하고 게임 ID가 없는 구서버 응답도 거절합니다.

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

## 실시간 3D reference 이동

host는 1명만 있어도 `WAITING` 룸에서 시작할 수 있습니다. `room.start` 성공 응답은 `payload.snapshot`에 초기 full 상태를 담고, 같은 연결에서는 응답 → `game.started` → `room.state` 순서로 받습니다. `game.started.payload.snapshot`은 초기 full 상태를, `room.state.payload`는 full 상태를 직접 담습니다. 다른 참가자는 `game.started` → `room.state`를 받습니다.

```json
{"version":1,"op":"room.start","request_id":"start","payload":{},"error":null}
{"version":1,"op":"game.action","request_id":"move-1","payload":{"match_id":"server-issued-match","input_seq":1,"action":{"type":"move","direction":{"x":0,"y":0,"z":1}}},"error":null}
{"version":1,"op":"game.action","request_id":"move-1","payload":{"match_id":"server-issued-match","input_seq":1,"server_tick":1},"error":null}
{"version":1,"op":"game.action","request_id":"stop-2","payload":{"match_id":"server-issued-match","input_seq":2,"action":{"type":"move","direction":{"x":0,"y":0,"z":0}}},"error":null}
```

ID와 ACK tick은 형식 예시입니다. ACK는 입력 접수 때가 아니라 방향 Apply와 해당 tick 계산이 성공한 뒤에 보냅니다. 같은 tick의 여러 방향 setter는 각각 ACK하며 마지막 방향으로 그 tick을 계산합니다. ACK 자체가 특정 이동 거리를 보장하지는 않습니다.

`move`는 지속 방향 갱신입니다. 후속 입력이 없어도 이동하며 `(0,0,0)`이 정지입니다. 각 x/y/z는 필수 finite numeric scalar이고 `[-1,1]`만 허용합니다. 벡터 길이가 1을 넘으면 정규화하고 1 이하이면 크기를 보존합니다. client position/speed/dt/timestamp와 미정의 필드는 거절합니다. 초기 위치는 입장 순서 index `i`에 `(2*i,0,0)`, 초기 속도는 0, 최대 속도는 초당 5 world units입니다. 축은 추상 world 좌표이며 렌더링의 좌우손 규칙을 정하지 않습니다.

PLAYING full 상태의 `game_state` 예시:

```json
{"players":[{"session_id":"guest-id","position":{"x":0,"y":0,"z":0.16666666666666666},"velocity":{"x":0,"y":0,"z":5}}]}
```

players와 members는 살아 있는 참가자의 입장 순서입니다. `game.snapshot.payload`는 `room.state.payload`와 같은 full 구조로, `match_id`, `revision`, `server_tick`, `snapshot_seq`, 참가자별 `last_processed_input_seq`와 `game_state`를 같은 시점에 캡처합니다. 상태가 변한 tick마다 revision을 한 번 증가시키고 idle tick은 revision을 유지합니다. `input_seq`는 JSON 정수 1~9007199254740991이며 gap은 허용합니다. 세션 수명 동안 성공 적용된 번호만 소비하고 중복·역순은 거절합니다. 실패 번호는 더 큰 번호가 아직 적용되지 않았다면 재사용할 수 있습니다. 새 match에서도 세션 번호와 룸의 snapshot 번호를 초기화하지 않습니다.

tick마다 고정 `dt=1/TickHz` 한 단계만 실행하고 놓친 ticker 신호는 보충하지 않습니다. 부하로 tick이 빠지면 simulation이 wall time보다 느려질 수 있습니다. 30Hz 계산·15Hz snapshot 기본값은 실제 완료율이나 도착 지연 보장이 아닙니다. 중요 응답·이벤트는 bounded FIFO로 보존하고 아직 송신하지 않은 주기 snapshot만 최신 한 칸으로 교체합니다. 전환 시 이전 latest를 폐기하고 원자 batch로 중요 순서를 등록합니다. 이미 write 시작이 확정된 snapshot은 마친 뒤 전환을 송신합니다.

PLAYING 신규 입장·재입장은 `ROOM_IN_PROGRESS`입니다. 일부 참가자가 나가면 해당 player와 pending 입력을 정리하고 match를 유지합니다. 마지막 참가자가 나가면 룸과 ticker를 정리합니다. 정상 게임 종료는 handler의 `EndReason` 경로입니다. reference에는 자동 종료·승패 조건이 없으며 client `room.end` 요청도 없습니다. `game.ended.payload`는 이전 `match_id`, `reason`, 마지막 `server_tick`, WAITING full 상태의 `snapshot`을 담고 이후 같은 상태의 `room.state`가 옵니다. 정상 종료·다음 match는 교체 가능한 test handler로 검증합니다.

## 오류 코드

| code | 의미 |
|---|---|
| `INVALID_MESSAGE` | 형식·값·미지원 op 오류 |
| `ROOM_FULL` | 정원 초과 |
| `ROOM_NOT_FOUND` | 최초 조회 시 해당 번호 없음 |
| `ROOM_CLOSED` | 진행 중 admission의 대상 룸이 닫힘 |
| `ROOM_IN_PROGRESS` | PLAYING 룸의 신규 입장·재입장 거절 |
| `ALREADY_IN_ROOM` | 이미 참가 중이거나 admission 진행 중 |
| `NOT_IN_ROOM` | 현재 룸 membership 없음 |
| `NOT_HOST` | 현재 host만 시작 가능 |
| `GAME_NOT_STARTED` | 아직 시작되지 않은 게임의 입력 |
| `STALE_MATCH` | 현재 match와 다른 입력 |
| `INVALID_INPUT_SEQ` | 입력 번호 범위·중복·역순 오류 |
| `INPUT_CANCELED` | 퇴장·종료로 미적용 입력 취소 |
| `GAME_ERROR` | handler 생성·시작·계산·퇴장 hook 오류 |
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
| `ADMIN_VIEWER_TOKEN` | 미설정(비활성) | 설정 시 32~256 bytes, `0x21..0x7e`만 허용 |
| `HTTP_ADDR` | `127.0.0.1:8080` | 비어 있지 않은 host와 port 1~65535 |
| `GAME_IDS` | `default` | 쉼표 구분 고유 ID 1~32개; ASCII `[a-z0-9][a-z0-9_-]{0,63}` |
| `WS_ORIGINS` | 추가 목록 없음 | 쉼표로 나눈 명시 host 또는 host:port; wildcard·scheme·host 내부 공백 금지, 항목 양끝 공백 제거 |
| `ROOM_MAX_ROOMS` | 128 | 1~4096 |
| `ROOM_MAX_SESSIONS` | 768 | 1~24576 |
| `ROOM_NUMBER_RETRIES` | 64 | 1~1024 |
| `ROOM_TICK_HZ` | 30 | 1~120 |
| `ROOM_SNAPSHOT_HZ` | 15 | 1~ROOM_TICK_HZ |
| `ROOM_INPUT_QUEUE` | 256 | 1~4096; inbox+pending 합계 |
| `ROOM_INPUTS_PER_TICK` | 64 | 1~ROOM_INPUT_QUEUE |
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

`ROOM_MAX_ROOMS`·`ROOM_MAX_SESSIONS`·hash pool·queue·failure-key 상한과 IP 실패 counter는 모든 게임이 공유합니다. 같은 번호라도 게임이 다르면 서로 다른 룸이며 룸 실패 counter가 합쳐지지 않습니다. 같은 IP의 여러 게임 실패는 공통 IP 한도에 누적됩니다. 한 게임이 전체 수용량을 사용할 수 있습니다.

Origin 검사는 기본으로 same host와 Origin이 없는 native 연결을 허용합니다. 외부 브라우저가 필요하면 `WS_ORIGINS=localhost:9000,example.com`처럼 추가합니다. `InsecureSkipVerify`는 켜지 않습니다. 제한에 쓰는 IP는 `RemoteAddr`에서 얻으며 `X-Forwarded-For`를 신뢰하지 않습니다. 외부 운영에서는 WSS 연결을 구성해야 합니다.

## 종료와 검증

`SIGINT` 또는 `SIGTERM`을 받으면 transport 신규 수용을 닫고 기존 WS를 cancel/close한 뒤 룸 세션·actor·hash worker를 정리하고 HTTP shutdown을 수행합니다. `http.Server.Shutdown`만으로 WS 정리가 되지 않으므로 transport가 직접 WS를 종료합니다. HTTP header timeout은 5s, idle timeout은 60s입니다.

`transport.Server.Close(ctx)`는 caller/config deadline이면 오류를 반환하면서 정리를 계속합니다. pending Accept와 연결 goroutine도 기다립니다. 정상 종료는 모든 정리가 끝난 뒤 반환합니다. 아직 끝나지 않은 Argon 계산 때문에 설정 시간을 넘길 수 있으며, 현재 실행 프로그램은 종료 timeout 오류가 반환되면 nonzero로 종료합니다. 운영 supervisor의 강제 종료 grace는 이 특성을 고려해야 합니다.

실제 실행 프로세스를 검사하려면 서버를 켜 둔 뒤 다른 터미널에서 다음을 실행합니다. 검증 CLI는 임시 룸을 만들고 검사 후 모든 WS를 닫습니다. assertion 실패나 연결 실패는 exit 1입니다.

```bash
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -game-id default -movement -timeout 30s
# Run only the room admission checks.
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -game-id default -movement=false -race-runs 3 -timeout 30s
# With GAME_IDS=default,game-a,game-b, also check foreign namespace isolation.
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -game-id game-a -other-game-id game-b -timeout 30s
```

검사 범위는 public/private 실제 6 WS 입장, 7번째 `ROOM_FULL`, 비밀번호 누락→오류→정답, private 목록 제외, 5명 룸의 마지막 자리 두 연결 동시 입장입니다. `-race-runs`는 1~100, `-timeout`은 0 초과 Go duration입니다. `-movement`는 기본 true이며 기존 입장 검사 이후 별도 6 WS로 시작 → 각 참가자의 seq1 이동 ACK → 두 capture의 양의 Z 이동·불변 X/Y·속도 5 → seq2 정지 ACK → 추가 두 capture의 정지 위치 유지를 검사합니다. 같은 `(match_id,revision,server_tick,snapshot_seq)`를 6명 모두 받으면 full 상태·member 순서·seq map을 비교합니다. reader는 연결별 하나이며 수신 채널과 최근 capture key는 각각 최대 128개, overflow는 실패입니다. 서버 TickHz를 추측해 이동 거리 공식을 강제하지 않습니다. 마지막 6개 퇴장 ACK와 빈 룸 제거를 확인하고 WS를 닫습니다. 부하·Godot 화면 검증은 포함하지 않습니다.

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

시험은 실제 `httptest` HTTP/WebSocket과 실제 룸 코어를 사용합니다. 정원·동시 입장·순서·재입장·host 승계·Origin·strict frame·read limit·timeout·reader 취소·queue 포화·종료 경합을 검사합니다. transport의 지연 검사는 package 내부 barrier로 수행하고 hash 진행 중 취소·늦은 결과는 코어의 별도 barrier 시험이 검사합니다.

## 현재 단계

현재 프로젝트는 초기 reference 단계입니다. 게임별 룸 namespace, free-space reference 3D 이동과 서버 동기화, 조회용 admin MVP, custom WebSocket + JSON [Godot desktop 3D/2D 예제](../examples/godot/README.md)를 제공합니다. 예제는 snapshot 위치만 표시하고 수신 간격 기반 bounded 보간을 사용합니다. 2D는 XZ를 32px/unit으로 표시하고 숨겨진 서버 Y를 보존합니다.

게임별 지형·충돌·중력·협동 목표·승패, admin 상세·조작·commands, 지속 audit, 전용 SDK·local prediction, 배포 구성, 자동 재접속 복원은 현재 미구현입니다. 실제 TCP 손실·jitter 주입, 운영 부하·수용 규모·표시 지연·목표 Hz는 측정하지 않았습니다. Godot 4.4 API 기준이며 실제 엔진 검증은 4.7.2입니다. web export·TLS 배포·OS 수동 keyboard/focus 검증은 포함하지 않습니다. deterministic barrier와 race 시험을 실제 네트워크 손실 시험이나 성능 보장으로 해석하지 않습니다. 미지원 WS op는 `INVALID_MESSAGE`이며 후속 admin 경로는 실행하지 않습니다.

## 고정한 dependency와 공식 자료

- [`github.com/coder/websocket v1.8.15`](https://pkg.go.dev/github.com/coder/websocket): Accept/Read/Write/CloseNow/Origin 계약. [공식 release](https://github.com/coder/websocket/releases/tag/v1.8.15), Go 최소 1.23.
- [`golang.org/x/crypto v0.58.0`](https://pkg.go.dev/golang.org/x/crypto/argon2): Argon2id, Go 최소 1.26.
- [`golang.org/x/sys v0.49.0`](https://pkg.go.dev/golang.org/x/sys): crypto의 간접 dependency.

`go.mod`는 Go 1.26.0과 위 버전을 고정하고 실제 import 후 `go mod tidy`로 정리했습니다.
