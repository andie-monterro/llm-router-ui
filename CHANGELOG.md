# CHANGELOG

## Unreleased

ADD: Agora integration for catalog advertisements, gateway serving over Agora Layer 1 tunnels, and provider connects through Agora services.

ADD: `--network=agora`, `--agora-integration-file`, and `AGORA_LLM_GATEWAY_INTEGRATION_FILE` support for Agora deployments.

CHANGE: zrok sharing and Agora serving are additive to the local HTTP listener.

## v0.1.4

CHANGE: Removed specificity in docs and implementation tying local instances to Ollama; we really support any local OpenAI-compatible inference. Works with Ollama, llama-server, vLLM, SGLang, or anything that exposes `/v1/chat/completions`.

FIX: Improved ziti context handling to eliminate an issue with leaks on long-running instances. (https://github.com/openziti/llm-gateway/issues/7)

## v0.1.3

FIX: Release attestation changes.

## v0.1.2

FIX: Fix for `draft-release` action; unauthenticated `curl` error.

## v0.1.1

Initial public release.
