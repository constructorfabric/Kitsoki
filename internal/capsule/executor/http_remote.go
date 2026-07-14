package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	DefaultRemoteConnectTimeout        = 10 * time.Second
	DefaultRemoteResponseHeaderTimeout = 30 * time.Second
	DefaultRemoteOverallTimeout        = 10 * time.Minute
)

// HTTPTimeouts is the bounded transport posture used by the production remote
// worker client. It is exported so preflight/doctor surfaces can report the
// actual controller deadlines without performing a paid run.
type HTTPTimeouts struct {
	Connect        time.Duration `json:"connect"`
	ResponseHeader time.Duration `json:"response_header"`
	Overall        time.Duration `json:"overall"`
}

func DefaultHTTPTimeouts() HTTPTimeouts {
	return HTTPTimeouts{Connect: DefaultRemoteConnectTimeout, ResponseHeader: DefaultRemoteResponseHeaderTimeout, Overall: DefaultRemoteOverallTimeout}
}

// Credential supplies an ephemeral transport credential. Its value is sent
// only in the request header; it is never copied into prepared state, envelopes
// or executor events.
type Credential func(context.Context) (string, error)

// HTTPRemoteWorker is the production transport adapter for a Capsule worker
// service. The service owns source materialization and story execution; this
// controller sends an already sealed envelope and accepts only its normalized
// typed result. It deliberately has no fallback to local task execution.
type HTTPRemoteWorker struct {
	Endpoint   string
	Client     *http.Client
	Credential Credential
	Timeouts   HTTPTimeouts
	// Source supplies the exact committed source object named by the sealed
	// envelope. Configured Capsule CI remotes always set this; nil is retained
	// only for adapters whose scheduler pre-materializes source out of band.
	Source SourceBundler
}

func (w HTTPRemoteWorker) Describe(ctx context.Context) (Capabilities, error) {
	var response struct {
		Capabilities Capabilities `json:"capabilities"`
	}
	if err := w.call(ctx, http.MethodGet, "/v1/capsules/capabilities", nil, &response); err != nil {
		return Capabilities{}, err
	}
	return response.Capabilities, nil
}

// AcceptPrepared validates the immutable execution at the worker without
// uploading source or starting a story. Doctor uses this through RemoteProvider
// so a green preflight means both controller and worker accepted the exact
// envelope/policy vocabulary.
func (w HTTPRemoteWorker) AcceptPrepared(ctx context.Context, prepared Prepared) error {
	validated, err := ValidatePrepared(prepared)
	if err != nil {
		return err
	}
	meta := newRemoteCallMetadata(http.MethodPost, "/v1/capsules/validate", w.Endpoint)
	_, err = w.callWithMetadataAccept(ctx, &meta, map[string]Prepared{"prepared": validated}, nil, nil)
	return err
}

