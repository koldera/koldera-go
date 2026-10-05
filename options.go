package koldera

import (
	"net/http"
	"time"
)

// Option configures NewClient.
type Option func(*config)

type config struct {
	baseURL         string
	token           string
	httpClient      *http.Client
	nilHTTPClient   bool
	timeout         time.Duration
	userAgentSuffix string
}

// WithBaseURL sets the origin of the koldera-core server, for example https://koldera.example.com.
// Default: the KOLDERA_URL environment variable.
func WithBaseURL(baseURL string) Option {
	return func(c *config) { c.baseURL = baseURL }
}

// WithToken sets the bearer token: a server token, or a client-session token minted by ClientSessions.Create. Any
// non-empty string is accepted; the server decides whether it is valid. Default: the KOLDERA_TOKEN environment
// variable.
func WithToken(token string) Option {
	return func(c *config) { c.token = token }
}

// WithHTTPClient supplies the HTTP client (proxies, TLS, connection pooling, instrumentation). Default: a new
// http.Client. The per-request timeout is applied through the request context, so the client's own Timeout can stay
// unset.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) {
		c.httpClient = hc
		c.nilHTTPClient = hc == nil
	}
}

// WithTimeout sets the time budget of one request (default 30 s). A request that asks the server to wait gets that
// wait plus 10 s when this is shorter.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithUserAgentSuffix appends a product token such as my-app/1.0 to the SDK's User-Agent.
func WithUserAgentSuffix(suffix string) Option {
	return func(c *config) { c.userAgentSuffix = suffix }
}

// CallOption adjusts one call. Options that do not apply to a call are refused with ErrConfiguration instead of
// being ignored.
type CallOption func(*callOptions)

type callOptions struct {
	used []string

	key           string
	wait          int
	untilFinished bool
	pollOptions   []WaitOption
	onResponse    func(ResponseMeta)

	fileName    string
	language    string
	title       string
	selector    string
	contentType UploadContentType
}

// WithIdempotencyKey sets the Idempotency-Key of a creation call (1-255 printable ASCII characters, sent trimmed).
// Without it the SDK generates one per call, so a repeated attempt of the same call is safe. Reuse a key to repeat the
// same logical request yourself; the server replays its stored answer.
func WithIdempotencyKey(key string) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithIdempotencyKey"); o.key = key }
}

// WithWait asks the server to hold the request for up to this many seconds (1-900, sent as Prefer: wait=N). A finished
// job comes back as it is (200); a job still running after the wait comes back with its current state (202). Nothing
// polls on its own unless WithUntilFinished is set.
func WithWait(seconds int) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithWait"); o.wait = seconds }
}

// WithUntilFinished keeps polling with Jobs.Wait when the returned job is not finished yet. The wait options apply to
// that polling.
func WithUntilFinished(poll ...WaitOption) CallOption {
	return func(o *callOptions) {
		o.used = append(o.used, "WithUntilFinished")
		o.untilFinished = true
		o.pollOptions = poll
	}
}

// WithOnResponse receives the metadata of the creation response: status, Location, Preference-Applied and whether the
// answer was an idempotent replay.
func WithOnResponse(fn func(ResponseMeta)) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithOnResponse"); o.onResponse = fn }
}

// WithFileName names the uploaded file (1-255 characters). Default: the name of an *os.File, else document.pdf,
// page.html or image.
func WithFileName(name string) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithFileName"); o.fileName = name }
}

// WithExplicitLanguage gives a BCP 47 tag, used only when the document has no language. It is never guessed.
func WithExplicitLanguage(tag string) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithExplicitLanguage"); o.language = tag }
}

// WithExplicitTitle gives the title to write (trimmed, 1-500 printable characters). It replaces an existing title.
func WithExplicitTitle(title string) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithExplicitTitle"); o.title = title }
}

// WithContentSelector prints only the outermost elements matching this CSS selector (1-200 printable characters)
// instead of the whole page. Whether it is valid CSS only the renderer can tell.
func WithContentSelector(selector string) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithContentSelector"); o.selector = selector }
}

// WithContentType sets the content type of an image upload. Default: the type of the file name's extension.
func WithContentType(contentType UploadContentType) CallOption {
	return func(o *callOptions) { o.used = append(o.used, "WithContentType"); o.contentType = contentType }
}

func buildCallOptions(opts []CallOption, allowed ...string) (*callOptions, error) {
	o := &callOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	for _, name := range o.used {
		ok := false
		for _, a := range allowed {
			if a == name {
				ok = true
				break
			}
		}
		if !ok {
			return nil, configError("option %s does not apply to this call", name)
		}
	}
	return o, nil
}

var jobCreation = []string{"WithIdempotencyKey", "WithWait", "WithUntilFinished", "WithOnResponse"}

func allowing(extra ...string) []string {
	return append(append([]string{}, jobCreation...), extra...)
}

// WaitOption adjusts Jobs.Wait.
type WaitOption func(*waitOptions)

type waitOptions struct {
	timeout      time.Duration
	initialDelay time.Duration
	onUpdate     func(*Job)
}

// WithPollTimeout gives up after this long (default 10 minutes) with a *TimeoutError. The job keeps running on the
// server.
func WithPollTimeout(d time.Duration) WaitOption {
	return func(o *waitOptions) { o.timeout = d }
}

// WithInitialDelay sets the first delay between polls when the server sends no Retry-After (default 1 s). It doubles
// up to 10 s.
func WithInitialDelay(d time.Duration) WaitOption {
	return func(o *waitOptions) { o.initialDelay = d }
}

// WithOnUpdate is called after every poll with the latest state of the job.
func WithOnUpdate(fn func(*Job)) WaitOption {
	return func(o *waitOptions) { o.onUpdate = fn }
}

func buildWaitOptions(opts []WaitOption) (waitOptions, error) {
	w := waitOptions{timeout: 10 * time.Minute, initialDelay: time.Second}
	for _, opt := range opts {
		if opt != nil {
			opt(&w)
		}
	}
	if w.timeout <= 0 || w.initialDelay <= 0 {
		return w, configError("poll timeout and initial delay must be positive, got %s and %s", w.timeout, w.initialDelay)
	}
	return w, nil
}
