# 04. 메시지와 API

**게임은 WebSocket의 JSON 메시지로, 운영 화면은 HTTP API로 서버와 이야기합니다.**
프로토콜은 서로 어떤 형식과 순서로 데이터를 주고받을지 정한 약속입니다.

Godot 활용은 확정 요구사항입니다. 연결은 custom WebSocket + JSON으로 제안하며 전용 SDK는 필수가 아닙니다.
아래 version 1 메시지와 조회 API는 현재 서버 계약입니다. 운영 상세·조작은 아래의 후속 API 절에 구분했습니다. 실행·설정·검증 범위는 [서버 가이드](../server-guide.md)를 참조하세요. Godot 예제의 전체 구현·화면 검증 진척은 별도 client 작업 범위입니다.

## 게임 namespace 선택

서버의 `GAME_IDS`는 쉼표로 나눈 고유 ID 1~32개의 allowlist이며 기본값은 `default`입니다. 각 ID는 ASCII `[a-z0-9][a-z0-9_-]{0,63}`이고 설정 항목 양끝 공백을 제거한 뒤 빈 값·중복·잘못된 ID를 시작 시 거절합니다. `GAME_IDS=default,game-a,game-b`로 실행했다면 게임 A는 `ws://127.0.0.1:8080/ws?game_id=game-a`, 게임 B는 `ws://127.0.0.1:8080/ws?game_id=game-b`에 접속합니다. bare `/ws`는 allowlist가 정확히 `default` 하나일 때만 허용합니다. 다중 게임이나 단일 named game 구성에서는 ID가 필수입니다.

handshake query는 `game_id` 하나만 허용하며 raw query 최대 1024 bytes입니다. 누락(위 예외 제외)·빈 값·중복(decoded key 기준)·문법 오류·미정의 key·malformed query는 upgrade 전에 HTTP 400 `Invalid game selection.`, 형식이 맞는 미등록 ID는 404 `Game was not found.`입니다. 실패 handshake는 JSON envelope나 guest를 만들지 않습니다. 기존 shutdown·전역 자원 부족의 503 경로는 유지합니다.

연결의 게임 binding은 guest 발급 시 고정되며 모든 room 생성·공개 목록·번호 입장은 그 게임을 대상으로 합니다. 번호는 게임 안에서만 고유합니다. 다른 게임에만 있는 번호는 `ROOM_NOT_FOUND`, 같은 번호의 자기 게임 룸이 있으면 자기 게임 룸에 대한 기존 검사를 수행합니다. private join의 순서는 형식 검사 → session reservation → 해당 게임 lookup → 비밀번호 누락/실패 제한/hash → phase·capacity 검사입니다. 타게임 private 번호는 비밀번호 유무·오답·정답 모두 `ROOM_NOT_FOUND`이며 해당 룸의 hash·실패 counter를 사용하지 않습니다.

요청 payload에는 `game_id`를 넣지 않습니다. 넣으면 기존 strict 규칙으로 `INVALID_MESSAGE`입니다. 게임을 바꾸려면 disconnect 후 새 연결·guest가 필요합니다. namespace는 공개 routing 정보이며 인증 증명이 아닙니다. 누구나 등록된 게임을 선택할 수 있고 게임별 인증·인가는 별도 정책입니다. room/session 상한·hash pool/queue·IP 실패 한도는 전체 게임 공통이며 quota·자원 성능 격리는 제공하지 않습니다. 등록된 모든 namespace는 현재 공통 movement handler를 사용하고, 서로 다른 rules·action/state schema 지원은 후속입니다.

## 두 사람이 서버와 이야기하는 예

1. 민수가 연결하면 서버가 guest 세션 ID를 발급합니다.
2. 민수가 `room.create`로 룸을 만듭니다. 지연은 `room.join`에 번호를 보내고 private이면 비밀번호를 추가해 재요청합니다. 성공 응답에는 각각 초기 상태가 담깁니다.
3. 민수가 `room.start`를 요청하면 서버가 새 match를 시작하고 `game.started`를 전달합니다.
4. 두 사람은 `game.action`으로 이동 의도를 보내고, 실제 적용 응답과 `game.snapshot`을 받습니다.
5. 운영자는 GET으로 이 룸을 조회합니다. 후속 close 조작은 POST로 접수하고 명령 조회로 실제 결과를 확인하는 설계입니다.

