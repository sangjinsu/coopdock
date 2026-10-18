# Go 공개 패키지 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 다른 Go module에서 CoopDock을 import해 기존 HTTP host와 함께 실행하고 게임별 룸과 Godot 호환을 유지한다.

**Architecture:** root public façade가 기존 internal manager/transport/admin/collector를 인스턴스별로 조립한다. public Config는 내부 타입을 노출하지 않고 CLI만 env·listener·signal을 소유한다. host와 CoopDock의 종료를 분리하고 기존 WebSocket cleanup을 재사용한다.

**Tech Stack:** Go 최소 `1.26.0`, 현재 검증 `go1.27.2 darwin/arm64`, stdlib `net/http`·`context`, 기존 coder/websocket·x/crypto, 기존 Godot 실제 연결 runner.

**Spec:** [go-public-package-design.md](go-public-package-design.md). 구현자는 [go-public-package-source-map.md](go-public-package-source-map.md), [go-public-package-api-references.md](go-public-package-api-references.md)도 읽는다.

## Global Constraints

- Module path는 `github.com/sangjinsu/coopdock`, package name은 `coopdock`이다.
- `go.mod`의 Go 최소 버전은 `1.26.0`으로 유지하며 현재 검증 도구는 `go1.27.2 darwin/arm64`다.
- dependency는 기존 `github.com/coder/websocket v1.8.15`, `golang.org/x/crypto v0.58.0`, indirect `golang.org/x/sys v0.49.0`을 유지한다.
- 문서·사용자 보고는 한국어, 코드 주석은 영어, 기존 wire error와 CLI error 원문은 유지한다.
- `DefaultConfig`, `Validate`, `New`는 환경변수를 읽거나 signal handler·listener·process exit를 만들지 않는다.
- 게임 allowlist는 1~32개, ID는 기존 `ValidGameID` 규칙, 기본은 `default` 하나다. 룸 capacity는 기존 1~6이다.
- 설정은 `DefaultConfig()`에서 필요한 값을 바꾸는 방식이다. zero Config와 잘못된 값은 명시적 error이며 묵시적 zero-to-default 보정은 없다.
- 모든 공개 signature는 builtin·stdlib·공개 CoopDock 타입만 참조한다. internal alias와 `HandlerFactory`는 공개하지 않는다.
- 기존 game namespace·global resource cap·private admission·movement·Godot v1 schema·HTTP status를 유지한다.
- 개발 작업은 현재 feature branch에서 root 검토 후 commit/push한다. main merge와 tag는 범위에 없다.
- 임시 consumer Go source는 `/private/tmp` 아래 독립 module에 둔다. 실행 cache는 `work/` 또는 임시 경로를 사용한다.

## Review Focus

1. public Config/DefaultConfig의 GameIDs·Origins slice를 New 이후 변경해도 기존 instance와 다른 default 호출의 정책은 바뀌지 않아야 한다 — Task 1 `TestConfigOwnership`.
2. invalid room/transport/admin 설정과 constructor 중간 실패가 worker·collector를 남기면 안 된다 — Task 1 `TestNewValidationBeforeWorkers`, `TestNewRollback`.
3. 이미 취소된 첫 Shutdown, 동시 caller, deadline 뒤 재호출에서도 즉시 admission 차단과 최종 cleanup이 보장되어야 한다 — Task 1 `TestShutdownLifecycle`.
4. host mux 정규화가 mount 내부 admin-prefix 인증/no-redirect 경계를 우회하면 안 된다 — Task 1 `TestHandlerAdminRouting`, Task 2 `TestExternalMountRouting`.
5. 같은 host에 두 instance를 만들고 하나만 종료하면 다른 instance의 WS와 host 고유 route가 살아 있어야 한다 — Task 1 `TestIndependentServers`, Task 2 `TestExternalConsumerLifecycle`.

---

## 실행 경계

