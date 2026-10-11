# 04. 메시지와 API

**게임은 WebSocket의 JSON 메시지로, 운영 화면은 HTTP API로 서버와 이야기합니다.**
프로토콜은 서로 어떤 형식과 순서로 데이터를 주고받을지 정한 약속입니다.

Godot 활용은 확정 요구사항입니다. 연결은 custom WebSocket + JSON으로 제안하며 전용 SDK는 필수가 아닙니다.
아래 version 1 메시지와 API 경로는 **초기 설계 계약**입니다. 현재 서버에서 제공하는 메시지·경로와 검증 범위는 [서버 README](../server/README.md)를 참조하세요. Godot 클라이언트와 운영 API는 후속 구현 범위입니다.

## 두 사람이 서버와 이야기하는 예

1. 민수가 연결하면 서버가 guest 세션 ID를 발급합니다.
2. 민수가 `room.create`로 룸을 만듭니다. 지연은 `room.join`에 번호를 보내고 private이면 비밀번호를 추가해 재요청합니다. 성공 응답에는 각각 초기 상태가 담깁니다.
3. 민수가 `room.start`를 요청하면 서버가 새 match를 시작하고 `game.started`를 전달합니다.
4. 두 사람은 `game.action`으로 이동 의도를 보내고, 실제 적용 응답과 `game.snapshot`을 받습니다.
5. 운영자는 GET으로 이 룸을 조회합니다. close 조작은 POST로 접수하고 명령 조회로 실제 결과를 확인합니다.

게임 사용자가 자기 `session_id`를 주장해 권한을 얻지는 않습니다.
서버는 실제 연결의 세션과 현재 membership(룸 입장 관계)으로 검사하며, 사용자가 보낸 좌표·승패 결과를 믿지 않습니다.

## 게임 메시지의 공통 봉투

모든 메시지는 다음 공통 필드를 사용합니다. `op`는 할 일의 이름입니다.

| 필드 | 의미 |
|---|---|
| `version` | 프로토콜 버전. 아래 예시는 초기 제안인 `1` |
| `op` | 요청·응답·이벤트 종류: 예를 들면 `room.join` |
| `request_id` | 요청과 응답을 연결하는 표식. 서버 이벤트는 `null` |
| `payload` | 해당 메시지의 데이터 |
| `error` | 실패 정보. 아래 정상 요청 예시는 `null` |