게임 사용자가 자기 `session_id`를 주장해 권한을 얻지는 않습니다.
서버는 실제 연결의 세션과 현재 membership(룸 입장 관계)으로 검사하며, 사용자가 보낸 좌표·승패 결과를 믿지 않습니다.

## 게임 메시지의 공통 봉투

모든 메시지는 다음 공통 필드를 사용합니다. `op`는 할 일의 이름입니다.

| 필드 | 의미 |
|---|---|
| `version` | 프로토콜 버전 `1` |
| `op` | 요청·응답·이벤트 종류: 예를 들면 `room.join` |
| `request_id` | 요청과 응답을 연결하는 표식. 서버 이벤트는 `null` |
| `payload` | 해당 메시지의 데이터 |
| `error` | 실패 정보. 아래 정상 요청 예시는 `null` |

응답은 요청과 같은 `op`와 `request_id`를 반환합니다.
`request_id`를 반복한다고 중복 실행이 방지되지는 않습니다. 운영 명령의 중복 방지 규칙과는 별개입니다.

연결 직후 guest는 `{"session_id":"opaque-session","game_id":"game-a"}` payload를 보냅니다. guest의 `game_id`와 아래 full snapshot의 `game_id`는 **version 1 nested 응답 schema 확장**입니다. envelope의 다섯 필드·request payload·action/leave ACK·game.event는 유지합니다. guest/snapshot의 key 집합을 엄격히 검사하는 구버전 client는 업데이트해야 합니다. bare URL 호환은 strict schema 호환을 보장하지 않으며 legacy projection·protocol negotiation은 없습니다. 갱신된 Godot adapter는 guest·목록·모든 full snapshot을 요청한 게임과 비교하고 ID가 없는 구서버 응답도 거절합니다.

### 공개·프라이빗 룸을 만드는 JSON 예시

공개 룸은 password 필드를 넣지 않습니다. visibility 미지정 시 `public`을 기본값으로 제안합니다.

```json
{"version":1,"op":"room.create","request_id":"create-public","payload":{"capacity":6,"visibility":"public"},"error":null}
```

프라이빗 룸은 생성자가 사용할 비밀번호를 지정합니다. 아래 비밀번호는 문서 예시입니다.

```json
{"version":1,"op":"room.create","request_id":"create-private","payload":{"capacity":3,"visibility":"private","password":"coop2026"},"error":null}
```

생성이 확정되면 생성자는 자동 입장하고 host가 됩니다. 응답은 초기 snapshot을 담으며 password를 되돌려 보내지 않습니다.
private의 비밀번호 누락·잘못된 길이, public에 password 지정은 `INVALID_MESSAGE`입니다.

### 번호로 입장하고 비밀번호를 요청받는 예시

```json
{
  "version": 1,
  "op": "room.join",
  "request_id": "req-002",
  "payload": {"room_number": "004271"},
  "error": null
}
```

public이면 이 요청으로 일반 입장을 처리합니다. private에서 비밀번호가 없으면 다음 응답을 반환합니다.

```json
{"version":1,"op":"room.join","request_id":"req-002","payload":null,"error":{"code":"PASSWORD_REQUIRED","message":"Room password is required."}}
```

클라이언트는 비밀번호 입력을 받고 같은 번호로 다시 요청합니다. 별도 lookup 메시지는 필요하지 않습니다.

```json
{"version":1,"op":"room.join","request_id":"req-003","payload":{"room_number":"004271","password":"coop2026"},"error":null}
```

비밀번호가 맞아도 룸이 `WAITING`이고 자리가 있어야 성공합니다. `PLAYING`은 비밀번호가 맞아도 입장을 거절합니다.
성공한 create/join의 `payload.snapshot`에는 내부 ID·번호·공개 여부와 전체 공개 상태가 담깁니다.
요청에 내부 `room_id`를 보내 번호 입장을 대신하지 않습니다.

