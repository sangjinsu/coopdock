# 08. 검증 계획 — 어떤 상황에서 무엇을 확인할 것인가

[전체 목차](README.md) · [상세 설계 원본](../go-room-server-design.md)

## 이 문서의 목적

**코드 구현 후 플레이어와 운영자가 기대한 결과를 얻는지 확인하는 계획이다.**
현재 구현·테스트 통과·성능 측정 결과를 보고하는 문서가 아니다. 구현된 Go reference와 실행 검증은 [서버 가이드](../server-guide.md), 작업별 증거는 [구현 계획](../implementation/realtime-movement-plan.md)과 작업 보고서를 참조한다. 실제 6 WS는 서버 checker 검증이며 Godot QA가 아니다.
룸당 1~6명은 요구 범위지만 전체 동시 룸 수와 서버 수용 인원은 아직 측정하지 않았다.

예를 들어 6명 룸에 마지막 한 자리가 남았을 때 두 사람이 동시에 입장할 수 있다.
성공 기준은 “요청 두 개를 받았다”가 아니라 한 사람만 들어가고 정원을 넘지 않는 것이다.

## 현재 운영 조회 검증과 후속 시험의 구분

현재 조회 MVP는 Basic viewer·미설정404·보호된 embed 자산, 정확한 allowlist JSON·nullable 지표, 전체 필터 뒤 pagination, cache-only HTTP, summary 게시에서 게임 revision/snapshot_seq 불변, 1초 heartbeat, source 지연 시 stale→recovery와 collector 종료를 deterministic 시험으로 확인한다. 아래 조작·audit·drain 항목은 후속 계획이며 현재 실행됐다는 뜻이 아니다.

실제 서버 검증은 current source binary에서 native WebSocket 6개를 유지하며 대시보드의 연결6·membership6·WAITING→PLAYING·private·match와 5초 polling/수동 조회를 관찰한다. 별도로 기존 `roomcheck -movement`를 실제 서버에서 실행해 6 WS 동일 capture·입력 ACK·이동·정지·정리를 확인한다. 임의 fixture 수치를 실제 접속 수나 성능으로 보고하지 않는다. leading zero가 실제 무작위 번호에서 관찰되지 않으면 fixture/기존 계약 시험과 구분해 기록한다.

UI mock fixture의 pagination·timeout·인증 purge·body abort·visibility 주입과 Node VM의 persisted pageshow 복구는 각각 독립 행위 시험이다. CSS zoom은 browser 메뉴 native zoom이 아니며 주입 visibility는 OS background 시험이 아니다. native Basic challenge 입력 UX·실제 OS throttle·메뉴200%zoom·실제 BFCache 적격성은 별도 관찰 없으면 미검증으로 남긴다. UI fixture 성공과 실제 Go HTTP/6 WS 증거를 구별한다.

최종 fresh `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`, `git diff --check` 결과와 browser 근거·cleanup은 `work/dashboard/final-report.md`에 보존한다. 결과 보고서는 root 독립 검토·최종 Architect 판정과 별도다. CPU·RTT·표시지연·실제 게임 부하 영향·Godot QA·배포는 이번 검증에서 성공으로 확대하지 않는다.

## 룸과 연결이 사용자의 기대대로 동작하는가

membership은 세션의 현재 룸 소속 관계, host는 방장, match는 한 번의 게임 실행이다.

