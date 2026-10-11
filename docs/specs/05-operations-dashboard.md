# 05. 운영 대시보드 — 현재 조회 MVP와 후속 화면

[전체 목차](README.md) · [운영 사용법](../server-guide.md#운영-대시보드-켜기) · [상세 설계 원본](../go-room-server-design.md)

## 현재 볼 수 있는 것

같은 Go binary의 `/admin`은 **Basic viewer 인증을 거친 조회 전용 화면**이다. 공개·비공개 룸을 모두 조회하며 게임 플레이 화면과는 별개다. HTML·CSS·JS는 embed되어 외부 CDN·font·image나 frontend build가 필요하지 않다.

| 화면 | 현재 구현 |
|---|---|
| 서버 개요 | RUNNING/STOPPING, readiness, instance ID, live 연결 수, 룸 membership 합계, 전체/대기/게임 중 룸 수 |
| 룸 목록 | 전체 내부 room ID·6자리 번호·visibility·phase·인원/정원·host ID·nullable match ID |
| 검색과 페이지 | 번호 앞자리 ASCII 0~6자리, WAITING/PLAYING, public/private, 100개 페이지와 이전/다음 |
| runtime | uptime 초, HeapAlloc MiB, goroutine 수; CPU·RTT·클라이언트 표시지연은 미측정 |
| 신선도 | cache와 actor row의 갱신 시각·age·stale, API별 마지막 성공 자료와 오류 |

RUNNING은 프로세스 실행 상태, readiness는 신규 수용 gate, freshness는 자료의 게시 시각이다. readiness가 true여도 정원이 남거나 낮은 지연이 보장되는 것은 아니다. live 연결에는 룸 밖 guest도 포함하며 membership은 룸에 실제 속한 인원이다. heap은 OS RSS가 아니며 goroutine은 플레이어 수가 아니다. 계측하지 않은 CPU·RTT·화면 지연은 API `null`, 화면 `미측정`이다.

전체 ID와 번호를 잘라내지 않으며 번호는 문자열로 앞 0을 유지한다. private도 운영 목록에 표시하지만 게임 `room.list`는 public-only다. password·hash·salt·운영 token·전체 member ID 목록·game_state는 운영 summary/cache/API/HTML/로그에 넣지 않는다.

## 갱신과 조회 경계

actor summary, collector, runtime sampler는 각각 1초다. collector가 manager의 registry refs/readiness, actor의 atomic allowlist summary, transport live count와 독립 runtime 자료를 모아 불변 cache를 게시한다. HTTP는 cache만 읽고 manager/actor lock·callback·runtime sampling을 수행하지 않는다. 게임 snapshot을 재사용하거나 revision/snapshot_seq를 증가시키지 않는다.

admin 미설정에도 actor의 1초 summary heartbeat는 유지한다. collector/runtime loop는 생성하지 않으므로 0개다. 고정 게시 비용과 runtime 수집의 실제 게임 부하 영향은 미측정이며 최대 룸 수는 `ROOM_MAX_ROOMS`로 제한한다.

`updated_at`은 UTC RFC3339의 **게시 시각**이다. status/rooms envelope는 collector cache, room row는 actor summary 시각이다. HTTP 응답 시각이나 게임 snapshot 생성 시각이 아니다. `started_at`은 instance 시작 시각, `generation`은 cache 세대다. `age_ms >= 5000`이면 stale이며 HTTP와 UI의 임계값은 고정 5초다. UI는 수신 후 monotonic 경과시간을 더하므로 수동 mode에서도 age가 진행한다. envelope가 최신이어도 개별 row는 오래될 수 있다.

polling은 두 API batch 완료 후 5초, batch timeout은 5초다. 한 batch만 진행한다. 자동 갱신 해제는 다음 자동 조회를 중단하고 진행 중 조회는 완료하며, hidden tab·pagehide는 진행 중 요청도 취소한다. persisted pageshow 복귀는 lifecycle/age 표시를 복구하고 visible·auto on·인증 유효 조건에서만 조회한다. 일반 최초 pageshow는 조회를 추가하지 않는다. 실제 BFCache 사용 여부는 browser가 결정한다.

일반 오류는 API별 마지막 성공 자료·조건·시각을 유지하면서 오류를 알린다. 최초 실패의 `—`와 성공한 0은 다르다. 401/403은 자료를 지우고 polling을 멈춘다. 재인증은 reload이며 native Basic 자격증명 cache를 지우거나 logout을 보장하지 않는다. 서버 종료 뒤 polling 실패는 종료에 따른 단절일 수 있다.

## 인증과 보호

`ADMIN_VIEWER_TOKEN`은 운영 전용으로 username `viewer`와 HTTP Basic으로 검증한다. 미설정·빈 값이면 모든 admin 경로 404와 collector/runtime 비생성이다. 설정값은 32~256 bytes의 공백 없는 printable ASCII `0x21..0x7e`만 허용한다. HTML·자산도 인증되며 공개 login shell은 없다.

오류 응답까지 no-store/self CSP/nosniff/frame DENY를 적용한다. UI fetch는 same-origin credentials와 no-store를 사용하고 token을 JS가 읽거나 저장하지 않는다. `WS_ORIGINS`는 게임 WebSocket 정책이다. 외부 운영의 Basic은 reverse proxy HTTPS가 필요하며 이번 구현은 배포/TLS 변경을 포함하지 않는다. 조용한 token 입력과 정확한 method/status/query/JSON은 [서버 가이드](../server-guide.md#운영-대시보드-켜기)와 [API 계약](04-messages-and-api.md#현재-운영-json-계약)을 따른다.

## 후속 화면과 미측정 범위

룸·연결 상세, 참가자 전체 목록, tick 완료율·밀림·큐·송신 지표, kick/close/drain, command 결과와 지속 audit, operator 계정·JWT/session/OIDC는 후속이다. 목표 설계는 [06. 운영 조작과 종료](06-operations-and-shutdown.md)에 있다. tick 설정값을 실제 처리율로, RTT를 클라이언트 표시지연으로 대신하지 않는다. 서버와 클라이언트의 시계를 단순히 빼 도착 지연으로 사용하지 않는다.

현재 검증은 deterministic actor blocking→stale→recovery·cache-only HTTP 계약, 보호된 실제 UI와 6 WS 서버 동작, 별도 mock/VM UI 행위로 구분한다. 실제 운영 부하·CPU·latency 영향·Godot 화면, native Basic prompt 입력 UX·OS background throttle·browser 메뉴 200% zoom·실제 BFCache 적격성은 별도 근거가 없으면 미검증이다. 검증 기준은 [08](08-validation.md)에 있다.
