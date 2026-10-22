# Sync Load Test and Godot Example Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 여러 룸의 동시 이동을 재현·측정하는 Go 도구와 연결·동기화 상태가 보이는 Godot 예제를 제공한다.

**Architecture:** 기존 서버를 외부 WebSocket client로 검사하는 `internal/loadtest`와 얇은 CLI를 추가한다. Godot의 기존 bounded pending·소속 검증을 이용해 현재 연결의 입력 ACK와 snapshot 수신 지표를 표시하고 공통 runner의 namespace 검증을 통합한다.

**Tech Stack:** Go `1.26.0` 이상, `github.com/coder/websocket v1.8.15`, 기존 JSON 프로토콜, Godot 최소 API `4.4`·실행 검증 엔진 `4.7.2`; 새 dependency 없음.

**Spec:** [동기화 부하 테스트와 Godot 예제 개선 설계](sync-loadtest-and-godot-design.md), [동기화 계약](../specs/03-realtime-sync.md), [검증 기준](../specs/08-validation.md). 기준 commit은 `597a0ade46a1769bf319b17a35b771945d200b01`이다. 사용자가 선택한 두 작업을 현재 CoopDock 경로에서 실행하고 작업별 커밋·푸시한다.

## Global Constraints

- 룸당 `1~6명`, 게임별 namespace와 공개 Go package·WebSocket version 1을 유지한다.
- 입력과 성공 ACK의 op는 `game.action`이다. FIFO 입력별 적용 ACK이며 ACK tick은 같을 수 있다.
- 기본값은 `30Hz tick / 15Hz snapshot`; 성능 보장이 아니다. 임의의 절대 지연·메모리 합격 수치를 만들지 않는다.
- 부하 CLI 기본값은 local URL, `10`룸·`6`명·`10s`·`20Hz`, `game-ids=default`다. 실제 측정은 `1/10/50`룸, 각 `20s`·`20Hz`다.
- setup·측정·drain을 분리하고 같은 장비에서 서버·생성기가 경쟁한다는 한계를 보고한다.
- 모든 메모리 보관은 bounded이고 표본 제한 시 observations·retained sample 수와 estimator/truncation/정밀도를 표시한다.
- guest 자동 복원·비밀번호 저장·local prediction·서버 게임 규칙·dependency를 추가하지 않는다.
- 코드 주석은 영어, 문서·사용자 안내는 한국어다. 소유하지 않은 파일이나 다른 작업자의 변경을 되돌리지 않는다.
- root가 Git·실행 증거·통합·커밋·푸시를 소유한다. 구현자는 자신의 파일·실제 검증 결과만 보고한다.

## 공식 API와 수명 경계