| 시험 상황 | 관측할 성공 기준 |
|---|---|
| 공개 룸 번호 입력 | 비밀번호 없이 번호로 입장한다. public 생성에 password를 보내면 INVALID_MESSAGE다. |
| private 생성·번호만 입력·비밀번호 재요청 | 생성자는 자동 입장한다. 참가자는 PASSWORD_REQUIRED 뒤 같은 번호+비밀번호로 입장한다. |
| 잘못된 번호·없는 번호·틀린 비밀번호 | 형식은 INVALID_MESSAGE, 없는 번호는 ROOM_NOT_FOUND, 불일치는 PASSWORD_INVALID로 구분된다. |
| 앞 0 번호·번호 충돌 | `004271`을 문자열로 보존하고 활성·생성 예약 번호가 중복되지 않는다. 충돌 재시도에는 상한이 있다. |
| 번호 재사용 뒤 이전 검증 결과 도착 | 검증 당시 room_id·generation을 대조해 새 룸에 적용하지 않는다. |
| 비밀번호 검증 후 마지막 자리 경합 | 검증 성공을 빈자리 예약으로 취급하지 않으며 한 명만 실제 입장한다. |
| private 생성/검증 중 timeout·disconnect·close·drain | 미게시 룸·번호·세션 예약을 정리하며 늦은 결과로 입장시키지 않는다. 수행 중 hash 즉시 취소를 가정하지 않는다. |
| 반복 비밀번호 실패·worker 포화 | TTL 제한은 RATE_LIMITED, worker 포화는 SERVER_BUSY다. 전체 룸을 영구 잠그지 않고 제한 후 정상 입장이 가능하다. |
| 공개 여부·비밀번호 표시와 기록 | 공개 room.list는 private를 제외하고 운영 목록은 번호·visibility를 포함하되 비밀번호·검증자료는 표시하지 않는다. 응답·snapshot·cache·로그·audit에 password/hash/salt가 없다. |
| host 승계·다음 판·퇴장 후 재입장 | 공개 여부·비밀번호는 유지된다. membership 유지 중 재입력 없고 새 입장은 다시 확인한다. PLAYING은 올바른 비밀번호로도 거절한다. |
| 1명으로 룸 생성·시작 | 제안한 시작 정책에 따라 host가 시작하고 서버가 게임 상태를 만든다. |
| 6명이 같은 룸에 입장 | 여섯 membership이 유지되고 모두 같은 match에 참여한다. |
| 가득 찬 6명 룸에 7번째 입장 | 정원을 넘지 않고 입장 요청에 `ROOM_FULL`을 반환한다. |
| 마지막 한 자리에 동시 입장 | 한 명만 성공하고 멤버 수가 정원을 넘지 않는다. |
| 같은 세션의 두 룸 입장 경합 | 한 룸 membership만 확정된다. |
| host 퇴장 / 마지막 참가자 퇴장 | host는 가장 먼저 입장한 잔여 참가자로 승계되고 빈 룸은 삭제된다. |
| 연결 종료 후 새 guest 연결 | 기존 상태를 자동 복원하지 않으며 PLAYING 룸 입장을 거절한다. |
| 입장 중 disconnect·timeout | 이전 membership이 남지 않고 정리가 끝난 뒤에만 다른 룸 입장이 가능하다. |
| 늦은 입장 결과와 새 예약 경합 | 이전 generation, 즉 예약 세대의 결과가 새 예약을 바꾸지 않는다. |
| 닫힌 룸에 대기 요청·정리 도착 | 요청은 `ROOM_CLOSED`로 끝나고 중복 정리에도 상태가 꼬이지 않는다. |

인원 표시와 실제 소속을 함께 관측한다. 초기 정책은 자동 재접속 복원 없음과 메모리 저장이다.
현재 재시작 후 게임 상태는 사라진다. 후속 persistent audit 보존은 별도로 확인한다.

## 이동·협동 결과와 메시지 순서가 맞는가

FIFO는 먼저 들어온 순서를 유지하는 대기열이고 ACK는 입력 적용을 확인하는 응답이다.
snapshot은 전체 상태 메시지, tick은 계산 단계, match는 한 번의 게임 실행이다.

| 시험 상황 | 관측할 성공 기준 |
|---|---|
| 이동과 일회성 행동을 연속 입력 | 모두 FIFO로 처리하며 승인 입력은 실제 적용 후 ACK한다. |
| mailbox 접수 뒤 계산을 지연 | 접수만 된 입력에 성공 ACK를 먼저 보내지 않는다. |
| 이전 match 입력·역순 입력 도착 | 현재 게임에 적용하지 않으며 실패 사유를 확인할 수 있다. |
| 상태와 입력 확인 번호 수신 | 같은 snapshot의 상태·tick·플레이어별 적용 번호가 같은 시점을 나타낸다. |
| 새 match 시작 | 세션 입력 번호는 유지되고 룸 수명 전체의 snapshot 번호도 역행하지 않는다. |
| join·start·end 직후 전송 | 초기 상태와 중요 이벤트가 먼저 전달되며 새 snapshot이 그 순서를 넘지 않는다. |
| 오래된 snapshot 뒤 룸 변경 도착 | snapshot 필터 때문에 `room.state`·종료·입장 이벤트를 버리지 않는다. |
| 최신 snapshot 교체 | 미전송 전체 상태만 교체하고 일회성 이벤트·중요 응답은 조용히 버리지 않는다. |
| 지연·jitter·TCP 손실 재현 | 공개 상태의 일치를 확인하고 실제 도착 대기를 계측한다. 0 지연을 기대하지 않는다. |
| 한 클라이언트가 거의 읽지 않음 | 중요 FIFO 포화 시 해당 연결을 닫고 정리하며 다른 룸 진행 영향을 측정한다. |

