package koldera

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version is the version of this SDK.
const Version = "0.1.0"

// CoreAPIVersion is the version of the koldera-core API this SDK targets.
const CoreAPIVersion = "0.1"

// Client talks to one koldera-core server. It is safe for concurrent use; create one and reuse it. Creation calls
// carry an Idempotency-Key (WithIdempotencyKey overrides the generated one), so a repeat after a lost response is
// replayed by the server. WithWait holds the request until the job finishes; WithUntilFinished keeps polling.
// Handle failures with errors.As on the error types and errors.Is on the Err sentinels.
type Client struct {
	Uploads        *Uploads
	Jobs           *Jobs
	PDF            *PDF
	HTMLToPDF      *HTMLToPDF
	Web            *Web
	Documents      *Documents
	Artifacts      *Artifacts
	ClientSessions *ClientSessions

	t *transport
}

// NewClient validates the configuration and sends nothing. The base URL comes from WithBaseURL or KOLDERA_URL, the
// token from WithToken or KOLDERA_TOKEN.
func NewClient(opts ...Option) (*Client, error) {
	cfg := config{timeout: 30 * time.Second}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.token == "" {
		cfg.token = os.Getenv("KOLDERA_TOKEN")
	}
	if cfg.baseURL == "" {
		cfg.baseURL = os.Getenv("KOLDERA_URL")
	}
	if strings.TrimSpace(cfg.token) == "" {
		return nil, configError("a token is required: use WithToken or set KOLDERA_TOKEN")
	}
	if cfg.baseURL == "" {
		return nil, configError("a base URL is required: use WithBaseURL or set KOLDERA_URL")
	}
	u, err := url.Parse(cfg.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, configError("the base URL must be an absolute http(s) URL")
	}
	if cfg.timeout <= 0 {
		return nil, configError("the timeout must be positive")
	}
	if cfg.nilHTTPClient {
		return nil, configError("the HTTP client must not be nil")
	}
	hc := cfg.httpClient
	if hc == nil {
		hc = &http.Client{}
	}
	agent := sdkName + "/" + Version
	if cfg.userAgentSuffix != "" {
		agent += " " + cfg.userAgentSuffix
	}
	t := &transport{http: hc, base: strings.TrimRight(cfg.baseURL, "/"), token: cfg.token, timeout: cfg.timeout, userAgent: agent, sleep: sleepContext}
	c := &Client{t: t}
	c.Uploads = &Uploads{c: c}
	c.Jobs = &Jobs{c: c}
	c.PDF = &PDF{c: c}
	c.HTMLToPDF = &HTMLToPDF{c: c}
	c.Web = &Web{c: c}
	c.Documents = &Documents{c: c}
	c.Artifacts = &Artifacts{c: c}
	c.ClientSessions = &ClientSessions{c: c}
	return c, nil
}

// String never shows the token.
func (c *Client) String() string { return "koldera.Client{" + c.t.base + "}" }

// GoString never shows the token.
func (c *Client) GoString() string { return c.String() }

// Info returns the version and enabled features of the server (GET /v1/info).
func (c *Client) Info(ctx context.Context) (*Info, error) {
	res, err := c.t.call(ctx, callSpec{method: http.MethodGet, path: "/v1/info", retryOnBusy: true})
	if err != nil {
		return nil, err
	}
	return decode[Info](res, nil)
}