func (w HTTPRemoteWorker) Run(ctx context.Context, prepared Prepared, _ Task, sink EventSink) (Result, error) {
	validated, err := ValidatePrepared(prepared)
	if err != nil {
		return Result{}, err
	}
	prepared = validated
	if w.Source != nil {
		if err := w.ensureSource(ctx, prepared, sink); err != nil {
			return Result{}, err
		}
	}
	meta := newRemoteCallMetadata(http.MethodPost, "/v1/capsules/run", w.Endpoint)
	if sink != nil {
		if err := sink.Emit(ctx, Event{Kind: "capsule.executor.started", At: meta.StartedAt, EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: "running", Fields: meta.fields()}); err != nil {
			return Result{}, fmt.Errorf("capsule executor: persist remote start event: %w", err)
		}
	}
	var response struct {
		Result Result          `json:"result"`
		Run    ExecutionStatus `json:"run"`
		Error  string          `json:"error,omitempty"`
	}
	statusCode, err := w.callWithMetadataAccept(ctx, &meta, map[string]Prepared{"prepared": prepared}, &response, map[int]bool{http.StatusConflict: true, http.StatusUnprocessableEntity: true})
	if err == nil {
		response.Result.ExecutionID = prepared.ID
		sort.Strings(response.Result.Artifacts)
		if sink != nil {
			for _, event := range response.Run.Events {
				if event.Fields == nil {
					event.Fields = map[string]any{}
				}
				event.Fields["worker_request_id"] = response.Run.RequestID
				event.Fields["worker_status"] = response.Run.Status
				event.Fields["worker_stage"] = response.Run.Stage
				if emitErr := sink.Emit(ctx, event); emitErr != nil {
					err = fmt.Errorf("capsule executor: persist remote worker event: %w", emitErr)
					break
				}
			}
		}
		if statusCode == http.StatusConflict || statusCode == http.StatusUnprocessableEntity {
			workerStatus, statusErr := normalizeExecutionStatus(response.Run, prepared.ID)
			if statusErr != nil {
				err = remoteCallError{Method: meta.Method, Path: meta.Path, Host: meta.Host, RequestID: meta.RequestID, Status: meta.Status, Duration: meta.Duration, Kind: "status", Body: sanitizeRemoteBody([]byte(response.Error)), Cause: statusErr}
			} else if workerStatus.Status == "cancelled" {
				err = ExecutionError{Execution: workerStatus, Cause: context.Canceled}
			} else {
				err = ExecutionError{Execution: workerStatus}
			}
		} else {
			workerStatus, statusErr := normalizeExecutionStatus(response.Run, prepared.ID)
			if statusErr != nil {
				err = remoteCallError{Method: meta.Method, Path: meta.Path, Host: meta.Host, RequestID: meta.RequestID, Status: meta.Status, Duration: meta.Duration, Kind: "status", Body: sanitizeRemoteBody([]byte(response.Error)), Cause: statusErr}
			} else if workerStatus.Status != "completed" || workerStatus.Stage != "terminal" || response.Result.ExitCode != 0 {
				if workerStatus.Error == "" {
					workerStatus.Error = "remote worker returned an unsuccessful completion"
				}
				err = ExecutionError{Execution: workerStatus}
			}
		}
	}
	if sink != nil {
		kind := "capsule.executor.finished"
		outcome := "passed"
		errorText := ""
		fields := meta.fields()
		if errors.Is(err, context.Canceled) {
			kind = "capsule.executor.cancelled"
			outcome = "cancelled"
			errorText = "remote executor cancelled"
			fields["error_kind"] = "cancelled"
			fields["message"] = "remote worker reported terminal cancellation"
		} else if err != nil {
			kind = "capsule.executor.failed"
			outcome = "failed"
			errorText = "remote executor request failed"
			for key, value := range RemoteErrorFields(err) {
				fields[key] = value
			}
		}
		if emitErr := sink.Emit(ctx, Event{Kind: kind, At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: outcome, Error: errorText, Fields: fields}); emitErr != nil {
			err = errors.Join(err, fmt.Errorf("capsule executor: persist remote terminal event: %w", emitErr))
		}
	}
	return response.Result, err
}

func (w HTTPRemoteWorker) ensureSource(ctx context.Context, prepared Prepared, sink EventSink) error {
	bundle, err := w.Source.Bundle(ctx, prepared.Envelope)
	if err != nil {
		return fmt.Errorf("capsule executor: package remote source: %w", err)
	}
	if err := ValidateSourceBundle(bundle, 0); err != nil {
		return err
	}
	if bundle.Head != prepared.Envelope.SourceDigest {
		return fmt.Errorf("capsule executor: source bundle HEAD does not match sealed envelope")
	}
	path := "/v1/capsules/sources/" + url.PathEscape(bundle.Head)
	meta := newRemoteCallMetadata(http.MethodHead, path, w.Endpoint)
	status, err := w.callWithMetadataAccept(ctx, &meta, nil, nil, map[int]bool{http.StatusNotFound: true})
	if err != nil {
		return err
	}
	fields := meta.fields()
	fields["source_digest"] = bundle.Head
	fields["bundle_digest"] = bundle.Digest
	fields["bundle_bytes"] = bundle.Size
	if status != http.StatusNotFound {
		fields["source_cache"] = "hit"
		if sink != nil {
			if err := sink.Emit(ctx, Event{Kind: "capsule.executor.source.ready", At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: "reused", Fields: fields}); err != nil {
				return fmt.Errorf("capsule executor: persist source-ready event: %w", err)
			}
		}
		return nil
	}
	fields["source_cache"] = "miss"
	if sink != nil {
		if err := sink.Emit(ctx, Event{Kind: "capsule.executor.source.uploading", At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: "running", Fields: fields}); err != nil {
			return fmt.Errorf("capsule executor: persist source-upload event: %w", err)
		}
	}
	put := newRemoteCallMetadata(http.MethodPut, path, w.Endpoint)
	_, err = w.callWithMetadataAccept(ctx, &put, remoteRawBody{Data: bundle.Data, ContentType: "application/vnd.git.bundle", Headers: map[string]string{"X-Kitsoki-Bundle-Digest": bundle.Digest}}, nil, nil)
	if err != nil {
		return err
	}
	fields = put.fields()
	fields["source_digest"] = bundle.Head
	fields["bundle_digest"] = bundle.Digest
	fields["bundle_bytes"] = bundle.Size
	if sink != nil {
		if err := sink.Emit(ctx, Event{Kind: "capsule.executor.source.ready", At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: "uploaded", Fields: fields}); err != nil {
			return fmt.Errorf("capsule executor: persist source-ready event: %w", err)
		}
	}
	return nil
}