jitter는 전송 지연이 일정하지 않고 흔들리는 현상이다.
상태 비교는 동일 `(match_id,revision,server_tick,snapshot_seq)` capture를 기준으로 한다. 실제 checker는 여섯 full 상태·member 순서·참가자 seq map을 비교하고 각 참가자의 seq1 이동 ACK, 양의 Z·불변 X/Y·속도 5, seq2 정지 ACK 뒤 추가 snapshot의 위치 유지를 검증한다. 서버 tickHz는 추측하지 않는다. revision은 상태 변경 버전이다.
숨겨진 정보가 있으면 참가자 전체에 공통으로 공개된 부분만 비교한다.
실제 이동·충돌·협동 성공 여부는 GameHandler 규칙과 비교한다. 같은 메시지를 받는 것만으로 판정하지 않는다.
Godot 예제는 수신 간격 기반 보간을 별도로 검사한다. local prediction과 게임별 판정 보정은 후속이다.

## 운영 기능이 게임 진행과 정리를 방해하지 않는가

actor는 룸 상태 변경 전담 작업, admin lane은 운영 명령 대기열, control 큐는 끊어진 소속 정리 대기열이다.
audit은 감사 기록, stale은 오래된 요약 표시다.

| 시험 상황 | 관측할 성공 기준 |
|---|---|
| 운영 GET 요청 폭주 | 조회는 요약 복사본을 읽고 actor 응답·전체 게임 상태 직렬화를 기다리지 않는다. |
| 요약 게시 지연·지표 미수집 | stale과 갱신 시각이 표시되고 미수집 CPU·지연은 `미측정`으로 나온다. |
| admin lane 포화 | 신규 조작을 거절하며 membership 정리 control 슬롯은 유지된다. |
| 게임 입력·운영·audit 경로 혼잡 | 정리가 우선 동작하고 actor가 HTTP·디스크·socket을 기다리지 않는다. |
| private 반복 생성/입장·hash worker 포화 | hash는 actor 밖에서 제한해 처리하고 CPU·메모리·tick 밀림과 다른 룸 영향을 측정한다. |
| 다른 룸으로 이동한 대상에 오래된 kick | 새 membership이나 그 연결을 종료하지 않는다. |
| viewer 조작·미인증·CSRF 요청 | API 서버가 조작을 거절하고 게임에 변화가 없다. |
| 접수 audit 저장 실패 | 명령을 실행하지 않고 접수 실패를 알린다. |
| 실행 뒤 audit 저장 실패 | 실제 실행 결과와 `audit_failed`를 구분하고 새 조작 접수를 막는다. |
| 운영자 브라우저 종료·HTTP 취소 | 이미 접수한 명령 수명을 서버가 유지한다. |
| 동일 key 재시도·내용 변경 | 같은 명령이면 같은 command를 반환하고 내용 변경이면 409로 거절한다. |
| 이전 instance 재시도·재시작 | 이전 실행을 지정한 POST는 거절하고 불명확 명령은 자동 재실행하지 않는다. |

## 점검 종료가 정해진 순서를 따르는가

