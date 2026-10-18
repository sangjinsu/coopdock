# Godot Reference Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 기존 Go 서버에 custom WebSocket/JSON로 연결하여 1~6명 룸과 서버 권위 3D와 XZ 평면 2D 이동을 보여 주는 실행 가능한 Godot desktop 예제를 제공한다.

**Architecture:** `CoopClient`가 단일 연결·request correlation·input sequence를 소유하고 `RoomState`가 수명과 freshness를 검증한다. 한국어 lobby는 room API만 소비한다. InputController 하나가 입력을 송신하며 3D/2D renderer는 accepted full snapshot과 local receive clock로 위치만 표시한다. 기존 Go source와 dependency는 변경하지 않는다.

**Tech Stack:** Godot 4.4 이상, 설치된 Godot 4.7.2, GDScript, GL Compatibility, `WebSocketPeer`, `JSON`, native Control, primitive mesh, 기존 Go 서버.

**Spec:** [Godot client 설계](godot-client-design.md). 실행 기준 branch `codex/godot-client-example`, 시작 HEAD `59dccc33c5f4bb3f939efb77b588301b74ab5b6b`. wire 권위 자료는 `work/godot-client/protocol-contract.md`; 공식 API 자료는 `work/godot-client/official-godot-research.md`. 사용자가 실행과 AGENTS의 전문 agent 위임 방식을 제공했으므로 계획 후 같은 turn에서 순차 실행한다.

## 후속 namespace 계약의 우선순위

이 계획의 시작 commit·아래 TDD 예제·작업 순서는 당시 기록이다. 단일 `/ws`·Go 무변경 가정은 후속 [게임별 룸 분리 설계](game-isolation-design.md)와 [현재 서버 계약](../server-guide.md)이 확장했다. Go 변경은 namespace 작업 소유였고 Godot 작업은 이를 소비했다. 서버는 `GAME_IDS`로 게임을 등록하며 bare `/ws` 호환은 `default` 하나만 등록한 경우에 한정한다. 현행 adapter는 query/fragment 없는 base URL과 game ID를 받아 명시적으로 연결한다.

아래 interface 표는 현행 signature를 반영한다. 연결의 `game_id`는 고정되고 guest/list/full snapshot의 일치를 검사한다. leave는 binding을 보존하며 새 연결은 비운다. version 1 응답의 추가 필드는 strict 구버전 client와 동시 업데이트가 필요하다. 공개 namespace는 인증이 아니며 모든 게임이 전역 자원 한도와 공통 movement handler를 공유한다. 현재 사용법·실행 결과는 [예제 안내](../../examples/godot/README.md)를 따르며 과거 계획을 완료 증거로 보지 않는다.

## Global Constraints

- `Godot 4.4 이상, GL Compatibility renderer, desktop`을 대상으로 한다. 최소 API는 공식 4.4 문서, 실제 실행 검증은 설치 엔진 4.7.2로만 한다.
- `외부 assets·SDK·dependency는 추가하지 않는다.` Godot 작업은 Go source/dependency를 변경하지 않으며 후속 namespace 서버 변경은 별도 작업 소유다.
- `코드 주석은 영어, UI·설명은 한국어, 서버 error message와 로그는 원문을 유지한다.`
- `기본 endpoint는 ws://127.0.0.1:8080/ws`다. `하나의 창은 하나의 guest 연결을 소유한다.`
- `room number는 ASCII [0-9] 6자리 string이며 선행 0을 보존한다.` 정원은 1~6명이다.
- `private create password는 4~64 Unicode code points, 정확한 입력을 유지한다.` password를 trim·normalization하지 않는다. public create에는 password 필드 자체를 넣지 않는다.
- `password와 요청 payload 원문을 pending·로그에 저장하지 않는다.` 성공·퇴장·disconnect 시 secret 입력을 비운다.
- connect 전에 `inbound_buffer_size=1048576`, `outbound_buffer_size=1048576`, `max_queued_packets=256`을 설정한다. inbound text cap은 1MiB, frame당 drain budget은 64 packet이다. 서버 요청 16KiB와 서버 room.list 응답 상한을 혼동하지 않는다.
- `기본 local request timeout은 10초다.` `기본 30초마다 유효한 room.list {}를 보내 read deadline을 갱신한다.` `pending은 최대 64개`다.
- `같은 socket의 leave/new room/new match에서는 input_seq를 초기화하지 않는다.` socket 교체만 sequence를 1로 초기화한다.
- `자기 avatar도 snapshot position만 사용한다.` local prediction·extrapolation·physics·collision·gravity·`@rpc`를 추가하지 않는다.
- `없어진 player는 최신 metadata/players에서 즉시 제거하고 이전 buffer에서 부활시키지 않는다.` 수신 gap에 따른 보간 duration은 16~200ms다. gap>500ms는 이전 sample을 버리고 즉시 snap한다.
- `receive clock과 server simulation clock은 동기화되지 않는다.` 추가 표시 지연과 서로 다른 창의 시간 차이를 문서화한다.
- `.gd`와 `.gd.uid`는 version control에 포함하고 `.godot/` import cache, 로그·PNG·테스트 executable은 ignored `work/godot-client/`에 둔다.
- web export·TLS 배포·production 부하·목표 Hz 달성은 검증 범위가 아니다. 새 wire op·server timestamp·advertised rates를 만들지 않는다.
- 3D 실제 여섯 client/native QA와 Task 3 commit을 먼저 끝낸 뒤 Task 4 2D를 구현·검증한다. 2D는 Vector2(x,z), 32px/unit이며 direction.y=0, 기존 server position.y는 변경하지 않는다. 모드 전환은 zero/release/timeline reset을 수행하되 room/session/input_seq는 유지한다.
- Task마다 소유 파일만 local commit한다. 다른 작업자의 변경을 되돌리거나 `git add .`를 사용하지 않는다. 각 task의 report는 ignored `work/godot-client/task-N-report.md`에 남긴다.

## Review Focus

1. 늦은 create/join success·이전 socket/room/match snapshot: timeout/leave/reconnect 이후 소속과 player를 되살리지 않는다. Task 1 `test_state_fences`, `test_pending_timeout`.
2. private 비밀번호의 공백·Unicode·잘못된 번호·누락/오답/정답: 정확한 문자열과 server 오류 순서를 유지하고 secret을 비운다. Task 1 `test_wire_payloads`, Task 2 `test_password_ui`, Task 3 `test_private_entry`.
3. focus 상실·UI text 입력·key release 누락·send 포화: zero를 보내고 다시 입력할 때까지 이동을 재개하지 않으며 unsent direction은 재시도한다. Task 3 `test_input_focus`, `test_direction_retry`; Task 4 `test_2d_projection_and_input`, `test_mode_preserves_session`도 포함한다.
4. snapshot burst·gap·같은 capture·host 퇴장: 보간 duration은 bounded, 처음/없어진 avatar는 즉시 반영, metadata host는 periodic filter로 잃지 않는다. Task 1 `test_state_fences`, Task 3 `test_buffer_lifecycle`, Task 3 `test_six_clients`.
5. 새 match·leave/rejoin·새 guest·JSON float 숫자: session seq는 유지하고 새 connection만 초기화하며 safe integer/finite vector를 검증한다. Task 1 `test_sequence_lifetime`, Task 3 `test_buffer_lifecycle`, Task 3 `test_six_clients`.

