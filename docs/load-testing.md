# 동기화 부하 테스트

[프로젝트 소개](../README.md) · [빠른 시작](getting-started.md) · [Godot 예제](../examples/godot/README.md) · [설계·검증 조건](implementation/sync-loadtest-and-godot-design.md)

여러 룸의 모든 참가자가 같은 시간 동안 이동 입력을 보내 서버의 처리 대기와 상태 일치를 측정합니다.
룸 입장·비밀번호·6인 이동을 확인하는 `cmd/roomcheck`와 달리, 부하 도구는 ACK를 기다린 뒤 다음 입력을 보내지 않습니다.
실제 전송률과 입력 처리 결과를 함께 봐야 느려진 서버 때문에 부하 자체가 줄어든 상황을 구별할 수 있습니다.

## 1. 준비하고 서버 실행하기

Go **1.26 이상**을 준비하고 현재 저장소의 root에서 실행합니다.
부하 도구는 저장소 소스로 실행합니다. 기존 `v0.1.0` 릴리스에 이 도구가 포함되어 있다는 뜻은 아닙니다.
도구는 대상 서버에 guest·룸·이동 입력을 실제 생성하고 종료 시 자신이 만든 연결과 룸을 정리합니다.

저장소 root에서 일반 build의 서버와 도구를 준비합니다. 측정할 때 race build를 사용하지 않습니다.

```bash
mkdir -p work/load-results
go build -o work/load-results/coopdock-server ./cmd/server
go build -o work/load-results/loadtest ./cmd/loadtest
```

다음은 두 게임을 등록한 로컬 서버입니다. 다른 터미널에서 도구를 실행하는 동안 켜 둡니다.

```bash
GAME_IDS=game-a,game-b work/load-results/coopdock-server
```

기본 서버 주소는 `127.0.0.1:8080`입니다. 환경변수로 주소나 룸 설정을 바꿨다면 도구의 URL과 실행 조건도 맞춥니다.
`game_id`는 연결 시 선택하는 공개 namespace이며 인증·게임별 quota·CPU 격리 기능은 아닙니다.
하나의 기본 게임만 사용할 때는 `GAME_IDS`를 지정하지 않고 서버를 실행하고 도구의 `-game-ids default`를 사용합니다.

## 2. 조건을 정하고 실행하기

다른 터미널의 저장소 root에서 실행합니다. 결과는 stdout의 JSON 한 줄이므로 redirect로 파일에 보관합니다.
파일 출력 전용 옵션은 없습니다.

```bash
work/load-results/loadtest \
  -url ws://127.0.0.1:8080/ws -game-ids game-a,game-b \
  -rooms 10 -players 6 -duration 20s -input-hz 20 \
  > work/load-results/10-rooms.json
```

| 옵션 | 기본값 | 의미 |
|---|---|---|
| `-url` | `ws://127.0.0.1:8080/ws` | 대상 base URL. query·fragment·credentials 불가 |
| `-game-ids` | `default` | 서버에 등록한 ID의 콤마 구분 목록. 룸을 순서대로 분배 |
| `-rooms` | `10` | 전체 룸 수, 허용 1~512 |
| `-players` | `6` | 각 룸의 참가자 수, 허용 1~6 |
| `-duration` | `10s` | setup을 제외한 측정 기간, 양수·최대 1시간 |
| `-input-hz` | `20` | 참가자당 목표 입력 빈도, 허용 1~120Hz |

도구의 기본값은 10룸 × 6명, 10초, 참가자당 20Hz입니다.
20Hz는 의도적으로 만든 부하 조건이며 방향이 바뀔 때 보내는 Godot 예제의 실제 송신 빈도가 아닙니다.
도구의 입력 상한이 서버의 상한을 늘리지는 않습니다. 서버 기본값은 전체 128룸·768세션이며 모든 게임이 공유합니다.
원하는 룸을 생성하지 못했다면 그 실행을 완성된 부하 결과로 해석하지 않습니다.

도구는 setup → 동시 측정 → ACK drain·최종 정지 → 퇴장·정리 순서로 실행합니다.
측정 구간이 끝나도 연결을 즉시 닫지 않고, 보낸 입력의 ACK와 최종 zero 입력의 실제 처리를 확인합니다.

| 제한 옵션 | 기본값 | 의미 |
|---|---|---|
| `-setup-timeout` | `30s` | 전체 연결·룸 구성·시작 제한 |
| `-drain-timeout` | `10s` | 측정 뒤 ACK·최종 정지 확인 제한 |
| `-cleanup-timeout` | `10s` | 취소와 독립적인 전체 퇴장·정리 제한 |
| `-request-timeout` | `5s` | 개별 요청·쓰기 제한. 지속 reader 수명과 분리 |
| `-max-pending` | `256` | 연결당 pending 상한. 넘으면 실패 |
| `-sample-limit` | `10000` | 각 분포의 uniform reservoir 보관 표본 상한 |
| `-capture-limit` | `128` | 룸당 비교 capture window. 오래된 항목부터 제거 |