사용자가 이미 실행과 작업별 commit/push를 승인했다. root가 설계 요지를 알린 뒤 다음 두 task를 순서대로 위임한다. 모든 worker는 지정 파일만 수정하고 다른 작업을 되돌리지 않으며 stage/commit/push는 하지 않는다. root는 task별 fresh review 후 commit/push하고 마지막 통합 Architect 검토를 받는다. 이 설계·계획은 별도 preflight commit으로 기록할 수 있다.

Task 1이 production Go 변경과 전체 Go 검증을 소유한다. Task 2는 consumer 예제·독립 소비 검증·Godot 호환·기술문서만 소유한다. 두 task의 제품 파일을 동시 수정하지 않는다. Task 2에서 Task 1 결함을 발견하면 root에게 돌려 focused fix/review와 영향 검증을 먼저 수행한다.

### Task 1: 공개 core·module·CLI·수명 계약

**Files:**
- Create: `config.go`, `coopdock.go`, `doc.go`, `config_test.go`, `coopdock_test.go`, `lifecycle_test.go`.
- Modify: `go.mod`, `cmd/server/main.go`, `cmd/server/main_test.go`, `internal/config/config.go`, `internal/config/config_test.go`.
- Modify(import only): go-public-package-source-map.md §3의 tracked Go 32개 파일. 이 집합의 실제 기능 변경은 위 ownership과 필요한 비공개 lifecycle seam에 한정한다.
- Modify(if required): `internal/transport/server.go`, `internal/observability/collector.go` 및 해당 tests — 동기 closing fence나 startup rollback 완료를 기존 API만으로 보장할 수 없을 때만 최소 변경한다.
- Evidence: `work/public-package/task-1/` (로그·source manifest·report; 임시 `.go` 파일 금지).

**Interfaces:**
- Consumes: 기존 `room.NewManager(room.Config) (*room.Manager,error)`, `transport.New(*room.Manager,transport.Config) (*transport.Server,error)`, `(*transport.Server).Close(context.Context) error`, collector/admin 조립. 모두 public API 내부에서만 참조한다.
- Produces: `Config{Room RoomConfig; Transport TransportConfig; Admin AdminConfig}`, 설계 표의 scalar 필드, `DefaultConfig() Config`, `(Config).Validate() error`, `New(Config) (*Server,error)`, `(*Server).Handler() http.Handler`, `(*Server).Shutdown(context.Context) error`.
- `Config.Room.GameIDs`, `Config.Transport.Origins`, `Config.Admin.ViewerToken`이 이후 예제에서 사용할 정확한 경로다. `Server`는 listener를 제공하지 않는다.

- [ ] **Step 1: 설정과 공개 소비 경계의 RED를 작성한다.** `config_test.go`의 `TestConfigValidation`은 설계 표의 최소/최대·초과값·교차 제약·zero Config·invalid token을 검사한다. `TestConfigOwnership`은 독립 DefaultConfig와 New 후 GameIDs/Origins 원본 변경 무효를 확인한다. `coopdock_test.go`는 `package coopdock_test`에서 공개 import와 stdlib만으로 설정/handler를 구성한다. 악의적 `GAME_IDS`, `WS_*`, `ADMIN_VIEWER_TOKEN` env가 public DefaultConfig/New 결과에 영향을 주지 않는 assertion을 포함한다.
- [ ] **Step 2: 위 RED를 실행하고 실패 원인을 기록한다.** `go test . -run 'TestConfig|TestPublic' -count=1`. 최초 public API 부재 compile 실패는 scaffold RED로 기록하고, API scaffold 후 실제 validation/ownership/no-env assertion의 실패도 확인한다. dependency download 실패는 RED가 아니다.
- [ ] **Step 3: module과 public 설정을 구현한다.** `go.mod`와 tracked import만 migration하고 역사 evidence는 치환하지 않는다. `config.go`에서 설계의 정확한 타입/기본값/범위를 구현하고 전체 validation을 worker 생성 전에 수행한다. 내부로 변환할 때 slice를 복사하고 내부 movement/crypto 고정값을 유지한다. CLI env loader는 public Config를 구성하는 adapter가 되며 `HTTP_ADDR`는 자체 보관한다. root에서 `internal/config`를 import하지 않는다.
- [ ] **Step 4: 생성·종료·라우팅 RED를 작성한다.** 다음 assertion을 `coopdock_test.go`와 비공개 seam을 사용하는 `lifecycle_test.go`에 둔다. 필요시 stub 상태에서 각각의 동작 실패를 먼저 확인한다.

