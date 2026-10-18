# Go 공개 패키지 소스·수명·검증 지도

기준: 2026-10-18, HEAD `edc04b1`, branch `codex/godot-client-example`. 아래 경로와 행 번호는 공개 패키지 추출 전 소스를 기준으로 한다. 최종 API와 구현 순서는 [설계](go-public-package-design.md), [계획](go-public-package-plan.md)을 따른다.

## 1. 현재 조립과 수명

| 소유자/단계 | 생성·동작 | 종료·실패 책임과 근거 |
|---|---|---|
| CLI | env `config.Load` → signal context → `room.NewManager` → `transport.New` → 선택적 collector → handler → `http.Server`/listen goroutine | `cmd/server/main.go:26-75`. address/listener, OS signal, logging, process exit는 외부 application이 소유해야 하므로 façade로 옮기지 않는다. |
| Manager | 설정/게임목록 검증 후 slice 복사, 기본 movement factory 보정, `HashWorkers` goroutine 즉시 시작 | `internal/room/manager.go:32-67`. transport 생성 실패 시 이미 시작한 manager를 반드시 닫아야 한다(`cmd/server/main.go:37-42`). |
| Transport | 설정 검증·Origins slice 복사, mux 등록. New 자체는 worker를 시작하지 않음 | `internal/transport/server.go:61-84`. 이후 Close가 manager까지 소유한다(`:153-180`). façade에서 manager를 따로 중복 종료할 필요 없음. |
| Admin collector | admin token 존재 시 초기 cache까지 기다림. runtime sampler와 room/transport collector 2 goroutine, background context, 각 ticker | `cmd/server/main.go:43-52`, `internal/observability/collector.go:87-127,129-163`. 초기 startup timeout/cancel 때 내부 cancel은 하지만 callback 반환까지 기다리지 않고 nil/error 반환(`:117-124`). |
| Admin handler | config 검증, enabled+nil collector 거절. worker 없음 | `internal/admin/http.go:24-31`. `/admin` 및 `/admin/` prefix를 public ServeMux **앞에서** dispatch(`cmd/server/main.go:77-90`): path canonical redirect가 인증 경계를 우회하지 않게 그대로 옮긴다. |
| WS accept | game query 선택 → mutex에서 closing 검사 및 wg.Add → manager session 등록 → upgrade → mutex에서 closing 재검사 → peer 등록/LiveStatus 증가 → run | `internal/transport/server.go:90-151`. 첫 fence와 wg가 pending upgrade를 추적하고 두 번째 fence가 close 동안 늦게 성공한 upgrade를 즉시 닫는다. |
| WS peer | background context. reader/processor/writer 3 workers; session.Done 또는 p.ctx.Done에서 stop, manager.Disconnect, workers.Wait | `internal/transport/server.go:128-129,207-246`. stopOnce가 cancel/CloseNow 및 active count 감소를 한 번만 수행한다. HTTP request context 종료만으로 이 수명은 종료되지 않는다. |
| Room actor | Create가 admission mutex 안에서 map 등록/wg.Add 후 goroutine 시작 | `internal/room/manager.go:202-232`. run의 defer가 registry 제거, ticker 중지, input credit 정리, membership 제거, done close, wg.Done을 담당(`internal/room/actor.go:339-359`); manager.stop도 관측(`:385-386`). |
| Shutdown | collector.MarkStopping → transport.Close → http.Server.Shutdown → collector.Close; 각 오류 join, 앞 단계 오류 후에도 뒤 단계 호출 | `cmd/server/main.go:92-106`. façade에는 transport/collector 종료가 남고, host HTTP 서버 종료는 application/CLI가 수행한다. 기존 CLI 순서는 유지할 수 있다. |

