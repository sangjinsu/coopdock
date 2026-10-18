# CoopDock 문서 길잡이

처음 실행할 때는 빠른 시작을, 클라이언트를 연결하거나 서버를 운영할 때는 서버 가이드를 읽으세요.
기능별 스펙과 전체 설계는 앞으로 만들 범위까지 포함합니다.
현재 실행 가능한 범위와 목표 설계를 구분해서 볼 수 있도록 아래에 문서를 모았습니다.

[프로젝트 소개](../README.md)

## 사용법 — 현재 실행 가능한 서버와 Godot 예제

| 문서 | 내용 |
|---|---|
| [빠른 시작](getting-started.md) | Go 준비, 서버 실행, 상태 확인, 실제 6개 연결 CLI 검사 |
| [Docker로 실행](docker.md) | Docker 준비, Compose 실행, 포트·게임 ID 설정과 종료 |
| [Go embedding 가이드](go-embedding.md) | public import, 기존 HTTP route 공존, 설정·admin·종료 |
| [서버 사용·운영 가이드](server-guide.md) | 현재 룸·이동 계약, WebSocket JSON, 오류 코드, 모든 환경변수, 종료와 검증 |
| [현재 서버 구조](architecture.md) | 게임 처리와 cache 기반 운영 조회의 내부 경로 |
| [Godot 3D/2D 예제](../examples/godot/README.md) | desktop 실행, 게임 ID·private 입장, 입력·보간과 검증 한계 |

서버 가이드의 [메시지 예제](server-guide.md#실제-websocket-메시지),
[3D 이동](server-guide.md#실시간-3d-reference-이동), [설정](server-guide.md#설정),
[검증 명령](server-guide.md#종료와-검증)에서 필요한 내용을 바로 찾을 수 있습니다.

## 기능 스펙 — 목표와 정책

기존 [기능별 스펙 목차](specs/README.md)는 게임을 만드는 사람과 운영하는 사람이 같은 규칙을 이해하기 위한 문서입니다.
현재 조회 전용 운영 대시보드·admin API·Godot 3D/2D 예제와 후속 상세·조작·게임별 규칙을 함께 다룹니다. 현재 제공 여부는 [서버 가이드의 현재 단계](server-guide.md#현재-단계)에서 확인하세요.

| 문서 | 주제 |
|---|---|
| [01. 전체 개요와 이름 후보](specs/01-overview.md) | 전체 구성과 기술 선택 |
| [02. 룸과 세션](specs/02-rooms-and-sessions.md) | 번호 입장, private 비밀번호, 시작·퇴장·재접속 정책 |
| [03. 실시간 동기화](specs/03-realtime-sync.md) | 서버 계산과 snapshot, 향후 화면 보정 |
| [04. 메시지와 API](specs/04-messages-and-api.md) | 게임 메시지, 현재 조회 API와 후속 운영 API의 설계 |
| [05. 운영 대시보드](specs/05-operations-dashboard.md) | 현재 조회 화면·지표와 후속 상세·조작 |
| [06. 운영 조작과 서버 종료](specs/06-operations-and-shutdown.md) | 후속 kick·close·drain·audit 정책 |
| [07. 구현 구조와 설정](specs/07-implementation-and-config.md) | 목표 모듈과 구현 순서 |
| [08. 검증 기준](specs/08-validation.md) | 전체 목표의 검증 계획과 미측정 범위 |

## 설계 — 전체 계약과 모듈 경계

[Go 룸형 협동 게임 서버 전체 설계](go-room-server-design.md)는 기능별 스펙의 원본입니다.
서버 구조, actor와 queue, 게임·운영 API, 종료와 audit의 세부 계약 및 공식 자료를 담습니다.
이 문서도 목표 설계이며 현재 구현 완료 보고가 아닙니다.

## 실행 계획 — 작업 당시의 결정과 기록

| 문서 | 기록하는 작업 |
|---|---|
| [룸 입장 구현 계획](implementation/room-entry-plan.md) | guest·룸·비밀번호·WebSocket 입장 기능 구현 |
| [실시간 이동 구현 계획](implementation/realtime-movement-plan.md) | reference 3D 이동, tick·snapshot·ACK, 실제 6개 연결 검사 구현 |
| [운영 대시보드 구현 계획](implementation/operations-dashboard-plan.md) | room summary·collector·인증된 조회 API·운영 화면 구현 |
| [게임별 룸 분리 설계](implementation/game-isolation-design.md) | 연결 게임 선택·게임별 번호·운영 필터·전역 자원 경계 |
| [Godot client 설계](implementation/godot-client-design.md) / [구현 계획](implementation/godot-client-plan.md) | transport·lobby·공통 입력·3D/2D 표시와 검증 계약 |

실행 계획은 작업 당시 기준 commit, 선택한 계약, 작업 단계와 완료 기록을 보존합니다.
계획의 과거 범위나 로컬 검증 환경을 현재 배포 상태로 해석하지 마세요.
현재 실행 방법과 설정은 서버 가이드를 따릅니다.
