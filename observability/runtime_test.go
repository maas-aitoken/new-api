package observability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collectortest "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewFromEnvDisabled(t *testing.T) {
	t.Setenv("NEW_API_OTEL_ENABLED", "false")
	t.Setenv("NEW_API_OTEL_CAPTURE_MODE", "")
	runtime, err := NewFromEnv()
	require.NoError(t, err)
	require.False(t, runtime.Enabled())
	require.Equal(t, captureModeOff, runtime.resolvedCaptureMode())
}

func TestResolveCaptureMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want contentCaptureMode
	}{
		{name: "full", mode: "full", want: captureModeFull},
		{name: "error", mode: "error", want: captureModeError},
		{name: "off", mode: "off", want: captureModeOff},
		{name: "missing", mode: "", want: captureModeOff},
		{name: "invalid", mode: "unknown", want: captureModeOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NEW_API_OTEL_CAPTURE_MODE", tt.mode)
			require.Equal(t, tt.want, resolveCaptureMode())
		})
	}
}

func TestNewFromEnvAcceptsLangfuseBaseURL(t *testing.T) {
	t.Setenv("NEW_API_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("LANGFUSE_OTEL_ENDPOINT", "")
	t.Setenv("LANGFUSE_HOST", "")
	t.Setenv("LANGFUSE_BASE_URL", "http://langfuse:3000")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	runtime, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, runtime)
	require.Equal(t, "http://langfuse:3000", runtime.langfuseHost)
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestNewFromEnvExportsToLangfuseEndpoint(t *testing.T) {
	received := make(chan struct{}, 1)
	var requestPath string
	var authorization string
	var receivedRequest collectortest.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		authorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err == nil {
			_ = proto.Unmarshal(body, &receivedRequest)
		}
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("NEW_API_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("LANGFUSE_OTEL_ENDPOINT", "")
	t.Setenv("LANGFUSE_HOST", server.URL)
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	runtime, err := NewFromEnv()
	require.NoError(t, err)

	info := &common.RelayInfo{RequestId: "req-export", OriginModelName: "gpt-test", RelayFormat: "openai"}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, &dto.GeneralOpenAIRequest{Model: "gpt-test"})
	runtime.RecordInput(ctx, []byte(`{"messages":[{"role":"user","content":"secret"}]}`), types.RelayFormatOpenAI)
	runtime.FinishLLM(ctx, span, nil, info)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, runtime.Shutdown(shutdownCtx))
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("OTLP exporter did not send a trace")
	}
	require.Equal(t, "/api/public/otel/v1/traces", requestPath)
	require.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test")), authorization)
	var attrs []string
	for _, resourceSpans := range receivedRequest.ResourceSpans {
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, exportedSpan := range scopeSpans.Spans {
				for _, attr := range exportedSpan.Attributes {
					if attr.Key == "gen_ai.input.messages" {
						attrs = append(attrs, attr.Value.GetStringValue())
					}
				}
			}
		}
	}
	require.Empty(t, attrs, "default capture mode must not export content")
}

func TestNewFromEnvErrorModeEndToEndExportPolicy(t *testing.T) {
	received := make(chan *collectortest.ExportTraceServiceRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			request := &collectortest.ExportTraceServiceRequest{}
			if proto.Unmarshal(body, request) == nil {
				received <- request
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("NEW_API_OTEL_ENABLED", "true")
	t.Setenv("NEW_API_OTEL_CAPTURE_MODE", "error")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("LANGFUSE_OTEL_ENDPOINT", "")
	t.Setenv("LANGFUSE_HOST", "")
	t.Setenv("LANGFUSE_BASE_URL", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	runtime, err := NewFromEnv()
	require.NoError(t, err)

	info := &common.RelayInfo{RequestId: "req-e2e-error", OriginModelName: "gpt-test", RelayFormat: types.RelayFormatOpenAI}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordInput(ctx, []byte(`{"messages":[{"role":"user","content":"secret"}]}`), types.RelayFormatOpenAI)
	runtime.RecordStreamChunk(ctx, `{"choices":[{"delta":{"content":"partial"}}]}`)
	runtime.FinishLLM(ctx, span, errors.New("upstream unavailable"), info)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, runtime.Shutdown(shutdownCtx))
	select {
	case request := <-received:
		var foundInput, foundOutput bool
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, exportedSpan := range scopeSpans.Spans {
					for _, attr := range exportedSpan.Attributes {
						switch attr.Key {
						case "gen_ai.input.messages":
							foundInput = true
						case "gen_ai.output.messages":
							foundOutput = true
						}
					}
				}
			}
		}
		require.True(t, foundInput)
		require.True(t, foundOutput)
	case <-time.After(time.Second):
		t.Fatal("OTLP exporter did not send error-mode trace")
	}
}

func TestResolveExporterConfigUsesLangfuseCredentials(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	endpoint, headers, err := resolveExporterConfig("http://langfuse:3000")
	require.NoError(t, err)
	require.Equal(t, "http://langfuse:3000/api/public/otel", endpoint)
	require.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test")), headers["Authorization"])
	require.Equal(t, "4", headers["x-langfuse-ingestion-version"])
}