---

## 파일·interface 규칙

새 파일은 설계의 역할 표를 따른다. 모든 class의 기본 `_init()`는 인자가 없으며 `Node` class는 scene tree에서 생성할 수 있다. type 이름을 쓰기 전에 `class_name`을 선언하고 project를 import한다. test는 `preload()`도 사용하여 editor에 의존하지 않는다. 한 거대 script 안에 transport·UI·world를 합치지 않는다.

| class | 공개 계약 |
|---|---|
| `WireCodec extends RefCounted` | static `encode_request(op:String,id:String,payload:Dictionary)->String`, `decode_server(text:String)->Dictionary`(`ok:bool,message:Dictionary,error:String`), `valid_room_number(number:String)->bool`, `valid_password(password:String)->bool`, `valid_create(capacity:int,visibility:String,password:String)->bool`, `create_payload(capacity:int,visibility:String,password:String)->Dictionary`, `join_payload(number:String,password:Variant=null)->Dictionary`. |
| `RoomState extends RefCounted` | `session_id:String`, `game_id:String`, `current_snapshot:Dictionary`, `connection_generation:int`, `operation_epoch:int`; `new_connection(generation:int)->void`, `accept_guest(id:String,game_id:String)->void`, `begin_admission()->int`, `apply_admission(snapshot:Dictionary,epoch:int)->bool`, `apply_event(op:String,payload:Dictionary)->bool`, `clear_room()->void`. snapshot은 deep copy한다. |
| `CoopClient extends Node` | `state:RoomState`, `next_input_seq:int=1`, `heartbeat_interval_ms:int=30000`, `request_timeout_ms:int=10000`, `max_pending:int=64`; `connect_guest(base_url:String,game_id:String="default")->Error`, `disconnect_guest()->void`, `create_room(capacity:int,visibility:String,password:String="")->String`, `join_room(number:String,password:Variant=null)->String`, `list_rooms()->String`, `leave_room()->String`, `start_room()->String`, `send_direction(direction:Vector3)->String`. 빈 request ID는 local 송신 거절을 뜻한다. |
| `CoopClient` signals | `connection_changed(status:String)`, `snapshot_changed(snapshot:Dictionary)`, `rooms_received(rooms:Array)`, `request_failed(op:String,code:String,message:String)`, `input_acknowledged(payload:Dictionary)`, `request_completed(op:String,request_id:String,payload:Dictionary,error:Dictionary)`. 성공 error와 실패 payload는 빈 Dictionary다. |
| `CoopClient` test seams | `_socket_open()->bool`, `_send_text(text:String)->Error`, `_now_msec()->int`는 FakeClient에서 override할 수 있다.`_accept_text(text:String,generation:int,received_ms:int)->void`, `_expire_requests(now_ms:int)->void`는 deterministic test에서 직접 호출한다. 실제 공개 RPC도 같은 경로를 사용한다. |
| `LobbyPanel extends Control` | `bind_client(client:CoopClient)->void`, `room_number_edit:LineEdit`, `join_password_edit:LineEdit`, `create_password_edit:LineEdit`, `status_label:Label`, `start_button:Button`, `_join_clicked()->void`, `_handle_failure(op:String,code:String,message:String)->void`, `_handle_snapshot(snapshot:Dictionary)->void`, `_handle_connection(status:String)->void`. |
| `MovementInput extends RefCounted` | static `ensure_actions()->void`, `sample(focused:bool,text_input_focused:bool,allow_y:bool=true)->Vector3`, `release_actions()->void`. physical key mapping은 ensure_actions가 등록한다. |
| `SnapshotBuffer extends RefCounted` | `push_snapshot(snapshot:Dictionary,received_ms:int)->bool`, `sample_positions(now_ms:int)->Dictionary`(`session_id -> Vector3`), `clear()->void`, `duration_ms:int`. |
| `AvatarView extends Node3D` | `set_avatar_identity(id:String,is_self:bool,is_host:bool)->void`, `set_authoritative_position(position:Vector3)->void`. |
| `InputController extends Node` | `bind_client(client:CoopClient)->void`, `submit_direction(direction:Vector3)->void`, `set_input_gate(focused:bool,text_input_focused:bool)->void`, `set_view_mode(mode:String)->void`, `view_mode:String="3d"`, `_client:CoopClient`, `_last_sent_direction:Vector3=Vector3.ZERO`. 유일한 송신 소유자이며 성공 send에서만 last_sent_direction을 갱신한다. |
| `GameWorld extends Node3D` | `bind_client(client:CoopClient)->void`, `reset_timeline(snapshot:Dictionary,received_ms:int)->void`, `avatars:Dictionary`(`session_id -> AvatarView`), `buffer:SnapshotBuffer`. renderer이며 요청을 보내지 않는다. |
| `GameWorld2D extends Node2D` | `bind_client(client:CoopClient)->void`, `reset_timeline(snapshot:Dictionary,received_ms:int)->void`, static `project_position(position:Vector3)->Vector2`, `positions:Dictionary`(`session_id -> Vector2`), `buffer:SnapshotBuffer`, `pixel_scale:float=32.0`. renderer이며 요청을 보내지 않는다. |
| `Main extends Node` | `_ready()->void`에서 client/lobby/world를 생성하고 tree에 추가한 뒤 bind한다. `_set_view_mode(mode:String)->void`가 controller 및 표시를 전환한다. Main은 root Window focus와 gui_get_focus_owner를 확인해 controller gate에 전달한다. main.tscn은 이 script를 참조한다. Task 1에서도 Main을 실행할 수 있다. |
| `TestDriver extends RefCounted` | `client:CoopClient`, `_init(client:CoopClient=null)`, `attach(client:CoopClient)->void`, `await_guest(timeout_ms:int=5000)->String`, `await_reply(id:String,timeout_ms:int=10000)->Dictionary`, `await_snapshot(predicate:Callable,timeout_ms:int=10000)->Dictionary`, `close()->void`. queue와 capture는 각각 최대 128개이며 overflow는 test failure다. |

status는 `disconnected`, `connecting`, `guest`, `waiting`, `playing`, `closing`의 문자열로 통일한다. client signal은 준비 상태와 snapshot 변경 이후 emit한다. UI는 wire payload를 보관하지 않는다. widget은 `_ready()`에서 생성하며 bind할 때 현재 snapshot과 연결 상태에도 반응한다.

## Task 1: 연결·room state·입력 번호를 가진 실행 가능한 project

**독립 산출물:** main scene을 열고 headless에서 guest→room API를 사용하며 오래된 상태와 timeout이 현재 소속을 바꾸지 않는다.

**Files:** Create `examples/godot/project.godot`, `examples/godot/scenes/main.tscn`, `examples/godot/scripts/main.gd`, `examples/godot/scripts/wire_codec.gd`, `examples/godot/scripts/room_state.gd`, `examples/godot/scripts/coop_client.gd`, `examples/godot/tests/test_support.gd`, `examples/godot/tests/run_unit.gd`와 생성 `.gd.uid`. Modify `.gitignore`에 `examples/godot/.godot/`을 추가한다. Task 1이 소유하며 후속 Main/run_unit 수정 권한은 명시적으로 넘긴다.