Transport.Close의 `closeOnce` callback은 mutex 안에서 closing=true/peer snapshot을 만든 뒤 peer stop 후 background cleanup을 시작한다. background가 manager.Close를 완료할 때까지 재시도하고 pending accept/peer handler wg를 기다린 후 closed를 닫는다. caller/config deadline은 대기만 중단한다(`internal/transport/server.go:153-180`). Manager.Close 역시 admission fence/stop close를 동기 실행하고, background에서 session disconnect → actor/hash wg wait → 남은 password queue clear → closed를 수행한다(`internal/room/manager.go:418-451`). 진행 중 Argon 계산 자체는 중단되지 않는다(`internal/room/password.go:30-50`).

## 2. façade 추출 때 필요한 경계

- root `coopdock.go`(또는 동등 파일): New가 manager/transport/collector/admin raw-path dispatch 조립을 소유하고 `Handler() http.Handler`와 `Shutdown(context.Context) error`를 제공. root public 타입의 private field에만 internal 포인터를 둔다. New가 listener를 열거나 OS signal을 등록하지 않는다.
- root `config.go`: public Config/RoomConfig/TransportConfig/AdminConfig는 scalar, `time.Duration`, `[]string` 등 외부에서 구성 가능한 타입만 사용. `internal/config.Config`나 `room.Config` alias 금지. 특히 `room.Config.HandlerFactory func() game.GameHandler`(`internal/room/types.go:55-78`)는 internal/game 타입을 노출하므로 이번 movement HTTP façade의 public signature에 복사하지 않는다. collector Source/operations snapshot 역시 public signature에서 숨긴다(`internal/observability/collector.go:27-35,64`). public slice를 internal 설정으로 변환할 때 복사하고 New 이후 caller mutation이 동작을 바꾸지 않게 한다.
- public config는 env를 읽지 않는다. `internal/config.Load`는 CLI용으로 유지하거나 public Config를 구성하는 adapter로 좁힌다. CLI address는 public handler 설정에서 분리한다. 환경변수·기존 defaults/validation 보존 여부를 별도 regression으로 확인한다(`internal/config/config.go:16-24,55-120`).
- root `doc.go`/embedding example: http.Server의 graceful shutdown이 hijacked WS를 소유하지 않는 현재 계약을 설명. host가 CoopDock Shutdown과 host HTTP Shutdown을 모두 호출해야 한다. host의 다른 endpoint/listener를 CoopDock가 닫아서는 안 된다.
- 실패 cleanup: 가능한 설정 오류는 worker 시작 전에 검증. manager 이후 transport 실패는 manager close, collector 이후 admin 실패는 transport와 collector 모두 close. 현재 CLI rollback branches(`main.go:37-58`)를 façade New로 옮기며 제거하지 않는다. startup collector callback은 내부의 bounded snapshot/LiveStatus만 사용하고 외부 arbitrary callback을 추가하지 않는다.
- Shutdown의 `sync.Once.Do` 안에서 완료 대기하면 다른 caller도 첫 caller의 context에 묶인다. once는 admission close/cleanup 시작만 수행하고 각 caller는 자체 context로 같은 completion을 기다려야 한다. **background goroutine을 띄운 뒤에만 transport.Close를 실행하면**, 이미 취소된 첫 caller가 반환한 직후 handler가 아직 accept 가능한 창이 생긴다. admission fence는 Shutdown 반환 전에 동기적으로 세워야 한다(기존 transport.Close가 이미 이 속성을 가짐).
- 기존 transport.Close는 closed와 ctx.Done이 동시에 ready면 select 결과가 고정되지 않는다(`server.go:175-179`). 공개 wrapper는 설계대로 완료된 Shutdown 재호출에 nil을 우선 반환하며 focused test로 고정한다.
- shutdown 이후 현재 `/readyz`는 503, valid `/ws`는 503이나 `/healthz`는 200이고 admin은 마지막 STOPPING cache를 계속 제공한다(`transport/server.go:70-81,100-104`; `collector.go:224-235`; `admin/http.go:69-90`). 공개 façade는 설계대로 기존 진단 동작을 보존하고 새 session/peer/room/worker 생성을 금지한다. path/query validation은 closing 검사보다 먼저이므로 invalid game query가 400/404여도 신규 admission 증거로 해석하지 않는다.

