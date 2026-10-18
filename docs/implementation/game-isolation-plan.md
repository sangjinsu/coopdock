# 게임별 룸 격리 실행 계획

설계: [game-isolation-design.md](game-isolation-design.md). 기준 HEAD `a580f71`; 기존 Godot Task 3는 `task-3-checkpoint.md`의 의도적인 RED다. root가 아래 3개 task를 순차 배정하고 검토 후 경로를 지정하여 local commit한다. worker는 소유 파일만 수정하고 다른 변경을 되돌리지 않으며 stage/commit/push하지 않는다. 원래 Godot Task 3~5는 아래 namespace delta 적용 후 기존 계획대로 이어간다.

## Preflight

- [ ] root는 HEAD/status와 기존 2 modified + 10 untracked scaffold를 기록하고 보존한다. source-map·설계·architecture-report를 전달하고 기존 Godot shared-contract/Task 3 brief에 새 namespace 계약 delta를 게시한다.
- [ ] `GAME_IDS` 기본 default, 1~32 allowlist, ID `[a-z0-9][a-z0-9_-]{0,63}`를 고정한다. missing query 호환은 **default 단독 설정만** 허용한다. 다중게임/named-only missing은 400, invalid/empty/duplicate는 400, explicit unknown은 404다. Connect 이전 검증하여 session/active-connection 소비가 0이어야 한다.
- [ ] dependency cache와 기존 공식 Godot API 조사 자료를 확인한다. 새 dependency/API를 추가하지 않는다. stdlib docs 근거는 architecture-report에 있다. 다운로드/compile 실패는 의미 있는 RED로 세지 않는다.

## Task 1 — Go namespace·transport·운영·CLI

소유: `internal/room/`, `internal/config/`, `internal/transport/`, `internal/protocol/` 관련 tests, `internal/observability/`, `internal/admin/`, `cmd/roomcheck/`, 필요시 `cmd/server/main.go`. 기존 Go contract 변경을 이 task 안에서 일관되게 끝낸다. Godot와 최종 README는 수정하지 않는다.

1. [ ] `internal/room/game_scope_test.go`, `internal/transport/game_scope_test.go`, config/admin/checker 관련 tests에 행위 RED부터 작성한다. 새 API 최소 scaffold는 가능하지만 실패 원인은 compile/missing symbol이 아닌 실제 격리·한도·HTTP 응답이어야 한다.
2. [ ] `Config.GameIDs`, `ValidGameID`, immutable registry/Session.gameID, `ConnectGame(peer,gameID)`, `Session.GameID()`를 추가한다. 기존 `Connect(peer)`는 default에만 연결하는 wrapper다. default 미등록 시 다른 게임으로 fallback하지 않는다. `ListPublic(ctx,session)`으로 바꾸고 모든 호출자를 갱신한다. 전역 목록 overload는 남기지 않는다.
3. [ ] `roomKey{gameID,number}`로 create collision/insert, join, actor admit/cleanup, start/input의 map 조회와 재검증을 바꾼다. actor/session 동일 game·index identity·membership token을 함께 확인한다. 기존 gate→session lock 순서와 password 검사 순서를 유지한다.
4. [ ] `GAME_IDS` 로드/검증 후 `ParseQuery(RawQuery)` error·decoded multiplicity·1024-byte raw query cap을 검사한다. handshake 오류 후 return하고 Connect/Accept를 수행하지 않는다. guest/full snapshot에 game_id를 추가하고 RPC payload의 game_id override는 strict decoder로 거절한다.
5. [ ] OperationsSummary/admin row game_id, `(game_id,room_number,room_id)` 정렬, dashboard 게임 열·필터를 추가한다. 기존 rooms API의 선택적 exact `game_id` filter는 invalid/empty/duplicate 400, 정상 문법의 없는 ID는 matched_total=0이다. 필터 후 pagination, total/status counter는 전체를 유지한다.
6. [ ] roomcheck에 `-game-id` 기본 default, `-other-game-id` 기본 empty를 추가한다. `-url`은 query/fragment 없는 base endpoint, CLI가 검증한 game query를 구성한다. guest/full snapshot의 expected game을 검증하고 other 지정 시 실제 cross-list/join/override/lifecycle checks를 실행한다. checker 실패 injection으로 assertion 누락을 잡는다.
7. [ ] 아래 행위의 RED→GREEN을 보고한다. 테스트 전용 고정 번호 seam은 기존 `Config.number`를 사용하며 production room-number 선택 API를 추가하지 않는다.