**Interfaces:** 위의 `WireCodec`, `RoomState`, `CoopClient`, `Main`을 produces. 후속 task는 state/signal/public RPC만 사용한다. root 소유 Go source와 dependency는 변경하지 않는다.

- [ ] **Step 1: 실제 wire fixture와 RED test를 작성한다.** `test_support.gd`는 아래 fixture와 FakeClient를 제공한다. `run_unit.gd extends SceneTree`는 `_initialize()`에서 각 `test_*()`를 호출하고 성공 시 건수를 출력하고 `quit(0)`한다. engine assert가 process exit에 반영되지 않는다면 공통 `check(condition,message)`를 만들고 `push_error`와 `quit(1)`을 호출한다. 실패를 exit 0으로 기록하지 않는다.

```gdscript
# tests/test_support.gd
extends RefCounted
class FakeClient extends CoopClient:
    var sent: Array[String] = []
    var clock_ms: int = 0
    func _socket_open() -> bool:
        return true
    func _send_text(text: String) -> Error:
        sent.append(text)
        return OK
    func _now_msec() -> int:
        return clock_ms

static func snapshot(room_id: String = "rA", match_id: Variant = null,
        revision: int = 1, seq: int = 1, ids: Array = ["sA"]) -> Dictionary:
    var members: Array = []
    var players: Array = []
    var processed: Dictionary = {}
    for index in ids.size():
        var id: String = ids[index]
        members.append({"session_id": id})
        processed[id] = 0
        players.append({"session_id": id, "position": {"x": index * 2, "y": 0, "z": 0},
            "velocity": {"x": 0, "y": 0, "z": 0}})
    return {"room_id": room_id, "room_number": "004271", "visibility": "public",
        "capacity": 6, "match_id": match_id,
        "phase": "WAITING" if match_id == null else "PLAYING", "host_id": ids[0],
        "members": members, "revision": revision,
        "game_state": null if match_id == null else {"players": players},
        "server_tick": 0, "snapshot_seq": seq, "last_processed_input_seq": processed}

static func event(op: String, payload: Dictionary, id: Variant = null) -> String:
    return JSON.stringify({"version": 1, "op": op, "request_id": id,
        "payload": payload, "error": null})
```

```gdscript
const Support = preload("res://tests/test_support.gd")

func test_wire_payloads() -> void:
    assert(WireCodec.valid_room_number("004271"))
    for bad in ["4271", "0000000", "１２３４５６", " 004271", "00427a"]:
        assert(not WireCodec.valid_room_number(bad))
    assert(not WireCodec.create_payload(6, "public", "unused").has("password"))
    assert(not WireCodec.join_payload("004271").has("password"))
    assert(WireCodec.join_payload("004271", " abcd ").password == " abcd ")
    assert(WireCodec.valid_password("가나다라"))
    assert(not WireCodec.valid_password("abc"))
    assert(not WireCodec.decode_server("[]").ok)
    assert(not WireCodec.decode_server('{"version":1,"op":"room.state"}').ok)

func test_state_fences() -> void:
    var state := RoomState.new()
    state.new_connection(1)
    state.accept_guest("sB")
    var epoch := state.begin_admission()
    assert(state.apply_admission(Support.snapshot("rA", null, 1, 1, ["sA", "sB"]), epoch))
    assert(state.apply_event("game.started", {"snapshot": Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])}))
    assert(not state.apply_event("game.snapshot", Support.snapshot("rB", "mA", 9, 99)))
    assert(not state.apply_event("game.snapshot", Support.snapshot("rA", "mOld", 9, 99)))
    assert(state.apply_event("room.state", Support.snapshot("rA", "mA", 3, 3, ["sB"])))
    assert(state.current_snapshot.host_id == "sB")
    assert(not state.apply_event("room.state", Support.snapshot("rA", "mA", 2, 2)))
    state.clear_room()
    assert(not state.apply_event("room.state", Support.snapshot("rA", "mA", 99, 99)))
    assert(not state.apply_admission(Support.snapshot(), epoch))
    state.new_connection(2)
    assert(state.session_id.is_empty() and state.current_snapshot.is_empty())

func test_sequence_lifetime() -> void:
    var client := Support.FakeClient.new()
    client.state.new_connection(1)
    client.state.accept_guest("sA")
    var epoch := client.state.begin_admission()
    client.state.apply_admission(Support.snapshot("rA", "mA"), epoch)
    assert(not client.send_direction(Vector3.FORWARD).is_empty())
    client.state.clear_room()
    epoch = client.state.begin_admission()
    client.state.apply_admission(Support.snapshot("rB", "mB"), epoch)
    assert(not client.send_direction(Vector3.ZERO).is_empty())
    assert(JSON.parse_string(client.sent[0]).payload.input_seq == 1)
    assert(JSON.parse_string(client.sent[1]).payload.input_seq == 2)
    client.disconnect_guest()
    assert(client.next_input_seq == 1)
    client.free()

func test_pending_timeout() -> void:
    var client := Support.FakeClient.new()
    client.state.new_connection(1)
    client.state.accept_guest("sA")
    var id := client.create_room(6, "private", " abcd ")
    client._expire_requests(10001)
    client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, id), 1, 10002)
    assert(client.state.current_snapshot.is_empty())
    assert(client.state.session_id.is_empty())
    client.free()
```

추가 table은 decode의 5 keys/type/null/finite numeric, safe integer 1/최대/범위 밖, capacity 1/6/0/7, private/public field omission, wrong op correlation, 중복 snapshot, ended→old started, 이전 connection callback, pending 64개 상한, heartbeat timer와 timeout 이후 unknown reply를 포함한다. `last_processed_input_seq`와 snapshot의 JSON 숫자는 float로 decode될 수 있으므로 정수 값과 safe 상한을 검사한다.

- [ ] **Step 2: RED를 확인한다.** project와 main은 테스트를 시작할 수 있는 최소 형태로 함께 만든다. 빠진 class/method 또는 위 행위 assert의 실패를 기록한다.

```bash
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --import
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_unit.gd
```

- [ ] **Step 3: 최소 동작을 구현한다.** JSON parse의 Error와 type을 먼저 검사한다. 단일 poll reader를 사용하고 CLOSING에서도 poll하며 이전 generation의 callback은 버린다. payload constructor는 아래처럼 secret 필드 존재 여부를 정확히 처리한다. valid_create가 거절한 private 값은 송신하지 않는다.

```gdscript
static func create_payload(capacity: int, visibility: String, password: String) -> Dictionary:
    var payload := {"capacity": capacity, "visibility": visibility}
    if visibility == "private":
        payload["password"] = password
    return payload

static func join_payload(number: String, password: Variant = null) -> Dictionary:
    var payload := {"room_number": number}
    if password != null:
        payload["password"] = password
    return payload

func send_direction(direction: Vector3) -> String:
    if state.current_snapshot.get("phase", "") != "PLAYING":
        return ""
    var payload := {"match_id": state.current_snapshot.match_id,
        "input_seq": next_input_seq, "action": {"type": "move",
        "direction": {"x": direction.x, "y": direction.y, "z": direction.z}}}
    var id := _request("game.action", payload)
    if not id.is_empty():
        next_input_seq += 1
    return id
```