func (w HTTPRemoteWorker) Cancel(ctx context.Context, id string) error {
	_, err := w.RequestCancel(ctx, id)
	return err
}

func (w HTTPRemoteWorker) Status(ctx context.Context, id string) (ExecutionStatus, error) {
	var response struct {
		Run ExecutionStatus `json:"run"`
	}
	if err := w.call(ctx, http.MethodGet, "/v1/capsules/executions/"+url.PathEscape(id), nil, &response); err != nil {
		return ExecutionStatus{}, err
	}
	return normalizeExecutionStatus(response.Run, id)
}

func (w HTTPRemoteWorker) RequestCancel(ctx context.Context, id string) (ExecutionStatus, error) {
	var response struct {
		Run ExecutionStatus `json:"run"`
	}
	if err := w.call(ctx, http.MethodDelete, "/v1/capsules/executions/"+url.PathEscape(id), nil, &response); err != nil {
		return ExecutionStatus{}, err
	}
	return normalizeExecutionStatus(response.Run, id)
}

func normalizeExecutionStatus(status ExecutionStatus, expectedID string) (ExecutionStatus, error) {
	if status.ExecutionID == "" || status.ExecutionID != expectedID || status.Status == "" {
		return ExecutionStatus{}, fmt.Errorf("capsule executor: remote returned invalid execution status for %q", expectedID)
	}
	if status.Agent != nil {
		if err := ValidateAgentDiagnostics(*status.Agent); err != nil {
			return ExecutionStatus{}, fmt.Errorf("capsule executor: remote returned invalid agent diagnostics for %q", expectedID)
		}
	}
	if status.Cleanup != nil {
		if err := ValidateWorkerCleanupDiagnostics(*status.Cleanup); err != nil {
			return ExecutionStatus{}, fmt.Errorf("capsule executor: remote returned invalid cleanup diagnostics for %q", expectedID)
		}
	}
	status.Schema = ExecutionStatusSchema
	return status, nil
}

var _ ExecutionController = HTTPRemoteWorker{}

func (w HTTPRemoteWorker) call(ctx context.Context, method, path string, body any, out any) error {
	meta := newRemoteCallMetadata(method, path, w.Endpoint)
	return w.callWithMetadata(ctx, &meta, body, out)
}

type remoteCallMetadata struct {
	Method, Path, Host, RequestID string
	StartedAt                     time.Time
	Duration                      time.Duration
	Status                        string
}

func newRemoteCallMetadata(method, path, endpoint string) remoteCallMetadata {
	return remoteCallMetadata{Method: method, Path: path, Host: endpointHost(endpoint), RequestID: newRequestID(), StartedAt: time.Now().UTC()}
}

func (m remoteCallMetadata) fields() map[string]any {
	fields := map[string]any{"transport": "https", "remote_host": m.Host, "request_id": m.RequestID, "method": m.Method, "path": strings.TrimPrefix(m.Path, "/")}
	if m.Duration > 0 {
		fields["duration_ms"] = m.Duration.Milliseconds()
	}
	if m.Status != "" {
		fields["status"] = m.Status
	}
	return fields
}

