package observability

import (
	"context"

	"github.com/QuantumNous/new-api/common"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	autoRouterSpanName        = "newapi.auto_router"
	jevClassifySpanName       = "newapi.jev.classify"
	schedulerDecisionSpanName = "newapi.scheduler.decide"
)

// Langfuse observation types used by the decision spans.
const (
	ObservationSpan       = "span"
	ObservationGeneration = "generation"
)

// Observation is what a decision span exports to Langfuse when it finishes.
// Input and Output are rendered as JSON; Metadata and TraceMetadata become
// filterable keys on the observation and on the trace respectively.
type Observation struct {
	Input  any
	Output any
	// Model names the model behind a generation observation.
	Model         string
	Metadata      map[string]string
	TraceMetadata map[string]string
	// Err marks the observation ERROR. Warning marks it WARNING with that
	// message when there is no error: the step degraded but the request went on.
	Err     error
	Warning string
}

// ContextWithRuntimeForTest returns ctx carrying a full-capture runtime that
// exports to provider, so tests outside this package can assert on spans.
func ContextWithRuntimeForTest(ctx context.Context, provider *sdktrace.TracerProvider) context.Context {
	runtime := &Runtime{
		enabled:         true,
		captureMode:     captureModeFull,
		captureMaxBytes: defaultCaptureMaxBytes,
		tracerProvider:  provider,
		tracer:          provider.Tracer("test"),
		propagator:      propagation.TraceContext{},
	}
	return context.WithValue(ctx, runtimeContextKey{}, runtime)
}

// StartAutoRouter starts the span of one auto model routing decision.
func (r *Runtime) StartAutoRouter(ctx context.Context) (context.Context, trace.Span) {
	return r.startObservation(ctx, autoRouterSpanName, ObservationSpan)
}

// StartJevClassify starts the span of one jev classification call. It is a
// generation so Langfuse shows the classifier model, but it never carries
// usage: jev tokens must not add to the trace's model usage totals.
func (r *Runtime) StartJevClassify(ctx context.Context) (context.Context, trace.Span) {
	return r.startObservation(ctx, jevClassifySpanName, ObservationGeneration)
}

// StartSchedulerDecision starts the span of one scheduler /v1/schedule call.
func (r *Runtime) StartSchedulerDecision(ctx context.Context) (context.Context, trace.Span) {
	return r.startObservation(ctx, schedulerDecisionSpanName, ObservationSpan)
}

func (r *Runtime) startObservation(ctx context.Context, name, observationType string) (context.Context, trace.Span) {
	if !r.Enabled() {
		return ctx, trace.SpanFromContext(ctx)
	}
	ctx, span := r.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(attribute.String("langfuse.observation.type", observationType))
	setSessionID(span, sessionIDFromContext(ctx))
	return ctx, span
}

// RecordsContent reports whether user content may be exported for a step that
// finished with err. Callers leave user text out of Observation input and
// output when it returns false.
func (r *Runtime) RecordsContent(err error) bool {
	return r.Enabled() && r.shouldRecordContent(err)
}

func (r *Runtime) FinishObservation(span trace.Span, observation Observation) {
	if !r.Enabled() || span == nil {
		return
	}
	for key, value := range map[string]any{
		"langfuse.observation.input":  observation.Input,
		"langfuse.observation.output": observation.Output,
	} {
		if value == nil {
			continue
		}
		if encoded, err := common.Marshal(value); err == nil {
			span.SetAttributes(attribute.String(key, string(encoded)))
		}
	}
	if observation.Model != "" {
		span.SetAttributes(attribute.String("langfuse.observation.model.name", observation.Model))
	}
	for key, value := range observation.Metadata {
		span.SetAttributes(attribute.String("langfuse.observation.metadata."+key, value))
	}
	for key, value := range observation.TraceMetadata {
		span.SetAttributes(attribute.String("langfuse.trace.metadata."+key, value))
	}
	switch {
	case observation.Err != nil:
		span.RecordError(observation.Err)
		span.SetStatus(codes.Error, observation.Err.Error())
	case observation.Warning != "":
		span.SetAttributes(
			attribute.String("langfuse.observation.level", "WARNING"),
			attribute.String("langfuse.observation.status_message", observation.Warning),
		)
	}
	span.End()
}
