# 동기화 부하 테스트와 Godot 예제 개선 설계

[문서 길잡이](../README.md) · [동기화 계약](../specs/03-realtime-sync.md) · [검증 기준](../specs/08-validation.md) · [구현 계획](sync-loadtest-and-godot-plan.md)

## 목적과 기준

여러 룸의 참가자가 동시에 이동할 때 입력 처리 대기와 상태 일치를 함께 관찰한다.
Godot 예제에서는 연결 결과와 현재 동기화 상태를 사용자가 직접 확인하기 쉽게 만든다.
이 문서는 구현·실행 결과 보고가 아니다. 실제 수치와 검증 범위는 [부하 테스트 가이드](../load-testing.md)에 기록한다.

기준 소스는 `main`의 `597a0ade46a1769bf319b17a35b771945d200b01`이다.
사용자는 동기화 부하 테스트와 예제 개선, 기존의 작업별 커밋·푸시 흐름을 선택했다.
아래 도구 기본값과 예제 표시 방식은 기존 계약 안에서 정한 이번 구현 선택이다.

## 보존할 계약

- Go `1.26.0` 이상과 `github.com/coder/websocket v1.8.15`를 사용하고 dependency를 추가하지 않는다.
- 룸당 1~6명, 게임별 namespace, 6자리 문자열 룸 번호, public/private 입장 계약을 유지한다.
- 공개 Go package와 WebSocket version 1의 op·payload를 변경하지 않는다. 이동 요청과 적용 ACK의 op는 **`game.action`**이다.
- guest 재연결은 새 세션이다. 자동 재입장·세션 복구·비밀번호 저장을 추가하지 않는다.
- reference 이동은 자유 공간의 3D 방향 입력이다. 충돌·중력·게임별 판정이나 local prediction을 추가하지 않는다.
- 기본 `30Hz tick / 15Hz snapshot`은 설정값이다. 전체 수용 인원이나 실제 Hz·지연의 보장으로 쓰지 않는다.
- 서버는 각 입력을 FIFO로 적용하고 tick 계산 성공 후 개별 ACK한다. 서로 다른 ACK의 `server_tick`은 같을 수 있다.
- 주기 snapshot은 최신 상태로 교체될 수 있다. `snapshot_seq` 중간 누락만으로 손실이나 정확성 실패를 선언하지 않는다.
- 설명은 한국어, 코드 주석은 영어다. 비밀번호·원본 요청 payload는 지표나 로그에 저장하지 않는다.

## 1. 동시에 동작하는 부하 도구

`cmd/loadtest`는 `internal/loadtest.Run(ctx, Config) (Report, error)`를 호출한다.
내부 package는 연결·측정·검증을 담당하며 외부 프로젝트용 API로 공개하지 않는다.
대상 서버는 이미 실행 중이어야 한다. 도구가 배포 설정이나 서버의 수용 상한을 바꾸지 않는다.

| 옵션 | 기본값과 의미 |
|---|---|
| `-url` | `ws://127.0.0.1:8080/ws`, 대상 WebSocket base URL |
| `-game-ids` | `default`, 서버에 등록된 게임 ID의 콤마 구분 목록 |
| `-rooms` | `10`, 게임 ID들에 분배할 전체 룸 수 |
| `-players` | `6`, 각 룸의 참가자 수, 허용 1~6 |
| `-duration` | `10s`, setup을 제외한 동시 입력 측정 기간 |
| `-input-hz` | `20`, 각 클라이언트의 목표 입력 송신 빈도 |

잘못된 URL·게임 ID·인원·빈도·기간은 연결 전에 거절한다.
setup·측정·ACK drain·정리에는 종료 가능한 deadline을 둔다. 추가 옵션과 자원 상한은 CLI help 및 실행 보고서에 명시한다.
20Hz는 도구의 부하 조건이며 방향 변경 시 보내는 현재 Godot 예제의 실제 전송 빈도가 아니다.

### 연결과 실행 구간

1. **Setup:** 게임 ID에 바인딩된 guest를 발급받고, 룸마다 생성자와 나머지 참가자를 연결한 뒤 match를 시작한다.
2. **측정 시작 barrier:** 모든 룸의 준비가 끝난 뒤 같은 측정 구간을 시작한다. setup의 ACK·snapshot은 성능 표본에 섞지 않는다.
3. **고정 주기 입력:** 각 참가자가 ACK 대기를 다음 송신 조건으로 삼지 않고 독립적으로 입력을 보낸다. 실제 송신 수와 실제 구간 길이를 기록한다.
4. **Drain과 정지:** 측정을 끝내고 미완료 ACK를 기다린 뒤 각 참가자의 마지막 zero 입력을 확인한다.
5. **정리:** 참가자가 퇴장하고 마지막 참가자 뒤 룸이 제거되는지 확인한 다음 소유 연결과 goroutine을 종료한다.