내부 `_request(op:String,payload:Dictionary)->String`는 상한·guest 준비·type을 확인하고 deadline과 generation만 pending에 저장하며 `_send_text` 실패 시 제거한다. request ID counter는 이 경로에서만 증가한다. room lifecycle wrapper는 pending op epoch로 검사하며 event는 RoomState가 판정한다. `room.list`는 current snapshot에 넣지 않는다. leave 송신 성공 직후 clear/fence하고 snapshot_changed({})를 emit한다. timeout의 local code는 `CLIENT_TIMEOUT`, pending 상한은 `CLIENT_BUSY`, invalid local 입력은 `CLIENT_INVALID_INPUT`으로 server code와 구분한다. create/join/start/leave timeout은 disconnect한다.

- [ ] **Step 4: GREEN과 실제 guest를 확인한다.** 위 headless unit을 실행하고 전용 127.0.0.1 port의 기존 Go 서버에서 `CoopClient`로 guest→public create→list→leave를 한 번 수행한다. process exit와 Godot parse/runtime stderr를 확인한다. main scene은 `/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --quit-after 5`로 검사한다. listener가 sandbox EPERM이면 root에 전용 서버 시작을 맡긴다. 종료 대상은 소유 PID로 한정한다.
- [ ] **Step 5: diff 검토 후 local commit한다.** 소유 path만 명시 stage하고 `git commit -m "feat: add Godot guest and room transport"`를 실행한다. task-1-report에 RED/GREEN, 실제 guest, 건수와 SHA를 기록하고 Main/run_unit의 소유권을 다음 task에 넘긴다.

## Task 2: native lobby와 private 입장 workflow

**독립 산출물:** 6자리 번호와 public/private 조작을 native UI에서 제공하고 host/member를 정확히 표시하며 실패 후에도 secret을 노출하지 않는다.

**Files:** Create `examples/godot/scripts/lobby.gd`와 `.gd.uid`. Modify `examples/godot/scripts/main.gd`, `examples/godot/tests/run_unit.gd`. 기존 transport/state는 Task 1의 계약을 소비하며 수정이 필요하면 root를 통해 소유권을 옮긴다.

**Interfaces:** Consumes CoopClient의 room methods/signals/state. Produces `LobbyPanel`과 공개 widget/handler 계약. `_ready()`와 `bind_client()`의 호출 순서가 바뀌어도 UI 구축과 signal 연결이 동작한다.

- [ ] **Step 1: UI RED test를 작성한다.** `run_unit.gd`의 main 처리를 async로 바꾸고 root에 Control을 추가한 뒤 process_frame을 기다려 widget을 검사한다. Task 1 fixture를 그대로 사용한다.

```gdscript
func test_password_ui() -> void:
    var client := Support.FakeClient.new()
    client.state.new_connection(1)
    client.state.accept_guest("sA")
    var lobby := LobbyPanel.new()
    root.add_child(lobby)
    lobby.bind_client(client)
    await process_frame
    lobby.room_number_edit.text = "004271"
    lobby._join_clicked()
    assert(not JSON.parse_string(client.sent.back()).payload.has("password"))
    var first_id: String = JSON.parse_string(client.sent.back()).request_id
    client._accept_text(JSON.stringify({"version": 1, "op": "room.join", "request_id": first_id,
        "payload": null, "error": {"code": "PASSWORD_REQUIRED", "message": "Room password is required."}}), 1, 1)
    assert(lobby.join_password_edit.visible)
    assert(lobby.join_password_edit.secret)
    lobby.join_password_edit.text = " abcd "
    lobby._join_clicked()
    assert(JSON.parse_string(client.sent.back()).payload.password == " abcd ")
    var second_id: String = JSON.parse_string(client.sent.back()).request_id
    client._accept_text(JSON.stringify({"version": 1, "op": "room.join", "request_id": second_id,
        "payload": null, "error": {"code": "PASSWORD_INVALID", "message": "Room password is invalid."}}), 1, 2)
    assert(client.sent.size() == 2)
    assert(lobby.status_label.text.contains("Room password is invalid."))
    lobby._handle_snapshot(Support.snapshot())
    assert(lobby.join_password_edit.text.is_empty())
    assert(lobby.create_password_edit.text.is_empty())
    assert(not lobby.start_button.disabled)
    lobby._handle_snapshot(Support.snapshot("rA", null, 2, 2, ["sB", "sA"]))
    assert(lobby.start_button.disabled)
    lobby.join_password_edit.text = "secret"
    lobby._handle_connection("disconnected")
    assert(lobby.join_password_edit.text.is_empty())
    lobby.queue_free()
    client.free()
    await process_frame
```

같은 step에서 invalid 5/7/fullwidth 번호가 송신되지 않는지, capacity selector 1..6, private create/공개 전환, 중복 bind가 요청을 중복 발행하지 않는지, connecting/guest/WAITING/PLAYING별 버튼 gating과 full ID 기반 자기 자신/host 비교를 검사한다.

- [ ] **Step 2: RED를 확인한다.** `/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --import` 후 `/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_unit.gd`를 실행한다. widget 부재나 위 UI 행위 실패를 기록한다.
- [ ] **Step 3: lobby와 Main을 구현한다.** URL/연결, public room list, capacity/visibility/password, 번호 join, member 목록, start/leave, status와 조작 설명을 native Control로 만든다. LineEdit의 secret을 사용하고 문자열은 trim하지 않는다. 최초 join은 password=null로 필드를 생략하며 서버 오류 이후에 입력란을 보여 준다.

```gdscript
func _join_clicked() -> void:
    var number := room_number_edit.text
    if not WireCodec.valid_room_number(number):
        status_label.text = "룸 번호는 반각 숫자 6자리로 입력하세요."
        return
    var password: Variant = null
    if join_password_edit.visible:
        password = join_password_edit.text
        if not WireCodec.valid_password(password):
            status_label.text = "비밀번호는 4~64자로 입력하세요."
            return
    _client.join_room(number, password)

func _handle_connection(status: String) -> void:
    if status in ["disconnected", "closing"]:
        join_password_edit.clear()
        create_password_edit.clear()
        join_password_edit.hide()
```

error code별 한국어 안내와 원문 message를 함께 보여준다. private required/invalid는 서버 code를 따르며 자동 재시도하지 않는다. snapshot 성공과 leave에서 secret을 clear한다. native IME 입력을 고려하되 OS 단축키 지원을 추측하지 않는다.

- [ ] **Step 4: GREEN과 native UI를 확인한다.** unit 통과 후 `/Applications/Godot.app/Contents/MacOS/Godot --path examples/godot`를 실행하여 한국어 glyph, 창 resize/스크롤, secret masking, leading-zero 번호, host start disabled와 error 원문을 확인한다. 필요시 설치된 system font fallback을 추가하며 다운로드하지 않는다. request/body/password 미로깅을 diff 검색으로 확인한다.
- [ ] **Step 5: local commit.** `git commit -m "feat: add Godot room lobby and password flow"`; task-2-report에 unit/UI 관찰·font 선택·SHA를 기록하고 Main/run_unit을 Task 3에 넘긴다.

## Task 3: 서버권위3D avatar·입력·bounded보간

**독립 산출물:** 게임 시작 후 자기/상대 avatar를 3D로 보며 WASD/화살표/QE direction만 전송한다. focus/텍스트 입력/release에서 멈추며 snapshot만 위치를 결정한다.

