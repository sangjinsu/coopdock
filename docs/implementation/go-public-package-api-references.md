# 공개 Go API의 공식 문서 확인

확인일: 2026-10-18. 확인에 사용한 배포본은 `go1.27.2 darwin/arm64`다. `GOROOT`는 각 환경에서 `go env GOROOT`로 찾는다. 아래는 설치된 Go 배포본의 공식 도움말·표준 library 문서를 직접 읽은 근거다. 이 기록은 문서 검증이며 실행 검증 결과가 아니다.

| 주제 | 공식 원문 위치(GOROOT 기준) | 확인 및 설계 반영 |
|---|---|---|
| internal package | `src/cmd/go/alldocs.go:2943-2969` | internal 위의 import prefix를 공유하는 코드만 내부 package를 import할 수 있다. 외부 module은 공개 root package를 import하며 root가 internal 구현을 조립한다. |
| module와 import path | `src/cmd/go/alldocs.go:2973-2997` | package import는 module path와 package 상대 경로로 구성된다. 첫 요소에 dot이 없는 경로는 표준 library용이다. module을 실제 GitHub 경로로 옮기고 저장소 import 문자열을 함께 갱신한다. |
| go get | `src/cmd/go/alldocs.go:690-730` | go get은 원하는 version의 dependency를 module에 추가하고 source를 cache에 받는다. public import의 증거는 실제 외부 module에서 build/run까지 포함해야 한다. |
| revision query | `src/cmd/go/internal/modload/query.go:36-75,139-161` | version query는 revision을 받을 수 있고 branch/non-version tag/commit ID를 처리한다. 문서의 branch 또는 pushed SHA를 실제 go get으로 확인하고 resolved pseudo-version을 기록한다. |
| HTTP Handler | `src/net/http/server.go:65-90` | ServeHTTP는 요청 응답을 처리하는 interface다. Handler 반환 API는 host의 listener나 process signal 소유를 요구하지 않는다. handler와 server 수명을 구분한다. |
| HTTP shutdown | `src/net/http/server.go:3252-3276` | http.Server.Shutdown은 listener/idle·active HTTP를 처리하지만 hijacked WebSocket을 닫거나 기다리지 않는다. CoopDock의 transport close를 따로 실행하고 완료를 기다린다. |
| HTTP close | `src/net/http/server.go:3213-3220` | http.Server.Close 역시 hijacked connection을 알지 못한다. HTTP Close를 WebSocket cleanup 대신 사용하지 않는다. |
| shutdown callback | `src/net/http/server.go:3313-3322` | RegisterOnShutdown callback은 protocol 종료를 시작하며 완료를 기다리지 않아야 한다. callback 등록만으로 CoopDock shutdown을 기다렸다고 표현하지 않는다. 예제는 명시적인 종료 호출·대기를 사용한다. |
| context | `src/context/context.go:34-54` | context는 첫 인자로 전달하고 nil 대신 실제 context를 전달한다. context는 여러 goroutine에서 안전하다. Shutdown(ctx)의 동시 호출·취소 동작을 공개 계약에 명시한다. |
| ServeMux 정규화 | `src/net/http/server.go:2724-2770` | ServeMux는 요청 경로를 clean한 뒤 redirect할 수 있다. 외부 host mux 이전 admin raw-prefix dispatch가 필요하다. |

현재 소스 연결점:

- `internal/transport/server.go`: `Handler`, admission fence, copied Origins, `Close(ctx)`의 peer stop/manager close/wait-group/background cleanup.
- `internal/room/manager.go`: allowlist 복제, constructor에서 hash worker 시작, `Close(ctx)`의 admission fence/session cleanup.
- `internal/observability/collector.go`: startup context에 따른 초기 cache 대기, 별도 worker context, `MarkStopping`, `Close(ctx)`.
- `cmd/server/main.go`: env/signal/listener 소유, admin raw-prefix dispatch, service shutdown 조립.
- `internal/config/config.go`: 공개 설정에서 유지할 scalar 범위·env parsing 계약.

환경변수와 process signal을 분리하는 결정, 공개 Config의 범위, default 기반 설정, timeout 이후 cleanup 지속은 위 공식 동작과 현재 CoopDock 수명 구현을 조합한 설계 결정이다. Go 자체가 CoopDock의 종료 계약을 제공한다는 뜻은 아니다.

외부 확인용 공식 문서 주소: [cmd/go](https://pkg.go.dev/cmd/go), [net/http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown), [context](https://pkg.go.dev/context), [Go module reference](https://go.dev/ref/mod). 이번 확인은 이 링크를 웹에서 새로 조회한 것이 아니라 위 설치 원문을 읽었다.