| 테스트 | 필수 assertion |
|---|---|
| TestNewValidationBeforeWorkers | invalid room/transport/admin별 New error, constructor hook으로 manager/collector 시작 0 확인. 전역 NumGoroutine 정확 개수 비교 금지 |
| TestNewRollback | manager 뒤 failure, transport 뒤 failure, admin startup failure 시 시작된 서비스 close·worker 완료 관찰. collector cancellation 후 goroutine 종료도 확인 |
| TestHandlerAdminRouting | disabled404; enabled unauth401/auth정상200; `/admin/../healthz`, double slash, dot path는 auth 경계/no-store 유지·Location 없음; `/administrator`404; 최초 cache 유효 |
| TestShutdownLifecycle | canceled first call이 반환한 즉시 valid WS503; pending accept-close race에서 leak0; 두 caller 서로 다른 deadline 독립; deadline 이후 cleanup 완료·fresh context 재호출nil; 완료후 canceled context 재호출nil |
| TestShutdownDiagnostics | 종료후 health200/ready503/valid ws503; invalid query400/unknown404 유지; enabled admin STOPPING/readyfalse, disabled404; collector worker 완료 |
| TestIndependentServers | 같은 host의 두 instance가 각각 room을 만들고 한쪽 종료후 다른 WS room/list/move 및 host `/own-health`200; 서로의 cap·allowlist·room/session 미공유 |
| TestPublicGameContract | default bareWS; multi missing400; caller override 거절; A/B create/list/join 분리·private admission·start/move·shutdown. 같은번호 deterministic 회귀는 기존 internal test 재사용 |

- [ ] **Step 5: `coopdock.go`의 생성·handler·Shutdown을 구현한다.** 설계의 초기 5초 collector 대기, 동기 closing fence, 완료 대기 밖 `sync.Once`, caller별 bounded wait와 background cleanup을 지킨다. raw admin dispatch는 기존 구현을 옮긴다. 제품 타입에 test hook을 공개하지 않는다.
- [ ] **Step 6: CLI를 공개 API consumer로 전환한다.** `cmd/server`가 New와 Handler를 사용하고 자체 HTTP server/signal을 유지한다. 앞 단계 종료 오류가 있어도 CoopDock/HTTP 두 cleanup을 모두 요청하며 오류를 결합한다. 기존 env bounds/defaults와 process 종료 테스트를 유지하고 core 조립 테스트는 root로 옮긴다.
- [ ] **Step 7: focused GREEN을 확인한다.** `go test . ./internal/config ./cmd/server -count=1`, 관련 기존 transport shutdown/game-scope와 collector startup/deadline tests를 실행한다. `go doc github.com/sangjinsu/coopdock`과 exported signature를 확인하여 internal 타입·env/process 의존 누출이 없는지 검토한다.
- [ ] **Step 8: production Go 최종 상태에서 전체 검증을 각 한 번 실행한다.** 사전에 cache 디렉터리를 만들고 다음 env를 적용한다: `GOCACHE=$PWD/work/go-build GOMODCACHE=$PWD/work/go-mod GOTMPDIR=$PWD/work/tmp GOTOOLCHAIN=local`. `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`, `git diff --check`가 모두 exit0이어야 한다. Go source/module/dependency manifest와 도구 버전·command·exit·로그를 report에 기록한다. 이전 module hash는 폐기하고 새 hash로 검증 연결을 시작한다.
- [ ] **Step 9: root가 review·commit·push한다.** reviewer는 공개 signature/생성 실패 cleanup/동시 shutdown/host 수명/CLI 호환/환경 설정 상한을 확인한다. 승인 후 `feat: expose embeddable CoopDock Go package` 의미의 commit과 현재 feature branch push를 수행하고 SHA를 Task 2에 전달한다. worker는 index를 만지지 않는다.