func TestRecordInputCapturesFilteredInputAndUsage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	request := &dto.GeneralOpenAIRequest{
		Model:    "gpt-test",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
	info := &common.RelayInfo{RequestId: "req-test", OriginModelName: "gpt-test", RelayFormat: "openai"}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, request)
	inputBody := mustJSON(t, map[string]any{
		"model":    "gpt-test",
		"messages": request.Messages,
		"stream":   true,
		"metadata": map[string]any{"internal": true},
	})
	runtime.RecordInput(ctx, []byte(inputBody), types.RelayFormatOpenAI)
	runtime.RecordUsage(ctx, 3, 5, 8, 42, 0.01, true)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, int64(3), attributeValue(spans[0].Attributes, "gen_ai.usage.input_tokens").AsInt64())
	require.Equal(t, int64(5), attributeValue(spans[0].Attributes, "gen_ai.usage.output_tokens").AsInt64())
	require.Equal(t, int64(8), attributeValue(spans[0].Attributes, "gen_ai.usage.total_tokens").AsInt64())
	require.Equal(t, int64(42), attributeValue(spans[0].Attributes, "new_api.billing.quota").AsInt64())
	require.JSONEq(t, `{"messages":[{"role":"user","content":"hello"}]}`, attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString())
	require.NotContains(t, attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString(), "model")
	require.NotContains(t, attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString(), "metadata")
	require.Equal(t, "", attributeValue(spans[0].Attributes, "langfuse.observation.input").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "langfuse.observation.output").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "langfuse.observation.model.name").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "langfuse.observation.usage_details").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "new_api.billing.gateway_cost_usd").AsString())
	require.JSONEq(t, `{"total":0.000084}`, attributeValue(spans[0].Attributes, "langfuse.observation.cost_details").AsString())
}

func TestCaptureModeErrorDefersContentUntilFinalError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeError,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-error-mode", OriginModelName: "gpt-test", RelayFormat: "openai"}
	input := `{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`

	t.Run("success omits content", func(t *testing.T) {
		ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
		runtime.RecordInput(ctx, []byte(input), types.RelayFormatOpenAI)
		runtime.RecordStreamChunk(ctx, `{"choices":[{"delta":{"content":"hello"}}]}`)
		runtime.FinishLLM(ctx, span, nil, info)
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "", attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString())
		require.Equal(t, "", attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString())
	})

	t.Run("final error records content", func(t *testing.T) {
		ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
		runtime.RecordInput(ctx, []byte(input), types.RelayFormatOpenAI)
		runtime.RecordStreamChunk(ctx, `{"choices":[{"delta":{"content":"hello"}}]}`)
		runtime.FinishLLM(ctx, span, errors.New("upstream failed"), info)
		spans := exporter.GetSpans()
		require.Len(t, spans, 2)
		got := spans[1]
		require.JSONEq(t, `{"messages":[{"role":"user","content":"hello"}]}`, attributeValue(got.Attributes, "gen_ai.input.messages").AsString())
		require.JSONEq(t, `[{"role":"assistant","content":"hello"}]`, attributeValue(got.Attributes, "gen_ai.output.messages").AsString())
	})
}

func TestCaptureModeOffDoesNotCollectOrExportContent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeOff,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	info := &common.RelayInfo{RequestId: "req-off-mode", OriginModelName: "gpt-test", RelayFormat: "openai"}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordInput(ctx, []byte(`{"messages":[{"role":"user","content":"secret"}]}`), types.RelayFormatOpenAI)
	runtime.RecordStreamChunk(ctx, `{"choices":[{"delta":{"content":"secret"}}]}`)
	runtime.FinishLLM(ctx, span, errors.New("failed"), info)
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "off", attributeValue(spans[0].Attributes, "new_api.capture.mode").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString())
	require.Equal(t, "", attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString())
}

func TestProviderErrorEnvelopeIsNotExportedAsOutput(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	info := &common.RelayInfo{RequestId: "req-provider-error", OriginModelName: "gpt-test", RelayFormat: types.RelayFormatOpenAI}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	body := runtime.WrapResponseBody(ctx, io.NopCloser(strings.NewReader(`{"error":{"message":"provider failed"}}`)))
	_, readErr := io.ReadAll(body)
	require.NoError(t, readErr)
	runtime.FinishLLM(ctx, span, errors.New("upstream failed"), info)
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "", attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString())
}