| 요청 필드 | 초기 계약 |
|---|---|
| create `capacity` | 정수 1~6, 미지정 6 제안 |
| create `visibility` | `public` 또는 `private`, 미지정 public 제안 |
| create `password` | private에 필수, public에서는 금지. 길이 4~64 Unicode 문자(code point) 제안 |
| join `room_number` | ASCII 숫자 6자리 문자열 제안. `"004271"`의 앞 0 보존 |
| join `password` | private 입장에 필요. 대소문자 구분, 임의 공백 제거·Unicode 정규화 없음 |

번호 형식·visibility·password 타입/길이 오류는 `INVALID_MESSAGE`입니다.
private join의 password 미지정은 `PASSWORD_REQUIRED`, 형식이 맞지만 값이 틀리면 `PASSWORD_INVALID`입니다.
membership을 유지하는 동안 다음 판마다 재입력하지 않으며, 퇴장 후 또는 새 guest 연결의 새 입장에서는 다시 확인합니다.
공개 여부와 비밀번호는 룸 수명 동안 고정하며 host 승계로 바뀌지 않습니다.

### 게임 조작을 보내는 JSON 예시

```json
{
  "version": 1,
  "op": "game.action",
  "request_id": "input-42",
  "payload": {
    "match_id": "match-7",
    "input_seq": 42,
    "action": {"type": "move", "direction": {"x": 1, "y": 0, "z": 0}}
  },
  "error": null
}
```

예시는 유효한 JSON의 설계 예시입니다. 존재하는 룸·match나 현재 제공되는 API를 뜻하지 않습니다.
현재 reference는 `move` 지속 방향을 구현합니다. x/y/z는 모두 필수 finite numeric scalar `[-1,1]`이며 길이 1 초과는 정규화, 이하이면 크기를 보존합니다. `(0,0,0)`은 정지이고 속도는 방향 × 5 world units/s입니다. position/speed/dt/timestamp나 미정의 필드는 거절합니다. `input_seq`는 JSON 정수 1~9007199254740991이며 gap은 허용하고 성공 적용 완료 번호 이하의 중복·역순은 거절합니다. 실패 번호는 더 큰 번호가 아직 적용되지 않았다면 재사용할 수 있습니다.

`room.start`는 host가 WAITING에서 `{}`로 요청하며 성공 응답 `payload.snapshot`을 먼저 보냅니다. 이어 `game.started.payload.snapshot` full 상태와 같은 `room.state.payload`가 옵니다. `game.action` 성공 `payload`는 `{"match_id":"match-7","input_seq":42,"server_tick":1}`이며 Apply와 tick 계산이 성공한 뒤 보냅니다. `game.ended.payload`는 `{"match_id":"old-match","reason":"handler reason","server_tick":42,"snapshot":waitingFullState}` 구조이고 다음 `room.state`도 같은 WAITING 상태를 씁니다. 정상 종료는 handler EndReason 경로이며 reference에는 자동 종료·승패 조건이 없습니다. client `room.end` op는 제공하지 않습니다.

## 게임 메시지 목록

| `op` | 역할 |
|---|---|
| `session.guest` | 서버가 발급한 guest `session_id`와 연결에 고정된 `game_id` 안내 |
| `room.create` / `room.list` / `room.join` | 공개·프라이빗 생성 / 공개 룸 목록 / 번호 입장 요청과 응답 |
| `room.leave` / `room.start` | 퇴장·방장의 시작 요청과 응답 |
| `game.action` | 현재 match에 대한 입력. 실제 적용 후 성공 응답 |
| `game.started` / `game.ended` | 서버가 알리는 게임 시작·종료 이벤트 |
| `game.event` | `event_id`가 있는 일회성 게임 이벤트 |
| `room.state` | 참가자·방장·상태 변경을 알리는 중요 메시지 |
| `game.snapshot` | 주기적으로 보내는 전체 게임 상태 |

