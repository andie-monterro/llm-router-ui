# Dummy Model

`dummy-model` is a small standalone binary that serves an OpenAI-compatible endpoint backed by a fake model. It performs no real inference and returns deterministic responses, so you can test and demo the gateway without installing a real backend (Ollama, vLLM, llama-server, OpenAI, Anthropic) or relying on network egress.

It implements the same wire format the gateway expects from any OpenAI-compatible backend, so the gateway routes to it with no special configuration.

## Running

The binary is built alongside the gateway by `make build` (`go install ./...`):

```bash
dummy-model --listen :8081
```

By default it echoes the last user message back. Hit it directly:

```bash
curl -s localhost:8081/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"dummy","messages":[{"role":"user","content":"hello there"}]}'
```

```json
{"id":"chatcmpl-dummy-...","object":"chat.completion","model":"dummy",
 "choices":[{"index":0,"message":{"role":"assistant","content":"you said: hello there"},"finish_reason":"stop"}],
 "usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}
```

It also serves `GET /v1/models` (advertising the configured `--model` ids) and `GET /health`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `:8081` | Listen address. Defaults to `:8081` to avoid colliding with the gateway's `:8080`. |
| `--model` | `dummy` | Model id advertised by `/v1/models`. Repeatable; the first is the default for responses. |
| `--response` | _(empty)_ | Canned reply text. When empty, the server echoes the last user message. |
| `--stream-delay` | `0s` | Delay inserted between streamed chunks (e.g. `30ms`) to mimic real token-by-token output. |
| `--error-rate` | `0` | Fraction of chat requests to fail, in `[0,1]`. Useful for testing client retry/error handling. |
| `--error-type` | `rate_limit_error` | OpenAI error type to inject when a request fails (e.g. `rate_limit_error` → 429, `server_error` → 500). |

## Behaviors

**Streaming.** Send `"stream": true` and the reply is delivered as SSE chunks (a role chunk, then content chunks, then a final `finish_reason: "stop"`, then `data: [DONE]`), with `--stream-delay` between chunks.

**Tool calls.** When a request includes a `tools` array (and `tool_choice` isn't `"none"`), the server responds with a deterministic `tool_calls` reply naming the first offered tool with empty arguments (`{}`) and `finish_reason: "tool_calls"`, in both streaming and non-streaming form. This exercises the gateway's tool-calling translation path without a real model.

**Error injection.** With `--error-rate` above `0`, that fraction of chat requests return an OpenAI-shaped error of `--error-type` before producing any output.

## Using it as a gateway backend

The gateway routes any model that isn't `gpt-*`/`o1-*`/`o3-*`/`claude-*` to its `local` provider, which calls `<base_url>/v1/chat/completions`. Point `local.base_url` at the dummy:

```yaml
listen: ":8080"
providers:
  local:
    base_url: "http://localhost:8081"
```

Run the dummy, run the gateway against that config, and any non-OpenAI/Anthropic model name sent to the gateway's `/v1/chat/completions` is served by the dummy.