func TestStripContextFieldsKeepsRequestParamsOnly(t *testing.T) {
	tests := []struct {
		name   string
		format types.RelayFormat
		body   string
		want   string
	}{
		{
			name:   "chat",
			format: types.RelayFormatOpenAI,
			body:   `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}],"stream":true,"reasoning_effort":"high","metadata":{"x":1}}`,
			want:   `{"model":"gpt","stream":true,"reasoning_effort":"high","metadata":{"x":1}}`,
		},
		{
			name:   "responses",
			format: types.RelayFormatOpenAIResponses,
			body:   `{"model":"gpt","instructions":"be concise","input":[{"role":"user","content":"hi"}],"tools":[],"include":["reasoning.encrypted_content"],"reasoning":{"effort":"xhigh"}}`,
			want:   `{"model":"gpt","include":["reasoning.encrypted_content"],"reasoning":{"effort":"xhigh"}}`,
		},
		{
			name:   "claude",
			format: types.RelayFormatClaude,
			body:   `{"model":"claude","system":"be helpful","messages":[{"role":"user","content":"hi"}],"tools":[],"thinking":{"type":"adaptive"},"metadata":{"session":"secret"}}`,
			want:   `{"model":"claude","thinking":{"type":"adaptive"},"metadata":{"session":"secret"}}`,
		},
		{
			name:   "gemini",
			format: types.RelayFormatGemini,
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"be brief"}]},"tools":[{"functionDeclarations":[]}],"generationConfig":{"temperature":0.7,"thinkingConfig":{"thinkingBudget":128}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"}]}`,
			want:   `{"generationConfig":{"temperature":0.7,"thinkingConfig":{"thinkingBudget":128}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"}]}`,
		},
		{
			name:   "context only leaves nothing",
			format: types.RelayFormatOpenAI,
			body:   `{"messages":[{"role":"user","content":"hi"}]}`,
			want:   "",
		},
		{
			name:   "unknown format",
			format: types.RelayFormatEmbedding,
			body:   `{"input":["hi"],"model":"text-embedding"}`,
			want:   "",
		},
		{
			name:   "invalid json",
			format: types.RelayFormatOpenAI,
			body:   `not json`,
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := stripContextFields([]byte(tt.body), tt.format)
			if tt.want == "" {
				require.False(t, ok)
				require.Nil(t, got)
				return
			}
			require.True(t, ok)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestRecordClientAndUpstreamParamsFollowCaptureMode(t *testing.T) {
	newRuntime := func(t *testing.T, mode contentCaptureMode) (*Runtime, *tracetest.InMemoryExporter) {
		exporter := tracetest.NewInMemoryExporter()
		provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
		runtime := &Runtime{
			enabled:         true,
			captureMode:     mode,
			captureMaxBytes: 4096,
			tracerProvider:  provider,
			tracer:          provider.Tracer("test"),
			propagator:      propagationTraceContextForTest(),
		}
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		return runtime, exporter
	}
	info := &common.RelayInfo{RequestId: "req-params", OriginModelName: "gpt-test", RelayFormat: types.RelayFormatOpenAI}
	clientBody := `{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"low","temperature":0.5}`
	upstreamBody := `{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high","temperature":0.2}`

	runRequest := func(t *testing.T, runtime *Runtime, finishErr error) {
		ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
		runtime.RecordClientRequest(ctx, []byte(clientBody), types.RelayFormatOpenAI)
		runtime.RecordInput(ctx, []byte(upstreamBody), types.RelayFormatOpenAI)
		runtime.FinishLLM(ctx, span, finishErr, info)
	}

	assertParams := func(t *testing.T, span tracetest.SpanStub, clientWant, upstreamWant string) {
		if clientWant == "" {
			require.Equal(t, "", attributeValue(span.Attributes, "gen_ai.input.value").AsString())
		} else {
			require.JSONEq(t, clientWant, attributeValue(span.Attributes, "gen_ai.input.value").AsString())
		}
		if upstreamWant == "" {
			require.Equal(t, "", attributeValue(span.Attributes, "new_api.request.upstream").AsString())
		} else {
			require.JSONEq(t, upstreamWant, attributeValue(span.Attributes, "new_api.request.upstream").AsString())
		}
	}

	t.Run("full exports client and upstream params", func(t *testing.T) {
		runtime, exporter := newRuntime(t, captureModeFull)
		runRequest(t, runtime, nil)
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		assertParams(t, spans[0],
			`{"model":"gpt-test","reasoning_effort":"low","temperature":0.5}`,
			`{"model":"gpt-test","reasoning_effort":"high","temperature":0.2}`)
	})

	t.Run("error mode defers params until final error", func(t *testing.T) {
		runtime, exporter := newRuntime(t, captureModeError)
		runRequest(t, runtime, nil)
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		assertParams(t, spans[0], "", "")

		runRequest(t, runtime, errors.New("upstream failed"))
		spans = exporter.GetSpans()
		require.Len(t, spans, 2)
		assertParams(t, spans[1],
			`{"model":"gpt-test","reasoning_effort":"low","temperature":0.5}`,
			`{"model":"gpt-test","reasoning_effort":"high","temperature":0.2}`)
	})

	t.Run("off exports neither", func(t *testing.T) {
		runtime, exporter := newRuntime(t, captureModeOff)
		runRequest(t, runtime, errors.New("failed"))
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		assertParams(t, spans[0], "", "")
	})

	t.Run("oversize params are dropped with marker", func(t *testing.T) {
		runtime, exporter := newRuntime(t, captureModeFull)
		runtime.captureMaxBytes = 8
		runRequest(t, runtime, nil)
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		require.True(t, attributeValue(spans[0].Attributes, "new_api.capture.params_truncated").AsBool())
		assertParams(t, spans[0], "", "")
	})
}

func TestBuildLangfuseInputKeepsProtocolContextOnly(t *testing.T) {
	tests := []struct {
		name   string
		format types.RelayFormat
		body   string
		want   string
	}{
		{
			name:   "chat",
			format: types.RelayFormatOpenAI,
			body:   `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}],"stream":true,"metadata":{"x":1}}`,
			want:   `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`,
		},
		{
			name:   "responses",
			format: types.RelayFormatOpenAIResponses,
			body:   `{"model":"gpt","instructions":"be concise","input":[{"role":"user","content":"hi"}],"tools":[],"include":["reasoning.encrypted_content"],"reasoning":{"effort":"xhigh"}}`,
			want:   `{"instructions":"be concise","input":[{"role":"user","content":"hi"}],"tools":[]}`,
		},
		{
			name:   "claude",
			format: types.RelayFormatClaude,
			body:   `{"model":"claude","system":"be helpful","messages":[{"role":"user","content":"hi"}],"tools":[],"thinking":{"type":"adaptive"},"metadata":{"session":"secret"}}`,
			want:   `{"system":"be helpful","messages":[{"role":"user","content":"hi"}],"tools":[]}`,
		},
		{
			name:   "gemini",
			format: types.RelayFormatGemini,
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"be brief"}]},"tools":[{"functionDeclarations":[]}],"generationConfig":{"temperature":0.7}}`,
			want:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"be brief"}]},"tools":[{"functionDeclarations":[]}]}`,
		},
		{
			name:   "gemini passthrough snake case",
			format: types.RelayFormatGemini,
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"system_instruction":{"parts":[{"text":"be brief"}]}}`,
			want:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"system_instruction":{"parts":[{"text":"be brief"}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := buildLangfuseInput([]byte(tt.body), tt.format)
			require.True(t, ok)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestNormalizeLangfuseOutputDropsResponseMetadata(t *testing.T) {
	got, ok := normalizeLangfuseOutput([]byte(`{"id":"resp_1","model":"gpt","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1}}`))
	require.True(t, ok)
	require.JSONEq(t, `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]`, string(got))
}