**Files:** Create `examples/godot/scripts/movement_input.gd`, `examples/godot/scripts/input_controller.gd`, `examples/godot/scripts/snapshot_buffer.gd`, `examples/godot/scripts/avatar.gd`, `examples/godot/scripts/world.gd`, `examples/godot/tests/test_driver.gd`, `examples/godot/tests/run_integration.gd`, `examples/godot/tests/run_render_qa.gd`, `examples/godot/tests/run_checks.sh`와 `.gd.uid`. Modify `examples/godot/scripts/main.gd`, `examples/godot/project.godot`, `examples/godot/tests/test_support.gd`, `examples/godot/tests/run_unit.gd`. transport/state 수정은 root가 ownership을 조정한다.

**Interfaces:** Consumes CoopClient `send_direction`, `snapshot_changed`, `state`; Produces MovementInput/InputController/SnapshotBuffer/AvatarView/GameWorld/TestDriver의 위 계약. `snapshot_changed`를받은때Time monotonic을저장하고AvatarView위치를매framesample한다. Input seq를 world/input에 복제하지 않는다. Main은 root Window의 focus/LineEdit/TextEdit focus를 controller에 전달한다. SubViewport의 focus owner만 확인하여 lobby focus를 놓치지 않는다.

- [ ] **Step 1: 보간·수명·focus RED test를 작성한다.** Task 1 fixture를 사용하며 tree에 world를 추가한 뒤 frame을 기다린다.

```gdscript
func test_buffer_lifecycle() -> void:
    var buffer := SnapshotBuffer.new()
    var a := Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])
    assert(buffer.push_snapshot(a, 1000))
    assert(buffer.sample_positions(1000).sA == Vector3.ZERO)
    var b := a.duplicate(true)
    b.snapshot_seq = 3
    b.game_state.players[0].position.x = 10
    assert(buffer.push_snapshot(b, 1100))
    assert(buffer.sample_positions(1150).sA.is_equal_approx(Vector3(5, 0, 0)))
    assert(buffer.sample_positions(2000).sA == Vector3(10, 0, 0))
    assert(not buffer.push_snapshot(a, 2001))
    var removed := Support.snapshot("rA", "mA", 3, 4, ["sA"])
    assert(buffer.push_snapshot(removed, 3000))
    assert(not buffer.sample_positions(3000).has("sB"))
    assert(buffer.duration_ms <= 200)
    assert(buffer.sample_positions(3000).sA == Vector3.ZERO)
    var fresh := Support.snapshot("rB", "mB", 1, 1)
    assert(buffer.push_snapshot(fresh, 3001))
    assert(buffer.sample_positions(3001).sA == Vector3.ZERO)
    buffer.clear()
    assert(buffer.sample_positions(4000).is_empty())

func test_input_focus() -> void:
    MovementInput.ensure_actions()
    var movement := MovementInput.new()
    var key := InputEventKey.new()
    key.physical_keycode = KEY_W
    key.pressed = true
    Input.parse_input_event(key)
    await process_frame
    assert(movement.sample(true, false) == Vector3.FORWARD)
    assert(movement.sample(false, false) == Vector3.ZERO)
    assert(movement.sample(true, true) == Vector3.ZERO)
    movement.release_actions()
    assert(movement.sample(true, false) == Vector3.ZERO)
    key.pressed = false
    Input.parse_input_event(key)
    await process_frame
    assert(movement.sample(true, false) == Vector3.ZERO)
```

`test_support.gd`에 다음 subclass를 넣고 `run_unit.gd`에 실제 retry test를 추가한다.

```gdscript
# tests/test_support.gd
class FailOnceClient extends FakeClient:
    var fail_next: bool = true
    func _send_text(text: String) -> Error:
        if fail_next:
            fail_next = false
            return ERR_BUSY
        sent.append(text)
        return OK

# tests/run_unit.gd
func test_direction_retry() -> void:
    var client := Support.FailOnceClient.new()
    client.state.new_connection(1)
    client.state.accept_guest("sA")
    client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
    var controller := InputController.new()
    root.add_child(controller)
    controller.bind_client(client)
    controller.submit_direction(Vector3.FORWARD)
    assert(client.sent.is_empty())
    controller.submit_direction(Vector3.FORWARD)
    assert(client.sent.size() == 1)
    controller.submit_direction(Vector3.FORWARD)
    assert(client.sent.size() == 1)
    controller.set_input_gate(false, false)
    assert(client.sent.size() == 2)
    assert(JSON.parse_string(client.sent.back()).payload.action.direction == {"x": 0, "y": 0, "z": 0})
    controller.queue_free()
    client.free()
    await process_frame
```

추가 `test_direction_retry`는 FakeClient의 `_send_text`를 한 번 ERR_BUSY로 실패시키는 subclass를 만든다. InputController.submit_direction을 검사하며 renderer에서 send를 호출하지 않는다. 같은 방향을 재시도하면 성공하며 성공 후 같은 방향을 반복 송신하지 않고 focus/text gate가 zero를 보내는지 검사한다. 새 player 즉시 표시, WAITING clear, room.state 제거 즉시 반영, same capture duplicate, 16ms 최소/burst/200ms 최대 gap, QE의 y, 대각선 normalize, 화살표 physical key와 seq 상한도 검증한다.

- [ ] **Step 2: RED를 확인한다.** project import 후 `/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_unit.gd`를 실행한다. 입력 event의 frame 처리를 await하며 Input.action_press를 engine keyboard 처리의 증거로 대체하지 않는다.
- [ ] **Step 3: 입력·buffer·world를 구현한다.** PLAYING이며 window focus가 있고 LineEdit/TextEdit focus가 없을 때만 방향을 sample한다. focus_exited/복귀에서는 release_actions와 zero를 사용한다. InputController가 방향이 달라질 때만 send하고 거절 시 last_sent를 갱신하지 않는다. Main이 root Window gate를 전달한다. camera를 tree에 추가한 뒤 look_at하며 CapsuleMesh/PlaneMesh/DirectionalLight3D/Label3D만으로 구성한다.

```gdscript
func sample(focused: bool, text_input_focused: bool, allow_y: bool = true) -> Vector3:
    if not focused or text_input_focused:
        return Vector3.ZERO
    var direction := Vector3(Input.get_axis("move_left", "move_right"),
        (Input.get_axis("move_down", "move_up") if allow_y else 0.0), Input.get_axis("move_forward", "move_back"))
    return direction.normalized() if direction.length_squared() > 1.0 else direction

func submit_direction(direction: Vector3) -> void:
    if direction == _last_sent_direction:
        return
    if not _client.send_direction(direction).is_empty():
        _last_sent_direction = direction

func sample_positions(now_ms: int) -> Dictionary:
    var result := {}
    var alpha := clampf(float(now_ms - _latest_received_ms) / float(duration_ms), 0.0, 1.0)
    for id in _latest_positions:
        var latest: Vector3 = _latest_positions[id]
        result[id] = _previous_positions.get(id, latest).lerp(latest, alpha)
    return result
```

