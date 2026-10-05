package koldera

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sdkName          = "koldera-sdk-go"
	maxBusyRetries   = 2
	maxNetRetries    = 2
	netRetryDelay    = 250 * time.Millisecond
	maxPollDelay     = 10 * time.Second
	maxResponseBytes = 16 << 20
)

type transport struct {
	http      *http.Client
	base      string
	token     string
	timeout   time.Duration
	userAgent string
	sleep     func(context.Context, time.Duration) error
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NewIdempotencyKey returns 32 hex characters from the system CSPRNG. The SDK generates one per creation call.
func NewIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("koldera: the system random source failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

type callSpec struct {
	method      string
	path        string
	body        any
	key         string
	wait        int
	anonymous   bool
	retryOnBusy bool
}

type callResult struct {
	status     int
	body       []byte
	retryAfter *time.Duration
	meta       ResponseMeta
}

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// call performs one JSON exchange. Reads are repeated after a 429 or 503 when asked to; a creation is repeated after
// a transport failure only because it carries an Idempotency-Key, with the same key.
func (t *transport) call(ctx context.Context, s callSpec) (*callResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var payload []byte
	if s.body != nil {
		var err error
		if payload, err = encodeJSON(s.body); err != nil {
			return nil, configError("the request body cannot be encoded: %v", err)
		}
	}
	timeout := t.timeout
	if s.wait > 0 {
		if held := time.Duration(s.wait+10) * time.Second; held > timeout {
			timeout = held
		}
	}
	repeatable := s.method == http.MethodGet || s.key != ""
	network, busy := 0, 0
	for {
		res, err := t.once(ctx, s, payload, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, ErrConfiguration) {
				return nil, err
			}
			// A held request that timed out would only hold again for as long; connection failures are repeated.
			heldTimeout := s.wait > 0 && isTimeout(err)
			if repeatable && !heldTimeout && network < maxNetRetries {
				network++
				if sleepErr := t.sleep(ctx, netRetryDelay*time.Duration(network)); sleepErr != nil {
					return nil, sleepErr
				}
				continue
			}
			return nil, fmt.Errorf("request to %s failed: %w", s.path, t.redact(err))
		}
		if res.status >= 200 && res.status < 300 {
			return res, nil
		}
		if s.method == http.MethodGet && s.retryOnBusy && (res.status == 429 || res.status == 503) && busy < maxBusyRetries {
			busy++
			pause := time.Second
			if res.retryAfter != nil {
				pause = *res.retryAfter
			}
			if pause > maxPollDelay {
				pause = maxPollDelay
			}
			if sleepErr := t.sleep(ctx, pause); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		return nil, errorFromProblem(readProblem(res.status, res.body), res.retryAfter)
	}
}

func (t *transport) once(ctx context.Context, s callSpec, payload []byte, timeout time.Duration) (*callResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, s.method, t.base+s.path, body)
	if err != nil {
		return nil, configError("the request cannot be built: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if !s.anonymous {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.key != "" {
		req.Header.Set("Idempotency-Key", s.key)
	}
	if s.wait > 0 {
		req.Header.Set("Prefer", "wait="+strconv.Itoa(s.wait))
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	return &callResult{
		status:     resp.StatusCode,
		body:       data,
		retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		meta: ResponseMeta{
			Status:             resp.StatusCode,
			Location:           resp.Header.Get("Location"),
			PreferenceApplied:  resp.Header.Get("Preference-Applied"),
			IdempotentReplayed: strings.EqualFold(resp.Header.Get("Idempotent-Replayed"), "true"),
		},
	}, nil
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// redact removes the token from the text of a transport error.
func (t *transport) redact(err error) error {
	if len(t.token) < 8 || !strings.Contains(err.Error(), t.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.token, "***"))
}

func readProblem(status int, body []byte) problem {
	title := http.StatusText(status)
	if title == "" {
		title = "HTTP error"
	}
	fallback := problem{Type: "about:blank", Title: title, Status: status, Code: "UNKNOWN"}
	if status >= 500 {
		fallback.Code = "INTERNAL_ERROR"
	}
	var raw struct {
		Type   string            `json:"type"`
		Title  string            `json:"title"`
		Code   string            `json:"code"`
		Detail string            `json:"detail"`
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Code == "" {
		return fallback
	}
	p := problem{Type: raw.Type, Title: raw.Title, Status: status, Code: raw.Code, Detail: raw.Detail}
	if p.Type == "" {
		p.Type = fallback.Type
	}
	if p.Title == "" {
		p.Title = fallback.Title
	}
	for _, item := range raw.Errors {
		var e struct {
			Path    *string `json:"path"`
			Code    *string `json:"code"`
			Message *string `json:"message"`
		}
		if json.Unmarshal(item, &e) == nil && e.Path != nil && e.Code != nil && e.Message != nil {
			p.Errors = append(p.Errors, ProblemError{Path: *e.Path, Code: *e.Code, Message: *e.Message})
		}
	}
	return p
}

// parseRetryAfter reads whole or fractional seconds, or an HTTP date.
func parseRetryAfter(value string) *time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > float64(math.MaxInt64/int64(time.Second)) {
			return nil
		}
		d := time.Duration(seconds * float64(time.Second))
		return &d
	}
	if at, err := http.ParseTime(value); err == nil {
		d := time.Until(at)
		if d < 0 {
			d = 0
		}
		return &d
	}
	return nil
}

func malformed(status int) error {
	return &ServerError{APIError{Status: status, Code: "INTERNAL_ERROR", Title: "Malformed response"}}
}

// decode parses a success body into T; a body of the wrong shape is a *ServerError, never a panic or a zero value.
func decode[T any](res *callResult, valid func(*T) bool) (*T, error) {
	var v T
	if err := json.Unmarshal(res.body, &v); err != nil || (valid != nil && !valid(&v)) {
		return nil, malformed(res.status)
	}
	return &v, nil
}

type itemList[T any] struct {
	Items *[]T `json:"items"`
}

func decodeItems[T any](res *callResult) ([]T, error) {
	list, err := decode[itemList[T]](res, func(l *itemList[T]) bool { return l.Items != nil })
	if err != nil {
		return nil, err
	}
	return *list.Items, nil
}

// storagePut sends bytes to a signed upload URL without any credential.
func (t *transport) storagePut(ctx context.Context, target string, data []byte, contentType UploadContentType) error {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, target, bytes.NewReader(data))
	if err != nil {
		return &StorageError{Message: "the upload URL is not usable: " + t.redact(err).Error()}
	}
	req.Header.Set("Content-Type", string(contentType))
	req.Header.Set("User-Agent", t.userAgent)
	resp, err := t.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &StorageError{Message: "upload failed: " + t.redact(err).Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return storageFailure(resp, http.MethodPut)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

// storageGet fetches a signed download URL without any credential and never reads more than expected+1 bytes.
func (t *transport) storageGet(ctx context.Context, target string, expected int64) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, &StorageError{Message: "the download URL is not usable: " + t.redact(err).Error()}
	}
	req.Header.Set("User-Agent", t.userAgent)
	resp, err := t.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &StorageError{Message: "download failed: " + t.redact(err).Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, storageFailure(resp, http.MethodGet)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, expected+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &StorageError{Message: "download failed: " + t.redact(err).Error()}
	}
	if int64(len(data)) > expected {
		return nil, &StorageError{Message: fmt.Sprintf("storage sent more than the declared %d bytes", expected)}
	}
	return data, nil
}

func storageFailure(resp *http.Response, method string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	p := readProblem(resp.StatusCode, body)
	return &StorageError{Message: fmt.Sprintf("storage answered HTTP %d (%s) to %s", resp.StatusCode, p.Code, method), Status: resp.StatusCode, Code: p.Code}
}