func TestStartLLMRequestPropagatesSessionID(t *testing.T) {
	tests := []struct {
		name     string
		request  dto.Request
		expected string
	}{
		{
			name: "codex",
			request: &dto.OpenAIResponsesRequest{
				Model:          "gpt-test",
				ClientMetadata: json.RawMessage(`{"session_id":"codex-session-123"}`),
			},
			expected: "codex-session-123",
		},
		{
			name: "claude code",
			request: &dto.ClaudeRequest{
				Model:    "claude-test",
				Metadata: json.RawMessage(`{"user_id":{"device_id":"device-123","account_uuid":"account-123","session_id":"claude-code-session-123"}}`),
			},
			expected: "claude-code-session-123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
			runtime := &Runtime{
				enabled:         true,
				captureMaxBytes: 1024,
				tracerProvider:  provider,
				tracer:          provider.Tracer("test"),
				propagator:      propagationTraceContextForTest(),
			}
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

			parentCtx, parentSpan := runtime.tracer.Start(context.Background(), "http")
			llmCtx, llmSpan := runtime.StartLLMRequest(parentCtx, &common.RelayInfo{
				RequestId:       "req-session",
				OriginModelName: "test-model",
				RelayFormat:     "openai",
			}, tt.request)
			attemptCtx, attemptSpan := runtime.StartAttempt(llmCtx)

			runtime.FinishAttempt(attemptCtx, attemptSpan, AttemptTarget{ChannelID: 7}, http.StatusOK, nil, nil)
			runtime.FinishLLM(llmCtx, llmSpan, nil, nil)
			parentSpan.End()

			spans := exporter.GetSpans()
			require.Len(t, spans, 3)
			for _, span := range spans {
				require.Equal(t, tt.expected, attributeValue(span.Attributes, "session.id").AsString(), span.Name)
			}
		})
	}
}

func TestExtractLangfuseSessionIDRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		req  dto.Request
	}{
		{name: "nil request"},
		{name: "other client", req: &dto.GeneralOpenAIRequest{}},
		{name: "missing", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{}`)}},
		{name: "malformed", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{`)}},
		{name: "non string", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{"session_id":42}`)}},
		{name: "empty", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{"session_id":"   "}`)}},
		{name: "non ascii", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{"session_id":"会话"}`)}},
		{name: "too long", req: &dto.OpenAIResponsesRequest{ClientMetadata: json.RawMessage(`{"session_id":"` + strings.Repeat("a", maxLangfuseSessionIDLen) + `"}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, extractLangfuseSessionID(tt.req))
		})
	}
	require.Equal(t, strings.Repeat("a", maxLangfuseSessionIDLen-1), extractLangfuseSessionID(&dto.OpenAIResponsesRequest{
		ClientMetadata: json.RawMessage(`{"session_id":"` + strings.Repeat("a", maxLangfuseSessionIDLen-1) + `"}`),
	}))
}

func TestExtractLangfuseSessionIDSupportsClaudeCode(t *testing.T) {
	require.Equal(t, "claude-code-session-123", extractLangfuseSessionID(&dto.ClaudeRequest{
		Metadata: json.RawMessage(`{"user_id":{"device_id":"device-123","account_uuid":"account-123","session_id":"claude-code-session-123"}}`),
	}))
	require.Equal(t, "claude-code-string-session-123", extractLangfuseSessionID(&dto.ClaudeRequest{
		Metadata: json.RawMessage(`{"user_id":"{\"device_id\":\"device-123\",\"account_uuid\":\"account-123\",\"session_id\":\"claude-code-string-session-123\"}"}`),
	}))
	require.Empty(t, extractLangfuseSessionID(&dto.ClaudeRequest{
		Metadata: json.RawMessage(`{"user_id":{"session_id":"会话"}}`),
	}))
}