Buffer 내부 `_latest_received_ms:int`, `_latest_positions:Dictionary`, `_previous_positions:Dictionary`, `_room_id:String`, `_match_id:String`, `_snapshot_seq:int`를 정의한다. push는 같은 identity의 seq 역행/중복을 거절하고 identity 변경 시 clear한다. gap>500ms에서는 previous=latest로 snap한다. 제거된 player는 두 map에서 삭제하며 duration을 clamp하고 새 player는 previous=latest로 만든다. world는 members와 game_state.players 양쪽으로 avatar를 reconcile한다. 이전 buffer가 제거된 member를 표시하지 않도록 한다. 퇴장은 같은 frame에 dictionary erase와 queue_free를 수행한다. ACK로 buffer를 바꾸지 않는다.

- [ ] **Step 4: GREEN과 3D preview를 확인한다.** unit/main 시작을 확인하고 두 native 창을 실제 서버에 연결하여 start→한 명 move→zero를 수행한다. 양쪽이 동일한 server position을 받은 것을 확인하고 아래 같은 task의 실제 QA에서 재현 가능한 capture/test로 고정한다. render 수신 위치와 server state를 구분해서 보고한다.

### Task 3의 실제 서버와 native QA 단계

3D를 먼저 검증한다. 같은 task의 위 Files에 integration/native helper가 포함되어 있다. 문서 갱신은 Task 5에서 한 번 수행한다.

**Interfaces:** Consumes Task 1~3 공개 API와 위 TestDriver 계약. Produces `run_checks.sh`의 unit+실제 서버 integration, `run_render_qa.gd`의 native PNG와 bounded QA report. TestDriver는 request_completed와 snapshot_changed를 연결하여 ID별 응답을 찾는다. 기다림은 SceneTree.process_frame과 Time deadline을 사용하며 timeout/overflow는 nonzero exit로 실패한다. 이미 queue에 도착한 응답도 찾아 signal 시점과 await 순서에 따라 응답을 잃지 않는다.

- [ ] **Step 5: 실제 entry/sync test를 먼저 작성한다.** `new_driver(url:String)->TestDriver`는 root에 CoopClient를 추가하고 driver.attach, connect_guest, guest 대기를 수행한다. `same_capture(states:Array)->bool`은 match/revision/server_tick/snapshot_seq별 같은 full snapshot을 deep 비교한다. 서로 다른 position을 섞은 unit으로 거짓 PASS를 막는다.

```gdscript
func test_private_entry(url: String) -> void:
    var host := await new_driver(url)
    var entrant := await new_driver(url)
    var made := await host.await_reply(host.client.create_room(1, "private", " abcd "))
    assert(made.error.is_empty())
    var number: String = made.payload.snapshot.room_number
    var missing := await entrant.await_reply(entrant.client.join_room(number))
    assert(missing.error.code == "PASSWORD_REQUIRED")
    var wrong := await entrant.await_reply(entrant.client.join_room(number, "abcd"))
    assert(wrong.error.code == "PASSWORD_INVALID")
    var full := await entrant.await_reply(entrant.client.join_room(number, " abcd "))
    assert(full.error.code == "ROOM_FULL")
    var listing := await entrant.await_reply(entrant.client.list_rooms())
    for room in listing.payload.rooms:
        assert(room.room_number != number)
    await host.await_reply(host.client.start_room())
    var playing := await entrant.await_reply(entrant.client.join_room(number, " abcd "))
    assert(playing.error.code == "ROOM_IN_PROGRESS")
    await host.await_reply(host.client.leave_room())
    host.close()
    entrant.close()

func test_six_clients(url: String) -> void:
    var drivers: Array[TestDriver] = []
    for index in 7:
        drivers.append(await new_driver(url))
    var made := await drivers[0].await_reply(drivers[0].client.create_room(6, "public"))
    var number: String = made.payload.snapshot.room_number
    for index in range(1, 6):
        var joined := await drivers[index].await_reply(drivers[index].client.join_room(number))
        assert(joined.error.is_empty())
    var rejected := await drivers[6].await_reply(drivers[6].client.join_room(number))
    assert(rejected.error.code == "ROOM_FULL")
    var started := await drivers[0].await_reply(drivers[0].client.start_room())
    var match_id: String = started.payload.snapshot.match_id
    for index in 6:
        await drivers[index].await_snapshot(func(s): return s.get("match_id") == match_id)
    var self_id: String = drivers[0].client.state.session_id
    var moved := await drivers[0].await_reply(drivers[0].client.send_direction(Vector3(0, 0, 1)))
    assert(moved.error.is_empty() and moved.payload.input_seq == 1)
    assert(not moved.payload.has("position"))
    var observed := await drivers[0].await_snapshot(func(s):
        return s.get("last_processed_input_seq", {}).get(self_id, 0) >= 1)
    assert(observed.server_tick >= moved.payload.server_tick)
    var stopped := await drivers[0].await_reply(drivers[0].client.send_direction(Vector3.ZERO))
    assert(stopped.error.is_empty() and stopped.payload.input_seq == 2)
    await require_common_capture(drivers.slice(0, 6), self_id, 2)
    await drivers[0].await_reply(drivers[0].client.leave_room())
    await drivers[1].await_snapshot(func(s):
        return s.host_id == drivers[1].client.state.session_id and s.members.size() == 5)
    var next := await drivers[0].await_reply(drivers[0].client.create_room(1, "public"))
    assert(next.error.is_empty())
    await drivers[0].await_reply(drivers[0].client.start_room())
    var again := await drivers[0].await_reply(drivers[0].client.send_direction(Vector3.ZERO))
    assert(again.payload.input_seq == 3)
    for index in 6:
        await drivers[index].await_reply(drivers[index].client.leave_room())
    for driver in drivers:
        driver.close()
```

`require_common_capture(drivers:Array,self_id:String,min_seq:int)->void`를 구현한다. 각 driver의 최근 128개 capture map에서 전체 공통 (match,revision,tick,seq)를 골라 same_capture를 검사한다. move는 position.z>spawn.z, velocity.z=5, x/y 불변, stop은 velocity zero와 후속 두 capture의 위치 불변을 확인한다. snapshot_seq/tick 차이로 15/30Hz나 이동 거리를 추측하지 않는다. 새 guest 재연결 시 새 session ID와 seq 1, PLAYING socket close 후 member/player 소멸, 같은 socket leave/rejoin도 검증한다. synthetic game.ended fixture로 state/buffer 종료를 검증하며 end RPC는 추가하지 않는다.

- [ ] **Step 6: RED 확인 후 helper를 구현한다.** 변경된 full snapshot이 same_capture=false가 되는 test를 먼저 실행하고 checker 부재나 오비교를 RED로 기록한다. TestDriver 대기 API, new_driver, require_common_capture를 구현하고 실제 서버 integration을 실행한다. assert 실패는 제품 결함으로 되돌려 수정하며 서버 동작을 지어내어 test를 완화하지 않는다.
- [ ] **Step 7: runner와 소유 server cleanup을 구현한다.** `run_checks.sh`는 repo root로 이동하고 Go executable을 ignored work에 build한다. 전용 127.0.0.1 random port에서 서버 시작, health bounded wait, Godot import→unit→integration을 수행하고 trap으로 소유 server PID만 kill/wait한다. `--url ws://127.0.0.1:<port>/ws`는 Godot `--` 이후 userargs로 전달한다. listener EPERM이면 root가 같은 owned runner를 approval 경로로 실행한다. pkill이나 타인의 서버 종료를 하지 않는다.

