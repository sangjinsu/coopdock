# 기존 Go 서버에 CoopDock 넣기

CoopDock을 `github.com/sangjinsu/coopdock`으로 import해 기존 HTTP 서버 안에서 실행할 수 있습니다. host application이 listener, HTTP timeout, TLS, signal과 process 종료를 소유하고, CoopDock은 자신의 룸·session·WebSocket·선택적 운영 collector를 소유합니다.

[문서 길잡이](README.md) · [서버 사용·운영 가이드](server-guide.md) · [실행 가능한 예제](../examples/go-embed/main.go)

## 설치와 실행

Go **1.26 이상**이 필요합니다. 공개 package는 현재 feature branch에 있으므로 외부 application의 Go module 안에서 검증한 commit을 고정합니다.

```bash
go get github.com/sangjinsu/coopdock@2b3c6f8d7e35e9f83a1b7173cde788ac52ca442e
```

같은 revision의 resolved pseudo-version도 사용할 수 있습니다.

```bash
go get github.com/sangjinsu/coopdock@v0.0.0-20261018115756-2b3c6f8d7e35
```

위 SHA는 공개 core가 들어 있는 commit입니다. `main`이나 `@latest`가 이 API를 제공한다고 가정하지 마세요. 내려받은 revision은 `go list -m -json github.com/sangjinsu/coopdock`과 `go mod download -json github.com/sangjinsu/coopdock`에서 확인할 수 있습니다. local `replace`는 개발 중에만 사용하고 배포할 application의 module에서는 실제 revision으로 고정하세요.

`@codex/godot-client-example` query는 Go가 `invalid version: disallowed version string`으로 거절하므로 설치 명령으로 사용하지 않습니다. 예제를 포함한 feature branch checkout이 필요하면 Git에서 branch를 선택합니다.

```bash
git clone --branch codex/godot-client-example https://github.com/sangjinsu/coopdock.git
cd coopdock
```

이 문서와 예제가 있는 checkout에서는 다음을 실행합니다.

```bash
go run ./examples/go-embed
curl -i http://127.0.0.1:8080/own-health
curl -i http://127.0.0.1:8080/coopdock/readyz
```

예제는 `127.0.0.1:8080`에서 host의 `/own-health`(204)와 CoopDock의 `/coopdock/healthz`, `/coopdock/readyz`, `/coopdock/ws`를 함께 제공합니다. Godot의 서버 URL은 `ws://127.0.0.1:8080/coopdock/ws`입니다. `cmd/server`와 예제는 같은 기본 port를 쓰므로 하나씩 실행하세요.

## 설정과 공개 API

```go
cfg := coopdock.DefaultConfig()
cfg.Room.GameIDs = []string{"default", "game-a", "game-b"}
cfg.Room.MaxRooms = 128
cfg.Transport.Origins = []string{"game.example.com"}
service, err := coopdock.New(cfg)
if err != nil {
    return err
}
handler := service.Handler()
_ = handler // Pass it to your HTTP host.
```

