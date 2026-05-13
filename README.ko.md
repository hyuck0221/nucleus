# Nucleus

[English](README.md)

Nucleus는 macOS에서 사용하기 좋은 로컬 AI 오케스트레이터입니다. Ollama와 함께 동작하며, 로컬 LLM 모델을 OpenAI 호환 API로 제공하고, 대시보드에서 모델 상태와 API 사용 현황을 실시간으로 확인할 수 있습니다.

## 주요 기능

- Ollama 상태 확인, 모델 목록 조회, 모델 다운로드 명령
- OpenAI 호환 `GET /v1/models`, `POST /v1/chat/completions`
- `http://127.0.0.1:8787`에서 확인하는 실시간 대시보드
- 현재 호출 중인 사용자, 최근 사용자, 최근 사용 모델, 클라이언트 헤더, 응답 상태, 지연 시간 추적
- 모니터링 연동을 위한 SSE 이벤트 스트림 `/api/events`
- Ollama 라이브러리 pull과 Hugging Face GGUF 검색을 함께 제공하는 모델 다운로드 다이얼로그
- macOS LaunchAgent 또는 개발자 CLI로 사용하기 쉬운 단일 Go 바이너리

## 빠른 시작

```bash
brew install ollama
ollama serve

go run ./cmd/nucleus pull llama3.2
go run ./cmd/nucleus serve
```

브라우저에서 `http://127.0.0.1:8787`을 엽니다.

OpenAI 호환 클라이언트에서는 다음처럼 호출할 수 있습니다.

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-Nucleus-User: shim' \
  -H 'X-Nucleus-Client: opencode' \
  -d '{
    "model": "llama3.2",
    "messages": [{"role": "user", "content": "Say hello from local AI"}]
  }'
```

## CLI

```bash
nucleus serve
nucleus status
nucleus models
nucleus pull llama3.2
nucleus version
```

## Tailscale에서 호출하기

기본적으로 `nucleus serve`는 `0.0.0.0:8787`에 바인딩됩니다. 그래서 localhost, LAN, Tailscale 인터페이스에서 들어오는 요청을 받을 수 있습니다.

```bash
nucleus serve
```

이미 다른 방식으로 떠 있다면 먼저 기존 프로세스를 종료한 뒤 다시 실행합니다.

```bash
lsof -nP -iTCP:8787 -sTCP:LISTEN
kill <PID>
nucleus serve
```

macOS에서 현재 바인딩 상태는 다음처럼 확인할 수 있습니다.

```bash
lsof -nP -iTCP:8787 -sTCP:LISTEN
```

정상이라면 `*:8787` 또는 `0.0.0.0:8787` 형태로 표시됩니다.

같은 Mac 안에서만 쓰고 싶다면 다음처럼 실행합니다.

```bash
nucleus serve --addr 127.0.0.1:8787
```

그다음 Tailscale 쪽에서 다음처럼 호출합니다.

```bash
curl http://hshim.taila7bd14.ts.net:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-Nucleus-User: shim' \
  -H 'X-Nucleus-Client: opencode' \
  -d '{
    "model": "gemma4:e2b",
    "messages": [{"role": "user", "content": "Say hello from local AI"}]
  }'
```

연결이 계속 안 되면 macOS 방화벽에서 Nucleus 바이너리의 수신 연결 허용 여부와 Tailscale 연결 상태를 확인합니다.

## macOS DMG 설치

태그 릴리즈를 만들면 CLI tarball과 함께 설치용 DMG 파일이 release assets에 첨부됩니다.

- `Nucleus-<version>-darwin-arm64.dmg`
- `Nucleus-<version>-darwin-amd64.dmg`

DMG를 열고 `Nucleus.app`을 `Applications`로 드래그한 뒤 실행하면 됩니다. 앱은 기본값인 `0.0.0.0:8787`로 로컬 서버를 시작합니다.

릴리즈 DMG는 ad-hoc signing만 적용됩니다. Apple 정식 notarization은 Developer ID 인증서와 notarization 자격 증명이 필요합니다.

## macOS LaunchAgent

먼저 바이너리를 빌드한 뒤 안정적인 위치에 설치합니다.

```bash
go build -o /usr/local/bin/nucleus ./cmd/nucleus
```

`~/Library/LaunchAgents/com.nucleus.ai.plist` 파일을 만듭니다.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.nucleus.ai</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/nucleus</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/nucleus.log</string>
  <key>StandardErrorPath</key><string>/tmp/nucleus.err</string>
</dict>
</plist>
```

LaunchAgent를 로드합니다.

```bash
launchctl load ~/Library/LaunchAgents/com.nucleus.ai.plist
```

## 클라이언트 식별

Nucleus는 다음 헤더를 읽어 사용자를 식별합니다.

- `X-Nucleus-User`, `X-User`, `X-Forwarded-User`
- `X-Nucleus-Client` 또는 `User-Agent`

이 방식은 opencode 같은 외부 도구와 단순하게 연결하면서도, 대시보드에서 누가 어떤 모델을 사용하는지 확인할 수 있게 해줍니다.

## 모델 검색 추천

대시보드의 `Download model` 버튼을 누르면 모델 다운로드 다이얼로그가 열립니다. 이 안에서 Ollama 라이브러리 모델명을 직접 입력하거나, Hugging Face의 GGUF 모델을 검색해서 다운로드할 수 있습니다.

Ollama 입력창은 설치된 로컬 모델과 기본 추천 모델을 함께 보여줍니다. 예를 들어 `code`를 입력하면 설치된 코드 모델과 `qwen2.5-coder`, `codellama` 같은 후보를 추천합니다.

관련 API는 다음과 같습니다.

```bash
curl 'http://127.0.0.1:8787/api/model-suggestions?q=code'
curl 'http://127.0.0.1:8787/api/huggingface/models?q=llama%203.2'
```

## 릴리즈

GitHub에 `v*.*.*` 형식의 태그를 push하면 GitHub Actions가 macOS용 바이너리를 빌드하고 Release를 생성합니다.

```bash
git tag v0.1.0
git push origin v0.1.0
```