```bash
examples/godot/tests/run_checks.sh
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_integration.gd -- --url ws://127.0.0.1:8080/ws
```

- [ ] **Step 8: 두 독립 native 창에서 render/input을 검증한다.** `run_render_qa.gd`는 Main을 tree에 추가하며 host/peer mode와 URL/room file/output prefix를 userargs에서 읽는다. host가 ignored work file에 room number를 쓰고 peer가 입장한다. 시작 후 두 창은 공통 snapshot capture를 관측한다. Camera/avatar를 렌더한 뒤 `await RenderingServer.frame_post_draw`와 `root.get_texture().get_image().save_png(path)`로 PNG 두 개를 저장한다. 각 창에서 physical key synthetic event로 move→release→zero를 수행하고 LineEdit.grab_focus와 window focus gate의 zero를 보고한다.

```gdscript
var key := InputEventKey.new()
key.physical_keycode = KEY_D
key.pressed = true
Input.parse_input_event(key)
await process_frame
key.pressed = false
Input.parse_input_event(key)
await process_frame
await RenderingServer.frame_post_draw
var image := root.get_texture().get_image()
assert(not image.is_empty())
assert(image.save_png(output_path) == OK)
```

PNG를 실제로 view하여 lobby glyph/잘림, floor/camera, 서로 다른 두 avatar와 자기 label, 이동/정지, 사라진 player를 확인한다. 이것은 synthetic engine input QA이며 수동 OS keyboard test로 기록하지 않는다. 두 창/native rendering이 환경에서 불가능하면 제한을 보고하며 headless 성공으로 native QA를 대체하지 않는다. 소유 window PID와 socket을 닫고 report를 남긴다.
heartbeat 시나리오는 별도 `WS_READ_TIMEOUT=3s` 서버에서 다음 행위 test를 실행한다. 기본 integration 서버를 이 값으로 바꾸어 긴 다른 test가 timeout되게 하지 않는다.

```gdscript
func test_heartbeat(url: String) -> void:
    var driver := await new_driver(url)
    var original_id: String = driver.client.state.session_id
    driver.client.heartbeat_interval_ms = 1000
    var replies := [0]
    driver.client.request_completed.connect(func(op: String, _id: String, _payload: Dictionary, error: Dictionary):
        if op == "room.list" and error.is_empty():
            replies[0] += 1)
    var deadline := Time.get_ticks_msec() + 6000
    while Time.get_ticks_msec() < deadline:
        await process_frame
    assert(driver.client.state.session_id == original_id)
    assert(replies[0] >= 4)
    driver.close()
```

- [ ] **Step 9: heartbeat 실제 유지와 local commit.** 전용 서버를 `WS_READ_TIMEOUT=3s`로 시작하고 client.heartbeat_interval_ms=1000으로 맞춘 뒤 6초 동안 room.list 응답과 같은 guest 연결이 유지되는지 검사한다. 유효한 data request가 read deadline을 유지했음을 확인하며 기본 120초 설정 전체를 기다렸다고 표현하지 않는다. 기존 기본 cadence 30초/timeout 10초는 유지한다. 3D unit/실제 여섯 연결/두 native 창의 성공과 소유 PID cleanup 후 `git commit -m "feat: verify authoritative Godot 3D clients"`로 Task 3을 commit하고 task-3-report에 기록한다. 이 검증과 commit 이전에는 2D 구현을 시작하지 않는다.

## Task 4: XZ 평면 2D 표시와 mixed 3D/2D 검증

**독립 산출물:** 동일 룸의 서버 권위 상태를 2D와 3D로 함께 보고 모드 전환·focus·정지가 sequence와 room 소속을 유지한다.

**Files:** Create `examples/godot/scripts/world_2d.gd`와 `.gd.uid`. Modify `examples/godot/scripts/main.gd`, `examples/godot/scripts/lobby.gd`(표시 모드 selector만), `examples/godot/scripts/movement_input.gd`, `examples/godot/scripts/input_controller.gd`, `examples/godot/tests/run_unit.gd`, `examples/godot/tests/run_integration.gd`, `examples/godot/tests/run_render_qa.gd`. Task 3 검증과 commit 완료 후 공유 파일 ownership을 Task 4로 넘긴다. renderer/world state buffer는 기존 API를 그대로 소비한다.

**Interfaces:** Consumes Task 1~3의 CoopClient/SnapshotBuffer/InputController/MovementInput. Produces GameWorld2D.project_position과 pure renderer 계약, Main._set_view_mode와 view_mode selector. InputController 하나만 두 mode에서 송신한다. mode는 정확히 `3d`/`2d`이고 allow_y는 3d일 때만 true다.

- [ ] **Step 1: 2D projection/input와 모드 수명의 RED test를 작성한다.**

```gdscript
func test_2d_projection_and_input() -> void:
    assert(GameWorld2D.project_position(Vector3(3, 20, -4)) == Vector2(3, -4))
    MovementInput.ensure_actions()
    var movement := MovementInput.new()
    var key := InputEventKey.new()
    key.physical_keycode = KEY_E
    key.pressed = true
    Input.parse_input_event(key)
    await process_frame
    assert(movement.sample(true, false, false) == Vector3.ZERO)
    assert(movement.sample(true, false, true) == Vector3.UP)
    movement.release_actions()
    key.pressed = false
    Input.parse_input_event(key)
    await process_frame

func test_mode_preserves_session() -> void:
    var client := Support.FakeClient.new()
    client.state.new_connection(1)
    client.state.accept_guest("sA")
    client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
    var controller := InputController.new()
    root.add_child(controller)
    controller.bind_client(client)
    controller.submit_direction(Vector3.FORWARD)
    var next_before := client.next_input_seq
    controller.set_view_mode("2d")
    assert(controller.view_mode == "2d")
    assert(client.state.session_id == "sA")
    assert(client.state.current_snapshot.room_id == "rA")
    assert(client.next_input_seq >= next_before)
    assert(JSON.parse_string(client.sent.back()).payload.action.direction == {"x": 0, "y": 0, "z": 0})
    controller.queue_free()
    client.free()
    await process_frame
```

같은 snapshot의 2D/3D projection, y가 화면에 안 보여도 server state는 보존, player 제거/새 player 즉시, WAITING clear, 2D snapshot buffer endpoint와 2D mode에서 duplicate game.action 없음도 검사한다. 모드 전환 zero send가 일시 거절되면 다음 frame에 먼저 zero를 재시도하고 key action을 clear하여 미송신 stop이 사라지지 않도록 한다.

- [ ] **Step 2: RED를 확인한다.** `/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --import` 후 같은 executable로 `--script res://tests/run_unit.gd`를 실행한다. 새 2D class 부재 또는 projection/input/mode 행위 실패를 기록한다.
- [ ] **Step 3: pure 2D renderer와 mode controller를 구현한다.**

```gdscript
static func project_position(position: Vector3) -> Vector2:
    return Vector2(position.x, position.z)

func _draw() -> void:
    for id in positions:
        draw_circle(positions[id] * pixel_scale, 12.0, Color(0.35, 0.75, 0.95))
```