`Config`는 `Room`, `Transport`, `Admin` 세 구성을 포함합니다. `DefaultConfig()`로 완전한 유효 설정을 얻은 뒤 필요한 필드만 바꿉니다. `Config{}`는 유효하지 않으며 zero 값을 자동으로 default로 바꾸지 않습니다. `cfg.Validate()`로 생성 전에 같은 검증을 실행할 수도 있습니다. 정확한 필드·기본값·상한은 [공개 설정 표](implementation/go-public-package-design.md#구성과-인터페이스)에 있습니다.

라이브러리는 `GAME_IDS`, `WS_*`, `ROOM_*`, `ADMIN_VIEWER_TOKEN` 같은 환경변수를 읽지 않습니다. host가 필요한 설정과 비밀을 읽어 Config로 전달합니다. `New`는 GameIDs와 Origins를 복사하므로 반환 후 caller slice를 바꿔도 실행 중 정책이 바뀌지 않습니다. `New` 실행 중에는 같은 slice를 동시에 수정하지 마세요. `Handler()`는 같은 instance의 안정된 `http.Handler`를 반환하며 요청을 동시에 처리할 수 있습니다.

`GameIDs`는 1~32개의 고유 ID이며 문법은 ASCII `[a-z0-9][a-z0-9_-]{0,63}`입니다. 공백과 중복을 자동 보정하지 않습니다. 기본 allowlist는 `default` 하나입니다. 이 경우만 query 없는 `/ws`를 허용합니다. 여러 게임이나 하나의 named game을 설정하면 `/coopdock/ws?game_id=game-a`처럼 명시합니다. Godot에는 query 없는 base URL과 게임 ID를 각각 입력하세요.

같은 Server의 게임들은 룸 생성·목록·번호 입장을 분리하면서 room/session cap·hash pool·queue·IP 실패 한도를 공유합니다. `game_id`는 공개 namespace이며 인증이나 CPU 격리가 아닙니다. 모든 게임은 현재 같은 reference movement와 schema를 사용합니다. 게임별 rules·지형·충돌·승패 handler를 등록하는 공개 API는 없습니다. 서로 다른 `New`로 만든 Server는 allowlist·룸·자원 한도·종료가 독립입니다.

## prefix mount와 기존 route 공존

host `ServeMux`가 먼저 dot segment나 중복 slash를 정규화하면 admin 인증 전에 redirect할 수 있습니다. mount 전체를 host mux보다 먼저 dispatch합니다.

```go
host := http.NewServeMux()
host.HandleFunc("GET /own-health", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
core := service.Handler()
mounted := http.StripPrefix("/coopdock", core)
handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if strings.HasPrefix(r.URL.Path, "/coopdock/") {
        mounted.ServeHTTP(w, r)
        return
    }
    host.ServeHTTP(w, r)
})
```

이 wrapper를 `http.Server.Handler`에 직접 지정합니다. `/coopdock/admin/../healthz`나 `/coopdock/admin//api/status`도 wrapper에서 CoopDock으로 전달해 인증·404 경계를 유지합니다. 앞단 middleware나 reverse proxy가 경로를 먼저 정규화하지 않는지도 확인하세요. WebSocket upgrade에 필요한 HTTP/1.1 `Hijacker` 지원을 없애는 ResponseWriter wrapper를 사용하지 마세요.

여러 instance는 `/one/`, `/two/`를 각각 먼저 식별하고 해당 instance에 `StripPrefix`로 전달합니다. `/one/ws`와 `/two/ws`는 서로 다른 guest·룸 수명을 사용합니다.

## 운영 화면은 명시적으로 켜기

`cfg.Admin.ViewerToken`은 기본 빈 값으로 admin과 collector를 비활성화합니다. host가 비밀 저장소 등에서 가져온 **32~256 bytes, 각 byte `0x21..0x7e`**의 token을 설정하면 활성화됩니다. Basic username은 `viewer`이며 password가 token입니다. token이나 Config 전체를 로그에 출력하지 마세요. 외부에서는 HTTPS가 필요합니다.

기존 dashboard는 `/admin/assets/*`와 `/admin/api/*` 절대 URL을 사용합니다. prefix mount만으로 dashboard 전체가 동작하지 않습니다. 예제의 `embeddingHandler`는 admin이 활성화되면 다음 root alias도 host mux 이전에 같은 instance로 전달합니다.

```go
if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
    core.ServeHTTP(w, r)
    return
}
```

이때 브라우저는 root `/admin`을 엽니다. HTML·CSS·JS·API 모두 같은 인증을 통과합니다. 기존 host가 이미 `/admin`을 쓰거나 한 origin에 여러 instance dashboard가 필요하면 별도 origin을 사용하세요. 임의 prefix 아래 dashboard URL을 바꾸는 기능은 제공하지 않습니다. 운영 인증·cache 의미는 [운영 가이드](server-guide.md#운영-대시보드-켜기)를 참조하세요.

## core와 HTTP를 각각 종료하기

```go
ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
coreErr := service.Shutdown(ctx)
httpErr := httpServer.Shutdown(ctx)
return errors.Join(coreErr, httpErr)
```

non-nil context를 전달합니다. `http.Server.Shutdown`은 hijacked WebSocket을 닫거나 기다리지 않으므로 CoopDock 종료도 호출하고 결과를 기다려야 합니다. 예제는 SIGINT/SIGTERM 또는 listen 실패 뒤 두 종료를 모두 호출하며 오류가 있으면 nonzero로 종료합니다. core 종료만 호출하면 host listener·`/own-health`·다른 CoopDock instance는 계속 살아 있습니다.

`service.Shutdown`은 최초 호출에서 신규 수용을 동기적으로 닫고 기존 WS·룸·worker·collector 정리를 시작합니다. 이미 취소된 context도 종료를 시작합니다. 모든 정리가 끝나야 nil을 반환합니다. caller deadline이나 `Transport.ShutdownTimeout`으로 먼저 반환하면 `errors.Is`로 context 오류를 확인할 수 있으며 background 정리는 계속됩니다. 실행 중 Argon 계산은 즉시 취소되지 않습니다. 정리 완료가 필요한 장기 host는 새 context로 `Shutdown`을 다시 호출하고 기다리세요. 이 예제의 process는 종료 오류 후 계속 기다리는 supervisor를 구현하지 않으므로 supervisor의 grace를 별도로 설계해야 합니다.

여러 caller가 각각의 context로 동시에 기다릴 수 있고, 완료 후 재호출은 취소된 context여도 nil입니다. 완료한 instance는 재시작하지 않습니다. core만 종료한 뒤에는 `/healthz`200, `/readyz`503, 유효 WS 요청503이 유지되며 잘못된 game query의 400/404 검증은 계속 적용됩니다. admin은 비활성404 또는 인증 후 cached STOPPING 상태입니다. 최종 counter가 반드시 0이라는 보장은 없습니다.

## 확인한 범위

외부 독립 Go module에서 public import와 prefix route, 두 instance·두 game의 실제 WS 생성·목록·private 입장·시작·서버 권위 이동·퇴장, 한 instance 종료 뒤 host와 다른 instance 생존을 검증했습니다. local replace와 별도로 위 pushed SHA를 실제 no-replace module로 내려받아 같은 검사를 실행했습니다. direct revision 조회의 `Origin.Hash`도 같은 full SHA였습니다. slash가 있는 branch query의 실패는 별도로 확인했습니다.

실제 Godot **4.7.2** client는 새 embedding binary의 `/coopdock/ws`에 연결해 default integration, 짧은 read timeout의 heartbeat, `default,game-a,game-b` namespace integration을 확인했습니다. 기존 Godot production 파일은 변경하지 않았습니다. native 화면·OS 수동 입력·목표 규모 load·성능·TLS 배포를 새로 검증한 결과는 아닙니다.