요청 응답과 룸 변경·일회성 이벤트는 중요 FIFO(먼저 들어온 메시지를 먼저 내보내는 대기열)에 넣습니다.
주기 full snapshot만 연결별 최신 슬롯에서 미전송 상태를 교체할 수 있습니다.
게임 입력의 성공 ACK는 **실제 적용 완료**를 뜻합니다. 접수나 대기열 등록만으로 성공을 답하지 않습니다.
플레이어 `room.list`에는 연결된 게임의 public만 표시합니다. private는 해당 게임에서 번호를 공유해 입장하고 운영 목록에서는 조회할 수 있습니다.

## 전체 상태에 담는 필드

`room.state.payload`와 `game.snapshot.payload`는 아래 상태 구조를 사용합니다.
성공한 create/join/start의 `payload.snapshot`, `game.started.payload.snapshot`에는 초기 full 상태를 담습니다.

| 필드 | 뜻 |
|---|---|
| `game_id` | 연결과 룸에 고정된 공개 게임 namespace ID |
| `room_id` / `match_id` | 현재 룸 / 게임 한 판의 서버 발급 ID |
| `room_number` / `visibility` | 사용자 입력 번호 문자열 / public·private 구분 |
| `phase` / `host_id` | `WAITING`·`PLAYING`·`CLOSED` / 현재 방장 |
| `members` | 입장 순서로 정렬한 참가자 |
| `revision` | 룸·게임 상태가 변경된 버전 |
| `game_state` | PLAYING reference는 `players` 배열(각 session_id·position x/y/z·velocity x/y/z), WAITING은 `null` |
| `server_tick` | 해당 match에서 실제 계산한 단계 번호 |
| `snapshot_seq` | 룸 수명 동안 증가하는 전체 상태 순번. 새 match에도 유지 |
| `last_processed_input_seq` | `session_id → 실제 적용 완료 입력 번호` map |

`input_seq`는 세션 수명 동안 증가하며 새 match에도 유지합니다. `server_tick`은 새 match마다 시작합니다.
`game_id`는 create/join/start snapshot, room.state/game.snapshot, game.started/game.ended 내부 snapshot, room.list의 모든 항목에 포함됩니다.
상태·tick·적용 번호를 같은 시점에 캡처합니다. 예측 보정은 [동기화 문서](03-realtime-sync.md)의 순서를 따릅니다.
`room.state`는 revision으로 처리하고, 오래된 snapshot·종료된 match 필터는 `game.snapshot`의 게임 상태에 적용합니다.
이 필터로 퇴장·종료 메타데이터나 중요한 입장·시작 초기 snapshot을 버리지 않습니다.
현재 reference 공개 상태는 모든 참가자에게 공통입니다. 비공개 정보가 필요한 게임은 후속 handler 계약 확장이 필요합니다. players와 members는 살아 있는 참가자의 입장 순서입니다. full capture마다 snapshot_seq가 증가하며 하나의 capture를 response/event에 재사용하면 같은 번호입니다. idle tick의 revision은 유지합니다.
password·hash·salt는 생성/입장 응답·`room.state`·`game.snapshot`에 넣지 않습니다.
운영 summary·cache·로그·audit에도 포함하지 않으며 요청 전체 payload를 기록하지 않습니다.
운영 환경의 게임 연결은 WSS, admin 화면·API는 HTTPS를 사용합니다. [OWASP WebSocket 보안 지침](https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html)

## HTTP API — 현재 연결과 운영 조회

| Method / path | 기능 / 권한 |
|---|---|
| GET `/healthz` | 프로세스 liveness, 200 |
| GET `/readyz` | 신규 게임 수용 gate, 수용 중 200·종료 시작 뒤 503 |
| GET `/ws?game_id=game-a` | 등록된 게임에 binding된 guest WebSocket 연결; bare `/ws`는 default 단독 구성만 허용 |
| GET/HEAD `/admin`, `/admin/` | Basic viewer 인증 후 embed HTML |
| GET/HEAD `/admin/assets/dashboard.css`, `/admin/assets/dashboard.js` | 같은 인증으로 보호된 embed 자산 |
| GET/HEAD `/admin/api/status` | 서버 instance·연결·membership·runtime·cache 신선도 |
| GET/HEAD `/admin/api/rooms` | public/private 전체를 필터·pagination하는 cache 목록 |