timeout은 양수·최대 5분입니다. 총 pending·sample·capture에도 조합 상한이 있습니다.
전체 옵션은 `work/load-results/loadtest -help`, 적용한 조건과 자원 상한은 report의 `conditions`와 `conditions.resource_limits`에서 확인합니다.
너무 짧은 측정은 이동이나 공통 capture를 충분히 관측하지 못해 실패할 수 있습니다.

빌드한 도구는 성공 시 exit 0, 검증·설정·취소·정리 실패 시 exit 1입니다.
알 수 없는 flag·위치 인수 등 CLI 구문 오류는 JSON 오류 report와 exit 2이며 상세 오류는 stderr에도 기록합니다.
실패한 report도 보존하고 `success`와 `errors`, `cleanup.errors`를 읽습니다.

## 3. 결과 읽기

### 입력 처리와 수신 간격

| 값 | 알 수 있는 것 |
|---|---|
| 입력 적용 ACK RTT p50·p95·p99·max | `game.action`을 보낸 뒤 같은 입력의 적용 확인 ACK를 받기까지의 왕복 시간 |
| Snapshot 도착 간격 p50·p95·p99·max | 같은 연결에 도착한 주기 `game.snapshot` 사이의 실제 경과 시간 |
| 실제 입력·ACK·snapshot 수 | 목표 Hz와 별개로 실제 발생한 송신·처리·수신량 |
| 관측 tick Hz | 측정 중 같은 룸의 tick 진행량을 같은 관측 시각 구간으로 나눈 값 |
| Snapshot sequence gap | 관측한 번호의 건너뜀. 최신 상태 교체로 발생할 수 있어 불일치와 별도 집계 |

p95는 관측값의 약 95%가 그 값 이하라는 뜻입니다. 평균이 낮더라도 p95·p99·최댓값이 크면 일부 입력이 더 오래 기다렸다는 뜻입니다.
ACK RTT에는 네트워크 왕복뿐 아니라 서버 큐·tick 대기가 포함됩니다. WebSocket Ping RTT·편도 지연·Godot 화면 표시 지연과 같은 값이 아닙니다.
측정 중 보낸 입력의 ACK가 drain에서 도착했다면 RTT 모수에 포함해 느린 마지막 응답을 숨기지 않습니다.
표본을 제한했다면 전체 관측 수, 실제 보관 수, 표본 선택 방식·truncation 또는 정밀도를 함께 읽습니다.

### JSON에서 찾아볼 항목

| 경로 | 확인할 내용 |
|---|---|
| `success`, `errors` | 전체 결과와 실패 사유 |
| `conditions`, `active_game_ids` | 요청한 조건과 실제 시작한 게임 목록 |
| `setup_seconds`, `measurement_seconds` | setup 경과와 동시 입력 측정 구간 |
| `total.input_ack_rtt`, `total.snapshot_arrival_interval` | 전체 입력 RTT·도착 간격 분포 |
| `rooms[].metrics.input_ack_rtt` | 각 룸 입력 RTT 분포. 전체 표본과 별도로 계산 |
| `rooms[].common_periodic_captures` | 해당 룸 전원이 같은 full periodic capture를 받은 횟수 |
| `rooms[].players_moved`, `rooms[].players_stopped` | 실제 이동·최종 정지를 확인한 참가자 수 |
| `rooms[].observed_tick_hz` | 해당 룸 host가 관측한 tick 진행률 |
| `cleanup` | 퇴장 ACK·제거 룸·닫힌 연결·종료 reader·남은 pending·오류 |

전체 counter는 `total`, 룸 counter는 `rooms[].metrics`에 있습니다.
관측 tick Hz는 같은 측정 구간의 `observed_tick_delta / tick_observation_seconds`로 계산합니다.
`inputs_sent`, `input_acks`, `unconfirmed_inputs`와 별도로 최종 zero 입력은 `zero_inputs_sent`, `zero_input_acks`를 확인합니다.
오류 counter는 연결 전체 수명의 오류를 포함하므로 측정 중 이동 입력 수와 구분합니다.