연결당 지속 reader 하나를 유지하고 writer 경로를 직렬화한다.
짧은 개별 요청 deadline으로 지속 reader를 취소하지 않는다. 라이브러리 method의 context 만료는 연결 자체를 닫을 수 있다.
연결의 run 수명과 측정 종료 신호를 분리한다. 측정 시간 종료로 reader를 닫아 drain이 불가능해지지 않게 한다.
pending 요청과 capture 보관, 통계 표본은 상한을 둔다. 상한에 도달한 입력을 조용히 누락하고 성공으로 보고하지 않는다.
실패·전체 취소에도 별도의 bounded cleanup을 시도하고 필요 시 `CloseNow`로 연결을 닫은 뒤 goroutine을 회수한다.
측정 중 단절과 의도한 종료를 구분하며 cleanup 중 정상 member 감소를 측정 중 소속 위반으로 오판하지 않는다.

### 측정하는 값

| 지표 | 계산과 해석 |
|---|---|
| 입력 적용 ACK RTT | `game.action` 송신 시각부터 같은 요청의 성공 ACK 수신까지의 monotonic 경과 시간; p50·p95·p99·max |
| Snapshot 도착 간격 | 같은 연결의 측정 구간 내 주기 `game.snapshot` 수신 시각 간 차이; p50·p95·p99·max |
| 실제 입력·수신률 | 실제 송신 입력 수·성공 ACK 수·수신 snapshot 수를 해당 측정 구간 길이와 함께 기록 |
| 관측 tick Hz | 각 룸의 측정 구간 내 첫·마지막 관측 tick 차이 / **같은 두 관측 시각의 경과 시간** |
| Sequence gap | 주기 snapshot 번호 사이의 건너뛴 개수; 상태 불일치와 별도 집계 |
| 정확성·종료 | 공통 capture 수, 이동·정지 확인, 오류·timeout·단절·미확인 ACK와 정리 결과 |

전체와 룸별 통계를 구분하고 입력 ACK를 WebSocket Ping RTT나 편도 지연으로 부르지 않는다.
ACK RTT의 모수는 측정 구간에 **송신한** 입력이다. drain에서 도착한 그 입력의 ACK도 포함해 느린 마지막 응답을 숨기지 않는다.
전체 observations 수와 보관한 sample 수를 함께 기록한다.
표본을 제한하면 estimator·선택 정책·truncation 또는 정밀도를 보고서에 적는다. 초반 일부만 보관한 p95를 전체 실행 p95로 표현하지 않는다.
시계가 다른 서버 timestamp와 client timestamp를 단순히 빼지 않는다.

## 2. 같은 상태를 받았는지 검사하기

각 메시지는 연결의 `game_id`, 룸·match·세션 소속, 정확한 member 집합을 검증한다.
성공 ACK는 pending `request_id`, `match_id`, `input_seq`와 일치해야 하며 같은 세션의 seq는 역행하지 않는다.
ACK tick은 비감소 조건으로 검사한다. enqueue 접수나 다른 요청의 응답은 적용 확인으로 세지 않는다.

비교 key는 `(game_id, room_id, match_id, revision, server_tick, snapshot_seq)`다.
같은 key의 full snapshot을 canonical한 내용으로 비교하고, 어느 참가자에게든 내용이 다르면 실패한다.
map key는 canonical하게 비교하되 member·player 배열 순서는 보존한다. 처리 seq는 송신한 번호를 앞서거나 이전 관측보다 역행하면 안 된다.
비교용 window는 유한하며 참가자별 수신 bitset으로 전원 공통 capture를 센다.
**각 룸에서 측정 구간의 주기 `game.snapshot`으로 전원 공통 capture가 최소 하나 있어야 한다.**
setup의 `game.started` 초기 capture나 비교 0회는 이 조건을 대신하지 못한다.

실제 각 참가자의 위치 변화도 관찰한다. ACK만 반환하고 이동하지 않는 서버를 성공으로 판정하지 않는다.
측정 후 deadline 안에 모든 in-flight ACK를 drain하고 각 세션의 최종 zero 입력 seq ACK를 기다린다.
각 zero seq 이상이 `last_processed_input_seq`에 포함된 snapshot에서 zero velocity를 확인한다.
그 뒤 추가 snapshot에서 해당 위치가 유지되는지 검사한다. 이전 snapshot이나 송신 성공만으로 정지했다고 판정하지 않는다.
마지막 퇴장 뒤에는 생성했던 룸 번호의 입장 probe가 `ROOM_NOT_FOUND`인지 확인하며 종료 실패를 report에 남긴다.
공개 목록은 길이가 제한되므로 목록의 부재만으로 룸 제거를 확정하지 않는다.