[설계의 공식 API 근거](sync-loadtest-and-godot-design.md#공식-api-근거)를 읽고 단일 reader·context·poll 규약을 따른다.
연결 run 수명과 측정 종료 신호를 분리해 측정이 끝난 뒤에도 ACK drain과 정지 확인이 가능해야 한다.
전체 취소 때는 별도 deadline의 cleanup·`CloseNow`·goroutine 회수를 확인한다.
측정 중 정확한 member 집합 검사와 cleanup 중 정상 member 감소를 구분한다.
RTT는 측정 중 송신한 입력을 모수로 삼아 drain 중 수신한 ACK도 포함한다.
Godot의 기존 data-frame heartbeat를 유지하고 옛 operation epoch의 응답도 현재 지표를 갱신하지 못하게 한다.

## Review Focus

| 입력·상황 | 기대 결과와 소유 시험 |
|---|---|
| 서버가 ACK만 주고 이동·snapshot을 보내지 않음 | 공통 capture·실제 이동 0회로 실패; Task 1 `TestRunRejectsAckWithoutMovement`, `TestRunRejectsMissingSnapshots` |
| 다른 룸·match·member 또는 동일 key의 다른 상태 | 소속 위반·capture mismatch로 실패; Task 1 `TestSnapshotBinding`, `TestCaptureMismatch` |
| 전체 측정과 다른 구간으로 통계를 계산하거나 표본을 자름 | setup 배제·같은 window·표본 설명 유지; Task 1 `TestMeasurementWindow`, `TestMetricsSampleBounds` |
| 옛 연결 ACK·stale snapshot·소속 변경 | 현재 HUD를 오염시키지 않고 미측정 reset; Task 2 `run_sync_status.gd`의 generation/stale/reset 사례 |
| 예기치 않은 종료·취소·최종 zero 미확인 | 명확한 실패와 bounded cleanup; Task 1 `TestRunCancellationCleanup`, Task 2 종료 안내 회귀 |

## Task 1: 동시 룸 부하 도구와 정확성 검사

**Files:** Create `internal/loadtest/run.go`, `client.go`, `metrics.go`, `validate.go`와 같은 디렉터리의 `_test.go`; Create `cmd/loadtest/main.go`, `main_test.go`. 기존 `cmd/roomcheck`는 유지한다.

**Interfaces — consumes:** 기존 guest/room create·join·start·leave/list, `game.action`, `game.snapshot` 계약과 coder/websocket. 통합 시험은 `coopdock.New`, `Handler`, `Shutdown` 및 `httptest.NewServer`를 사용한다.

**Interfaces — produces:** 내부 `Config`, `Report`, `Run(context.Context, Config) (Report, error)`와 `cmd/loadtest` 실행 명령. Config는 URL·game IDs·룸/인원·기간·입력 Hz와 bounded timeout을 담고, Report는 실행 조건·전체/룸별 지표·정확성·정리 결과를 제공한다. 세부 JSON schema와 추가 flags는 구현자가 CLI help와 Task 3 가이드에 일치시킨다.

- [x] **Step 1: RED — 통계·검증 시험을 먼저 작성한다.** `TestPercentiles`는 작은 고정 데이터의 p50/p95/p99/max를 확인한다. `TestMetricsSampleBounds`는 보관 상한과 전체 관측 수·표본 설명을 확인한다.
- [x] **Step 2: RED — wire 변조를 검사한다.** `TestSnapshotBinding`은 다른 game/room/match/member를 거절하고 `TestCaptureMismatch`는 동일 capture key의 위치 변조를 거절한다. ACK 시험은 request ID·input seq·match 불일치를 거절하되 같은 tick의 서로 다른 ACK를 허용한다.
- [x] **Step 3: RED 실행.** `go test ./internal/loadtest ./cmd/loadtest -count=1`로 미구현 실패의 이유와 exit를 기록한다. import 실패만으로 변조 회귀를 입증했다고 쓰지 않는다.
- [x] **Step 4: 최소 CLI와 metrics를 구현한다.** 옵션 유효성을 연결 전에 검사하고 비정상 report는 nonzero exit로 끝낸다. monotonic `time.Now`/`Since`로 RTT와 수신 간격을 계산하고 bounded 표본의 통계 의미를 보고한다.
- [x] **Step 5: 단일 reader·직렬 writer client를 구현한다.** 연결 게임을 검증하고 bounded pending record를 request ID·seq·match·송신 시각으로 매칭한다. reader는 연결 수명 context를 쓰며 요청별 timeout으로 끊지 않는다. timeout·단절·서버 오류를 성공 ACK로 세지 않는다.
- [x] **Step 6: 동시 측정 barrier와 구간 검증을 구현한다.** 모든 룸 setup 후 각 참가자가 고정 주기로 보낸다. `TestMeasurementWindow`는 setup ACK·초기 capture가 성능·전원 공통 capture에 포함되지 않으며 tick delta와 elapsed가 같은 관측 window를 쓰는지 확인한다.
- [x] **Step 7: 이동·동일 capture·최종 정지를 구현한다.** 측정 중 각 룸에 주기 snapshot 전원 공통 capture가 최소 1개 있어야 한다. 각 참가자의 실제 위치 변화를 검사한다. 측정 종료 뒤 모든 in-flight ACK, 마지막 zero seq ACK, 처리 seq 이상 snapshot의 zero velocity와 후속 위치 유지를 확인한다.
- [x] **Step 8: 부정 서버·취소 회귀를 추가한다.** `TestRunRejectsAckWithoutMovement`, `TestRunRejectsMissingSnapshots`, `TestRunCancellationCleanup`은 잘못된 성공과 무한 대기를 막는다. 정리 여부와 남은 pending도 확인한다.
- [x] **Step 9: 실제 작은 multiroom 통합 시험을 작성한다.** 두 게임에 분배한 룸에 실제 WebSocket을 연결해 전체 경로와 정확성·마지막 퇴장 뒤 룸 제거를 검사한다. 시간은 bounded이며 최소 표본이 없으면 실패한다.
- [x] **Step 10: GREEN 및 회귀.** `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`, `git diff --check`를 실행한다. 새 CLI help와 실제 작은 실행의 stdout/report·exit를 확인한다.
- [x] **Step 11: Task 검토·커밋·푸시.** root가 소유 파일과 설계/계획을 대조하고 Architect 승인을 받은 뒤 `feat: add concurrent synchronization load tests`로 커밋·푸시한다. 실제 SHA와 원본 증거 위치를 작업 기록에 남긴다.

## Task 2: Godot 연결 안내와 공통 동기화 HUD

**Files:** Modify `examples/godot/scripts/coop_client.gd`, `lobby.gd`, `main.gd`; 필요 시 `input_controller.gd`. Create `examples/godot/tests/run_sync_status.gd`; Modify `examples/godot/tests/run_checks.sh`와 새 동작에 영향받는 기존 시험만.

**Interfaces — consumes:** 기존 `CoopClient` pending·`RoomState` 소속/generation 필터, `request_completed`, 수락 snapshot, local monotonic clock. Task 1 내부 package에는 의존하지 않는다.

**Interfaces — produces:** 입력 ACK RTT·수락 snapshot age·tick/seq를 읽는 공통 지표 accessor와 3D/2D HUD. field 이름과 accessor signature는 구현 보고서에 고정하고 그 명칭을 Task 3 문서가 그대로 쓴다. 기존 public signals와 `connect_guest`/룸/입력 호출은 보존한다.

- [x] **Step 1: RED — 연결과 지표 회귀를 작성한다.** guest 승인 뒤 목록 요청 1회, 수동 종료와 예기치 않은 종료 구분, 미측정 표시를 확인한다. 기존 controlled peer·manual clock seam을 사용한다.
- [x] **Step 2: RED — 현재 소속 검증을 작성한다.** 이전 generation·operation epoch·match의 ACK, stale snapshot은 지표를 갱신하지 않는다. 퇴장·새 match·재연결은 지표를 지우며 같은 socket의 input seq는 유지한다.
- [x] **Step 3: RED 확인.** 설치된 Godot의 `--headless --path examples/godot --script res://tests/run_sync_status.gd`를 실행해 기대 동작 부재를 기록한다. 필요 import는 기존 runner 방식을 따른다.
- [x] **Step 4: guest 성공과 종료 안내를 구현한다.** valid guest 뒤 초기 public 목록을 요청하고, 비정상 닫힘에 오류·새 guest 재연결 안내를 표시한다. 수동 종료는 비정상 오류로 표시하지 않는다.
- [x] **Step 5: bounded 지표를 구현한다.** 원본 payload 없이 pending의 송신 시각과 필요한 identity scalar를 사용한다. 현재 `game.action` 성공 ACK와 수락 snapshot만 반영하고 아직 값이 없으면 nullable/미측정으로 노출한다.
- [x] **Step 6: 공통 HUD를 연결한다.** local clock으로 snapshot age를 갱신하며 입력 ACK RTT와 tick/seq의 의미를 구분한다. 700px 창·긴 game ID·3D/2D 전환에서도 기존 tooltip과 조작 영역을 보존한다.
- [x] **Step 7: runner를 통합한다.** 기본 검증에 `run_sync_status.gd`, `run_game_scope_unit.gd`, 별도 `GAME_IDS=game-a,game-b` 서버의 `run_game_scope_integration.gd`를 포함한다. runner가 시작한 process만 deadline 내 종료·wait한다.
- [x] **Step 8: GREEN과 실제 회귀.** `GODOT_CHECKS_OUTPUT_DIR=work/sync-loadtest/godot examples/godot/tests/run_checks.sh`를 실행한다. import·unit·입력·실제 wire·heartbeat·namespace의 실제 count와 failures를 읽는다.
- [x] **Step 9: 3D/2D native 경로.** `GODOT_CHECKS_OUTPUT_DIR=work/sync-loadtest/godot-mixed examples/godot/tests/run_checks.sh --mixed-native`로 두 native+네 headless의 같은 capture와 전환/정지를 검사한다. 제한으로 미실행이면 이유와 범위를 명시하며 성공으로 확대하지 않는다.
- [x] **Step 10: Task 검토·커밋·푸시.** root가 실제 diff·증거를 확인하고 Architect 승인 후 `feat: improve Godot connection and sync feedback`으로 소유 파일만 커밋·푸시한다.

## Task 3: 실제 부하 측정과 실행 문서

**Files:** Create `docs/load-testing.md`, `docs/validation/sync-load-results.json`; Modify `examples/godot/README.md`, `docs/README.md`, 필요 시 `docs/getting-started.md`·`README.md`의 링크만. 실측 원본과 상세 로그는 ignored `work/sync-loadtest/`에 보관하고, 공개 JSON에는 환경·명령과 원본 report를 포함한다.

**Interfaces — consumes:** Task 1 CLI의 실제 flags/Report, Task 2 지표 이름·실제 runner 결과. 문서가 구현보다 앞선 성공 수치를 만들지 않는다.

**Interfaces — produces:** 재현 명령·측정표·증거와 한계, 갱신된 Godot 사용법과 검증 범위. README는 홍보용 소개를 유지하고 기술 설명은 별도 문서로 연결한다.

- [x] **Step 1: 실행 조건을 고정한다.** 현재 소스의 일반 build 서버와 load CLI를 별도 process로 실행한다. Go·OS/architecture·장비·게임 IDs·30/15Hz 등 실제 설정과 정확한 argv를 기록한다. race build의 시간을 성능표에 섞지 않는다.
- [x] **Step 2: 1·10·50룸을 순차 측정한다.** 각 6명·20초·20Hz, 모든 준비 뒤 동시 측정한다. 실제 입력 수·ACK·공통 capture·정지·정리와 전체/룸별 RTT·도착 간격·tick 진행률을 원본 JSON으로 남긴다.
- [x] **Step 3: 수치와 실패를 읽는다.** exit만 확인하지 않고 충분한 실제 표본·모든 룸 공통 capture·멤버 이동·최종 zero 확인을 검사한다. 목표 송신률과 실제 송신률, 표본 제한·관측 window를 구분한다. 실패한 단계가 있으면 원인을 해결하거나 실측 한계로 남긴다.
- [x] **Step 4: 가이드를 작성한다.** CLI 준비·한 번 실행·게임별 분배·보고서 읽기·재현 조건·실측 표를 적는다. localhost는 인터넷·화면 지연·최대 수용 인원·지연/손실 주입·게임별 성능 격리의 보장이 아님을 명시한다.
- [x] **Step 5: Godot 사용·검증 기록을 갱신한다.** 연결 후 목록과 끊김 안내·지표 의미를 설명하고 새 assembled/mixed 결과를 과거 실행과 구분한다. 지원 최소 API 4.4와 실제 검증 엔진 4.7.2를 혼동하지 않는다.
- [x] **Step 6: 최종 문서·소스 검증.** 링크·명령·지표 field를 실제 구현과 대조하고 `git diff --check` 및 변경에 필요한 fresh checks를 확인한다. task-owned process/socket이 남지 않았는지 검사한다.
- [ ] **Step 7: 최종 Architect 검토·커밋·푸시.** exact diff·실측 원본·Godot 결과를 독립 검토한 뒤 `docs: publish sync load results and Godot guide`로 커밋·푸시한다. remote SHA와 해당 GitHub CI 결과를 확인하고 검증한 범위만 보고한다.

## 실행 증거와 완료 기준

root는 `work/sync-loadtest/`에 단계별 argv·exit·소유 process·원본 report·review·commit SHA를 보존한다.
의도한 RED, 실제 제품 실패, 환경 제한, 미실행을 구분한다. 계획 체크박스는 실제 완료 뒤에만 바꾼다.
세 Task의 소스·문서·실측·정리가 확인되고 Architect 승인·커밋·푸시·CI가 확인되어야 전체 완료를 보고한다.

### Task 1·2 실행 완료 기록

- Task 1: [`425c173`](https://github.com/sangjinsu/coopdock/commit/425c173fe0a29caab0b08cf5befad93c49b55431), `feat: add concurrent synchronization load tests`. 정확한 소유 9파일의 최종 Architect 승인 뒤 `main`에 커밋·푸시했다. Go test·race 13개 package, vet·build를 통과했고 2게임·2룸·12명 실제 smoke에서 입력 240개/ACK 240개, 공통 capture 룸당 15개, 전원 이동·정지와 룸·연결·reader 정리를 확인했다. 검토에서 찾은 목록 잘림·배열 순서·미래/역행 처리 seq의 잘못된 성공은 실제 동작 RED 후 수정했다.
- Task 2: [`e5fa8b3`](https://github.com/sangjinsu/coopdock/commit/e5fa8b38d6a8b25090849cbc79c50561421a89a8), `feat: improve Godot connection and sync feedback`. 정확한 소유 9파일의 최종 Architect 승인 뒤 `main`에 커밋·푸시했다. Godot assembled 8 suite는 752 checks/0 failures, child 13개 종료·회수다. 독립 mixed 3D/2D 두 native+네 headless는 242/0, 전원 공통 full capture 94개, child 8개 전부 exit 0·회수다. 최초 native helper의 정지 barrier 실패 기록은 보존하고 helper 회귀 9/0과 수정 후 전체 재실행으로 확인했다.
- Task 3: 소스 `425c173`의 일반 build에서 1·10·50룸 × 6명 × 20초 × 20Hz를 매번 새 서버로 순차 실행했다. 입력/ACK는 2,400·24,000·120,000개이며 전원 이동·정지·모든 룸 제거·연결/reader 정리를 확인했다. 환경·실제 조건·원본 SHA와 61개 룸 report는 [공개 JSON](../validation/sync-load-results.json), 통계 해석과 재현 절차는 [가이드](../load-testing.md)에 보존한다. 소스 CI `39873910696`는 성공이며, 최종 문서의 Architect 검토·커밋·푸시·그 CI는 이 기록 시점에 대기 중이다.
