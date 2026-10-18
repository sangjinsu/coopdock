# Docker로 실행하기

Docker Compose로 CoopDock 서버를 빌드하고 실행합니다. 호스트에 Go를 설치할 필요는 없습니다. Godot 예제는 호스트에서 별도로 실행합니다.

## 준비

Docker Engine과 **Docker Compose 2.24.0 이상**이 필요합니다. Docker daemon이 실행 중인지 확인하세요. Compose 최소 버전은 `.env` 파일을 선택적으로 읽는 설정에 필요합니다.

```bash
docker version
docker compose version
```

저장소가 없다면 복제한 뒤 루트로 이동합니다.

```bash
git clone https://github.com/sangjinsu/coopdock.git
cd coopdock
```

## 빠른 시작

저장소 루트에서 실행하세요. 기본 실행에는 `.env` 파일을 만들 필요가 없습니다.

```bash
docker compose up -d --build
docker compose ps
curl -f http://127.0.0.1:8080/readyz
```

`coopdock` 서비스가 로컬 이미지 `coopdock:local`을 빌드합니다. 기본 접속 주소는 `127.0.0.1:8080`, 게임 ID는 `default`입니다. 운영 대시보드는 기본으로 꺼져 있습니다.

처음 빌드할 때는 base image와 Go 의존성을 내려받습니다. 컨테이너가 시작된 직후 health 상태는 `starting`일 수 있습니다. `/readyz`가 HTTP 200을 반환하고 `docker compose ps`에 `healthy`가 표시되는지 확인하세요. `/readyz`의 정상 응답 본문은 비어 있으므로 `curl -f`에 출력이 없는 것이 정상입니다. 연결 오류가 나면 잠시 뒤 다시 확인합니다.

시작 명령에서 health 확인까지 기다리려면 다음을 사용할 수 있습니다.

```bash
docker compose up --build --wait --wait-timeout 120
```

`--wait`는 백그라운드 실행을 포함합니다. `/healthz`도 HTTP 200과 빈 본문을 반환하며, 신규 연결 수용 여부는 `/readyz`로 확인합니다. health 성공은 게임 부하나 Godot 화면 동기화 검증을 뜻하지 않습니다.

## Godot에서 연결하기

같은 컴퓨터의 [Godot 예제](../examples/godot/README.md)에서 다음 두 값을 따로 입력합니다.

| 입력 | 기본값 |
|---|---|
| 서버 주소 | `ws://127.0.0.1:8080/ws` |
| 개발용 게임 ID | `default` |