분포에는 `observations`, `retained`, `sample_limit`, `sample_policy`, `estimator`, `precision`, `approximate`와 네 개 ms 값이 있습니다.
표본 정책은 Algorithm R의 uniform reservoir이고 quantile은 nearest-rank 방식입니다.
`approximate=false`면 관측을 모두 보관한 quantile, `true`면 uniform sample의 추정 quantile이며 고정 오차 보장은 없습니다.
`max_ms`는 표본 보관 여부와 관계없이 모든 관측에서 계산한 최댓값입니다. `observations=0`의 숫자 0은 유효한 지연 측정으로 읽지 않습니다.
기본 10,000개를 넘는 전체 분포는 추정값이어도 개별 룸 분포는 전수일 수 있으므로 각 분포의 `approximate`를 따로 확인합니다.

예를 들어 저장한 report의 실제 모수와 전체 p95를 읽을 수 있습니다.

```python
import json

with open("work/load-results/10-rooms.json") as source:
    report = json.load(source)
rtt = report["total"]["input_ack_rtt"]
print(report["success"], rtt["observations"], rtt["retained"], rtt["p95_ms"])
```

### 상태가 실제로 맞는지

도구는 연결 게임·룸·match·참가자 소속과 입력 ACK의 요청 ID·입력 seq를 검사합니다.
각 룸에서 같은 `(game_id,room_id,match_id,revision,server_tick,snapshot_seq)`의 full snapshot을 전원이 받은 사례가 있어야 합니다.
setup의 초기 상태만 같거나 전원 비교가 0회인 경우는 동시 이동 검증 통과로 세지 않습니다.
같은 capture에 서로 다른 내용이 있거나 실제 참가자 이동을 관측하지 못하면 실패입니다.
map key는 canonical하게 비교하되 member·player 배열의 순서는 보존합니다.
참가자별 처리 seq가 보낸 번호를 앞서거나 이전 관측보다 역행하는 상태도 거절합니다.

측정 후에는 모든 미완료 ACK를 확인하고, 각 세션의 마지막 zero 입력 ACK와 그 seq 이상이 반영된 정지 snapshot을 확인합니다.
후속 snapshot의 위치 유지와 마지막 참가자 퇴장 뒤 룸 정리도 검사합니다.
공개 목록은 길이가 제한되므로 목록에서 보이지 않는 것만으로 삭제를 확정하지 않습니다. 생성했던 룸 번호의 입장 probe가 `ROOM_NOT_FOUND`인지 확인합니다.
입력 송신 성공이나 ACK 수신만으로 이동·정지·정리를 성공 처리하지 않습니다.

## 4. 로컬 실측 결과

**50룸·300개 연결이 동시에 20초 동안 이동한 실행까지 상태·ACK·정리 검사를 통과했습니다.**
이는 아래 장비의 localhost에서 각 조건을 한 번씩 실행한 결과입니다. 최대 수용 인원이나 인터넷 게임 지연을 보장하는 수치는 아닙니다.

