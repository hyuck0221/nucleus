# Nucleus

[한국어](README.ko.md)

Nucleus is a macOS-friendly local AI orchestrator. It runs beside Ollama, exposes an OpenAI-compatible API, and provides a live dashboard for local model state and API usage.

## Features

- Ollama status, model listing, and model pull commands
- OpenAI-compatible `GET /v1/models` and `POST /v1/chat/completions`
- Real-time dashboard at `http://127.0.0.1:8787`
- Tracks active callers, recent users, recent models, client headers, status, and latency
- SSE event stream at `/api/events` for monitoring integrations
- Model download dialog with Ollama library pull and Hugging Face GGUF search
- Single Go binary suitable for macOS LaunchAgent or developer CLI usage

## Quick Start

```bash
brew install ollama
ollama serve

go run ./cmd/nucleus pull llama3.2
go run ./cmd/nucleus serve
```

Open `http://127.0.0.1:8787`.

Call it from any OpenAI-compatible client:

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

## Tailscale Access

By default, `nucleus serve` listens on `0.0.0.0:8787`, so it accepts requests through localhost, LAN, and Tailscale interfaces.

```bash
nucleus serve
```

Check the active listener:

```bash
lsof -nP -iTCP:8787 -sTCP:LISTEN
```

It should show `*:8787` or `0.0.0.0:8787`.

For local-only access, run:

```bash
nucleus serve --addr 127.0.0.1:8787
```

## macOS DMG

Tagged releases attach both CLI tarballs and installable DMG files:

- `Nucleus-<version>-darwin-arm64.dmg`
- `Nucleus-<version>-darwin-amd64.dmg`

Open the DMG, drag `Nucleus.app` to `Applications`, then launch it. The app starts the local server with the default `0.0.0.0:8787` listener.

Release DMGs are signed and notarized in GitHub Actions. Configure these repository secrets before pushing a release tag:

- `APPLE_DEVELOPER_ID_CERTIFICATE_BASE64`: base64-encoded Developer ID Application `.p12`
- `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`: `.p12` password
- `APPLE_NOTARY_KEY`: App Store Connect API key `.p8` contents
- `APPLE_NOTARY_KEY_ID`: App Store Connect key ID
- `APPLE_NOTARY_ISSUER_ID`: App Store Connect issuer ID
- `APPLE_BUILD_KEYCHAIN_PASSWORD`: temporary CI keychain password

Without these secrets, the release workflow fails instead of publishing a DMG that macOS Gatekeeper would block.

## macOS LaunchAgent

Build and install the binary somewhere stable:

```bash
go build -o /usr/local/bin/nucleus ./cmd/nucleus
```

Create `~/Library/LaunchAgents/com.nucleus.ai.plist`:

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

Then load it:

```bash
launchctl load ~/Library/LaunchAgents/com.nucleus.ai.plist
```

## Client Identity

Nucleus records identity from these headers:

- `X-Nucleus-User`, `X-User`, or `X-Forwarded-User`
- `X-Nucleus-Client` or `User-Agent`

This keeps the API simple for tools like opencode while still making dashboard usage visible.

## Model Downloads

Use the dashboard `Download model` button to pull models from the Ollama library or search Hugging Face GGUF repositories. Hugging Face results are downloaded through Ollama using names like `hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF`.
