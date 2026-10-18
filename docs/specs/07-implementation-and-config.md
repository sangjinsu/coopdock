# 07. 구현 순서와 설정 — 무엇부터 만들 것인가

[전체 목차](README.md) · [상세 설계 원본](../go-room-server-design.md)

## 이 문서의 목적

**서버의 역할을 나누고 개발 순서와 조정할 설정을 정한다.**
예를 들어 먼저 “6명이 같은 룸에 들어가고 퇴장한다”를 확인한 다음 이동 규칙과 운영 화면을 붙인다.
아래 폴더·파일은 목표 구조이며 현재 서버 구현의 파일 구성과 다를 수 있다. 실제 구현 범위·구성과 실행 방법·검증은 [서버 가이드](../server-guide.md)를 참조한다. Basic viewer 인증·status/rooms API·embed 대시보드 자산은 현재 구현이다. command/audit/lifecycle의 조작 모듈은 후속 목표다.

## 확정 범위와 초기 선택

| 구분 | 내용 |
|---|---|
| 확정 요구 | Go 우선, 룸당 1~6명, 실시간 이동·협동, Godot 연결, 운영 대시보드 |
| 확정 요구 | 번호 직접 입장, private는 생성자가 지정한 비밀번호 확인 |
| 초기 제안 | 단일 프로세스와 메모리 저장, `net/http` + `coder/websocket`, guest 세션 |
| 후속 범위 | 게임별 규칙·local prediction, operator 계정·로그인 제품 선택, 규모에 따른 확장 |

단일 프로세스는 하나의 서버 실행 프로그램에서 룸과 운영 기능을 함께 처리한다는 뜻이다.
메모리 저장 제안에서는 서버 재시작 시 세션·룸·게임 상태가 사라진다. 감사 기록(audit)의 별도 지속 보관은 후속 설계다.
전용 SDK(클라이언트 연동 개발 도구 묶음)는 필수가 아니다. Godot [desktop 예제](../../examples/godot/README.md)는 `WebSocketPeer`와 공개 JSON 계약으로 연결한다.
guest는 로그인 계정 없이 새 연결에 부여하는 임시 세션이다.
Go reference 3D 이동과 실시간 서버 동기화는 구현했으며 실행·검증은 [서버 가이드](../server-guide.md)를 참조한다. Godot 예제는 3D와 XZ 2D(32px/unit), snapshot 보간을 제공한다. 게임별 지형·충돌·협동 목표·승패는 후속 작업이다.

## 목표 폴더 구조와 현재 조회 모듈

아래 `command.go`, audit/lifecycle 등은 후속 목표다. 현재 조회 source는 `internal/admin/http.go`, `auth.go`, `web.go`, `web/index.html`, `web/dashboard.js`, `web/dashboard.css`이며 observability collector/runtime와 room atomic summary를 사용한다.

```text
cmd/server/main.go
internal/
  transport/
  protocol/
  session/
  room/
    password.go
  game/handler.go
  admin/
    http.go
    auth.go
    command.go
    web.go
    web/
      index.html
      dashboard.js
      dashboard.css
  observability/
  audit/
  lifecycle/
  config/
```

actor는 변경 가능한 룸 상태를 전담하는 실행 작업이고 GameHandler는 게임 규칙을 담당하는 코드다.

| 담당 영역 | 책임 |
|---|---|
| transport / protocol | 통신 연결, 메시지 형식·크기 검사 |
| session | 연결별 읽기·쓰기, 룸 입장 예약, 송신 관리 |
| room | 내부 ID·게임별 번호 registry와 룸별 actor; actor만 멤버·게임 상태 변경 |
| room/password.go | actor 밖의 bounded worker에서 Argon2id 생성·검증, 비밀정보를 공개 상태와 분리 |
| game | GameHandler로 이동·충돌·협동·게임 종료 규칙 계산 |
| admin | 현재 Basic viewer 인증과 cache-only 조회·embed 화면. 명령 접수·결과 조회는 후속 |
| observability | 운영 요약 복사본과 지표 집계 |
| audit / lifecycle | 감사 기록 디스크 쓰기 / drain과 종료 조율 |
| config | 설정 읽기와 범위 검사 |