측정한 소스는 [`425c173`](https://github.com/sangjinsu/coopdock/commit/425c173fe0a29caab0b08cf5befad93c49b55431)입니다.
환경·실제 argv·원본 SHA-256·61개 룸의 전체 report는 [공개 결과 JSON](validation/sync-load-results.json)에 있습니다.
각 `cases[].report`는 원본 JSON을 반올림하거나 구조를 바꾸지 않고 포함합니다. 아래 표의 ms만 소수 셋째 자리로 반올림했습니다.

| 항목 | 실측 환경과 조건 |
|---|---|
| CPU / 메모리 | Apple M2, 논리 CPU 8개, RAM 16GiB |
| OS / architecture | macOS 27.0.1, Darwin 27.0.0, arm64 |
| Go | go1.27.2 darwin/arm64 |
| 실행 시각 | 2026-10-23 KST; 원본 `started_at`은 UTC 2026-10-22 23:49~23:50 |
| 서버 | 일반 build, 게임 `game-a,game-b`, 기본 30Hz tick·15Hz snapshot·128룸·768세션, admin 비활성 |
| 부하 | 룸당 6명·20초·참가자당 20Hz, 각 조건 한 번·매번 새 서버 |
| 실행 분리 | 서버와 생성기는 같은 장비. 세 조건은 순차 실행하며 다른 build·race·Godot 검사와 병행하지 않음 |
| 계측하지 않은 값 | 서버 CPU 사용률·RSS. 장비 사양으로 서버 자원 예산을 추정하지 않음 |

입력 수에는 측정 구간의 이동 입력만 포함하고 마지막 zero 입력은 별도로 확인했습니다.

| 룸 / 연결 | 활성 / 설정 게임 | 입력 = ACK | 실제 입력/초 | RTT p50 / p95 / p99 (ms) | RTT 전수 max (ms) | 전체 RTT 보관 / 관측 |
|---|---|---|---|---|---|---|
| 1 / 6 | 1 / 2 | 2,400 | 120 | 19.138 / 34.657 / 35.064 | 36.034 | 2,400 / 2,400 전수 |
| 10 / 60 | 2 / 2 | 24,000 | 1,200 | 23.770 / 32.344 / 34.105 | 37.377 | 10,000 / 24,000 표본 |
| 50 / 300 | 2 / 2 | 120,000 | 6,000 | 16.931 / 32.115 / 34.106 | 92.066 | 10,000 / 120,000 표본 |

10룸·50룸의 전체 p50/p95/p99는 Algorithm R 표본 추정이며 고정 오차 보장이 없습니다.
각 룸의 RTT는 관측 2,400개를 모두 보관했고, 위 max는 전체 입력의 전수 최댓값입니다.
게임 ID를 두 개 설정해도 1룸 조건은 실제 `game-a` 한 게임만 활성화했습니다.

| 룸 | Snapshot 수신 수 | Sequence gap 합계 | 도착 간격 p95 / 전수 max (ms) | 간격 보관 / 관측 | 룸별 전원 공통 capture | 룸별 관측 tick Hz |
|---|---|---|---|---|---|---|
| 1 | 1,795 | 0 | 68.674 / 76.342 | 1,789 / 1,789 전수 | 299 | 30.000252 |
| 10 | 17,976 | 18 | 71.303 / 139.785 | 10,000 / 17,916 표본 | 296~300 | 29.998388~30.000997 |
| 50 | 89,810 | 190 | 68.722 / 199.761 | 10,000 / 89,510 표본 | 291~300 | 29.948919~30.049146 |

Snapshot 수와 sequence gap은 모든 수신 연결의 관측을 합산한 값입니다.
gap을 패킷 유실 수로 해석하거나 0을 무손실 증거로 쓰지 않습니다. 최신 상태 교체와 실제 네트워크 손실은 같은 지표가 아닙니다.
비교 window 상한 128개에 따라 각 룸에서 오래된 capture 175개가 제거됐으며, 이 역시 상태 불일치나 패킷 유실의 개수가 아닙니다.

세 조건에서 각각 **6·60·300명 전원의 이동·최종 정지**를 확인했습니다.
최종 zero 입력/ACK, 퇴장 ACK, 닫힌 연결, 종료 reader는 각각 참가자 수와 같았고, `ROOM_NOT_FOUND` probe로 **1·10·50개 룸 제거**를 확인했습니다.
미확인 입력·남은 pending·서버 오류·timeout·비정상 단절·pending 초과·정리 오류는 모두 0이었습니다.
CLI와 각 서버는 exit 0으로 종료·회수됐고 해당 localhost port도 닫혔습니다.

같은 조건을 재현하려면 2절 명령의 `-rooms`와 결과 파일명을 1·10·50에 맞춰 바꾸고, **각 실행 전 서버를 정상 종료한 뒤 새로 시작**합니다.
본문은 기본 포트와 `work/load-results/`로 정리한 실행 예시입니다. JSON에는 실제 임시 localhost 포트와 argv를 보존하고 실행 파일 경로만 저장소 상대 경로로 바꿨습니다.
실행 당시 `ROOM_*`·`WS_*`·`ADMIN_*` override와 Go runtime override를 제거한 조건도 JSON에 기록했습니다.

해당 소스의 [GitHub Actions](https://github.com/sangjinsu/coopdock/actions/runs/39873910696)는 두 Go 버전의 test·race·vet·build와 GHCR 발행을 통과했습니다.
이는 위 실제 부하 실행·[Godot 검증](../examples/godot/README.md#검증-명령과-확인한-범위)과 구분되는 CI 결과입니다.

## 5. 해석 범위와 재현

서버와 부하 생성기는 같은 장비에서 실행되므로 CPU·메모리·스케줄링 자원을 경쟁합니다.
1·10·50룸 중 가장 큰 성공 결과는 그 장비·설정·기간에서의 관측이며 서버의 최대 지원 규모가 아닙니다.
30Hz tick·15Hz snapshot 설정으로 실제 Hz나 지연을 대신 계산하지 않으며 임의의 절대 지연·메모리 합격선을 제시하지 않습니다.

인터넷 지연·jitter·실제 TCP 손실 주입·느린 수신자·TLS·운영 대시보드 polling·게임별 충돌/승패 규칙·화면 표시 지연은 별도 시험입니다.
`go test -race` 통과는 경합 검사의 근거이며 일반 build의 성능표를 대신하지 않습니다.
서버 코드·Go 버전·게임 설정·클라이언트 수·입력 빈도·기간을 같게 맞춘 뒤 결과를 비교하세요.

Godot의 입력 ACK와 snapshot 경과 표시는 [예제 안내](../examples/godot/README.md#연결과-동기화-상태-확인하기)에서 확인합니다.
Go 테스트·race·vet·build 명령은 [서버 검증](server-guide.md#종료와-검증)을 따릅니다.
