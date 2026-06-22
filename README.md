<p align="center">
  <img src="assets/icons/app-icon.png" alt="Nucleus logo" width="128">
</p>

<h1 align="center">Nucleus</h1>

<p align="center">
  Run local LLMs on macOS, expose them through an OpenAI-compatible API, and monitor everything from one dashboard.
</p>

<p align="center">
  <a href="README.ko.md">한국어</a>
</p>

---

## What Nucleus does

Nucleus is a macOS app for people who want one place to:

- install and run local models through Ollama
- provide an OpenAI-compatible API for other tools
- monitor who is using which model in real time
- manage models, update checks, and usage history from a dashboard

Open the app, choose a model, and use it locally or from another machine on your network.

## 1. Install

### Install Ollama first

Nucleus uses Ollama for downloaded local models, so install and run it when you want to use Ollama models. Antigravity CLI chat can operate independently.

```bash
brew install ollama
ollama serve
```

To expose Antigravity CLI through the chat API too, install it locally and sign in once. Nucleus launches the local `agy` process; it does not call a model provider API directly.

```bash
curl -fsSL https://antigravity.google/cli/install.sh | bash
agy
```

### Install Nucleus on macOS

1. Download the latest DMG from GitHub Releases.
2. Open the DMG.
3. Drag `Nucleus.app` into `Applications`.
4. Launch the app.

Because Nucleus is distributed through GitHub and not Apple notarization, macOS may block the first launch. If you trust the release, remove the quarantine attribute and open it again:

```bash
xattr -dr com.apple.quarantine /Applications/Nucleus.app
open /Applications/Nucleus.app
```

When the app opens, the local server starts automatically and the dashboard appears in a macOS window.

## 2. Use the dashboard

The dashboard is the easiest way to operate Nucleus.

- Download models from the Ollama library
- Search and install GGUF models from Hugging Face
- See download progress directly in the model list
- Delete models with confirmation
- Copy model names with one click
- Review active requests and force-stop them when needed
- Inspect API usage history, success or failure status, timestamps, and client information
- Clear usage records manually or configure automatic cleanup in Settings

The app serves the dashboard and API on:

```text
http://127.0.0.1:8787
```

By default, the server listens on `0.0.0.0:8787`, so LAN and Tailscale access can also work when the machine allows inbound traffic.

## 3. Call the API

Nucleus exposes OpenAI-compatible chat and image generation endpoints:

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

Useful endpoints:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/images/generations`

For live token streaming, send:

```json
{
  "stream": true
}
```

Use `antigravity-cli` as the model ID to run Antigravity CLI's default model. Models reported by `agy models` are also exposed as `antigravity-cli/<model-name>`.

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"antigravity-cli","messages":[{"role":"user","content":"Say hello"}]}'
```

Antigravity models are listed only when the `agy` executable is installed. Requests return `503 Service Unavailable` when it is missing. This route is text-chat only: OpenAI tool requests and file/image content are rejected. Each request runs non-interactively with `--sandbox` in an empty temporary workspace, and `@file` expansion is escaped. If `agy` is not on the app's `PATH`, set `ANTIGRAVITY_CLI_PATH` or use `--antigravity-command /path/to/agy`.

Image generation uses Ollama image generation models installed locally, such as `x/z-image-turbo` or `x/flux2-klein`:

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

For long image generations, avoid client or proxy timeouts by requesting an async job:

```bash
curl 'http://127.0.0.1:8787/v1/images/generations?async=true' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "x/z-image-turbo",
    "prompt": "A detailed architectural concept render",
    "size": "1024x1024",
    "response_format": "b64_json"
  }'
```

The response includes a `url` such as `/api/images/generations/{id}`. Poll that URL until `status` is `done` or `error`.

The dashboard records:

- user name from `X-Nucleus-User`, `X-User`, or `X-Forwarded-User`
- client name from `X-Nucleus-Client` or `User-Agent`

That makes remote usage visible without requiring a separate analytics stack.

## 4. Connect OpenCode

The simplest route is the built-in export flow:

1. Open Nucleus.
2. Choose `Export`.
3. Select `CLI .json config file export`.
4. Pick `OpenCode`.
5. Choose the Nucleus address you want to use.
6. Download `opencode.json`.
7. Place it in the OpenCode project root.

Nucleus generates an OpenAI-compatible provider config using the detected local or Tailscale address.

OpenCode supports custom providers through `@ai-sdk/openai-compatible` and a custom `baseURL`, which is exactly what Nucleus exports.

## 5. Connect OpenClaw

Nucleus also exports an OpenClaw provider file:

1. Open Nucleus.
2. Choose `Export`.
3. Select `CLI .json config file export`.
4. Pick `OpenClaw`.
5. Choose the Nucleus address.
6. Download `models.json`.
7. Merge that provider block into your OpenClaw model configuration.

The generated file uses a custom provider with:

- `baseUrl` pointing to Nucleus
- `api: "openai-completions"`
- the locally installed Nucleus models

OpenClaw documents custom OpenAI-compatible local proxies in this style. Its docs also note that proxy-style OpenAI routes differ from native provider integrations, especially around advanced runtime behavior and some tool-calling expectations.

## 6. Helpful notes

### Tailscale access

If you want to call Nucleus from another device over Tailscale:

1. Keep Nucleus running.
2. Confirm it is listening on `0.0.0.0:8787`.
3. Use your Tailscale DNS name or Tailscale IP.

Example:

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

If it does not connect:

- check that Nucleus is running
- check macOS firewall rules
- confirm Tailscale connectivity
- verify that port `8787` is listening

```bash
lsof -nP -iTCP:8787 -sTCP:LISTEN
```

### Model runtimes

Ollama runs downloaded local models, while the optional Antigravity integration runs the local `agy` executable. If either runtime is unavailable, its models are omitted while the other runtime can continue serving chat.

### Settings worth knowing

In Settings, you can:

- launch Nucleus automatically at login
- check for new versions
- install an update from inside the app
- automatically delete older API usage history after a retention period

## License

MIT License. See `LICENSE`.