func TestRecordStreamChunkCapturesOneFinalJSONEvent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-stream-output", OriginModelName: "gpt-test", RelayFormat: "openai", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-test"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1","model":"gpt-test"}}`,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_text.done","text":"hello"}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`,
	}
	for _, event := range events {
		runtime.RecordStreamChunk(ctx, event)
	}
	runtime.RecordStreamChunk(ctx, `{"type":"response.output_text.delta","delta":"late"}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	output := attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString()
	require.JSONEq(t, `[{"type":"message","content":[{"text":"hello","type":"output_text"}]}]`, output)
	require.NotContains(t, output, "response.completed")
	require.NotContains(t, output, "response.created")
}

func TestRecordStreamChunkFallsBackToDeltaWhenCompletedOutputIsEmpty(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-empty-completed-output", OriginModelName: "gpt-test", RelayFormat: "openai", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"type":"response.created","response":{"id":"resp_1"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"response.output_text.delta","delta":"hello "}`)
	runtime.RecordStreamChunk(ctx, `{"type":"response.output_text.delta","delta":"from delta"}`)
	runtime.RecordStreamChunk(ctx, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	output := attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString()
	var got []any
	require.NoError(t, rootcommon.UnmarshalJsonStr(output, &got))
	require.JSONEq(t, responsesAssistantOutputJSON("hello from delta"), mustJSON(t, got))
}

func TestRecordStreamChunkKeepsPartialResponsesOutput(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-incomplete-stream", OriginModelName: "gpt-test", RelayFormat: "openai", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"type":"response.output_text.delta","delta":"partial"}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.JSONEq(t,
		responsesAssistantOutputJSON("partial"),
		attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
	)
}