`ADMIN_VIEWER_TOKEN` 미설정·빈 값이면 모든 admin 경로 404입니다. 설정값은 32~256 bytes의 공백 없는 ASCII `0x21..0x7e`, username은 `viewer`입니다. 미인증·잘못된 인증은 401과 Basic challenge, 알려진 조회 경로에서 GET/HEAD 외 method는 405(`Allow: GET, HEAD`)입니다. HEAD는 GET과 같은 status/header, body 없음입니다. query의 미정의/중복 key·범위·형식 오류는 400이며 서버 오류는 500입니다. 오류 body는 `text/plain`, 성공 API는 JSON입니다. 모든 admin 응답에는 no-store/self CSP/nosniff/frame DENY를 적용합니다. HTTPS·조용한 token 입력·native Basic cache 한계는 [운영 가이드](../server-guide.md#운영-대시보드-켜기)를 따릅니다.

### 현재 운영 JSON 계약

status는 다음 필드만 반환합니다. CPU·RTT·표시지연은 nullable이며 현재 항상 `null`입니다.

| 필드 | 타입 / 의미 |
|---|---|
| `server_instance_id`, `state`, `ready` | string, `RUNNING` 또는 `STOPPING`, boolean 신규 수용 gate |
| `active_connections`, `room_member_count` | integer 전체 게임의 live 연결 / 실제 membership 합계 |
| `room_count`, `waiting_room_count`, `playing_room_count` | integer 전체 게임의 전체/WAITING/PLAYING 룸 수 |
| `started_at`, `uptime_seconds` | UTC RFC3339 시작 시각 / integer 경과 초 |
| `heap_alloc_bytes`, `goroutines` | integer HeapAlloc bytes / Go goroutine 수 |
| `cpu_percent`, `rtt_ms`, `client_display_delay_ms` | number 또는 null; 현재 미측정 null |
| `updated_at`, `age_ms`, `stale`, `generation` | UTC RFC3339 cache 게시 시각, integer age, boolean 오래됨, integer cache 세대 |

rooms envelope는 `total`, `matched_total`, `offset`, `limit`(integer), `rooms`(배열), `updated_at`, `age_ms`, `stale`, `generation`을 반환합니다. `total`은 필터와 관계없이 전체 게임의 룸 수, `matched_total`은 모든 필터 일치 수입니다. 필터 뒤 `(game_id, room_number, room_id)` 오름차순 정렬 후 pagination하며 빈 결과는 `[]`입니다.

| 각 `rooms` row | 타입 / 의미 |
|---|---|
| `game_id` | string 공개 게임 namespace; dashboard의 게임 열에도 표시 |
| `room_id`, `room_number` | string 내부 identity / ASCII 6자리 사용자 번호, 선행 0 유지 |
| `visibility`, `phase` | `public` 또는 `private` / `WAITING` 또는 `PLAYING` |
| `capacity`, `member_count` | integer 정원 / 실제 인원 |
| `host_id`, `match_id` | string host / string 또는 null match; WAITING은 null |
| `updated_at`, `age_ms`, `stale` | actor summary 게시 시각 UTC RFC3339 / integer age / boolean 오래됨 |

query는 `limit` 기본100(1~100), `offset` 기본0(0~4096), `number_prefix` 기본빈값(ASCII 숫자 0~6자리), `phase=all|WAITING|PLAYING`, `visibility=all|public|private`, 선택적 `game_id` exact filter입니다. `game_id` 생략은 전체이며 명시하면 위 ID 문법을 따라야 하고 빈 값·잘못된 문법·중복은 400입니다. 문법이 맞는 미등록 ID는 200, `matched_total=0`, `rooms=[]`입니다. dashboard의 게임 입력도 같은 exact filter를 사용합니다. 필터가 있어도 status counter와 rooms `total`은 계속 전체 게임 합계입니다. admin 인증은 전체 운영 조회 권한이며 게임별 tenant 권한을 뜻하지 않습니다. 숫자 query는 ASCII decimal이며 UI limit는 100입니다.

`updated_at`은 HTTP 응답 시각이 아닙니다. row와 envelope 시각은 각각 actor 게시와 collector cache 게시를 뜻하며 `age_ms >= 5000`이면 stale입니다. actor summary/collector/runtime는 각각 1초, UI는 batch 완료 후 5초 polling과 batch timeout 5초입니다. HTTP는 cache만 읽으며 actor·manager callback/lock·runtime sampling을 실행하지 않습니다. 운영 조회는 게임 revision·snapshot_seq를 바꾸지 않습니다. 비밀번호·hash·salt·운영 token·전체 member ID 목록·game_state는 운영 DTO에서 제외합니다. 게임 `room.list`는 public-only입니다.

## 후속 HTTP API — 운영 상세와 조작

아래는 설계이며 현재 구현되지 않았습니다. 인증한 상세·commands 조회는 404입니다. viewer의 알려진 kick/close/drain POST는 403이며 실행하지 않습니다. 현재 operator 계정이나 202 command 처리 경로는 없습니다.

| Method / path | 후속 기능 / 권한 |
|---|---|
| GET `/admin/api/rooms/{id}` | 내부 room_id 대상 상세. viewer / operator |
| GET `/admin/api/commands/{command_id}` | 실행 결과·audit 상태. viewer / operator |
| POST `/admin/api/rooms/{id}/kick` | 대상 membership 종료. operator |
| POST `/admin/api/rooms/{id}/close` | 입장 차단·참가자 정리·룸 제거. operator |
| POST `/admin/api/server/drain` | 신규 입장 차단과 종료 진행. operator |

후속 `202 Accepted + command_id`는 접수이며 실제 실행 성공과 구분합니다. command·audit·idempotency·cookie CSRF/Origin 정책은 [운영 조작과 종료](06-operations-and-shutdown.md)의 목표 설계입니다. 게임 guest·룸 비밀번호는 운영 권한을 주지 않습니다. 운영 대상은 재사용 가능한 번호가 아닌 내부 room ID입니다.

## 주요 게임 오류와 구현 후 확인

| 오류 | 뜻 |
|---|---|
| `ROOM_FULL` / `ROOM_NOT_FOUND` | 자리가 없음 / 룸이 없음 |
| `ROOM_IN_PROGRESS` / `ROOM_CLOSED` | 게임 중이라 입장 불가 / 룸이 닫힘 |
| `ALREADY_IN_ROOM` / `NOT_IN_ROOM` | 이미 룸에 속함 / 현재 참가자가 아님 |
| `NOT_HOST` / `INVALID_MESSAGE` | 시작 권한 없음 / 메시지 형식·내용이 잘못됨 |
| `PASSWORD_REQUIRED` | private 입장에 비밀번호 필요. 입력받아 같은 번호로 재요청 |
| `PASSWORD_INVALID` | private 비밀번호 불일치 |
| `GAME_NOT_STARTED` / `STALE_MATCH` | 아직 시작 안 됨 / 현재 match와 다른 입력 |
| `INVALID_INPUT_SEQ` | 번호 범위·중복·역순 오류 |
| `INPUT_CANCELED` / `GAME_ERROR` | 퇴장·종료로 미적용 입력 취소 / handler 오류 |
| `RATE_LIMITED` | 실패 시도 제한 중. 설정한 제한 시간이 지난 뒤 재시도 |
| `SERVER_BUSY` | 대기열·hash worker·번호 확보 재시도 등 서버 처리 한계에 도달 |

Godot `@rpc`·자동 동기화는 custom Go JSON 계약과 자동 호환되지 않습니다. [Godot 공식 설명](https://docs.godotengine.org/en/4.7/tutorials/networking/high_level_multiplayer.html#high-level-vs-low-level-api)
구현 후 JSON 검증·요청 응답 연결·실제 적용 ACK·초기 상태 전송 순서·권한 거부를 확인합니다.
번호 앞 0 보존·비밀번호 누락/오류·재요청과 비밀정보 없는 응답/로그도 확인합니다.
현재 운영 GET이 cache만 읽는지 확인합니다. 후속 조작은 202와 실제 결과를 구분해 검증합니다.

다음: [운영 대시보드](05-operations-dashboard.md) · [전체 원본](../go-room-server-design.md)