GameWorld2D는 Node2D draw_circle/draw_line과 Label children으로 원·격자·full ID 기반 avatar label을 만든다. origin은 gameplay 영역 중앙이다. Main은 2D/3D 활성 renderer와 view selector를 바꾸고 양쪽 reset_timeline에 현재 accepted snapshot과 같은 monotonic time을 전달한다. _set_view_mode에서 InputController.set_view_mode를 먼저 호출해 zero/release한다. inactive renderer는 input을 처리하지 않는다. physics·추가 wire·y teleport를 구현하지 않는다.
- [ ] **Step 4: GREEN과 실제 mixed native QA.** unit을 통과한 후 기존 실제 Go server에 두 독립 native 창(첫 3D, 둘째 2D)을 연결하여 create/join/start한다. 2D의 W/D→release→focus stop과 Q/E 비활성, 3D의 QE y 이동을 synthetic engine event로 검증한다. 공통 (match,revision,tick,seq)의 full state가 동일하고 2D가 Vector2(x,z)로 표시하는지 확인한다. 2D↔3D 전환 후 room/session/input_seq가 유지되고 zero가 ACK되며 같은 방향 중복 송신이 없음을 확인한다. frame_post_draw 뒤 각 native PNG를 저장하여 한국어 glyph/여섯 표시 위치/모드 selector/자기 label을 view한다. 기존 3D full QA를 새 변경으로 영향받은 범위에서 재검증하되 중복 성공 실행을 불필요하게 반복하지 않는다.
- [ ] **Step 5: cleanup과 local commit.** 소유 native PID/socket/server와 ignored capture를 확인하고 `git commit -m "feat: add and verify Godot 2D projection"`로 소유 파일만 commit한다. task-4-report에 2D unit/mixed 실제 wire/native 결과와 SHA를 기록한다.

## Task 5: 실행 문서와 최종 client 검증

**독립 산출물:** 이용자는 Go 서버 뒤 Godot 예제를 3D/2D로 열 수 있고 실제 검증 범위와 한계를 구분할 수 있다.

**Files:** Create `examples/godot/README.md`. Modify `README.md`, `docs/getting-started.md`, `docs/server-guide.md`, `docs/README.md`, `docs/specs/01-overview.md`, `docs/specs/07-implementation-and-config.md`, `docs/specs/08-validation.md`. 최종 runner 통합이 필요하면 `examples/godot/tests/run_checks.sh`만 Task 4에서 ownership을 넘긴다. 제품 버그는 root가 해당 소유 task로 되돌린다. 이 설계/계획 수정은 writer 소유다.

**Interfaces:** Consumes Task 1~4 실행 명령/API/보고서. Produces 버전·endpoint·키·mode·timeout·heartbeat·검증 범위를 설명하는 README와 실행 가능한 최종 runner. 새 제품 API는 없다.

- [ ] **Step 1: README와 현재 범위를 갱신한다.** examples README에 Godot 4.4+ API 기준/실제 4.7.2 검증, Go 시작, project 열기, 3D/2D 선택·32px XZ 투영·서버 y 보존, 두 창 연결, private workflow, WASD/arrows/QE, free-space/no-collision, focus stop, heartbeat/timeout, receive clock/추가 latency, 재연결 새 guest, test 명령을 쓴다. root README는 기존 소개를 유지하며 Godot 예제 CTA와 현재 단계 한 문장만 수정한다. getting-started/server-guide/spec overview/implementation/docs index의 Godot 미구현 표현을 예제와 미검증 범위에 맞추며 기존 Go checker 기록은 변경하지 않는다. docs 08에 Godot 검증 참조를 추가한다.
문서의 이용자 안내는 아래 내용을 포함하며 실제 검증 report와 정확히 맞춘다.

```markdown
Godot 예제는 examples/godot/project.godot을 열어 실행합니다.
먼저 저장소 root에서 go run ./cmd/server를 실행하세요.
기본 주소는 ws://127.0.0.1:8080/ws이며 연결마다 새로운 guest입니다.
public/private 룸은 정원 1~6명이고 룸 번호는 선행 0을 포함한 반각 숫자 6자리입니다.
WASD/화살표는 XZ 이동, 3D의 Q/E는 Y 이동입니다. 2D에서는 Q/E를 사용하지 않습니다.
2D는 서버 위치의 XZ 평면을 표시하며 서버 Y 값을 바꾸지 않습니다.
위치는 서버 snapshot만 사용합니다. 보간에는 추가 표시 지연이 있습니다.
포커스 상실과 텍스트 입력 중에는 정지 요청을 보냅니다.
기본 heartbeat는 room.list 요청 30초이며 서버 read timeout을 바꾸면 간격도 조정하세요.
Godot 4.4 API를 기준으로 작성했고 실제 실행 검증은 4.7.2에서 수행했습니다.
web export, TLS 배포, production 성능과 충돌/중력은 검증 범위가 아닙니다.
```

- [ ] **Step 2: 최종 GREEN과 Architect gate를 확인한다.** 새 문제가 없다면 run_checks.sh를 한 번 fresh로 실행하고 unit/실제 wire/native QA 건수를 구분한다. Go source 변경이 없다면 이전 Go full/race 결과는 carry 기록이며 이번 실행으로 표현하지 않는다. `git diff --check`, tracked import cache/로그/비밀 값, PID cleanup을 확인한다. 독립 Architect에 diff와 report를 넘기고 거절 사항을 수정한 뒤 해당 test를 실행한다. 최종 보고를 기록하고 다음 step에서 문서만 commit한다. push/PR는 이번 범위에 추가하지 않는다.

- [ ] **Step 3: 문서 링크와 최종 commit.** 다음 명령으로 새 예제 링크와 과거 Godot 미구현 문구를 점검하고 `git diff --check`를 실행한다. 남은 미래 범위의 문장을 임의로 현재 구현으로 바꾸지 않는다. `git commit -m "docs: publish Godot 3D and 2D example guidance"`로 문서와 마지막 runner 수정만 commit한다.

```bash
rg -n "examples/godot|Godot|미구현|2D|3D" README.md docs/getting-started.md docs/server-guide.md docs/README.md docs/specs/01-overview.md docs/specs/07-implementation-and-config.md docs/specs/08-validation.md examples/godot/README.md
git diff --check
```

## 자기 리뷰와 완료 근거

설계의 모든 요구는 T1 wire/session/secret/fence/seq/timeout/bounds, T2 native lobby/password/host, T3 공통 입력·보간·3D·실제 여섯 client/native QA·heartbeat, T4 2D projection/모드 수명/mixed native QA, T5 문서와 최종 검증으로 대응한다. placeholder scan과 class/method/signal 이름 정합, Review Focus 다섯 항목의 실제 test 소유권을 확인한다. 단일 InputController 입력 소유권과 root Window focus는 양 renderer에서 동일하다. 공식 4.4 API 문서와 실제 4.7.2 조사 결과로 WebSocket inbound/outbound 속성, poll, packet 종류, JSON float, physical key/focus, look_at, monotonic time과 frame_post_draw capture를 대조했다.

계획서는 구현 성공이나 QA 통과의 증거가 아니다. 완료 보고는 다섯 local commit, fresh Godot unit/실제 wire/3D native/2D mixed native QA, Architect 판단과 남은 web/TLS/performance/clock 한계를 포함한다. 사용자의 계속 진행·같은 turn 실행과 위임 방식 지시를 유지하며 새 실행 방식 선택이나 재승인 대기를 삽입하지 않는다.
