# CHANGELOG

## Unreleased

FEATURE: Full bidirectional tool-calling translation for the Anthropic provider, including streaming. OpenAI `tools` and `tool_choice` now map to Anthropic `tools`/`input_schema` and `tool_choice`; Anthropic `tool_use` responses map back to OpenAI `tool_calls` (with `finish_reason: "tool_calls"`); multi-turn `tool` messages round-trip as coalesced Anthropic `tool_result` blocks; and streamed tool calls are assembled from `content_block_start`/`input_json_delta` events into OpenAI-shaped tool-call deltas.

FIX: The Anthropic provider previously dropped `tools` and `tool_choice` entirely and flattened assistant `tool_calls` and `tool` results into plain text, so MCP/tool-calling clients never received their tools and Claude replied with prose instead of tool calls.

## v0.1.4

CHANGE: Removed specificity in docs and implementation tying local instances to Ollama; we really support any local OpenAI-compatible inference. Works with Ollama, llama-server, vLLM, SGLang, or anything that exposes `/v1/chat/completions`.

FIX: Improved ziti context handling to eliminate an issue with leaks on long-running instances. (https://github.com/openziti/llm-gateway/issues/7)

## v0.1.3

FIX: Release attestation changes.

## v0.1.2

FIX: Fix for `draft-release` action; unauthenticated `curl` error.

## v0.1.1

Initial public release.