주소 입력란에는 query 없는 base URL을 사용합니다. adapter가 게임 ID query를 붙입니다. 호스트에 Go가 있다면 추가로 [실제 6개 WebSocket 연결 검사](getting-started.md#4-여섯-연결로-검사하기)를 실행할 수 있습니다.

## 설정 바꾸기

설정이 필요할 때만 예제 파일을 복사해 `.env`를 편집합니다. `.env`는 로컬 설정 파일이며 Git에 포함하지 않습니다.

```bash
cp .env.example .env
```

이미 `.env`가 있다면 기존 파일을 편집하세요. 변경한 설정은 다음 명령으로 반영합니다. 설정 변경으로 컨테이너가 재생성되면 진행 중인 연결과 메모리의 룸 상태가 사라집니다.

```bash
docker compose up -d
docker compose ps
```

### 호스트 포트 바꾸기

8080 포트를 다른 프로그램이 사용 중이면 `.env`의 다음 값을 변경합니다.

```dotenv
COOPDOCK_PORT=19080
```

다시 `docker compose up -d`를 실행한 뒤 `http://127.0.0.1:19080/readyz`, `ws://127.0.0.1:19080/ws`를 사용합니다. 컨테이너 내부는 계속 `0.0.0.0:8080`을 사용하므로 `HTTP_ADDR`나 Compose의 컨테이너 포트를 바꿀 필요가 없습니다. 일회성 shell 환경변수로 포트를 지정하면 후속 Compose 명령에도 같은 값이 필요하므로 `.env` 사용을 권장합니다.

### 여러 게임 등록하기

`.env`에서 허용할 게임 ID를 쉼표로 나눕니다.

```dotenv
GAME_IDS=game-a,game-b
```

Godot 주소는 그대로 두고 게임 ID에 `game-a` 또는 `game-b`를 입력합니다. 일반 WebSocket client는 `ws://127.0.0.1:8080/ws?game_id=game-a`처럼 게임을 지정해야 합니다. 룸 생성·목록·번호 입장은 연결한 게임 안에서 처리됩니다. 게임 ID는 공개 namespace이며 인증 정보가 아닙니다. ID 형식과 공유 자원 경계는 [서버 가이드](server-guide.md)에 있습니다.

### 다른 컴퓨터에서 연결하기

기본 포트는 호스트의 loopback에만 공개됩니다. LAN에서 연결하려면 `.env`를 다음과 같이 바꾸고 `docker compose up -d`를 실행합니다.

```dotenv
COOPDOCK_BIND_IP=0.0.0.0
```

다른 컴퓨터의 Godot 주소에는 Docker 호스트의 실제 LAN IP를 넣습니다. 예를 들어 호스트가 `192.168.0.20`이면 `ws://192.168.0.20:8080/ws`를 사용합니다. 포트를 변경했다면 그 포트도 반영하세요. 호스트 방화벽에서 해당 포트의 접근도 허용해야 합니다.

이 구성은 plain HTTP/WS입니다. 외부 운영의 HTTPS/WSS와 reverse proxy 요건은 [서버 가이드의 운영 인증 안내](server-guide.md#운영-대시보드-켜기)를 따르세요.

### 운영 대시보드와 추가 환경변수

운영 대시보드가 필요하면 `.env`의 `ADMIN_VIEWER_TOKEN`에 **32~256 bytes의 공백 없는 printable ASCII (`0x21..0x7e`)** 값을 설정하고 `docker compose up -d`로 반영합니다. 미설정 또는 빈 값이면 admin 경로는 404입니다. 활성화한 뒤 `http://127.0.0.1:8080/admin`에서 HTTP Basic username `viewer`, password로 설정한 token을 사용합니다. token은 URL이나 로그에 넣지 마세요. 포트를 바꿨다면 대시보드 주소도 바꿉니다.

나머지 서버 환경변수는 [설정 표](server-guide.md#설정)를 참고해 `.env`에 추가할 수 있습니다. Compose는 컨테이너의 `HTTP_ADDR=0.0.0.0:8080`을 고정합니다. `WS_ORIGINS`는 기본으로 설정하지 않으며, native Godot 연결은 이 상태로 사용할 수 있습니다. 브라우저의 추가 Origin을 허용할 때만 예제 파일의 주석을 풀고 유효한 host 또는 host:port를 지정하세요. **`WS_ORIGINS=`처럼 빈 값으로 명시하면 서버가 시작을 거절합니다.** 인증 및 Origin 정책은 [서버 가이드](server-guide.md#설정)를 따릅니다.

## 로그, 종료와 다시 빌드하기

로그를 계속 확인합니다. 여기서 `Ctrl+C`는 로그 보기만 끝내며 서버는 계속 실행됩니다.

```bash
docker compose logs -f coopdock
```

서버를 중지하거나, 컨테이너와 Compose 네트워크까지 제거합니다.

```bash
docker compose stop
docker compose down
```

두 명령 중 필요한 것을 사용하면 됩니다. Compose는 `SIGTERM`을 보내고 최대 30초 뒤에도 종료되지 않으면 강제 종료합니다. 앱의 기본 종료 timeout은 10초이며, Compose의 30초는 앱 timeout을 늘리지 않습니다. `WS_SHUTDOWN_TIMEOUT` 등 앱 종료 timeout을 늘렸다면 `compose.yaml`의 `stop_grace_period`도 그보다 길게 조정하세요. 종료 동작은 [종료와 검증](server-guide.md#종료와-검증)에 설명되어 있습니다.

Compose의 `restart: unless-stopped` 정책은 예기치 않은 종료 뒤 서버를 다시 시작합니다. 직접 중지한 서비스는 `docker compose up -d`로 다시 실행합니다.

소스를 업데이트한 뒤에는 다시 빌드합니다.

```bash
docker compose up -d --build
```

룸·비밀번호 자료·membership·게임 상태는 메모리에만 있습니다. 컨테이너를 중지하거나 재시작하면 사라지며 이 구성에는 영속 volume이 없습니다.

## Compose 없이 실행하기

Dockerfile만으로도 기본 서버를 실행할 수 있습니다. Compose 서버가 같은 포트를 사용 중이면 먼저 `docker compose down`으로 종료하세요.

```bash
docker build -t coopdock:local .
docker run --rm -p 127.0.0.1:8080:8080 coopdock:local
```

서버는 터미널에서 실행되며 `Ctrl+C`로 종료합니다. 다른 터미널에서 `/readyz`를 확인하거나 Godot으로 연결할 수 있습니다. 이 명령은 Compose의 `.env`를 자동으로 읽지 않습니다.