func (w HTTPRemoteWorker) callWithMetadata(ctx context.Context, meta *remoteCallMetadata, body any, out any) error {
	_, err := w.callWithMetadataAccept(ctx, meta, body, out, nil)
	return err
}

type remoteRawBody struct {
	Data        []byte
	ContentType string
	Headers     map[string]string
}

func (w HTTPRemoteWorker) callWithMetadataAccept(ctx context.Context, meta *remoteCallMetadata, body any, out any, accepted map[int]bool) (int, error) {
	defer func() { meta.Duration = time.Since(meta.StartedAt) }()
	endpoint := strings.TrimRight(w.Endpoint, "/")
	if endpoint == "" || !strings.HasPrefix(endpoint, "https://") {
		return 0, fmt.Errorf("capsule executor: remote endpoint must use https")
	}
	endpointURL, _ := url.Parse(endpoint)
	var reader io.Reader
	contentType := ""
	headers := map[string]string{}
	if body != nil {
		if raw, ok := body.(remoteRawBody); ok {
			reader = bytes.NewReader(raw.Data)
			contentType = raw.ContentType
			headers = raw.Headers
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				return 0, err
			}
			reader = bytes.NewReader(raw)
			contentType = "application/json"
		}
	}
	req, err := http.NewRequestWithContext(ctx, meta.Method, endpoint+meta.Path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Kitsoki-Request-ID", meta.RequestID)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if w.Credential != nil {
		credential, err := w.Credential(ctx)
		if err != nil {
			return 0, fmt.Errorf("capsule executor: remote credential: %w", err)
		}
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
	}
	client := boundedHTTPClient(w.Client, w.EffectiveTimeouts())
	response, err := client.Do(req)
	if err != nil {
		return 0, remoteCallError{Method: meta.Method, Path: meta.Path, Host: endpointURL.Host, RequestID: meta.RequestID, Duration: time.Since(meta.StartedAt), Kind: "transport", Cause: err}
	}
	defer response.Body.Close()
	meta.Status = response.Status
	if (response.StatusCode < 200 || response.StatusCode >= 300) && !accepted[response.StatusCode] {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return response.StatusCode, remoteCallError{Method: meta.Method, Path: meta.Path, Host: endpointURL.Host, RequestID: firstNonEmpty(response.Header.Get("X-Kitsoki-Request-ID"), meta.RequestID), Status: response.Status, Duration: time.Since(meta.StartedAt), Kind: "status", Body: sanitizeRemoteBody(body)}
	}
	if out == nil {
		return response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return response.StatusCode, remoteCallError{Method: meta.Method, Path: meta.Path, Host: endpointURL.Host, RequestID: firstNonEmpty(response.Header.Get("X-Kitsoki-Request-ID"), meta.RequestID), Status: response.Status, Duration: time.Since(meta.StartedAt), Kind: "decode", Cause: err}
	}
	return response.StatusCode, nil
}

// EffectiveTimeouts returns the non-zero deadlines the worker will enforce.
// Individual values may be overridden for deployment or deterministic tests;
// zero never disables a production deadline.
func (w HTTPRemoteWorker) EffectiveTimeouts() HTTPTimeouts {
	return normalizeHTTPTimeouts(w.Timeouts)
}

func normalizeHTTPTimeouts(in HTTPTimeouts) HTTPTimeouts {
	defaults := DefaultHTTPTimeouts()
	if in.Connect <= 0 {
		in.Connect = defaults.Connect
	}
	if in.ResponseHeader <= 0 {
		in.ResponseHeader = defaults.ResponseHeader
	}
	if in.Overall <= 0 {
		in.Overall = defaults.Overall
	}
	return in
}

func boundedHTTPClient(in *http.Client, timeouts HTTPTimeouts) *http.Client {
	timeouts = normalizeHTTPTimeouts(timeouts)
	if in != nil {
		clone := *in
		if clone.Timeout <= 0 {
			clone.Timeout = timeouts.Overall
		}
		if clone.Transport == nil {
			clone.Transport = boundedHTTPTransport(http.DefaultTransport.(*http.Transport), timeouts)
		} else if transport, ok := clone.Transport.(*http.Transport); ok {
			clone.Transport = boundedHTTPTransport(transport, timeouts)
		}
		return &clone
	}
	base := boundedHTTPTransport(http.DefaultTransport.(*http.Transport), timeouts)
	return &http.Client{Transport: base, Timeout: timeouts.Overall}
}

