<p align="center">
  <img src="assets/icons/app-icon.png" alt="Nucleus logo" width="128">
</p>

<h1 align="center">Nucleus</h1>

<p align="center">
  macOS에서 로컬 LLM을 실행하고, OpenAI 호환 API로 외부에 제공하며, 사용 현황까지 한 화면에서 관리하는 로컬 AI 오케스트레이터
</p>

<p align="center">
  <a href="README.md">English</a>
</p>

---

## Nucleus로 할 수 있는 것

Nucleus는 다음 흐름을 한 앱 안에서 처리합니다.

- Ollama 기반 로컬 모델 설치와 실행
- 외부 프로그램이 호출할 수 있는 OpenAI 호환 API 제공
- 누가 어떤 모델을 쓰는지 실시간 모니터링
- 모델 다운로드, 삭제, 업데이트 확인, 사용 기록 관리

앱을 켜고 모델을 준비하면, 같은 Mac 안에서도 쓰고 다른 장치에서도 호출할 수 있습니다.

## 1. 설치

### 먼저 Ollama 설치

Nucleus는 Ollama를 로컬 모델 런타임으로 사용합니다. 따라서 Ollama가 먼저 설치되어 있고 실행 중이어야 합니다.

```bash
brew install ollama
ollama serve
```

### macOS에 Nucleus 설치

1. GitHub Releases에서 최신 DMG를 다운로드합니다.
2. DMG를 엽니다.
3. `Nucleus.app`을 `Applications`로 드래그합니다.
4. 앱을 실행합니다.

Nucleus는 GitHub 배포만 사용하고 Apple notarization은 적용하지 않기 때문에, 첫 실행에서 macOS가 차단할 수 있습니다. 릴리즈를 신뢰한다면 quarantine 속성을 제거한 뒤 다시 엽니다.

```bash
xattr -dr com.apple.quarantine /Applications/Nucleus.app
open /Applications/Nucleus.app
```

앱이 열리면 로컬 서버도 함께 시작되고, 대시보드가 macOS 창으로 표시됩니다.

## 2. 대시보드 사용

대시보드에서 대부분의 작업을 처리할 수 있습니다.

- Ollama 라이브러리 모델 다운로드
- Hugging Face GGUF 모델 검색 및 설치
- 설치 진행률을 모델 목록에서 바로 확인
- 확인 메시지와 함께 모델 삭제
- 모델 이름 클릭 시 즉시 복사
- 현재 실행 중인 요청 확인 및 강제 중지
- API 사용 기록의 성공/실패, 시간, 클라이언트 확인
- 사용 기록 수동 삭제 또는 자동 정리 설정

기본 접속 주소는 다음과 같습니다.

```text
http://127.0.0.1:8787
```

기본적으로 서버는 `0.0.0.0:8787`에 바인딩되므로, 네트워크와 방화벽 조건이 맞으면 LAN이나 Tailscale에서도 접근할 수 있습니다.

## 3. API 호출

Nucleus는 OpenAI 호환 채팅 및 이미지 생성 API를 제공합니다.

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-Nucleus-User: tester' \
  -H 'X-Nucleus-Client: curl' \
  -d '{
    "model": "llama3.2",
    "messages": [
      {"role": "user", "content": "Say hello from local AI"}
    ]
  }'
```

주요 엔드포인트:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/images/generations`

실시간 스트리밍 응답이 필요하면 요청 본문에 아래 값을 추가합니다.

```json
{
  "stream": true
}
```

이미지 생성은 `x/z-image-turbo`, `x/flux2-klein`처럼 로컬 Ollama에 설치된 이미지 생성 모델을 사용합니다.

```bash
curl http://127.0.0.1:8787/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "x/z-image-turbo",
    "prompt": "A cute robot learning to paint",
    "size": "1024x1024",
    "response_format": "b64_json"
  }'
```

대시보드는 다음 헤더를 읽어 호출 정보를 표시합니다.

- 사용자: `X-Nucleus-User`, `X-User`, `X-Forwarded-User`
- 클라이언트: `X-Nucleus-Client` 또는 `User-Agent`

이렇게 호출하면 별도 분석 도구 없이도 누가 어떤 모델을 쓰는지 화면에서 바로 확인할 수 있습니다.

## 4. OpenCode 연결

가장 쉬운 방법은 Nucleus가 직접 설정 파일을 만들어주는 내보내기 기능을 사용하는 것입니다.

1. Nucleus를 엽니다.
2. `Export`를 누릅니다.
3. `CLI .json config file export`를 선택합니다.
4. `OpenCode`를 선택합니다.
5. 사용할 Nucleus 주소를 고릅니다.
6. `opencode.json`을 다운로드합니다.
7. 해당 파일을 OpenCode 프로젝트 루트에 둡니다.

Nucleus는 로컬 주소나 Tailscale 주소를 반영한 OpenAI 호환 provider 설정을 생성합니다.

OpenCode는 `@ai-sdk/openai-compatible`와 사용자 지정 `baseURL`을 통해 이런 연결 방식을 지원합니다.

## 5. OpenClaw 연결

OpenClaw도 대시보드에서 전용 설정 파일을 만들 수 있습니다.

1. Nucleus를 엽니다.
2. `Export`를 누릅니다.
3. `CLI .json config file export`를 선택합니다.
4. `OpenClaw`를 선택합니다.
5. 사용할 Nucleus 주소를 고릅니다.
6. `models.json`을 다운로드합니다.
7. 그 provider 블록을 OpenClaw 모델 설정에 합칩니다.

생성되는 설정은 다음 방향을 따릅니다.

- `baseUrl`은 Nucleus 주소
- `api: "openai-completions"`
- 현재 설치된 Nucleus 모델 목록 포함

OpenClaw 공식 문서도 커스텀 OpenAI 호환 로컬 프록시를 이런 방식으로 연결하는 흐름을 설명합니다. 다만 이런 프록시형 연결은 일부 고급 동작이나 tool-calling 기대치가 네이티브 provider와 다를 수 있습니다.

## 6. 참고사항

### Tailscale로 외부 호출

다른 장치에서 Tailscale로 Nucleus를 호출하려면:

1. Nucleus가 실행 중인지 확인합니다.
2. `0.0.0.0:8787`에 바인딩되어 있는지 확인합니다.
3. Tailscale DNS 이름이나 Tailscale IP를 사용합니다.

예시:

```bash
curl http://your-mac.tailnet-name.ts.net:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-Nucleus-User: tester' \
  -H 'X-Nucleus-Client: remote-cli' \
  -d '{
    "model": "llama3.2",
    "messages": [
      {"role": "user", "content": "Hello from another machine"}
    ]
  }'
```

연결이 안 되면 다음을 확인합니다.

- Nucleus가 실제로 실행 중인지
- macOS 방화벽이 수신 연결을 막고 있지 않은지
- Tailscale 연결이 정상인지
- `8787` 포트가 열려 있는지

```bash
lsof -nP -iTCP:8787 -sTCP:LISTEN
```

### Ollama는 필수

Nucleus는 모델을 관리하고 API를 제공하지만, 실제 모델 실행은 Ollama가 담당합니다. Ollama가 꺼져 있으면 모델 목록 조회와 추론이 동작하지 않습니다.

### 알아두면 좋은 설정

Settings에서 다음을 관리할 수 있습니다.

- 로그인 시 Nucleus 자동 실행
- 새 버전 자동 확인
- 앱 안에서 업데이트 설치
- 오래된 API 사용 기록 자동 삭제

## 라이선스

MIT License. 자세한 내용은 `LICENSE`를 참고하세요.
