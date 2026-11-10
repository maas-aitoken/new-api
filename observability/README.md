# OpenTelemetry and Langfuse

new-api exports relay traces directly to a Langfuse OTLP/HTTP endpoint. The
feature is disabled unless `NEW_API_OTEL_ENABLED=true`.

## Configuration

```text
NEW_API_OTEL_ENABLED=true
LANGFUSE_BASE_URL=http://langfuse:3000
LANGFUSE_PROJECT_ID=your-project-id
LANGFUSE_PUBLIC_KEY=pk-...
LANGFUSE_SECRET_KEY=sk-...
NEW_API_OTEL_CAPTURE_MODE=error
NEW_API_OTEL_CAPTURE_MAX_BYTES=4194304
```

The exporter posts to `${LANGFUSE_BASE_URL}/api/public/otel/v1/traces`. An
explicit `OTEL_EXPORTER_OTLP_ENDPOINT` takes precedence over the Langfuse base
URL; `LANGFUSE_HOST` is accepted as a backwards-compatible alias. The endpoint
may point to a collector or another OTLP/HTTP backend. `OTEL_EXPORTER_OTLP_HEADERS`
can add custom headers, while Langfuse credentials add the Basic Auth and
ingestion-version headers automatically.

`NEW_API_OTEL_CAPTURE_MODE` controls serialized model content on the trace:

- `full` records input and output for every completed request.
- `error` records input and semantic model output only when the final gateway
  result is an error. A request that succeeds after an upstream retry does not
  export content.
- `off` records no input/output content.

The setting defaults to `off` when absent or invalid.

The capture mode is also editable at runtime from the admin console under
Operations Settings, Log Maintenance, as `OtelCaptureMode`. A console value is
persisted and takes precedence over `NEW_API_OTEL_CAPTURE_MODE`, and it applies
to every subsequent request without a restart. The env var remains the startup
default when no console value has been saved.

Input and output remain bounded independently by
`NEW_API_OTEL_CAPTURE_MAX_BYTES`. Stream frames are aggregated in memory and
never exported individually, including in `error` mode. The mode reduces
exported content, not the peak per-request capture memory.

Provider error envelopes and response metadata are never exported as model
output. On an error, only already-generated semantic assistant content is
eligible for output capture.

Besides the conversation context, the trace also carries request parameters
under the same capture mode gating:

- `gen_ai.input.value` holds the client's request body minus its
  conversation-context fields (`messages`/`tools`, plus `system` for Claude,
  `instructions`/`input` for Responses, and `contents`/`systemInstruction` for
  Gemini). It is taken from the raw client body before relay conversion, so
  unknown fields survive DTO round-trips.
- `new_api.request.upstream` holds the final upstream request body after the
  same strip, so parameter rewrites applied by the gateway (param overrides,
  model mapping, reasoning-effort conversion) are visible by diffing the two.

Both are plain JSON (no compression), include `metadata`/`user` fields as-is,
follow the `full`/`error`/`off` capture mode exactly like message content, and
are bounded by `NEW_API_OTEL_CAPTURE_MAX_BYTES`; an oversize value is dropped
and only the `new_api.capture.params_truncated` marker is exported. Formats
not listed above (embeddings, rerank, images, audio, realtime and the rest)
have no projection and export neither.

Each upstream protocol has its own aggregator behind the `streamAggregator`
interface in `stream_aggregator.go`, one per file. The first frame that an
aggregator recognizes decides which one owns the stream, so supporting another
provider means adding a `stream_aggregator_<protocol>.go` and one entry in the
`streamAggregators` registry, ahead of the loose OpenAI chat shape.

A registry entry lists the upstream API types known to speak its protocol, and
those aggregators are offered the first frame first. The list is only an
ordering hint: a frame that none of the preferred aggregators recognizes is
still offered to the rest, so a custom or aggregating channel proxying a
protocol its API type does not advertise keeps its output.

An OpenAI Responses stream is aggregated by the same
`relayconvert.ResponsesBufferedAccumulator` the buffered Responses-to-Chat
converter uses, so text, reasoning and tool calls are all collected. The trace
then receives the terminal frame verbatim. Only when that frame carries an empty
`output` array is the array replaced by the aggregated output, leaving every
other terminal field untouched; the rebuilt items come from `dto.ResponsesOutput`
and therefore carry its empty scalar keys. A stream that never reaches a
terminal event exports the aggregated output array on its own, because a partial
answer is worth more on the trace than none.

A Claude Messages stream is replayed through `relayconvert.StreamResponseClaude2OpenAI`
and `relayconvert.FormatClaudeResponseInfo`, the same converters the relay path
uses to serve a Claude upstream in OpenAI format, so the trace and the client
read the stream through one implementation. The per-frame deltas are folded back
into a single assistant message — text, reasoning and tool calls, with the
argument fragments of each tool call joined under the content block index that
carries them — and exported in the same
`[{"role":"assistant","content":"..."}]` shape as every other chat upstream.

A Gemini generateContent stream is replayed frame by frame through
`relayconvert.StreamResponseGeminiChat2OpenAI`, the converter the relay path
uses to serve a Gemini upstream in OpenAI format. Each frame carries only the
parts generated since the previous one, so text, thoughts and function calls are
summed into one assistant message of the same shape. A non-stream Gemini
response is read through `relayconvert.ResponseGeminiChat2OpenAI` in the same
way.

Every other stream is reduced to its assistant text
(`choices[].delta.content`, or `choices[].text` for legacy completions) and
exported as a single `[{"role":"assistant","content":"..."}]` value.

Content capture is disabled when the setting is absent. Do not enable it for
requests that may contain credentials, personal data, or other sensitive material
without an appropriate access-control and retention policy.

For completed text/audio/realtime billing paths, `langfuse.observation.cost_details`
is the gateway charge in USD (`settled quota / QuotaPerUnit`), not an upstream
provider invoice. Ratio-based text requests expose an input/output split when it
is mathematically stable; fixed-price and tiered-expression requests expose a
total only because a trustworthy split is not available. The same total is also
`new_api.billing.cost_semantics=gateway_charge_usd` identifies this as a
gateway charge. The internal quota and consume log remain authoritative for
accounting.

`langfuse.observation.completion_start_time` is emitted for streamed requests
that have a recorded first response. It follows the existing `RelayInfo`
first-response boundary used by performance metrics; some providers may report
the first protocol response frame rather than a semantic generated token.

The trace is a side channel. Export errors are handled by the OTel batch exporter
and do not change the model response or billing result. Existing consume logs and
performance metrics remain the source of truth for billing and aggregate dashboards.