### Task 2: 외부 consumer·Godot 호환·사용 문서

**Files:**
- Create: `examples/go-embed/main.go`, `examples/go-embed/main_test.go`, `docs/go-embedding.md`.
- Modify: `README.md` (짧은 Go embedding CTA와 문서 링크), `docs/getting-started.md`, `docs/server-guide.md` (현재 module/CLI/종료 계약만).
- Evidence: `work/public-package/task-2/` (remote import·external integration·Godot 로그와 source manifest·report).
- Temporary: `/private/tmp/coopdock-consumer-*/go.mod`, 공개 API만 사용하는 consumer source/tests와 owned process runner. 저장소에 nested go.mod나 임시 source를 추가하지 않는다.
- Godot production 파일은 수정하지 않는다. 실제 발견한 호환 결함이 있으면 root에게 근거와 범위를 먼저 전달한다.

**Interfaces:**
- Consumes: Task 1의 공개 `DefaultConfig/New/Handler/Shutdown`, `Room.GameIDs`, `Admin.ViewerToken`, pushed SHA. 공개 signature를 재정의하지 않는다.
- Produces: `go run ./examples/go-embed`로 동작하는 embedding 예제, host `/own-health`, mount `/coopdock/`의 CoopDock endpoints. 실제 외부 module import·real WS·Godot 보고서와 정확한 설치 명령을 제공한다.