| 시험 상황 | 관측할 성공 기준 |
|---|---|
| drain과 WS·create·join·start 경합 | 같은 admission gate(새 수용 잠금 구간)에서 차단과 입장 확정의 순서가 정해지며 차단 이후 신규 확정이 없다. |
| 진행 중 게임이 있는 drain | 새 수용은 막고 `/readyz`는 503이지만 기존 match는 끝이나 deadline까지 진행한다. |
| drain command 성공 표시 | 새 게임 차단 완료와 이후 프로세스 종료 진행을 별도로 표시한다. |
| match 0개 또는 deadline 도달 | WS close/cancel → membership 정리 → actor 대기 → audit flush → HTTP 종료 순서로 진행한다. |
| 느린 연결·audit writer가 있는 종료 | 연결·actor 대기와 audit flush가 설정 상한 안에서 처리되는지 관측한다. |
| 종료 후 운영 polling 중단 | 단절만으로 drain 실패로 바꾸지 않고 마지막 확정 결과와 구분한다. |

HTTP 종료만으로 WebSocket이 정리됐다고 판단하지 않는다. [Server.Shutdown의 연결 수명 규칙](https://pkg.go.dev/net/http#Server.Shutdown)
연결 수·membership·actor 작업이 어떻게 정리됐는지 종료 과정에서 함께 기록한다.

## 성능과 경합 검사는 구현 뒤에 수행한다

30Hz 계산·15Hz snapshot은 미측정 시작값이다. 실제 완료 빈도, 계산 밀림, 전송량, 송신 대기, 룸 간 영향을 측정한다.
같은 장비와 GameHandler 조건에서 룸·접속 수를 늘려 병목과 사용 가능한 규모를 찾는다.
RTT는 왕복 시간이며 화면 지연을 대신하지 않는다. 클라이언트 시계와 서버 시각을 단순히 빼지 않는다.
화면 도착·표시 지연은 클라이언트 계측이 있어야 측정한다. 임의의 절대 지연·메모리 합격 수치를 만들지 않는다.

`go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`로 Go 실행 경로를 검증한다. 실행 결과는 작업 보고서에 기록한다. deterministic queue/ticker·writer barrier는 내부 순서·경합 시험이며 실제 TCP 손실·jitter 주입 시험은 아니다. 게임별 충돌·승패·운영 부하·UI 지연은 후속이며 미측정이다. Godot QA 범위는 아래 별도 기록을 따른다.
race 검사 통과와 위 상황 시험, 실제 게임 판정·성능 측정 결과를 함께 기록해야 완료 근거가 된다.
구현 순서는 [07. 구현 순서와 설정](07-implementation-and-config.md)을 따른다.

## Godot desktop 예제의 별도 검증

재현 명령과 실행 방법은 [Godot 3D/2D 예제](../../examples/godot/README.md#검증-명령과-확인한-범위)에 있다. 기본 runner는 import·unit **354 checks**·실제 WebSocket **62 checks**·짧은 read timeout heartbeat **5 checks**를 수행한다. Go 서버를 현재 source로 build하지만 Go full/race suite를 실행하는 명령은 아니다.

별도 native 3D 두 창과 mixed 3D/2D 두 native + 네 headless, 총 여섯 guest의 공통 full snapshot 비교를 수행했다. mixed 실행에서 동일 `(game_id, room_id, match_id, revision, server_tick, snapshot_seq)` **85개**의 full 상태를 비교했고 2D XZ 투영·숨겨진 Y 보존·모드 전환 zero/input_seq 연속성·player 제거를 확인했다. native 입력은 synthetic physical key와 explicit focus signal이며 수동 OS keyboard/focus 검증이 아니다. 긴 game ID HUD는 별도 unit/geometry와 native 한 창 fixture로 확인했으며 실제 tooltip hover는 검증하지 않았다.

namespace는 별도 unit **58**과 실제 네 Godot socket **89** 검사를 수행한 증거를 재사용한다. 기본 runner에 해당 namespace 검사가 포함된 것은 아니다. 서로 다른 게임의 같은 번호 공존은 deterministic core 근거이며 실제 네 socket으로 고정 같은 번호를 입증했다고 표현하지 않는다. 운영 정렬 계약의 모든 tie-break가 단일 룸/게임 실행으로 증명된 것도 아니다.

최소 API는 Godot 4.4 기준, 실제 엔진은 4.7.2다. 보간은 동기화되지 않은 local receive clock을 쓰며 추가 표시 지연이 있다. web export·TLS 배포·목표 Hz·production 부하와 모든 근접 avatar label 배치의 가독성은 검증하지 않았다.