| 경계 | 필수 검증 |
|---|---|
| core 격리 | A room은 B list에 없음, B join A 번호 NOT_FOUND, 고정 004271의 A/B 공존·다른 room ID, A cleanup 후 B 유지, 같은-game 번호 충돌 재시도 |
| Session | foreign/closed ListPublic 거절, immutable binding, registry slice 외부 수정 방지, unknown ConnectGame 실패 |
| private | 타게임 private는 유효 형식의 누락/오답/정답 모두 NOT_FOUND; 같은 game은 REQUIRED→INVALID→정답 뒤 FULL/IN_PROGRESS 순서 유지 |
| 전역 자원 | 두 게임 합계 MaxRooms/MaxSessions, IP 실패 누적 공유, room failure key는 opaque room ID로 분리, hash workers/queue·failure key cap 하나 유지 |
| 실제 WS | 양쪽 guest/create/join/start/move/stop, A host leave 후 A host 승계와 B 불변, 모든 RPC game override INVALID_MESSAGE, 다른 게임 이벤트 미전파 |
| handshake/종료 | default 단독 bare /ws 허용; multi/named-only missing 400; malformed/duplicate/encoded duplicate/unknown key 400; unknown ID404; 실패 session/conn 누수0; upgrade/accept-close race; 모든 게임 shutdown/readiness |
| 운영 | 동일번호 두 game rows, 전체 합계, exact filter·pagination·정렬·cache clone·secret 미노출 |

좁은 실행 예시(실제로 작성한 test 이름에 맞추고 실행 목록 확인):

```sh
go test ./internal/room -run 'TestGameScope|TestForeignSessionAndResourceBounds' -count=1
go test ./internal/config ./internal/protocol ./internal/transport ./internal/observability ./internal/admin ./cmd/roomcheck -run 'TestGameScope|TestGameIDs|Test.*Operations|Test.*Rooms' -count=1
```

관련 기존 password/lifecycle tests를 영향에 맞게 추가한다. full/race/vet/build는 Task 3 최종 Go source에서 한 차례 수행한다. report는 `work/game-scope/go-report.md`; root가 review/commit한다. 이 시점의 구 Godot strict parser는 새 응답을 거절하므로 전체 예제 완료라고 하지 않는다.

## Task 2 — Godot game binding과 독립 검증 runner

소유: `examples/godot/scripts/{coop_client,wire_codec,room_state,lobby}.gd`, 새 `tests/run_game_scope_unit.gd`, `tests/run_game_scope_integration.gd`와 UID. `tests/{run_unit,test_support}.gd` 공유 변경은 **fixture game_id/guest signature migration만** 허용한다. 기존 Task 3 input/buffer/world/test_driver 및 기대값·Main/project는 수정하지 않는다.