- [ ] **Step 1: 외부 소비와 mount RED를 작성한다.** local `replace`가 있는 독립 temporary module에서 root 공개 API만 import한다. `TestExternalMountRouting`은 `/one/admin/../healthz` 등의 path가 redirect되지 않고 정상 인증/404 경계를 지키는지 확인한다. `TestExternalConsumerLifecycle`은 두 instance/두 game, custom health, real WS create/list/join/start/move/leave와 A 종료 후 host/B 생존을 검사한다. `.go`를 repo `work/` 아래 만들지 않는다. 실제 동작 테스트가 없는 단순 compile만으로 GREEN으로 삼지 않는다.
- [ ] **Step 2: 예제와 mount 구성을 구현한다.** `examples/go-embed/main.go`는 DefaultConfig에서 설정하고 자체 HTTP server/signal/timeout을 소유한다. host 기존 mux의 `/own-health`와 coexist하며, 최상위 wrapper가 `/coopdock/` prefix 전체를 `http.StripPrefix("/coopdock", service.Handler())`에 먼저 전달한다. admin 기본 off이며 활성 시 최상위 `/admin` 및 `/admin/`도 동일 instance Handler로 직접 dispatch해 기존 dashboard 절대 asset/API 경로를 살린다. public API는 추가하지 않는다. `main_test.go`는 같은 routing helper의 normal mount·admin no-redirect뿐 아니라 enabled root alias의 HTML/CSS/JS/API 요청도 검증한다. 두 instance 수명 테스트는 admin off로 구성한다.
- [ ] **Step 3: 외부 consumer GREEN과 예제 검사를 실행한다.** temporary module의 `go test ./... -count=1`과 저장소의 `go test ./examples/go-embed -count=1`, `go build ./examples/go-embed`, `git diff --check`를 실행한다. 예제 source가 Task 1 full suite 뒤 추가되므로 이 focused 검증을 manifest chain에 추가한다. core source가 그대로면 full/race/vet/build를 다시 실행하지 않는다.
- [ ] **Step 4: pushed code를 실제 remote module로 가져온다.** replace와 go.work 없이 새 temporary module, `GOWORK=off`, 별도 cache에서 `go get github.com/sangjinsu/coopdock@<Task1-pushed-SHA>`를 실행한다. `@codex/godot-client-example` query도 시도하되, 실제 거절(`invalid version: disallowed version string`)을 기록하고 설치 문서는 성공한 pin/pseudo-version으로 정정한다. module `Origin/Version/Sum`, 사용 SHA, no-replace 상태를 기록하고 동일 external integration을 remote dependency에 대해 실행한다. 네트워크 거절은 local replace 성공과 분리해 미검증으로 보고하며 remote 성공을 꾸미지 않는다.
- [ ] **Step 5: 새 embedding binary에 실제 Godot client를 연결한다.** temporary module의 public API host를 새로 build하고 임시 port/PID를 소유하는 wrapper로 실행한다. default 단독 서버에서 `Godot --headless --path examples/godot --script res://tests/run_integration.gd -- --url ws://127.0.0.1:<owned-port>/coopdock/ws`를 실행한다. heartbeat는 `Config.Transport.ReadTimeout = 3*time.Second`로 설정한 별도 소유 embedding host에 `Godot --headless --path examples/godot --script res://tests/run_integration.gd -- --url ws://127.0.0.1:<owned-port>/coopdock/ws --heartbeat-only`를 직접 실행해 검사한다. 별도 `default,game-a,game-b` 설정에서는 `Godot --headless --path examples/godot --script res://tests/run_game_scope_integration.gd -- --url=ws://127.0.0.1:<owned-port>/coopdock/ws --game-a=game-a --game-b=game-b`를 실행한다. Godot 실행 파일 기본은 `/Applications/Godot.app/Contents/MacOS/Godot`이다. 기존 run_checks.sh는 cmd/server를 직접 띄우고 namespace는 실행하지 않으므로 그대로 실행한 결과로 embedding+namespace를 주장하지 않는다. 고정18080/옛 binary runner를 재사용하지 않는다. exit0·assertion failures0·engine errors0·소유 process cleanup을 기록한다.
- [ ] **Step 6: 사용 문서를 작성한다.** `docs/go-embedding.md`에 pinned SHA/pseudo-version 설치와 Git branch checkout, 완전 Config, no-env, mount prefix, host route 공존, 게임별 allowlist·global cap, admin opt-in과 root `/admin` alias 필요성·별도 origin 조건, 두 종류 Shutdown·deadline 후 cleanup, 공통 movement 한계를 설명한다. bare go get/@latest가 현재 branch를 가져온다고 쓰지 않는다. README는 짧은 CTA만 추가하고 상세 기술 설명은 새 문서로 연결한다.
- [ ] **Step 7: 증거 chain과 fresh review를 제출한다.** Task 1 core hash 동일 여부, 새 예제 focused tests, independent remote module, actual default/multi-game Godot 결과를 묶는다. root가 Architect의 전체 diff/계약/검증 경계 승인을 받고 `docs: add Go embedding example and integration guide` 의미의 task commit/push를 수행한다. remote branch SHA 일치까지 확인한다. main/tag/deployment/native 시각 QA 결과로 확대하지 않는다.

## 자체 spec/plan 교차 검토

- 설계의 공개 타입·필드·signature는 Task 1 Interfaces와 일치하고 Task 2는 같은 이름을 소비한다.
- 설정·수명·CLI·wire·admin·external consumer·배포 증거마다 소유 task와 검증 step이 있다.
- Review Focus 5개는 모두 위 테스트 이름과 assertion에 연결했다.
- 전체 Go 검증은 최종 production 변경 뒤 한 번, 예제 추가는 focused 검증으로 연결한다. core 변경이 다시 있으면 영향 검증과 source chain을 갱신한다.
- 이 계획은 API/행위/파일/검증을 지정하며 제품 함수 본문을 미리 재작성하지 않는다.