## 3. Godot 예제에서 확인할 상태

연결은 `connect_to_url()`의 `OK`가 아니라 socket open 뒤 올바른 `session.guest` 수신까지 성공해야 준비 상태다.
새 연결의 guest 승인 직후 public 룸 목록을 한 번 요청해 첫 목록을 기다리는 시간을 줄인다.
예기치 않은 닫힘에는 오류와 수동 재연결 안내를 표시한다. 사용자가 누른 연결 종료와 구분한다.
재연결로 이전 룸을 복구했다는 표시나 비밀번호 저장은 하지 않는다.

3D·2D 화면은 하나의 공통 HUD에서 아래 정보를 보여 준다.

| 표시 | 의미 |
|---|---|
| 입력 ACK RTT | 현재 소속에서 유효하게 확인한 최근 `game.action` 적용 ACK의 왕복 시간 |
| Snapshot 경과 | 마지막으로 **수락한** 현재 match snapshot 이후의 local monotonic 경과 시간 |
| Tick / snapshot seq | 같은 수락 snapshot의 `server_tick`과 `snapshot_seq` |

RTT는 bounded pending record의 송신 시각과 seq·match·generation을 검증한 뒤 갱신한다.
지표가 없을 때는 0ms로 꾸미지 않고 미측정으로 표시한다.
stale snapshot이나 옛 generation·operation epoch·match의 ACK가 초기화된 현재 지표를 되살리지 않아야 한다.
퇴장·새 match·재연결에서 소속 지표를 초기화하며 같은 연결의 `input_seq` 수명 규칙은 유지한다.
기존 긴 game ID tooltip과 작은 창에서의 HUD 가독성, 2D의 XZ 투영·숨겨진 Y·정지 우선 입력을 보존한다.
기존 `room.list` data-frame heartbeat를 WebSocket control Ping으로 대체하지 않는다. CLOSING 상태도 종료까지 poll한다.

## 4. 검증과 산출물

- Go: percentile·표본 제한, capture 변조·소속 오류, 잘못된 ACK, ACK만 주는 무이동 서버, snapshot 부재, 취소·정리 회귀와 작은 실제 multiroom 통합 시험.
- Godot: 초기 목록 1회, 수동/비정상 종료 구분, 이전 ACK·stale snapshot 배제, 지표 reset·HUD 배치 회귀.
- 단일 Godot runner에 기존 namespace unit와 `game-a,game-b` 서버 integration을 포함하고 기본·mixed native 3D/2D를 실행한다.
- 실제 부하는 1·10·50룸 × 6명, 각 20초·20Hz 조건으로 순차 실행한다. 장비·Go·서버 설정·실제 전송량과 원본 report를 보존한다.
- 각 작업은 fresh 검증과 Architect 검토 후 별도 커밋·푸시한다. README는 소개 중심으로 유지하고 실행·측정 설명은 별도 문서에 둔다.

localhost 결과는 서버와 부하 생성기가 같은 장비의 CPU·메모리를 경쟁한 관측이다.
인터넷 지연·jitter·TCP 손실 주입·느린 수신자·TLS·운영 대시보드 polling·게임별 충돌 규칙·화면 표시 지연을 검증한 결과로 확대하지 않는다.
임의의 절대 지연·메모리 합격선을 만들지 않으며 가장 큰 성공 시나리오를 서버의 최대 지원 규모로 표현하지 않는다.

## 공식 API 근거

구현 전 확인한 계약은 [coder/websocket v1.8.15 연결](https://github.com/coder/websocket/blob/v1.8.15/conn.go)·[reader](https://github.com/coder/websocket/blob/v1.8.15/read.go),
[Go 1.26 time](https://github.com/golang/go/blob/go1.26.0/src/time/time.go),
[Godot 4.4 WebSocketPeer](https://github.com/godotengine/godot/blob/4.4-stable/modules/websocket/doc_classes/WebSocketPeer.xml)·[Time](https://github.com/godotengine/godot/blob/4.4-stable/doc/classes/Time.xml)다.
동시 reader 금지·context 만료에 따른 연결 종료·monotonic clock·정기 poll 계약을 따르며 버전 추측으로 API를 추가하지 않는다.
