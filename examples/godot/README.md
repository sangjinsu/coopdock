# Godot 3D/2D reference 예제

같은 Go 서버의 룸에 연결하고, 서버가 계산한 위치를 3D 공간 또는 2D XZ 평면으로 표시하는 desktop 예제입니다. 외부 asset·SDK·dependency가 필요하지 않습니다.

[프로젝트 소개](../../README.md) · [빠른 시작](../../docs/getting-started.md) · [서버 계약](../../docs/server-guide.md) · [client 설계](../../docs/implementation/godot-client-design.md)

## 서버와 두 창 실행하기

Go **1.26 이상**과 Godot를 준비합니다. 최소 API는 **Godot 4.4** 기준이며 실제 실행 검증은 macOS의 **Godot 4.7.2**, GL Compatibility renderer에서 수행했습니다. 4.4 runtime 자체를 실행 검증한 것은 아닙니다.

1. 저장소 root의 터미널에서 `go run ./cmd/server`를 실행합니다.
2. Godot에서 [project.godot](project.godot)을 열고 실행합니다. 기본 주소는 `ws://127.0.0.1:8080/ws`, 개발용 게임 ID는 `default`입니다. `연결`을 누르면 새 guest가 됩니다.
3. 정원 1~6명과 공개 여부를 골라 방을 만듭니다. 생성자는 자동으로 입장하고 방장이 됩니다.
4. 두 번째 Godot 실행 창을 열고 같은 주소·게임 ID로 연결합니다. 첫 창의 룸 번호를 입력하거나 공개 방 목록에서 선택한 뒤 `입장`을 누릅니다.
5. 방장이 `게임 시작`을 누릅니다. 혼자 시작할 수도 있습니다. 상단 selector에서 `3D 공간` / `2D XZ 평면`을 선택합니다. 게임 중 `로비 열기`로 연결 정보와 전체 게임 ID를 확인할 수 있습니다.

macOS에 설치된 엔진으로 두 창을 띄우려면 별도 터미널마다 저장소 root에서 다음을 실행할 수도 있습니다. 다른 설치 경로에서는 실행 파일 경로를 바꾸세요.

```sh
/Applications/Godot.app/Contents/MacOS/Godot --path examples/godot
```

룸 번호는 선행 0을 포함한 **반각 ASCII 숫자 6자리 문자열**입니다. 공개 방 목록에는 연결한 게임의 public 룸만 보입니다. private 방은 번호로 입장하며, 비밀번호 없이 요청하면 `PASSWORD_REQUIRED` 뒤 비밀번호 입력란이 나타납니다. 생성 시 정한 비밀번호를 정확히 입력하고 다시 입장하세요. 비밀번호는 4~64 Unicode code point이며 공백·대소문자·Unicode 형태를 그대로 구별합니다. 정답이어도 가득 찬 방은 `ROOM_FULL`, 진행 중인 방은 `ROOM_IN_PROGRESS`입니다. 입장 성공·퇴장·연결 종료 시 secret 입력을 비웁니다.

한 창은 한 guest 연결을 소유합니다. 퇴장은 연결을 유지하지만, 연결을 끊고 재연결하면 새 `session_id`를 받습니다. 이전 membership과 진행 상태를 자동 복원하지 않습니다. 서버를 재시작하면 메모리의 룸과 상태가 사라집니다.

## 여러 게임에서 사용하기

서버 시작 시 등록한 게임 ID를 각 창의 `개발용 게임 ID`에 지정합니다.

```sh
GAME_IDS=default,game-a,game-b go run ./cmd/server
```

`GAME_IDS`는 1~32개의 고유 ID allowlist이며 ID 문법은 ASCII `[a-z0-9][a-z0-9_-]{0,63}`입니다. 항목 양끝 공백은 제거하며 빈 항목·중복·잘못된 ID는 서버 시작을 거절합니다. 미설정 기본값은 `default`입니다.

Godot 주소 입력에는 **query와 fragment 없는 base URL**을 사용합니다. 예를 들어 주소 `ws://127.0.0.1:8080/ws`, 게임 ID `game-a`를 각각 입력합니다. 공개 API는 `CoopClient.connect_guest(base_url: String, game_id: String = "default") -> Error`이며 adapter가 `?game_id=game-a`를 붙입니다. query가 이미 있는 URL을 주소란에 넣으면 거절합니다.

일반 WebSocket client의 bare `/ws` 호환은 등록 목록이 정확히 `default` 하나일 때만 가능합니다. 다중 게임이나 단일 named game 구성은 명시적 `game_id` query가 필요합니다. Godot 예제는 기본 게임도 명시해서 연결합니다.