1. [ ] 기존 `work/godot-client/official-godot-research.md`를 읽고 기존 connect_to_url/JSON/Control API를 재사용한다. 새 SDK 없이 dedicated unit runner에서 invalid ID/base query·fragment, 잘못된 guest binding, 구서버의 game_id 누락, 타게임 admission/event/list, leave/reconnect binding 수명 RED를 먼저 확인한다.
2. [ ] `connect_guest(base_url,game_id="default")`가 validated ID query를 붙이고 private expected game을 보관한다. `RoomState.game_id`, `accept_guest(id,game_id)`, strict snapshot game_id를 구현한다. guest mismatch/타게임 snapshot·list는 protocol error와 disconnect이며 준비 완료로 표시하지 않는다. RPC payload는 기존 형태다.
3. [ ] lobby game ID 입력(기본 default), 현재 binding 표시, 연결 중 설정 lock/끊은 뒤 변경을 구현한다. 기존 secret masking/clear와 원문 error는 유지한다. fixture만 좁게 migration한다.
4. [ ] dedicated unit GREEN을 확인하고 기존 run_unit의 **알려진 Task 3 25 RED 외 새 regression이 없는지** 실패 이름으로 비교한다. 기존 RED를 숨기거나 GREEN으로 바꾸지 않는다.
5. [ ] integration은 미완성 TestDriver를 사용하지 않고 실제 CoopClient/WebSocketPeer와 bounded reply/snapshot helper로 A host/A peer/B host/B peer를 연결한다. guest/list/join 격리, 양쪽 create/start/move/stop ACK+snapshot, A host leave와 B 불변, reconnect game 교체를 확인한다. timeout/overflow/engine error는 nonzero다. 인자는 `-- --url=ws://127.0.0.1:18080/ws --game-a=game-a --game-b=game-b`다.

```sh
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --import
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_game_scope_unit.gd
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_unit.gd
```

report는 `work/game-scope/godot-report.md`. root가 소유 파일만 리뷰/commit하며 기존 Task 3 scaffold를 namespace commit에 섞지 않는다.

## Task 3 — 통합 확인·계약 문서·기존 작업 재개

소유: `work/game-scope/` 실행 evidence/소유 process runner, `docs/server-guide.md`, `docs/specs/04-messages-and-api.md`, 이 설계/계획의 구현 결과 보정. 최종 README/Godot 사용 가이드·screenshots는 기존 Godot Task 5 owner에게 남긴다. 기존 protocol-contract 원기록은 과거 기준으로 보존하고 root가 새 delta 링크를 붙인다.

1. [ ] `GAME_IDS=default,game-a,game-b`의 owned local 서버를 여유 port로 실행한다. `work/game-scope/bin/`에 binary를 만들고 PID+trap으로 소유 process만 정리한다. 아래 full commands는 **최종 Go source에서 각 한 번** 수행한다. 이후 Go 수정/실패가 있으면 필요한 검증만 갱신한다.

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
git diff --check
go run ./cmd/roomcheck -url ws://127.0.0.1:18080/ws -game-id default
go run ./cmd/roomcheck -url ws://127.0.0.1:18080/ws -game-id game-a -other-game-id game-b
/Applications/Godot.app/Contents/MacOS/Godot --headless --path examples/godot --script res://tests/run_game_scope_integration.gd -- --url=ws://127.0.0.1:18080/ws --game-a=game-a --game-b=game-b
```

2. [ ] default 단독 bare `/ws` 호환과 다중게임 missing 거절은 actual-WS transport suite 증거로 확인한다. 실제 Godot 결과·CLI 결과·deterministic 같은번호 core 증거를 구분한다. 네 Godot 연결 결과를 원래 Task 3 six-client/native QA 완료로 표현하지 않는다.
3. [ ] admin 게임 표시/filter와 전체 합계, 종료 후 세션/룸 cleanup을 확인한다. GAME_IDS 정책, v1 nested additive field/strict 구버전 업데이트, public namespace≠인증, global caps, 공통 movement만 지원함을 서버 계약 문서에 반영한다.
4. [ ] root가 변경 diff·fresh verification report를 Architect에게 검토받는다. 승인 후 namespace 완료를 기록하고 기존 Godot Task 3 owner에게 checkpoint와 새 계약을 전달한다. Task 3~5는 기존 계획대로 지속하며 전체 사용자 목표 완료는 별도로 판단한다.