응답은 요청과 같은 `op`와 `request_id`를 반환합니다.
`request_id`를 반복한다고 중복 실행이 방지되지는 않습니다. 운영 명령의 중복 방지 규칙과는 별개입니다.

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
    "action": {"type": "move", "direction": {"x": 1, "y": 0}}
  },
  "error": null
}
```

예시는 유효한 JSON의 설계 예시입니다. 존재하는 룸·match나 현재 제공되는 API를 뜻하지 않습니다.
이동 의도의 구체적인 종류와 검증 규칙은 후속 `GameHandler` 구현에서 정합니다.

## 게임 메시지 목록

| `op` | 역할 |
|---|---|
| `session.guest` | 서버가 발급한 guest `session_id` 안내 |
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
플레이어 `room.list`에는 public만 표시하도록 제안합니다. private는 번호를 공유해 입장하고 운영 목록에서는 조회할 수 있습니다.

## 전체 상태에 담는 필드

`room.state.payload`와 `game.snapshot.payload`는 아래 상태 구조를 사용합니다.
성공한 create/join의 `payload.snapshot`, `game.started.payload.snapshot`에는 초기 full 상태를 담습니다.

| 필드 | 뜻 |
|---|---|
| `room_id` / `match_id` | 현재 룸 / 게임 한 판의 서버 발급 ID |
| `room_number` / `visibility` | 사용자 입력 번호 문자열 / public·private 구분 |
| `phase` / `host_id` | `WAITING`·`PLAYING`·`CLOSED` / 현재 방장 |
| `members` | 입장 순서로 정렬한 참가자 |
| `revision` | 룸·게임 상태가 변경된 버전 |
| `game_state` | 게임 규칙이 만든 공개 상태. GameHandler가 없으면 `null` |
| `server_tick` | 해당 match에서 실제 계산한 단계 번호 |
| `snapshot_seq` | 룸 수명 동안 증가하는 전체 상태 순번. 새 match에도 유지 |
| `last_processed_input_seq` | `session_id → 실제 적용 완료 입력 번호` map |

`input_seq`는 세션 수명 동안 증가하며 새 match에도 유지합니다. `server_tick`은 새 match마다 시작합니다.
상태·tick·적용 번호를 같은 시점에 캡처합니다. 예측 보정은 [동기화 문서](/Users/sangjinsu/Documents/Codex/2026-10-10/gameanvil/outputs/specs/03-realtime-sync.md)의 순서를 따릅니다.
`room.state`는 revision으로 처리하고, 오래된 snapshot·종료된 match 필터는 `game.snapshot`의 게임 상태에 적용합니다.
이 필터로 퇴장·종료 메타데이터나 중요한 입장·시작 초기 snapshot을 버리지 않습니다.
비공개 정보가 있는 게임은 GameHandler가 참가자별 공개 상태를 만듭니다.
password·hash·salt는 생성/입장 응답·`room.state`·`game.snapshot`에 넣지 않습니다.
운영 summary·cache·로그·audit에도 포함하지 않으며 요청 전체 payload를 기록하지 않습니다.
운영 환경의 게임 연결은 WSS, admin 화면·API는 HTTPS를 사용합니다. [OWASP WebSocket 보안 지침](https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html)

## HTTP API — 연결과 운영 조회

GET은 조회 또는 연결 진입이며 상태 변경용으로 사용하지 않습니다.

| Method / path | 기능 / 권한 |
|---|---|
| GET `/healthz` | 프로세스가 살아 있는지 최소 상태 확인 |
| GET `/readyz` | 신규 게임 수용 여부 확인. drain 중 실패 |
| GET `/ws` | 게임 guest WebSocket 연결 |
| GET `/admin` | 운영 인증 후 HTML 화면 제공 |
| GET `/admin/api/status` | 서버 instance·집계·종료 진행 요약. viewer / operator |
| GET `/admin/api/rooms` | public·private 모두 포함한 개수 제한 룸 목록. viewer / operator |
| GET `/admin/api/rooms/{id}` | 내부 room_id 대상 운영 요약. viewer / operator |
| GET `/admin/api/commands/{command_id}` | 명령의 실제 실행 결과·audit 상태. viewer / operator |

## HTTP API — 운영 조작

| Method / path | 기능 / 권한 |
|---|---|
| POST `/admin/api/rooms/{id}/kick` | 내부 room_id의 대상 membership 종료. operator |
| POST `/admin/api/rooms/{id}/close` | 내부 room_id의 입장 차단·참가자 정리·룸 제거. operator |
| POST `/admin/api/server/drain` | 신규 입장 차단과 서버 종료 진행 요청. operator |

`202 Accepted + command_id`는 **접수**입니다. 실행 성공은 명령 GET의 실제 결과로 확인합니다.
운영 POST는 현재 `server_instance_id`와 idempotency key, 즉 중복 요청을 구분할 표식을 사용합니다.
실행 전 접수 audit 저장과 명령 등록이 성공해야 202를 반환합니다. 상세 규칙은 [운영 조작과 종료](/Users/sangjinsu/Documents/Codex/2026-10-10/gameanvil/outputs/specs/06-operations-and-shutdown.md)에 있습니다.
guest 연결·룸 비밀번호와 운영 인증은 분리합니다. viewer는 조회, operator는 조작이며 서버가 권한을 검사합니다.
운영 대상의 `{id}`는 사용자 입력 번호가 아닌 고정 내부 `room_id`입니다.
cookie로 인증한 변경 요청에는 CSRF 방어와 Origin 검증이 필요합니다. [OWASP 지침](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)

## 주요 게임 오류와 구현 후 확인

| 오류 | 뜻 |
|---|---|
| `ROOM_FULL` / `ROOM_NOT_FOUND` | 자리가 없음 / 룸이 없음 |
| `ROOM_IN_PROGRESS` / `ROOM_CLOSED` | 게임 중이라 입장 불가 / 룸이 닫힘 |
| `ALREADY_IN_ROOM` / `NOT_IN_ROOM` | 이미 룸에 속함 / 현재 참가자가 아님 |
| `NOT_HOST` / `INVALID_MESSAGE` | 시작 권한 없음 / 메시지 형식·내용이 잘못됨 |
| `PASSWORD_REQUIRED` | private 입장에 비밀번호 필요. 입력받아 같은 번호로 재요청 |
| `PASSWORD_INVALID` | private 비밀번호 불일치 |
| `RATE_LIMITED` | 실패 시도 제한 중. 설정한 제한 시간이 지난 뒤 재시도 |
| `SERVER_BUSY` | 대기열·hash worker·번호 확보 재시도 등 서버 처리 한계에 도달 |

Godot `@rpc`·자동 동기화는 custom Go JSON 계약과 자동 호환되지 않습니다. [Godot 공식 설명](https://docs.godotengine.org/en/4.7/tutorials/networking/high_level_multiplayer.html#high-level-vs-low-level-api)
구현 후 JSON 검증·요청 응답 연결·실제 적용 ACK·초기 상태 전송 순서·권한 거부를 확인합니다.
번호 앞 0 보존·비밀번호 누락/오류·재요청과 비밀정보 없는 응답/로그도 확인합니다.
운영 202와 실제 결과를 구분하고, GET으로 상태를 변경하지 않는지도 확인합니다.

다음: [운영 대시보드](/Users/sangjinsu/Documents/Codex/2026-10-10/gameanvil/outputs/specs/05-operations-dashboard.md) · [전체 원본](/Users/sangjinsu/Documents/Codex/2026-10-10/gameanvil/outputs/go-room-server-design.md)