연결 읽기·쓰기는 각각 한 작업으로 제한한다.
룸 목록 잠금은 짧게 잡고 네트워크·감사 기록 IO·비밀번호 hash 계산은 actor 밖에서 처리한다.
번호는 [crypto/rand](https://pkg.go.dev/crypto/rand#Int)로 후보를 만든 뒤 registry 잠금 안에서 충돌 검사·예약을 함께 한다. 재시도 횟수는 제한한다.
비밀번호 생성·검증이 끝난 뒤 세션 예약·대상 내부 ID·정원·상태·admission gate를 다시 확인한다.
[argon2.IDKey](https://pkg.go.dev/golang.org/x/crypto/argon2#IDKey)는 context를 받지 않는다. 취소돼도 수행 중 계산이 끝날 때까지 worker 자원을 쓸 수 있다.
`admin/web.go`가 같은 패키지 하위의 `web/`을 [embed](https://pkg.go.dev/embed)하도록 배치한다.
HTML·CSS·작은 JS를 Go 실행 파일에 포함하며 초기 구성에 Node 빌드를 필수로 두지 않는다.

## 처음 정할 값과 실측으로 정할 값

tick은 게임 계산 한 단계이고 snapshot은 전체 상태 전송 메시지다.
Hz는 초당 횟수다. TTL은 기록 보관 시간, stale은 오래된 요약 표시, rotation은 로그 파일을 나누는 정책이다.
아래 수치는 구현·측정의 시작점이며 처리 성능을 보장하는 값이 아니다.

| 설정 | 현재 기준 | 판단 기준 |
|---|---|---|
| 게임 등록 | GAME_IDS 기본 default, 1~32개 ID allowlist | 연결 때 game_id 고정, 공개 namespace이며 인증이 아님; 자원 한도·공통 handler 공유 |
| 룸 정원 | 허용 1~6명, 기본 6명 제안 | 범위 검사; 생성자가 지정한 정원 적용 |
| 번호 / 공개 여부 | 숫자 6자리 문자열 / 기본 public 제안 | 선행 0·활성 중복 방지·충돌 재시도 상한 |
| private 비밀번호 | 4~64 Unicode 문자(code point) 제안 | 대소문자 구분, 임의 trim·정규화 없음; 원문 저장·응답 없음 |
| hash 비용·동시성 | Argon2id 파라미터·worker 수·대기 한계는 실측 후 확정 | CPU·메모리·반복 시도 중 tick 영향, worker 포화 시 SERVER_BUSY |
| 실패 시도 제한 | IP/session/room별 횟수·TTL 제안, 구체값 미측정 | 신뢰 proxy의 IP 정책, 재접속 우회·정상 참가자 영향; RATE_LIMITED 반환 |
| 계산 / 전송 빈도 | 기본 ROOM_TICK_HZ=30, ROOM_SNAPSHOT_HZ=15 | tick 1~120, snapshot 1~TickHz; 실제 완료 빈도·도착 지연 미측정 |
| 게임 입력 queue / tick budget | ROOM_INPUT_QUEUE=256, ROOM_INPUTS_PER_TICK=64 | queue 1~4096, budget 1~InputQueue; inbox+pending 합계 credit |
| 운영 조회 간격 | batch 완료 후 5초, batch timeout 5초; actor/collector/runtime 각각 1초 | 자동 갱신 해제는 다음 자동 조회 중단·진행 중 조회 완료, hidden/pagehide는 진행 중 요청도 취소, persisted 복귀 복구; 부하 영향 미측정 |
| 운영 인증 | ADMIN_VIEWER_TOKEN 미설정/빈값이면 비활성404 | 설정 시32~256 bytes·ASCII 0x21..0x7e, username viewer |
| 총 룸·접속 한계 | 구현 후 실측으로 결정 | 실행 장비와 실제 GameHandler 기준 |
| 입력·중요 송신·운영 큐 | 유한 크기; 숫자는 실측 후 결정 | 포화 시 오류·거절·느린 연결 정리 검증 |
| membership(현재 룸 소속) 정리 큐 | 룸 정원 이상, 전용 용량 | 운영 큐가 그 슬롯을 차용하지 않음 |
| 메시지 크기·timeout(대기 제한 시간) | 구현 단계에서 상한 설정 | 정상 게임 동작과 느린 연결 시험 |
| 요약 stale·목록 제한 | 고정 age_ms>=5000, limit 기본100·1~100, offset 0~4096 | 필터 뒤 pagination, row/envelope 게시 시각 구분 |
| command 저장·TTL·로그 rotation | 초기 보관 정책 제안 | 재시도 기간·audit 용량·운영 규모 |
| drain(새 수용 차단 후 종료)·연결/actor 대기·audit flush(남은 기록 저장) | 종료 시간 상한 설정 | 진행 게임 유지와 종료 시험 |

tick은 고정 dt 한 단계이며 놓친 ticker 신호를 catch-up하지 않는다. 부하로 simulation 시간이 wall time보다 늦어질 수 있다. 숫자·기간 환경변수의 빈 문자열·0·음수·범위 초과와 snapshot>tick·budget>queue, 주소·Origin 설정의 빈값·잘못된 형식은 시작 시 거절하며, `ADMIN_VIEWER_TOKEN`의 빈 문자열은 정상적인 운영 비활성 설정이다.

CPU 사용량이나 동시 룸 수의 실측 결과는 없다. 임의의 수용 규모를 기본 보장으로 삼지 않는다.
[OWASP 저장 지침](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)의 Argon2id·고유 salt를 참고한다. hash 파라미터와 worker 수가 게임 tick에 미치는 영향은 별도로 측정한다.
운영 환경에서는 게임 WSS·admin HTTPS와 비밀번호 없는 로그·audit를 적용한다.

## 단계별 구현 순서

| 순서 | 만들 것 | 다음 단계로 가기 전에 볼 결과 |
|---|---|---|
| 1 | 연결·guest·프로토콜·번호·public/private 룸·hash worker | 번호/비밀번호 입장, 정원·한 세션 한 룸, host 승계·빈 룸 삭제 |
| 2 | 이동·협동 GameHandler | 서버가 입력을 적용하고 위치·협동 결과를 판정 |
| 3 | 실시간 동기화 | 시험용 6클라이언트로 입력 순서·상태 일치·전송 순서 확인 |
| 4 | 현재 읽기 전용 대시보드 | Basic viewer·불변 cache·요약 신선도·실제 6 WS 병행 확인; 게임 부하 영향은 미측정 |
| 5 | 운영 조작 + 지속 audit | 접수와 실제 결과 구분, 중복 재시도·기록 실패 처리 |
| 6 | drain·종료·운영 부하 | 신규 입장 경합, 연결·actor 정리, audit flush 상한 확인 |

연결만 중계하는 서버로는 이동·충돌·승패가 구현되지 않는다.
전체 설계에는 운영 조작까지 포함하되, 화면 조회를 먼저 만들고 조작과 audit를 함께 추가한다.
Godot 예제의 표시·보간은 별도 client unit·실제 wire·native 렌더로 확인한다. local prediction은 후속이며 서버 시험 결과로 화면의 부드러움까지 확정하지 않는다.

## 구현 후 확인할 것

- 설정값이 실제 실행에 적용되고 룸 정원 1~6 범위를 벗어나면 거절되는지 확인한다.
- actor 외부에서 룸 상태를 변경하지 않고 정리·운영·송신의 대기열이 분리되는지 확인한다.
- 번호 충돌·private 입장 검증·취소·포화와 비밀정보 제외를 검증하고 hash 비용의 tick 영향을 측정한다.
- 각 단계의 사용자 결과를 [08. 검증 계획](08-validation.md)에 따라 확인한다.
- 서버·시험용 클라이언트·실제 Godot 클라이언트의 검증 결과를 각각 구분해 기록한다.