func TestRecordStreamChunkAggregatesChatCompletionsDeltas(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-chat-stream", OriginModelName: "gpt-test", RelayFormat: "openai", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
	runtime.RecordStreamChunk(ctx, `{"id":"c1","choices":[{"index":0,"delta":{"content":"hello "}}]}`)
	runtime.RecordStreamChunk(ctx, `{"id":"c1","choices":[{"index":0,"delta":{"content":"world"}}]}`)
	runtime.RecordStreamChunk(ctx, `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	runtime.RecordStreamChunk(ctx, `{"id":"c1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.JSONEq(t,
		`[{"role":"assistant","content":"hello world"}]`,
		attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
	)
}

func TestRecordStreamChunkAggregatesLegacyCompletionsText(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-completions-stream", OriginModelName: "gpt-test", RelayFormat: "openai", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"choices":[{"text":"once ","finish_reason":""}]}`)
	runtime.RecordStreamChunk(ctx, `{"choices":[{"text":"upon","finish_reason":"stop"}]}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.JSONEq(t,
		`[{"role":"assistant","content":"once upon"}]`,
		attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
	)
}

func TestRecordStreamChunkRebuildsClaudeTextMessage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-claude-stream", OriginModelName: "claude-test", RelayFormat: "claude", IsStream: true, ChannelMeta: &common.ChannelMeta{ApiType: constant.APITypeAnthropic}}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[]}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_stop","index":0}`)
	runtime.RecordStreamChunk(ctx, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"message_stop"}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.JSONEq(t,
		`[{"role":"assistant","content":"hello world"}]`,
		attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
	)
}

// TestRecordStreamChunkRebuildsClaudeThinkingAndToolUse also covers the
// fallback dispatch pass: the relay info claims the default OpenAI API type,
// so the Claude aggregator is only reached because a frame no preferred
// aggregator recognized is still offered to the rest.
func TestRecordStreamChunkRebuildsClaudeThinkingAndToolUse(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	info := &common.RelayInfo{RequestId: "req-claude-tool", OriginModelName: "claude-test", RelayFormat: "claude", IsStream: true}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.RecordStreamChunk(ctx, `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-test","content":[]}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_stop","index":0}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"SF\"}"}}`)
	runtime.RecordStreamChunk(ctx, `{"type":"content_block_stop","index":1}`)
	runtime.RecordStreamChunk(ctx, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.JSONEq(t,
		`[{"role":"assistant","content":"","reasoning_content":"let me think\n","tool_calls":[{"id":"toolu_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}]`,
		attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
	)
}

func TestGeminiUpstreamExportsInputAndOutput(t *testing.T) {
	newRuntime := func(t *testing.T) (*Runtime, *tracetest.InMemoryExporter) {
		exporter := tracetest.NewInMemoryExporter()
		provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		return &Runtime{
			enabled:         true,
			captureMode:     captureModeFull,
			captureMaxBytes: 4096,
			tracerProvider:  provider,
			tracer:          provider.Tracer("test"),
			propagator:      propagationTraceContextForTest(),
		}, exporter
	}
	upstreamBody := `{"contents":[{"role":"user","parts":[{"text":"weather in SF?"}]}],"generationConfig":{"temperature":0.2}}`

	t.Run("non-stream", func(t *testing.T) {
		runtime, exporter := newRuntime(t)
		info := &common.RelayInfo{RequestId: "req-gemini", OriginModelName: "gemini-test", RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &common.ChannelMeta{ApiType: constant.APITypeGemini}}
		ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
		runtime.RecordInput(ctx, []byte(upstreamBody), types.RelayFormatGemini)
		body := runtime.WrapResponseBody(ctx, io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"checking","thought":true},{"text":"Sunny."},{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"SF"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6},"modelVersion":"gemini-test"}`)))
		_, readErr := io.ReadAll(body)
		require.NoError(t, readErr)
		runtime.FinishLLM(ctx, span, nil, info)

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		require.JSONEq(t, `{"contents":[{"role":"user","parts":[{"text":"weather in SF?"}]}]}`, attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString())
		require.JSONEq(t, `{"generationConfig":{"temperature":0.2}}`, attributeValue(spans[0].Attributes, "new_api.request.upstream").AsString())
		require.JSONEq(t,
			`[{"role":"assistant","content":"Sunny.","reasoning_content":"checking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}]`,
			attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
		)
	})

	// A Vertex AI channel prefers the Claude aggregator first, so the Gemini
	// frames also prove that aggregator turns them down.
	t.Run("stream", func(t *testing.T) {
		runtime, exporter := newRuntime(t)
		info := &common.RelayInfo{RequestId: "req-gemini-stream", OriginModelName: "gemini-test", RelayFormat: types.RelayFormatOpenAI, IsStream: true, ChannelMeta: &common.ChannelMeta{ApiType: constant.APITypeVertexAi}}
		ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
		runtime.RecordInput(ctx, []byte(upstreamBody), types.RelayFormatGemini)
		runtime.RecordStreamChunk(ctx, `{"candidates":[{"content":{"role":"model","parts":[{"text":"checking","thought":true}]},"index":0}]}`)
		runtime.RecordStreamChunk(ctx, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Sunny"}]},"index":0}]}`)
		runtime.RecordStreamChunk(ctx, `{"candidates":[{"content":{"role":"model","parts":[{"text":" today."}]},"index":0}]}`)
		runtime.RecordStreamChunk(ctx, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"SF"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}}`)
		runtime.FinishLLM(ctx, span, nil, info)

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		require.JSONEq(t, `{"contents":[{"role":"user","parts":[{"text":"weather in SF?"}]}]}`, attributeValue(spans[0].Attributes, "gen_ai.input.messages").AsString())
		require.JSONEq(t,
			`[{"role":"assistant","content":"Sunny today.","reasoning_content":"checking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}]`,
			attributeValue(spans[0].Attributes, "gen_ai.output.messages").AsString(),
		)
	})
}

// responsesAssistantOutputJSON is the compact semantic form recorded for an
// OpenAI Responses assistant message after response metadata is removed.
func responsesAssistantOutputJSON(text string) string {
	return fmt.Sprintf(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]`, text)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := rootcommon.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestNestedAttemptUsageAndTTFTStayOnGenerationSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:         true,
		captureMaxBytes: 1024,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	start := time.Now().Add(-500 * time.Millisecond).UTC()
	firstResponse := start.Add(175 * time.Millisecond)
	info := &common.RelayInfo{
		RequestId:         "req-nested",
		OriginModelName:   "gpt-test",
		RelayFormat:       "openai",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: firstResponse,
	}
	llmCtx, llmSpan := runtime.StartLLMRequest(context.Background(), info, nil)
	attemptCtx, attemptSpan := runtime.StartAttempt(llmCtx)
	inputCost := 0.001
	outputCost := 0.002
	runtime.RecordUsageWithCosts(attemptCtx, 10, 20, 30, 15000, 0, false, &BillingCostDetails{
		InputUSD:  &inputCost,
		OutputUSD: &outputCost,
		TotalUSD:  0.003,
	})
	runtime.FinishAttempt(attemptCtx, attemptSpan, AttemptTarget{ChannelID: 7}, http.StatusOK, nil, nil)
	runtime.FinishLLM(llmCtx, llmSpan, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	var generation, attempt tracetest.SpanStub
	for _, span := range spans {
		switch span.Name {
		case llmRequestSpanName:
			generation = span
		case relayAttemptSpanName:
			attempt = span
		}
	}
	require.Equal(t, llmRequestSpanName, generation.Name)
	require.Equal(t, relayAttemptSpanName, attempt.Name)
	require.WithinDuration(t, start, generation.StartTime, time.Nanosecond)
	require.Equal(t, int64(10), attributeValue(generation.Attributes, "gen_ai.usage.input_tokens").AsInt64())
	require.JSONEq(t, `{"input":0.001,"output":0.002,"total":0.003}`, attributeValue(generation.Attributes, "langfuse.observation.cost_details").AsString())
	require.Equal(t, "gateway_charge_usd", attributeValue(generation.Attributes, "new_api.billing.cost_semantics").AsString())
	require.Equal(t, "", attributeValue(generation.Attributes, "new_api.billing.gateway_cost_usd").AsString())
	require.Equal(t, "", attributeValue(attempt.Attributes, "langfuse.observation.cost_details").AsString())
	require.Equal(t, firstResponse.Format(time.RFC3339Nano), attributeValue(generation.Attributes, "langfuse.observation.completion_start_time").AsString())
	require.Equal(t, int64(175), attributeValue(generation.Attributes, "new_api.ttft_ms").AsInt64())
}

func TestFinishLLMDoesNotSetTTFTWithoutResponse(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:        true,
		tracerProvider: provider,
		tracer:         provider.Tracer("test"),
		propagator:     propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	start := time.Now().Add(-100 * time.Millisecond)
	info := &common.RelayInfo{
		RequestId:         "req-no-response",
		OriginModelName:   "gpt-test",
		RelayFormat:       "openai",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(-time.Second),
	}
	ctx, span := runtime.StartLLMRequest(context.Background(), info, nil)
	runtime.FinishLLM(ctx, span, nil, info)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "", attributeValue(spans[0].Attributes, "langfuse.observation.completion_start_time").AsString())
}

func propagationTraceContextForTest() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func TestShouldTracePath(t *testing.T) {
	require.True(t, shouldTracePath("/v1/chat/completions"))
	require.True(t, shouldTracePath("/v1beta/models"))
	require.True(t, shouldTracePath("/pg/chat/completions"))
	require.False(t, shouldTracePath("/health"))
	require.False(t, shouldTracePath("/api/logs"))
}

func TestMiddlewareRecordsUsernameAsUserID(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	runtime := &Runtime{
		enabled:        true,
		tracerProvider: provider,
		tracer:         provider.Tracer("test"),
		propagator:     propagationTraceContextForTest(),
	}
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	engine := gin.New()
	engine.Use(runtime.Middleware())
	engine.GET("/v1/models", func(c *gin.Context) {
		c.Set(string(constant.ContextKeyUserName), "alice")
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "alice", attributeValue(spans[0].Attributes, "user.id").AsString())
}

func TestParseHeaders(t *testing.T) {
	result := parseHeaders("Authorization=Basic abc==,x-test=Bearer%20token")
	require.Equal(t, "Basic abc==", result["Authorization"])
	require.Equal(t, "Bearer token", result["x-test"])
}

func TestNormalizeTraceEndpoint(t *testing.T) {
	require.Equal(t, "http://langfuse:3000/api/public/otel/v1/traces", normalizeTraceEndpoint("http://langfuse:3000/api/public/otel"))
	require.Equal(t, "http://collector:4318/v1/traces", normalizeTraceEndpoint("http://collector:4318/v1/traces"))
}

func TestSetContentCaptureModeOverridesLive(t *testing.T) {
	t.Cleanup(func() { captureModeOverride.Store(contentCaptureMode("")) })
	captureModeOverride.Store(contentCaptureMode(""))

	runtime := &Runtime{enabled: true, captureMode: captureModeOff}
	require.Equal(t, captureModeOff, runtime.resolvedCaptureMode())
	require.False(t, runtime.contentCaptureEnabled())

	require.True(t, SetContentCaptureMode("error"))
	require.Equal(t, captureModeError, runtime.resolvedCaptureMode())
	require.True(t, runtime.contentCaptureEnabled())
	require.True(t, runtime.shouldRecordContent(errors.New("boom")))
	require.False(t, runtime.shouldRecordContent(nil))
	require.Equal(t, "error", ContentCaptureMode())

	require.True(t, SetContentCaptureMode("FULL"))
	require.Equal(t, captureModeFull, runtime.resolvedCaptureMode())
	require.True(t, runtime.shouldRecordContent(nil))

	require.False(t, SetContentCaptureMode("bogus"))
	require.Equal(t, captureModeFull, runtime.resolvedCaptureMode())

	require.True(t, SetContentCaptureMode("off"))
	require.Equal(t, captureModeOff, runtime.resolvedCaptureMode())
	require.False(t, runtime.contentCaptureEnabled())
}

func TestContentCaptureModeFallsBackToEnv(t *testing.T) {
	t.Cleanup(func() { captureModeOverride.Store(contentCaptureMode("")) })
	captureModeOverride.Store(contentCaptureMode(""))
	t.Setenv("NEW_API_OTEL_CAPTURE_MODE", "error")
	require.Equal(t, "error", ContentCaptureMode())
	t.Setenv("NEW_API_OTEL_CAPTURE_MODE", "")
	require.Equal(t, "off", ContentCaptureMode())
}

func TestIsValidContentCaptureMode(t *testing.T) {
	for _, valid := range []string{"full", "error", "off", "FULL", " off "} {
		require.True(t, IsValidContentCaptureMode(valid), valid)
	}
	for _, invalid := range []string{"", "on", "verbose"} {
		require.False(t, IsValidContentCaptureMode(invalid), invalid)
	}
}

func TestMiddlewareRecordsMaasUserIDHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		present bool
	}{
		{name: "header present", header: "user-42", present: true},
		{name: "header absent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
			runtime := &Runtime{
				enabled:         true,
				captureMaxBytes: 1024,
				tracerProvider:  provider,
				tracer:          provider.Tracer("test"),
				propagator:      propagationTraceContextForTest(),
			}
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.present {
				req.Header.Set("X-Maas-User-Id", tt.header)
			}
			c.Request = req

			runtime.Middleware()(c)

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			value := attributeValue(spans[0].Attributes, "new_api.request.maas_user_id")
			if tt.present {
				require.Equal(t, tt.header, value.AsString())
			} else {
				require.Equal(t, attribute.INVALID, value.Type())
			}
		})
	}
}

func attributeValue(attrs []attribute.KeyValue, key string) attribute.Value {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value
		}
	}
	return attribute.Value{}
}

func TestFinishAttemptRecordsTargetAndUpstreamOutcome(t *testing.T) {
	newRuntime := func(t *testing.T) (*Runtime, *tracetest.InMemoryExporter) {
		exporter := tracetest.NewInMemoryExporter()
		provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		return &Runtime{
			enabled:         true,
			captureMode:     captureModeFull,
			captureMaxBytes: 1024,
			tracerProvider:  provider,
			tracer:          provider.Tracer("test"),
			propagator:      propagationTraceContextForTest(),
		}, exporter
	}
	target := AttemptTarget{RetryIndex: 1, ChannelID: 7, ChannelName: "primary", ChannelType: 1, UpstreamModel: "gpt-upstream"}

	t.Run("upstream error", func(t *testing.T) {
		runtime, exporter := newRuntime(t)
		ctx, span := runtime.StartAttempt(context.Background())
		req := httptest.NewRequest(http.MethodPost, "https://upstream.test/v1/chat/completions", nil)
		resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
		resp.Header.Set(rootcommon.RequestIdKey, "up-123")
		runtime.RecordUpstreamResponse(ctx, req, resp)
		runtime.FinishAttempt(ctx, span, target, http.StatusTooManyRequests, nil, errors.New("rate limited"))

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		attrs := spans[0].Attributes
		require.Equal(t, relayAttemptSpanName, spans[0].Name)
		require.JSONEq(t, `{"retry_index":1,"channel_id":7,"channel_name":"primary","channel_type":1,"upstream_model":"gpt-upstream"}`, attributeValue(attrs, "langfuse.observation.input").AsString())
		var output map[string]any
		require.NoError(t, rootcommon.UnmarshalJsonStr(attributeValue(attrs, "langfuse.observation.output").AsString(), &output))
		require.Equal(t, float64(http.StatusTooManyRequests), output["upstream_status_code"])
		require.Equal(t, "up-123", output["upstream_request_id"])
		require.Equal(t, "upstream.test", output["server_address"])
		require.Equal(t, "rate limited", output["error"])
		require.Contains(t, output, "headers_ms")
		require.Equal(t, "7", attributeValue(attrs, "langfuse.observation.metadata.channel_id").AsString())
		require.Equal(t, "429", attributeValue(attrs, "langfuse.observation.metadata.status_code").AsString())
		require.Equal(t, codes.Error, spans[0].Status.Code)
	})

	t.Run("abnormal stream end", func(t *testing.T) {
		runtime, exporter := newRuntime(t)
		ctx, span := runtime.StartAttempt(context.Background())
		status := common.NewStreamStatus()
		status.SetEndReason(common.StreamEndReasonClientGone, nil)
		runtime.FinishAttempt(ctx, span, target, http.StatusOK, status, nil)

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		attrs := spans[0].Attributes
		require.Equal(t, "WARNING", attributeValue(attrs, "langfuse.observation.level").AsString())
		require.Equal(t, "stream ended: client_gone", attributeValue(attrs, "langfuse.observation.status_message").AsString())
		require.NotEqual(t, codes.Error, spans[0].Status.Code)
		require.NotContains(t, attributeValue(attrs, "langfuse.observation.output").AsString(), "upstream_status_code")
	})
}

func TestFinishObservationExportsLangfuseFields(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exporter)))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	runtime := FromContext(ContextWithRuntimeForTest(context.Background(), provider))

	routeCtx, routeSpan := runtime.StartAutoRouter(context.Background())
	_, jevSpan := runtime.StartJevClassify(routeCtx)
	runtime.FinishObservation(jevSpan, Observation{Input: map[string]string{"questions_version": "v"}, Model: "jev-27b", Err: errors.New("timeout")})
	runtime.FinishObservation(routeSpan, Observation{
		Output:        map[string]string{"tier": "mid"},
		Metadata:      map[string]string{"tier": "mid"},
		TraceMetadata: map[string]string{"auto_router_tier": "mid"},
	})

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	jev, route := spans[0], spans[1]
	require.Equal(t, jevClassifySpanName, jev.Name)
	require.Equal(t, route.SpanContext.SpanID(), jev.Parent.SpanID())
	require.Equal(t, "generation", attributeValue(jev.Attributes, "langfuse.observation.type").AsString())
	require.Equal(t, "jev-27b", attributeValue(jev.Attributes, "langfuse.observation.model.name").AsString())
	require.Equal(t, "", attributeValue(jev.Attributes, "langfuse.observation.usage_details").AsString())
	require.Equal(t, codes.Error, jev.Status.Code)

	require.Equal(t, autoRouterSpanName, route.Name)
	require.Equal(t, "span", attributeValue(route.Attributes, "langfuse.observation.type").AsString())
	require.JSONEq(t, `{"tier":"mid"}`, attributeValue(route.Attributes, "langfuse.observation.output").AsString())
	require.Equal(t, "mid", attributeValue(route.Attributes, "langfuse.observation.metadata.tier").AsString())
	require.Equal(t, "mid", attributeValue(route.Attributes, "langfuse.trace.metadata.auto_router_tier").AsString())
}
