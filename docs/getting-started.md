# 빠른 시작

CoopDock 서버를 로컬에서 실행하고, 실제 WebSocket 연결로 룸 입장과 3D 이동을 확인합니다.
아래 Go 검증 CLI로 연결을 확인하고, [Godot 3D/2D 예제](../examples/godot/README.md)로 같은 서버에 게임 화면을 연결할 수 있습니다.

[문서 길잡이](README.md) · [서버 사용·운영 가이드](server-guide.md)

## 1. 준비하기

Go **1.26 이상**과 Git을 준비합니다. 저장소를 받을 상위 폴더에서 다음을 실행합니다.
이미 저장소를 내려받았다면 `git clone`을 생략하고 저장소 루트로 이동합니다.

```bash
git clone https://github.com/sangjinsu/coopdock.git
cd coopdock
go version
```

Go module 이름은 내부 식별자인 `roomserver`입니다.
첫 Go 명령 실행 시 `go.mod`에 고정된 dependency를 내려받으므로 네트워크 연결이 필요할 수 있습니다.

## 2. 서버 실행하기

```bash
go run ./cmd/server
```

기본 주소는 `127.0.0.1:8080`입니다. 서버가 시작되면 다음 로그가 표시됩니다.
앞에는 Go logger의 날짜·시각이 붙습니다.

```text
Room entry server listening on 127.0.0.1:8080
```

포트를 바꾸려면 환경변수를 함께 지정합니다.

```bash
HTTP_ADDR=127.0.0.1:9090 go run ./cmd/server
```

## 3. 상태 확인하기

서버를 켜 둔 채 다른 터미널에서 실행합니다.

```bash
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

정상 실행 중에는 두 경로 모두 HTTP 200을 반환하며 본문은 비어 있습니다.
`readyz`는 서버 종료가 시작되면 HTTP 503을 반환합니다.
포트를 바꿨다면 URL도 함께 바꿉니다.

## 4. 여섯 연결로 검사하기

다른 터미널에서 저장소 루트로 이동한 뒤 실행합니다.

```bash
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -movement -timeout 30s
```

CLI는 임시 룸을 만들고 public/private 입장, 7번째 입장 거절, 비밀번호 확인,
private 목록 제외와 마지막 자리 동시 입장을 검사합니다.
이어 별도 6개 연결로 시작·이동 ACK·이동 상태·정지 ACK·정지 상태를 비교합니다.
검사 후 퇴장을 확인하고 연결을 닫습니다. 모두 통과하면 `PASS:` 결과를 출력하며,
연결이나 assertion이 실패하면 exit 1입니다. 부하 시험과 Godot 화면 검사는 포함하지 않습니다.

입장 검사만 실행하려면 다음을 사용합니다.

```bash
go run ./cmd/roomcheck -url ws://127.0.0.1:8080/ws -movement=false -race-runs 3 -timeout 30s
```

검사 옵션과 정확한 비교 범위는 [서버 가이드의 종료와 검증](server-guide.md#종료와-검증)에 있습니다.

## 5. 종료하고 다음 단계 살펴보기

서버 터미널에서 `Ctrl+C`를 누르면 새 연결 수용을 닫고 기존 연결과 룸을 정리합니다.
모든 상태는 메모리에 있어 다음 실행 때 이어지지 않습니다.

클라이언트 연결은 [실제 WebSocket 메시지](server-guide.md#실제-websocket-메시지)부터 확인합니다.
Godot **4.4 API 기준**의 custom WebSocket + JSON 예제가 있으며 실제 실행 검증 엔진은 **4.7.2**입니다.
[예제 안내](../examples/godot/README.md)를 따라 `examples/godot/project.godot`을 열어 두 창에서 같은 게임 ID와 룸으로 연결하세요.
서버의 기본 게임은 `default`이며 다른 게임을 쓰려면 `GAME_IDS=default,game-a`로 등록합니다. Godot에는 query 없는 기본 URL과 게임 ID를 각각 입력합니다.
브라우저 Origin 허용과 외부 WSS 연결, timeout, 종료 특성은 [서버 가이드](server-guide.md)에서 확인하세요.
