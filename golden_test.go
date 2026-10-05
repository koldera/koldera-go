package koldera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	secretKey    = "test-server-token"
	sessionToken = "v1.eyJmYWtlIjoidHJ1ZSJ9.ZmFrZS1zaWduYXR1cmU"
	jobID        = "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01"
	otherJobID   = "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b02"
	scanURL      = "https://example.com/page"
)

var printable = regexp.MustCompile(`^[\x20-\x7e]{1,255}$`)

type fixture struct {
	Name    string `json:"name"`
	Request struct {
		Method           string            `json:"method"`
		Path             string            `json:"path"`
		Headers          map[string]string `json:"headers"`
		ForbiddenHeaders []string          `json:"forbiddenHeaders"`
		Body             json.RawMessage   `json:"body"`
		RawBody          *string           `json:"rawBody"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
		RawBody *string           `json:"rawBody"`
		SHA256  string            `json:"sha256"`
	} `json:"response"`
}

var fixtures = loadFixtures()

func loadFixtures() map[string]*fixture {
	out := map[string]*fixture{}
	files, err := filepath.Glob(filepath.Join("contracts", "golden", "*.json"))
	if err != nil || len(files) == 0 {
		panic("no golden fixtures found")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		var f fixture
		if err := json.Unmarshal(data, &f); err != nil {
			panic(file + ": " + err.Error())
		}
		out[f.Name] = &f
	}
	return out
}

func latin1(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return b
}

func rawRequest(name string) []byte  { return latin1(*fixtures[name].Request.RawBody) }
func rawResponse(name string) []byte { return latin1(*fixtures[name].Response.RawBody) }

func requestBody(name string) map[string]any {
	var v map[string]any
	if err := json.Unmarshal(fixtures[name].Request.Body, &v); err != nil {
		panic(err)
	}
	return v
}

var (
	pdfBytes  = rawRequest("upload-put-pdf")
	htmlBytes = rawRequest("upload-put-html")
	pngBytes  = rawRequest("upload-put-image-png")
)

// Steps of a scripted conversation.
type fix string  // answered from the fixture of that name, request verified
type lost string // the server handles the request (fixture verified) but the answer never arrives
type fail struct{ err error }
type reply struct {
	status  int
	body    any
	headers map[string]string
}

type exchange struct {
	fixture *fixture
	method  string
	uri     string
	header  http.Header
	body    []byte
}

func (e exchange) json(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(e.body, &v); err != nil {
		t.Fatalf("request body is not a JSON object: %v", err)
	}
	return v
}

type golden struct {
	t          *testing.T
	srv        *httptest.Server
	mu         sync.Mutex
	script     []any
	exchanges  []exchange
	mismatches []string
	fallback   http.HandlerFunc
	sleeps     []time.Duration
	budgets    []time.Duration
}

func newGolden(t *testing.T) *golden {
	g := &golden{t: t}
	g.srv = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(func() {
		g.srv.Close()
		g.mu.Lock()
		defer g.mu.Unlock()
		problems := append([]string{}, g.mismatches...)
		for _, s := range g.script {
			problems = append(problems, fmt.Sprintf("step not reached: %v", s))
		}
		if len(problems) > 0 {
			t.Errorf("golden script:\n%s", strings.Join(problems, "\n"))
		}
	})
	return g
}

func (g *golden) expect(steps ...any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.script = append(g.script, steps...)
}

func (g *golden) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.exchanges)
}

func (g *golden) last(n int) []exchange {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]exchange{}, g.exchanges[len(g.exchanges)-n:]...)
}

func (g *golden) resolve(s string) string {
	return strings.NewReplacer("{{baseUrl}}", g.srv.URL, "{{secretKey}}", secretKey, "{{sessionToken}}", sessionToken).Replace(s)
}

// RoundTrip injects transport failures and records the time budget each request was given.
func (g *golden) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	if deadline, ok := req.Context().Deadline(); ok {
		g.budgets = append(g.budgets, time.Until(deadline))
	} else {
		g.budgets = append(g.budgets, 0)
	}
	var head any
	if len(g.script) > 0 {
		head = g.script[0]
	}
	if f, ok := head.(fail); ok {
		g.script = g.script[1:]
		var body []byte
		if req.GetBody != nil {
			rc, _ := req.GetBody()
			body, _ = io.ReadAll(rc)
		}
		g.exchanges = append(g.exchanges, exchange{method: req.Method, uri: req.URL.RequestURI(), header: req.Header.Clone(), body: body})
		g.mu.Unlock()
		return nil, f.err
	}
	g.mu.Unlock()
	resp, err := http.DefaultTransport.RoundTrip(req)
	if _, ok := head.(lost); ok && err == nil {
		_ = resp.Body.Close()
		return nil, errors.New("read: connection reset by peer")
	}
	return resp, err
}

func (g *golden) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	ex := exchange{method: r.Method, uri: r.URL.RequestURI(), header: r.Header.Clone(), body: body}
	if len(g.script) == 0 {
		g.exchanges = append(g.exchanges, ex)
		fallback := g.fallback
		if fallback == nil {
			g.mismatches = append(g.mismatches, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.RequestURI()))
		}
		g.mu.Unlock()
		if fallback != nil {
			fallback(w, r)
			return
		}
		writeJSON(w, 500, map[string]any{"type": "about:blank", "title": "unscripted", "status": 500, "code": "INTERNAL_ERROR"}, nil)
		return
	}
	step := g.script[0]
	g.script = g.script[1:]
	defer g.mu.Unlock()
	switch s := step.(type) {
	case reply:
		g.exchanges = append(g.exchanges, ex)
		writeJSON(w, s.status, s.body, s.headers)
	case fix:
		g.answer(w, ex, fixtures[string(s)])
	case lost:
		g.answer(w, ex, fixtures[string(s)])
	default:
		g.mismatches = append(g.mismatches, fmt.Sprintf("unexpected step %T", step))
	}
}

func writeJSON(w http.ResponseWriter, status int, body any, headers map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (g *golden) answer(w http.ResponseWriter, ex exchange, f *fixture) {
	ex.fixture = f
	g.exchanges = append(g.exchanges, ex)
	g.verify(f, ex)
	for k, v := range f.Response.Headers {
		if !strings.EqualFold(k, "content-length") {
			w.Header().Set(k, g.resolve(v))
		}
	}
	w.WriteHeader(f.Response.Status)
	switch {
	case f.Response.RawBody != nil:
		_, _ = w.Write(latin1(*f.Response.RawBody))
	case len(f.Response.Body) > 0:
		_, _ = w.Write([]byte(g.resolve(string(f.Response.Body))))
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (g *golden) verify(f *fixture, ex exchange) {
	bad := func(format string, args ...any) {
		g.mismatches = append(g.mismatches, f.Name+": "+fmt.Sprintf(format, args...))
	}
	want := f.Request
	if ex.method != want.Method {
		bad("method %s != %s", ex.method, want.Method)
	}
	if ex.uri != want.Path {
		bad("path %s != %s", ex.uri, want.Path)
	}
	for name, value := range want.Headers {
		actual := ex.header.Get(name)
		switch {
		case actual == "":
			bad("missing header %s", name)
		case value == "{{idempotencyKey}}":
			if !printable.MatchString(actual) {
				bad("Idempotency-Key not 1-255 printable ASCII: %q", actual)
			}
		case strings.TrimSpace(strings.Split(actual, ";")[0]) != g.resolve(value):
			bad("header %s: %s != %s", name, actual, g.resolve(value))
		}
	}
	for _, name := range want.ForbiddenHeaders {
		if ex.header.Get(name) != "" {
			bad("forbidden header %s present", name)
		}
	}
	switch {
	case want.RawBody != nil:
		if !bytes.Equal(ex.body, latin1(*want.RawBody)) {
			bad("raw body differs")
		}
	case len(want.Body) > 0:
		if !jsonEqual(ex.body, want.Body) {
			bad("body %s != %s", ex.body, want.Body)
		}
	case len(ex.body) > 0:
		bad("unexpected request body")
	}
}

// client builds a client for the scripted server; the token and base URL can be overridden.
func (g *golden) client(opts ...Option) *Client {
	g.t.Helper()
	all := append([]Option{WithBaseURL(g.srv.URL), WithToken(secretKey), WithHTTPClient(&http.Client{Transport: g})}, opts...)
	c, err := NewClient(all...)
	if err != nil {
		g.t.Fatalf("NewClient: %v", err)
	}
	c.t.sleep = func(ctx context.Context, d time.Duration) error {
		g.mu.Lock()
		g.sleeps = append(g.sleeps, d)
		g.mu.Unlock()
		return ctx.Err()
	}
	return c
}

func (g *golden) slept() []time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]time.Duration{}, g.sleeps...)
}

func jobJSON(fields map[string]any) map[string]any {
	base := map[string]any{
		"id": jobID, "kind": "pdf", "mode": "analyze", "state": "queued", "createdAt": "2026-10-05T10:00:00Z",
		"startedAt": nil, "finishedAt": nil, "validation": nil, "baselineValidation": nil, "finalValidation": nil,
		"remediationStatus": nil, "manualReviewRequired": true, "engineVersion": nil, "error": nil, "targetUrl": nil,
	}
	for k, v := range fields {
		base[k] = v
	}
	return base
}

func problemJSON(status int, code string, extra map[string]any) map[string]any {
	p := map[string]any{"type": "about:blank", "title": "stub", "status": status, "code": code}
	for k, v := range extra {
		p[k] = v
	}
	return p
}