## 3. Module/import 및 수정 파일군

`go.mod:1`의 `module roomserver`를 `github.com/sangjinsu/coopdock`으로 바꾸면 tracked Go 32개 파일의 import가 함께 바뀐다. `git grep -l roomserver -- '*.go'`로 확인했다. go.sum의 dependency 버전 변경은 필요 없다. 기존 internal 구현을 public으로 통째 이동할 필요 없이 root façade가 저장소 안에서 import 가능하다.

정확한 import 수정 집합:

```text
cmd/roomcheck/{game_scope_helpers_test.go,game_scope_test.go,main.go,main_test.go,movement.go,movement_test.go,namespace.go}
cmd/server/{main.go,main_test.go}
internal/admin/{game_scope_test.go,http.go,http_test.go}
internal/config/config.go
internal/observability/{collector.go,collector_test.go,game_scope_test.go}
internal/protocol/message.go
internal/room/{actor.go,game_scope_test.go,manager.go,operations_test.go,realtime.go,realtime_test.go,types.go}
internal/transport/{game_scope_helpers_test.go,game_scope_test.go,movement_test.go,operations_test.go,outbox.go,outbox_test.go,server.go,server_test.go}
```

기능 변경군은 위 import-only 집합과 구분한다: 신규 root façade/config/doc + 외부 package tests, `cmd/server/main.go`/`main_test.go`, `internal/config/config.go`/관련 config tests(선택한 adapter 구조에 따라), README/current Go guides, embedding example. module 참조가 있는 현행 문서는 `docs/getting-started.md:19`, `docs/server-guide.md:3`; 역사 계획은 `docs/implementation/realtime-movement-plan.md:9`, `room-entry-plan.md:15`에 당시 식별자다. `room-entry-plan.md:293`의 `bin/roomserver`는 실행 파일 이름이므로 module rewrite 대상 아님. historical work evidence를 전역 치환하지 않는다.

tracked module은 root go.mod 하나다. `examples/` 및 Godot runner에 literal `roomserver` import/module 참조는 없다. runner는 `go build ./cmd/server`(`examples/godot/tests/run_checks.sh:63`)하므로 module 이름 자체에 의존하지 않는다. 새 standalone Go embedding example에 별도 go.mod를 두면 root `go test ./...`에서 제외되므로 별도 외부 module 검사 명령을 반드시 추가한다. 임시 디렉터리에 `go.mod(require github.com/sangjinsu/coopdock v0.0.0; replace => checkout)`를 만들고 import/build/test하는 방식이면 원치 않는 nested module을 제품에 추가하지 않고 external-consumer 제약을 입증할 수 있다.

## 4. focused tests 추천 (실행하지 않음)

1. external consumer: 다른 module에서 root만 import, DefaultConfig→New→httptest/host mux 연결. public exported Config/signature에 internal package 참조가 없는지 확인. host `/own` endpoint 공존, CoopDock Shutdown 후 `/own` 여전히 200.
2. root config validation/rollback: invalid room/transport/admin 설정별 New error; enabled admin 초기 cache 읽기 가능, disabled admin 404. 초기 실패 때 worker 시작 전 검증 또는 시작된 서비스 종료를 private deterministic hooks/내부 상태로 입증. `runtime.NumGoroutine` exact-count처럼 불안정한 assertion은 피한다.
3. root lifecycle: 취소된 첫 Shutdown도 즉시 신규 `/ws` 차단; deadline 이후 cleanup 계속/재호출 완료; 여러 context의 동시 Shutdown; 동일 instance Shutdown 여러 번; 종료와 accept 경합에서 socket/session 누수 없음. façade 전체 handler의 shutdown 정책을 고정한다.
4. root routing: 기존 `/admin`, `/admin/../healthz`, doubled slash, unknown/admin-prefixed path 인증·404/redirect 동작 보존. 기존 `cmd/server/main_test.go:73`의 `TestBuildHandlerRouting`을 조립 소유 위치로 이동하고 CLI 테스트는 thin bootstrap/process에 집중.
5. root wire/namespace: default 단독 bare `/ws` 호환, multi-game missing query 거절, A/B create/list/join/movement/leave 격리, shutdown 모두 종료. 내부 deterministic 동일 번호 namespace 테스트는 기존대로 유지.