// Health is the liveness probe (GET /healthz). It sends no credential.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	res, err := c.t.call(ctx, callSpec{method: http.MethodGet, path: "/healthz", anonymous: true, retryOnBusy: true})
	if err != nil {
		return nil, err
	}
	return decode[Health](res, nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validJob(j *Job) bool { return j.ID != "" && j.State != "" }

// createJob posts a job request and applies the wait options: a 200 (finished) and a 202 (still running) are returned
// as they are, and the job is polled to its end only when WithUntilFinished asks for it.
func (c *Client) createJob(ctx context.Context, path string, body any, key string, o *callOptions) (*Job, error) {
	res, err := c.t.call(ctx, callSpec{method: http.MethodPost, path: path, body: body, key: key, wait: o.wait})
	if err != nil {
		return nil, err
	}
	if o.onResponse != nil {
		o.onResponse(res.meta)
	}
	job, err := decode[Job](res, validJob)
	if err != nil {
		return nil, err
	}
	if o.untilFinished && !job.IsFinished() {
		return c.Jobs.Wait(ctx, job.ID, o.pollOptions...)
	}
	return job, nil
}

// ---- uploads ------------------------------------------------------------------------------------

// Uploads issues tickets and sends bytes to storage.
type Uploads struct{ c *Client }

// Upload reads r, issues a ticket and PUTs the bytes to its signed URL (no bearer on the PUT). It returns the consumed
// ticket; its UploadID is what job requests and image blocks refer to. The SDK reads at most 100 MiB.
func (u *Uploads) Upload(ctx context.Context, r io.Reader, contentType UploadContentType, fileName string, opts ...CallOption) (*UploadTicket, error) {
	o, err := buildCallOptions(opts, "WithIdempotencyKey", "WithOnResponse")
	if err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	return u.c.upload(ctx, r, contentType, fileName, key, o.onResponse)
}

func (c *Client) upload(ctx context.Context, r io.Reader, contentType UploadContentType, fileName, key string, onResponse func(ResponseMeta)) (*UploadTicket, error) {
	if err := checkUpload(contentType, fileName); err != nil {
		return nil, err
	}
	data, err := readSource(r)
	if err != nil {
		return nil, err
	}
	body := struct {
		FileName    string            `json:"fileName"`
		SizeBytes   int               `json:"sizeBytes"`
		SHA256      string            `json:"sha256"`
		ContentType UploadContentType `json:"contentType"`
	}{fileName, len(data), sha256Hex(data), contentType}
	res, err := c.t.call(ctx, callSpec{method: http.MethodPost, path: "/v1/uploads", body: body, key: key})
	if err != nil {
		return nil, err
	}
	if onResponse != nil {
		onResponse(res.meta)
	}
	ticket, err := decode[UploadTicket](res, func(t *UploadTicket) bool { return t.UploadID != "" && t.UploadURL != "" })
	if err != nil {
		return nil, err
	}
	if ticket.MaxBytes != nil && int64(len(data)) > *ticket.MaxBytes {
		return nil, configError("the file (%d bytes) exceeds the ticket limit of %d bytes", len(data), *ticket.MaxBytes)
	}
	if err := c.t.storagePut(ctx, ticket.UploadURL, data, contentType); err != nil {
		// A replayed ticket belongs to an earlier attempt whose PUT already stored these exact bytes (size and hash
		// are part of the replayed request).
		storage, isStorage := err.(*StorageError)
		stored := isStorage && storage.Status == http.StatusConflict && storage.Code == "UPLOAD_ALREADY_COMPLETED"
		if !(res.meta.IdempotentReplayed && stored) {
			return nil, err
		}
	}
	return ticket, nil
}

// ---- PDF ------------------------------------------------------------------------------------------

type jobBody struct {
	UploadID         string `json:"uploadId"`
	Mode             string `json:"mode"`
	Profile          string `json:"profile"`
	ContentSelector  string `json:"contentSelector,omitempty"`
	ExplicitLanguage string `json:"explicitLanguage,omitempty"`
	ExplicitTitle    string `json:"explicitTitle,omitempty"`
}

// PDF creates PDF jobs.
type PDF struct{ c *Client }

// CreateJob uploads the PDF and queues an analyze or fix job. Options: WithFileName, WithExplicitLanguage,
// WithExplicitTitle, WithIdempotencyKey, WithWait, WithUntilFinished, WithOnResponse.
func (p *PDF) CreateJob(ctx context.Context, pdf io.Reader, mode PDFMode, opts ...CallOption) (*Job, error) {
	o, err := buildCallOptions(opts, allowing("WithFileName", "WithExplicitLanguage", "WithExplicitTitle")...)
	if err != nil {
		return nil, err
	}
	if err := checkMode(string(mode), string(PDFModeAnalyze), string(PDFModeFix)); err != nil {
		return nil, err
	}
	if err := checkLanguage(o); err != nil {
		return nil, err
	}
	title, err := checkTitle(o)
	if err != nil {
		return nil, err
	}
	if err := checkWait(o); err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	ticket, err := p.c.upload(ctx, pdf, ContentTypePDF, fileNameOption(o, pdf, "document.pdf"), derivedKey(key, "upload"), nil)
	if err != nil {
		return nil, err
	}
	body := jobBody{UploadID: ticket.UploadID, Mode: string(mode), Profile: "ua1", ExplicitLanguage: o.language, ExplicitTitle: title}
	return p.c.createJob(ctx, "/v1/pdf/jobs", body, key, o)
}

// HTMLToPDF renders one self-contained static HTML file to a tagged PDF. It never takes a URL.
type HTMLToPDF struct{ c *Client }

// CreateJob uploads the HTML and queues render, tag inspection and validation. Options: WithFileName,
// WithExplicitLanguage, WithExplicitTitle, WithContentSelector, WithIdempotencyKey, WithWait, WithUntilFinished,
// WithOnResponse.
func (h *HTMLToPDF) CreateJob(ctx context.Context, html io.Reader, mode HTMLMode, opts ...CallOption) (*Job, error) {
	o, err := buildCallOptions(opts, allowing("WithFileName", "WithExplicitLanguage", "WithExplicitTitle", "WithContentSelector")...)
	if err != nil {
		return nil, err
	}
	if err := checkMode(string(mode), string(HTMLModeGenerateAnalyze), string(HTMLModeGenerateFix)); err != nil {
		return nil, err
	}
	if err := checkLanguage(o); err != nil {
		return nil, err
	}
	title, err := checkTitle(o)
	if err != nil {
		return nil, err
	}
	selector, err := checkSelector(o)
	if err != nil {
		return nil, err
	}
	if err := checkWait(o); err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	ticket, err := h.c.upload(ctx, html, ContentTypeHTML, fileNameOption(o, html, "page.html"), derivedKey(key, "upload"), nil)
	if err != nil {
		return nil, err
	}
	body := jobBody{UploadID: ticket.UploadID, Mode: string(mode), Profile: "ua1", ContentSelector: selector, ExplicitLanguage: o.language, ExplicitTitle: title}
	return h.c.createJob(ctx, "/v1/html-to-pdf/jobs", body, key, o)
}

// ---- web ------------------------------------------------------------------------------------------

// Web queues scans of public pages.
type Web struct{ c *Client }

// CreateScan queues one automated scan of a publicly reachable http(s) URL.
func (w *Web) CreateScan(ctx context.Context, target string, opts ...CallOption) (*Job, error) {
	o, err := buildCallOptions(opts, jobCreation...)
	if err != nil {
		return nil, err
	}
	if err := checkScanURL(target); err != nil {
		return nil, err
	}
	if err := checkWait(o); err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	return w.c.createJob(ctx, "/v1/web/jobs", struct {
		URL string `json:"url"`
	}{target}, key, o)
}

// ---- documents ------------------------------------------------------------------------------------

// Documents renders structured documents to tagged PDF.
type Documents struct{ c *Client }

// CreateJob queues a structured document for rendering. Images are referenced by the UploadID of UploadImage. Structure
// is declared by the document, never inferred.
func (d *Documents) CreateJob(ctx context.Context, doc Document, opts ...CallOption) (*Job, error) {
	o, err := buildCallOptions(opts, jobCreation...)
	if err != nil {
		return nil, err
	}
	if err := checkWait(o); err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	return d.c.createJob(ctx, "/v1/documents/jobs", doc, key, o)
}

// UploadImage uploads one PNG, JPEG or SVG image for use in an Image block. The type comes from WithContentType, else
// from the extension of the file name (WithFileName, or the name of an *os.File).
func (d *Documents) UploadImage(ctx context.Context, image io.Reader, opts ...CallOption) (*UploadTicket, error) {
	o, err := buildCallOptions(opts, "WithIdempotencyKey", "WithOnResponse", "WithFileName", "WithContentType")
	if err != nil {
		return nil, err
	}
	contentType, err := imageTypeOf(o, image)
	if err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	name := fileNameOption(o, image, "image"+map[UploadContentType]string{ContentTypePNG: ".png", ContentTypeJPEG: ".jpg", ContentTypeSVG: ".svg"}[contentType])
	return d.c.upload(ctx, image, contentType, name, key, o.onResponse)
}

// ---- jobs -----------------------------------------------------------------------------------------

// Jobs reads jobs, polls them and works with reading-order evidence.
type Jobs struct{ c *Client }

func (j *Jobs) read(ctx context.Context, id, suffix string) (*callResult, error) {
	path, err := jobPath(id)
	if err != nil {
		return nil, err
	}
	return j.c.t.call(ctx, callSpec{method: http.MethodGet, path: "/v1/jobs/" + path + suffix, retryOnBusy: true})
}

// Get returns the current state of a job. Reads are repeated at most twice after a 429 or 503 (honouring
// Retry-After).
func (j *Jobs) Get(ctx context.Context, id string) (*Job, error) {
	res, err := j.read(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return decode[Job](res, validJob)
}

// Wait polls until the job is no longer queued or running and returns it. A failed job is returned, not an error: read
// Job.Error. It honours Retry-After, otherwise backs off from 1 s up to 10 s. Cancel through ctx; WithPollTimeout
// bounds the wait with a *TimeoutError.
func (j *Jobs) Wait(ctx context.Context, id string, opts ...WaitOption) (*Job, error) {
	w, err := buildWaitOptions(opts)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	delay := w.initialDelay
	for {
		res, err := j.read(ctx, id, "")
		if err != nil {
			return nil, err
		}
		job, err := decode[Job](res, validJob)
		if err != nil {
			return nil, err
		}
		if w.onUpdate != nil {
			w.onUpdate(job)
		}
		if job.IsFinished() {
			return job, nil
		}
		pause := delay
		if res.retryAfter != nil {
			pause = *res.retryAfter
		}
		if time.Since(started)+pause > w.timeout {
			return nil, &TimeoutError{JobID: job.ID, Timeout: w.timeout}
		}
		if err := j.c.t.sleep(ctx, pause); err != nil {
			return nil, err
		}
		if delay *= 2; delay > maxPollDelay {
			delay = maxPollDelay
		}
	}
}

// Findings lists the findings of a finished job. Machine checks and items for a person are mixed and flagged.
func (j *Jobs) Findings(ctx context.Context, id string) ([]Finding, error) {
	res, err := j.read(ctx, id, "/findings")
	if err != nil {
		return nil, err
	}
	return decodeItems[Finding](res)
}

// Actions tells what each repair rule did on a fix job: applied, skipped, failed or rolled_back, with a reason.
func (j *Jobs) Actions(ctx context.Context, id string) ([]RemediationAction, error) {
	res, err := j.read(ctx, id, "/actions")
	if err != nil {
		return nil, err
	}
	return decodeItems[RemediationAction](res)
}

// Artifacts lists the files of a job with fresh signed download URLs. Call it when needed; links expire.
func (j *Jobs) Artifacts(ctx context.Context, id string) ([]Artifact, error) {
	res, err := j.read(ctx, id, "/artifacts")
	if err != nil {
		return nil, err
	}
	return decodeItems[Artifact](res)
}

// ReadingOrder returns the evidence bound to the artifact hash. ReviewRequired is always true.
func (j *Jobs) ReadingOrder(ctx context.Context, id string) (*ReadingOrderSnapshot, error) {
	res, err := j.read(ctx, id, "/reading-order")
	if err != nil {
		return nil, err
	}
	return decode[ReadingOrderSnapshot](res, func(s *ReadingOrderSnapshot) bool { return s.JobID != "" })
}

// RemediateReadingOrder queues a correction a person approved: the complete children of one parent in the new order,
// bound to the snapshot's artifact hash and version. Acknowledged must be true. A *ConflictError means the snapshot is
// stale or the artifact expired (load it again and review); a *ValidationError with code ORDER_FIX_UNSUPPORTED means
// the plan cannot be applied safely. Options: WithIdempotencyKey, WithWait, WithUntilFinished, WithOnResponse.
func (j *Jobs) RemediateReadingOrder(ctx context.Context, id string, approval ReadingOrderRemediation, opts ...CallOption) (*Job, error) {
	o, err := buildCallOptions(opts, jobCreation...)
	if err != nil {
		return nil, err
	}
	path, err := jobPath(id)
	if err != nil {
		return nil, err
	}
	if !approval.Acknowledged {
		return nil, configError("a reading-order correction must be acknowledged by the reviewer (Acknowledged: true)")
	}
	if len(approval.OrderedNodeIDs) < 2 || hasDuplicateOrEmpty(approval.OrderedNodeIDs) {
		return nil, configError("OrderedNodeIDs must list at least two distinct node ids")
	}
	if !sha256Form.MatchString(approval.BaseArtifactSHA256) {
		return nil, configError("BaseArtifactSHA256 must be the 64-hex artifact hash from the snapshot")
	}
	if approval.Target != RemediationTargetPDFTags && approval.Target != RemediationTargetSourceHTML {
		return nil, configError("the target must be pdf_tags or source_html")
	}
	reviewer, err := checkReviewer(approval.Reviewer)
	if err != nil {
		return nil, err
	}
	if err := checkWait(o); err != nil {
		return nil, err
	}
	key, err := o.idempotencyKey()
	if err != nil {
		return nil, err
	}
	body := struct {
		BaseArtifactSHA256 string            `json:"baseArtifactSha256"`
		SnapshotVersion    int               `json:"snapshotVersion"`
		Target             RemediationTarget `json:"target"`
		OrderedNodeIDs     []string          `json:"orderedNodeIds"`
		Acknowledged       bool              `json:"acknowledged"`
		Reviewer           string            `json:"reviewer,omitempty"`
	}{approval.BaseArtifactSHA256, approval.SnapshotVersion, approval.Target, approval.OrderedNodeIDs, true, reviewer}
	return j.c.createJob(ctx, "/v1/jobs/"+path+"/reading-order/remediations", body, key, o)
}

func hasDuplicateOrEmpty(ids []string) bool {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup || id == "" {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

// ---- artifacts ------------------------------------------------------------------------------------

// Artifacts downloads files through their signed URLs.
type Artifacts struct{ c *Client }

func (a *Artifacts) fetch(ctx context.Context, artifact Artifact) ([]byte, error) {
	data, err := a.c.t.storageGet(ctx, artifact.DownloadURL, artifact.SizeBytes)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != artifact.SizeBytes {
		return nil, &StorageError{Message: fmt.Sprintf("artifact %s: received %d bytes, expected %d", artifact.Kind, len(data), artifact.SizeBytes)}
	}
	if !strings.EqualFold(sha256Hex(data), artifact.SHA256) {
		return nil, &StorageError{Message: fmt.Sprintf("artifact %s: SHA-256 mismatch", artifact.Kind)}
	}
	return data, nil
}

// Download fetches the artifact (no bearer is sent) and returns its bytes after the declared size and SHA-256 have been
// verified. Nothing is returned for bytes that do not match.
func (a *Artifacts) Download(ctx context.Context, artifact Artifact) ([]byte, error) {
	return a.fetch(ctx, artifact)
}

// DownloadTo verifies the artifact like Download and only then writes it to w.
func (a *Artifacts) DownloadTo(ctx context.Context, artifact Artifact, w io.Writer) error {
	data, err := a.fetch(ctx, artifact)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// DownloadToFile verifies the artifact like Download and only then writes the file. A file is never left behind for
// bytes that do not match.
func (a *Artifacts) DownloadToFile(ctx context.Context, artifact Artifact, path string) error {
	data, err := a.fetch(ctx, artifact)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ---- client sessions ------------------------------------------------------------------------------

// ClientSessions mints short-lived tokens for browsers.
type ClientSessions struct{ c *Client }

// Create mints a scoped session for one of your authenticated end users. Call it from your backend only after you
// verified the user, and hand SessionToken to the browser, never the server token. The call carries no idempotency key
// and is not repeated.
func (s *ClientSessions) Create(ctx context.Context, req ClientSessionRequest) (*ClientSession, error) {
	if err := checkOrigin(req.AllowedOrigin); err != nil {
		return nil, err
	}
	res, err := s.c.t.call(ctx, callSpec{method: http.MethodPost, path: "/v1/client-sessions", body: req})
	if err != nil {
		return nil, err
	}
	return decode[ClientSession](res, func(c *ClientSession) bool { return c.SessionToken != "" })
}