func boundedHTTPTransport(in *http.Transport, timeouts HTTPTimeouts) *http.Transport {
	base := in.Clone()
	base.DialContext = (&net.Dialer{Timeout: timeouts.Connect, KeepAlive: 30 * time.Second}).DialContext
	base.ResponseHeaderTimeout = timeouts.ResponseHeader
	base.TLSHandshakeTimeout = timeouts.Connect
	return base
}

type remoteCallError struct {
	Method    string
	Path      string
	Host      string
	RequestID string
	Status    string
	Duration  time.Duration
	Kind      string
	Body      string
	Cause     error
}

func (e remoteCallError) Error() string {
	parts := []string{fmt.Sprintf("capsule executor: remote %s %s failed", e.Method, e.Path)}
	if e.Kind != "" {
		parts = append(parts, "kind="+e.Kind)
	}
	if e.Host != "" {
		parts = append(parts, "host="+e.Host)
	}
	if e.Status != "" {
		parts = append(parts, "status="+e.Status)
	}
	if e.RequestID != "" {
		parts = append(parts, "request_id="+e.RequestID)
	}
	if e.Duration > 0 {
		parts = append(parts, "duration="+e.Duration.Round(time.Millisecond).String())
	}
	if e.Body != "" {
		parts = append(parts, "body="+e.Body)
	}
	if e.Cause != nil {
		parts = append(parts, "cause="+e.Cause.Error())
	}
	return strings.Join(parts, " ")
}

func (e remoteCallError) Unwrap() error { return e.Cause }

func remoteErrorKind(err error) string {
	var executionErr ExecutionError
	if errors.As(err, &executionErr) {
		if executionErr.Execution.Status == "cancelled" {
			return "cancelled"
		}
		return "execution"
	}
	var e remoteCallError
	if errors.As(err, &e) && e.Kind != "" {
		return e.Kind
	}
	return "unknown"
}

// RemoteErrorFields projects a remote transport failure into provider-safe,
// structured trace fields. It intentionally excludes response bodies and
// credential material; the bounded redacted body remains only in the local
// terminal error string.
func RemoteErrorFields(err error) map[string]any {
	fields := map[string]any{"error_kind": remoteErrorKind(err)}
	var executionErr ExecutionError
	if errors.As(err, &executionErr) {
		fields["execution_id"] = executionErr.Execution.ExecutionID
		fields["worker_status"] = executionErr.Execution.Status
		fields["worker_stage"] = executionErr.Execution.Stage
		fields["message"] = firstNonEmpty(executionErr.Execution.Error, "remote execution failed")
		return fields
	}
	var e remoteCallError
	if !errors.As(err, &e) {
		fields["message"] = "remote request failed"
		return fields
	}
	fields["method"] = e.Method
	fields["path"] = strings.TrimPrefix(e.Path, "/")
	fields["remote_host"] = e.Host
	fields["request_id"] = e.RequestID
	fields["duration_ms"] = e.Duration.Milliseconds()
	if e.Status != "" {
		fields["status"] = e.Status
	}
	switch e.Kind {
	case "status":
		fields["message"] = "remote request returned " + firstNonEmpty(e.Status, "an error")
	case "decode":
		fields["message"] = "remote response decode failed"
	default:
		fields["message"] = "remote transport request failed"
	}
	return fields
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(b[:])
}

func endpointHost(endpoint string) string {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return ""
	}
	return u.Host
}

func sanitizeRemoteBody(raw []byte) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\n", " "))
	text = strings.ReplaceAll(text, "\r", " ")
	fields := strings.Fields(text)
	for i, field := range fields {
		lower := strings.ToLower(field)
		for _, marker := range []string{"token", "secret", "password", "credential", "api_key", "private_key"} {
			if strings.Contains(lower, marker) {
				fields[i] = marker + "=<redacted>"
				break
			}
		}
	}
	text = strings.Join(fields, " ")
	if len(text) > 512 {
		return text[:512] + "…"
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var _ RemoteWorker = HTTPRemoteWorker{}