재사용할 기존 경계 시험: `transport/server_test.go:494 TestShutdownDeadlineContinuesCleanup`, `:525 TestCloseWaitsForAcceptRace`; `transport/operations_test.go:80 TestLiveStatusFailedAccept`, `:124 TestLiveStatusConcurrentStopAndClose`; `transport/game_scope_test.go:134 TestGameScopeActualWebSocketsAndShutdown`, `:224 TestGameScopeAcceptCloseRace`, `:257 TestGameScopeRawQueryLimitAndFailedUpgrade`; `observability/collector_test.go:190 TestCollectorCloseCanceledCaller`, `:206 TestCollectorStaleAndBoundedClose`, `:276 TestCollectorStartupDeadline`, `:328 TestCollectorStoppingOverlaySurvivesCollection`; `cmd/server/main_test.go:133 TestCloseServicesStopsAllAfterEarlierError`, `:221 TestCloseServicesNilCollector`, `:250 TestRunInitialCacheAndShutdown`. facade 생성 실패·외부 host 수명은 이 내부 테스트만으로 입증되지 않으므로 새 public boundary 시험이 필요하다.

## 5. 검증 명령과 runner 경계

Go env는 `GOCACHE=$PWD/work/go-build GOMODCACHE=$PWD/work/go-mod GOTMPDIR=$PWD/work/tmp GOTOOLCHAIN=local`; tmp/cache 디렉터리는 미리 생성. `docs/server-guide.md:253-257`의 명령은 `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`. public 추출은 모든 import와 bootstrap을 바꾸므로 구현 이후 새 전체 4개 검사가 필요하다. 과거 성공을 새 module 검증으로 재사용하지 않는다. 외부 module consumer test와 Godot 검증은 별도다.

기본 Godot 실제 runner: `GODOT_CHECKS_OUTPUT_DIR=work/public-package/godot examples/godot/tests/run_checks.sh`. `GODOT_BIN` 기본 `/Applications/Godot.app/Contents/MacOS/Godot`. import/unit/final-fix 입력회귀/actual integration/짧은 heartbeat를 실행하고 `GAME_IDS=default` 및 ROOM_/WS_/ADMIN_ unset을 고정한다(`run_checks.sh:4,13-21,63-75`). `--actual-only`는 import/unit/final-fix를 생략하고 actual+heartbeat 실행. `--heartbeat-only`는 heartbeat만 실행. `--native`는 두 native 창, `--mixed-native`는 native2+headless4이며 별도 QA다(`:76-108`). runner는 자신이 시작한 process만 finally terminate/wait한다(`:110-117`). 이번 module/HTTP façade 변경에는 native 시각 시험보다 actual WS+namespace 호환 재검증이 우선이다.

namespace unit은 별도 명령: `$GODOT_BIN --headless --path examples/godot --script res://tests/run_game_scope_unit.gd`. namespace actual은 `run_game_scope_integration.gd -- --url=ws://127.0.0.1:<owned-port>/ws --game-a=game-a --game-b=game-b`; 서버는 `GAME_IDS=default,game-a,game-b`로 따로 띄워야 한다(`examples/godot/tests/run_game_scope_integration.gd:5-29`). 기본 run_checks.sh는 이 별도 namespace suite를 호출하지 않는다. 과거 `work/game-scope/godot/run_real.py:5-11`는 고정 18080 port/기존 binary를 사용하므로 그대로 재사용하지 말고 새 산출 경로/새 build/owned 임시 port wrapper로 실행한다.
이 문서와 연결된 설계·계획만으로 구현 입력을 읽을 수 있다. `work/` 경로는 실행 시 생성하는 로컬 산출물 또는 재사용하지 않을 옛 runner를 가리키며 fresh clone의 필수 입력이 아니다.