게임은 연결 시 고정되고 room payload로 바꿀 수 없습니다. 다른 게임을 선택하려면 연결을 끊고 다시 연결합니다. 번호는 게임 안에서만 고유하므로 다른 게임은 같은 번호를 가질 수 있고, 다른 게임에만 있는 번호는 `ROOM_NOT_FOUND`입니다. `game_id`는 **공개 namespace이며 인증 정보가 아닙니다**. 등록된 ID를 누구나 선택할 수 있습니다.

version 1 응답의 guest·full snapshot 및 room list에 추가된 `game_id`를 사용하는 현재 서버와 client를 함께 사용하세요. key를 엄격히 검사하는 구버전 client는 함께 업데이트해야 합니다. 이 adapter는 누락되거나 연결 게임과 다른 guest/list/snapshot 응답을 protocol 오류로 거절합니다. URL 호환은 구버전 schema 호환을 보장하지 않습니다. 상세 필드·handshake 오류는 [서버 계약](../../docs/server-guide.md#실제-websocket-메시지)을 따릅니다.

모든 등록 게임은 현재 **공통 reference movement handler**를 사용합니다. 게임별 rules·승패·action/state schema를 등록하는 기능은 없습니다. room/session 상한·hash worker/queue·IP 실패 제한은 전역 공유이며 게임별 quota·CPU/성능 격리를 보장하지 않습니다.

## 이동과 표시

| 조작 | 동작 |
|---|---|
| WASD / 화살표 | 서버 XZ 평면 이동 |
| Q / E | 3D에서 Y 감소 / 증가. 2D에서는 비활성 |
| 3D / 2D selector | 같은 서버 상태의 표시 모드 변경 |
| 방 나가기 | 정지 요청 후 현재 룸 퇴장 |

2D는 서버 위치의 `Vector2(x,z)`를 **32px/unit**으로 투영합니다. 서버 Y는 숨겨져 있을 뿐 보존됩니다. 2D 입력의 `direction.y`는 0이며, 3D에서 높이를 바꾼 뒤 2D로 전환해 이동해도 기존 Y는 유지합니다. 바닥과 격자는 시각 표시입니다. 이동은 free-space reference이며 충돌·중력·지형·협동 목표가 없습니다.

자기 avatar를 포함해 모든 위치는 **서버 snapshot만** 사용합니다. local prediction·extrapolation·physics·Godot `@rpc`는 사용하지 않습니다. 청록색은 자신, 주황색은 다른 참가자이고 방장을 label로 구분합니다. 플레이어가 제거되면 이전 보간 buffer에서 다시 나타나지 않습니다. 근접 avatar의 여러 줄 label이 겹칠 수 있으며 이는 수용한 prototype 표시 한계입니다. 가까운 플레이어의 역할/짧은 ID 가독성이 낮아질 수 있고, 전체 identity는 모델과 로비에 유지됩니다. 배치 개선은 후속입니다.

입력 송신은 하나의 `InputController`가 담당합니다. 포커스 상실·텍스트 입력 중·키 해제·모드 전환 시 zero 방향을 요청합니다. 포커스를 되찾아도 키를 다시 입력해야 이동합니다. 로컬 전송만으로 처리를 확정하지 않습니다. 현재 요청 ID·input_seq의 ACK 또는 authoritative 처리 sequence를 확인합니다. `SERVER_BUSY`·`REQUEST_TIMEOUT`·`CLIENT_TIMEOUT`·일시적인 로컬 송신 실패는 현재 의도에 한해 100→200→400→800→1000ms(상한) 간격으로 재시도하며, 새 sequence를 사용합니다. 실패하거나 아직 확인되지 않은 zero는 처리 확인까지 새 이동보다 우선합니다. 영구 오류는 자동 반복하지 않습니다. 정지가 영구 거절되면 이동을 막고 퇴장/재입장·새 match·재연결 때 추적을 초기화합니다. 모드 전환은 키 상태와 표시 timeline을 초기화하고 game/session/room/match 소속과 `input_seq`를 유지합니다. 같은 socket의 퇴장·새 룸·새 match에서도 sequence를 유지하며 새 연결에서만 1부터 시작합니다.

보간은 local monotonic **수신 시각**과 수신 간격을 쓰며 duration은 16~200ms입니다. 500ms를 넘는 gap에서는 이전 sample을 버리고 즉시 snap합니다. 보간에는 추가 표시 지연이 있습니다. 서버 simulation clock과 각 창의 receive clock은 동기화되지 않으므로 같은 snapshot을 받아도 두 창의 순간 표시 위치·보간 alpha가 같다고 보장하지 않습니다. 설정된 Hz나 tick 개수로 실제 처리율·표시 지연을 추정하지 않습니다.

## 연결과 동기화 상태 확인하기

연결은 WebSocket 요청 접수만으로 완료되지 않고 서버의 올바른 guest 응답까지 받아야 준비 상태가 됩니다.
guest 연결이 완료되면 public 룸 목록을 한 번 바로 요청합니다. 다른 참가자의 생성·퇴장 뒤 목록을 다시 보려면 새로고침합니다.
예기치 않게 연결이 닫히면 오류와 재연결 안내를 확인하세요. 재연결하면 새 guest가 되며 이전 룸이나 진행 중인 match를 자동 복원하지 않습니다.

3D·2D는 같은 입력·수신 상태를 사용하며 공통 HUD에서 다음 값을 확인합니다.

| 표시 | 읽는 방법 |
|---|---|
| 입력 ACK RTT | 최근 유효한 `game.action` 입력을 보내 서버의 적용 ACK를 받기까지의 왕복 시간 |
| Snapshot 경과 | 현재 match에서 마지막으로 수락한 주기 snapshot 이후의 local 경과 시간 |
| Tick / snapshot seq | 같은 수락 snapshot의 서버 계산 번호와 룸 snapshot 번호 |

값이 아직 없을 때는 `—`로 표시하며, 실제 관측한 0ms는 `0 ms`로 표시할 수 있습니다. 이동 입력이 없으면 ACK RTT도 새로 측정되지 않습니다.
Snapshot 경과가 늘어나는 동안 화면은 마지막 상태를 표시할 수 있습니다. 경과 시간은 서버가 계산한 시간이나 화면의 편도 지연이 아닙니다.
이전 연결·operation epoch·match의 응답과 stale snapshot은 현재 지표를 되살리지 않으며, 퇴장·새 match·재연결에서는 소속 지표를 초기화합니다.
mode 전환은 소속을 바꾸지 않으므로 3D·2D 지표의 의미도 같습니다.
여러 룸을 동시에 측정하려면 [동기화 부하 테스트](../../docs/load-testing.md)를 사용하세요. 20Hz 연속 입력의 부하 조건은 예제의 방향 변경 입력과 구분합니다.

자신의 표시 component에서도 `CoopClient.get_sync_status() -> Dictionary`로 같은 값을 읽을 수 있습니다.

| field | type | 내용 |
|---|---|---|
| `input_ack_rtt_ms` | `int` 또는 `null` | 현재 소속의 최근 입력 적용 ACK 왕복 ms |
| `snapshot_age_ms` | `int` 또는 `null` | 마지막 수락 periodic snapshot 이후 경과 ms |
| `server_tick` | `int` 또는 `null` | 같은 periodic snapshot의 서버 tick |
| `snapshot_seq` | `int` 또는 `null` | 같은 periodic snapshot의 룸 snapshot seq |

```gdscript
var sync: Dictionary = client.get_sync_status()
if sync.input_ack_rtt_ms != null:
    print("Input ACK RTT: %d ms" % sync.input_ack_rtt_ms)
```

입장 응답·`game.started`·`room.state`를 받은 것만으로 주기 snapshot 지표를 채우지 않습니다.
연결 전·퇴장·새 match·재연결 직후에는 네 field가 모두 `null`이며, 현재 소속의 실제 ACK·periodic snapshot을 받으면 각각 갱신합니다.

## Timeout과 heartbeat

client request timeout은 기본 **10초**, pending 요청 상한은 **64개**입니다. 기본 **30초마다 `room.list {}`** data request를 보내 서버 read deadline을 유지합니다. 서버 `WS_READ_TIMEOUT` 기본값은 120초입니다. 서버 timeout을 줄일 때는 `CoopClient.heartbeat_interval_ms`도 그보다 짧게 조정하세요. 기본 UI에는 이 값을 바꾸는 설정란이 없습니다. WebSocket ping만으로 data request를 대신하지 않습니다.

오류는 원래 서버 code/message로 표시합니다. timeout·퇴장·재연결 뒤 늦게 도착한 성공이나 이전 socket/room/match 상태로 소속을 복원하지 않습니다. private 비밀번호와 요청 payload 원문을 pending·로그에 보관하지 않습니다.

## 검증 명령과 확인한 범위

저장소 root에서 Python 3, Go와 설치된 Godot로 실행합니다. `GODOT_BIN`은 위 macOS 기본 경로를 대체할 수 있습니다. `GODOT_CHECKS_OUTPUT_DIR`은 ignored 산출물 디렉터리입니다.

```sh
GODOT_CHECKS_OUTPUT_DIR=work/godot-client/local examples/godot/tests/run_checks.sh
# 별도 native 렌더 QA: 두 native 창 + 네 headless guest
GODOT_CHECKS_OUTPUT_DIR=work/godot-client/local-mixed examples/godot/tests/run_checks.sh --mixed-native
```

기본 runner는 import와 아래 8개 suite를 실행합니다. 임시 localhost port를 골라 `GAME_IDS=default` 서버를 build/start하고, namespace integration에는 별도 `GAME_IDS=game-a,game-b` 서버를 사용합니다. 상속받은 `ROOM_*`·`WS_*`·`ADMIN_*` 설정을 제거해 검사 조건을 고정합니다. 실행별 argv·exit·owned PID는 출력 경로의 `runner-checks.json` 또는 `runner-mixed-native.json`에 남습니다. 각 단계에는 deadline이 있으며 finally에서 자신이 시작한 process만 종료하고 wait합니다. Godot SCRIPT ERROR/engine ERROR와 nonzero exit는 실패입니다. GUI·socket 실행이 제한된 환경에서는 해당 실행 권한이 필요합니다.

연결·지표 개선 소스는 [`e5fa8b3`](https://github.com/sangjinsu/coopdock/commit/e5fa8b38d6a8b25090849cbc79c50561421a89a8)입니다. 그 최종 파일과 동일한 소스에서 확인한 기본 runner 결과는 다음과 같습니다. 실제 엔진은 macOS arm64의 Godot **4.7.2**입니다.

| 기본 suite | Checks / failures | 확인한 내용 |
|---|---|---|
| Unit | 366 / 0 | codec·state·보간·3D/2D·기존 UI 경계 |
| 입력/lifecycle | 77 / 0 | 입력 재시도·정지 우선·같은 연결의 seq 연속성 |
| Native 입력 helper | 9 / 0 | 실제 렌더 시험 helper의 정지 barrier 처리 |
| 동기화 상태 | 86 / 0 | 최초 목록·연결 종료 안내·RTT·snapshot age·scope reset·HUD |
| Namespace unit | 58 / 0 | 게임 선택·응답 소속 검사 |
| 실제 wire | 62 / 0 | 실제 서버 룸 입장과 입력·상태 |
| Namespace integration | 89 / 0 | 실제 4개 WebSocketPeer의 두 게임 이동·입장·퇴장 격리 |
| 짧은 timeout heartbeat | 5 / 0 | `WS_READ_TIMEOUT=3s`·heartbeat 1초에서 연결 유지 |
| **합계** | **752 / 0** | build·import와 모든 suite exit 0, 소유 child 13개 종료·회수 |

별도 mixed native 실행은 **3D host와 2D peer의 실제 두 창 + 네 headless guest**, 총 독립 연결 6개입니다. host **48 / 0**, peer **66 / 0**, headless 각각 **32 / 0**, 합계 **242 / 0**을 확인했습니다. 전원 공통 PLAYING full capture **94개**의 내용이 모두 일치했고, 소유 child 8개가 모두 exit 0으로 종료·회수되었습니다.

공통 full-state 비교는 같은 `(game_id,room_id,match_id,revision,server_tick,snapshot_seq)` 전체 snapshot deep equality입니다. mixed 검사는 hidden Y=2.5 보존, 2D Q/E 비활성, mode 전환 zero ACK와 `input_seq` 연속성을 포함합니다. native 입력은 synthetic physical key와 explicit focus signal이며 **OS 수동 keyboard/focus 시험은 아닙니다**. namespace의 같은 번호 공존은 deterministic core 시험 근거이며 실제 네 socket이 고정 같은 번호를 입증한 것은 아닙니다.

초기 mixed 실행에서는 기존 native 시험 helper의 2D 전환·정지 barrier 경합으로 timeout이 발생했습니다. 실패 원본을 보존하고 시험 helper의 입력 순서를 고친 뒤, 위의 helper 회귀와 최종 mixed 전체를 다시 실행했습니다. 이번 개선의 production 변경은 CoopClient·Lobby·Main이며 기존 InputController는 유지했습니다.

동기화 상태의 작은 창 회귀는 700px에서 지표·긴 game ID tooltip·상단 조작 영역을 검사합니다. 제어한 응답 peer의 timeout·거절 회귀는 실제 Go queue 포화 재현이 아니며 native 입력도 OS 수동 조작 시험이 아닙니다. 과거 실행 기록을 이번 최종 소스의 통과 횟수에 합산하지 않습니다. 기본 runner가 Go test·race·vet·build 전체를 실행하는 것은 아니며 해당 명령은 [서버 검증](../../docs/server-guide.md#종료와-검증)을 따릅니다.

web export·TLS 배포·production 부하/수용 규모·목표 Hz 달성·Godot 4.4 runtime·실제 OS keyboard/focus는 검증 범위 밖입니다. [검증 계획](../../docs/specs/08-validation.md)은 향후 게임 규칙·성능 시험까지 포함하므로 현재 실행 결과와 구분하세요.
