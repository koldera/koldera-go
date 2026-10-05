package koldera

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var bg = context.Background()

var (
	uploadPDF  = []any{fix("upload-ticket-pdf"), fix("upload-put-pdf")}
	uploadHTML = []any{fix("upload-ticket-html"), fix("upload-put-html")}
)

func steps(groups ...any) []any {
	var out []any
	for _, g := range groups {
		if list, ok := g.([]any); ok {
			out = append(out, list...)
		} else {
			out = append(out, g)
		}
	}
	return out
}

func needErr(t *testing.T, err error) error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func wantConfig(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a configuration error containing %q", contains)
	}
	if !errors.Is(err, ErrConfiguration) || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v; want ErrConfiguration containing %q", err, contains)
	}
}

func jobOf(t *testing.T) func(*Job, error) *Job {
	return func(job *Job, err error) *Job {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return job
	}
}

// ---- configuration -----------------------------------------------------------------------------

func TestConfiguration(t *testing.T) {
	t.Setenv("KOLDERA_URL", "")
	t.Setenv("KOLDERA_TOKEN", "")
	cases := []struct {
		name string
		opts []Option
		want string
	}{
		{"no token", []Option{WithBaseURL("http://core.test")}, "KOLDERA_TOKEN"},
		{"blank token", []Option{WithBaseURL("http://core.test"), WithToken("   ")}, "KOLDERA_TOKEN"},
		{"no url", []Option{WithToken("x")}, "KOLDERA_URL"},
		{"bad url", []Option{WithToken("x"), WithBaseURL("not a url")}, "absolute http(s) URL"},
		{"ftp url", []Option{WithToken("x"), WithBaseURL("ftp://core.test")}, "absolute http(s) URL"},
		{"zero timeout", []Option{WithToken("x"), WithBaseURL("http://core.test"), WithTimeout(0)}, "timeout"},
		{"nil http client", []Option{WithToken("x"), WithBaseURL("http://core.test"), WithHTTPClient(nil)}, "HTTP client"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewClient(c.opts...)
			wantConfig(t, err, c.want)
		})
	}
}

func TestConfigurationFromEnvironment(t *testing.T) {
	t.Setenv("KOLDERA_URL", "http://core.test/")
	t.Setenv("KOLDERA_TOKEN", secretKey)
	if _, err := NewClient(); err != nil {
		t.Fatalf("NewClient from env: %v", err)
	}
}

func TestAnyNonEmptyTokenIsAccepted(t *testing.T) {
	for _, token := range []string{"x", "pk_looks_like_a_vendor_key", "plain token with spaces", "0123456789"} {
		if _, err := NewClient(WithBaseURL("http://core.test"), WithToken(token)); err != nil {
			t.Errorf("token %q: %v", token, err)
		}
	}
}

func TestSendsTheTokenAsBearerAndIdentifiesTheSDK(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("info"))
	if _, err := g.client().Info(bg); err != nil {
		t.Fatal(err)
	}
	h := g.last(1)[0].header
	if h.Get("Authorization") != "Bearer "+secretKey {
		t.Errorf("Authorization = %q", h.Get("Authorization"))
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "koldera-sdk-go/") {
		t.Errorf("User-Agent = %q", h.Get("User-Agent"))
	}
	g.expect(fix("info"))
	if _, err := g.client(WithUserAgentSuffix("my-app/1.0")).Info(bg); err != nil {
		t.Fatal(err)
	}
	if ua := g.last(1)[0].header.Get("User-Agent"); !strings.HasSuffix(ua, " my-app/1.0") {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestFormattingAClientNeverShowsTheToken(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	for _, text := range []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(text, secretKey) {
			t.Fatalf("formatted client contains the token: %s", text)
		}
	}
}

func TestClientIsSafeForConcurrentUse(t *testing.T) {
	g := newGolden(t)
	g.fallback = func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, jobJSON(nil), nil) }
	c := g.client()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Jobs.Get(bg, jobID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// ---- service -----------------------------------------------------------------------------------

func TestInfoAndHealth(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	g.expect(fix("info"))
	info, err := c.Info(bg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(info.Features, []Feature{FeatureDocument, FeatureHTML, FeaturePDF, FeatureWeb}) || info.Version != "0.1.0" || info.EngineVersion != "0.1.0" {
		t.Errorf("info = %+v", info)
	}
	g.expect(fix("info-session"))
	session, err := g.client(WithToken(sessionToken)).Info(bg)
	if err != nil || len(session.Features) == 0 {
		t.Fatalf("session info = %+v, %v", session, err)
	}
	g.expect(fix("healthz"))
	health, err := c.Health(bg)
	if err != nil || health.Status != "ok" {
		t.Fatalf("health = %+v, %v", health, err)
	}
	if g.last(1)[0].header.Get("Authorization") != "" {
		t.Error("health sent a credential")
	}
}

// ---- job creation ------------------------------------------------------------------------------

func TestPDFIssuesATicketPutsTheBytesWithoutTheBearerAndQueuesTheJob(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-accepted"))...)
	job := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeAnalyze, WithFileName("sample.pdf")))
	if job.ID != jobID || job.State != JobStateQueued || !job.ManualReviewRequired || job.Kind != JobKindPDF {
		t.Errorf("job = %+v", job)
	}
	ex := g.last(3)
	if ex[0].header.Get("Idempotency-Key") == "" || ex[0].header.Get("Idempotency-Key") == ex[2].header.Get("Idempotency-Key") {
		t.Error("the ticket and the job must carry different keys")
	}
	if ex[1].header.Get("Authorization") != "" {
		t.Error("the storage PUT carried the bearer")
	}
}

func TestPDFFixSendsLanguageAndTrimmedTitleAndOmitsWhatIsAbsent(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-accepted-fix"))...)
	job := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithExplicitLanguage("en"), WithExplicitTitle("  Sample report  ")))
	if job.Mode != JobModeFix {
		t.Errorf("mode = %q", job.Mode)
	}
	body := g.last(1)[0].json(t)
	if body["explicitTitle"] != "Sample report" || body["explicitLanguage"] != "en" {
		t.Errorf("body = %v", body)
	}
	g.expect(steps(uploadPDF, fix("pdf-job-accepted"))...)
	jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeAnalyze, WithFileName("sample.pdf")))
	want := map[string]any{"uploadId": "11111111-1111-4111-8111-111111111111", "mode": "analyze", "profile": "ua1"}
	if got := g.last(1)[0].json(t); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v; want only %v", got, want)
	}
}

func TestPDFAcceptsOnlyAnalyzeAndFix(t *testing.T) {
	g := newGolden(t)
	for _, mode := range []PDFMode{"safe_fix", "structure_fix", "scan", "generate_fix", ""} {
		_, err := g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), mode)
		wantConfig(t, err, "analyze, fix")
	}
}

func TestHTMLToPDFUploadsHTMLWithSelectorLanguageAndTitle(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadHTML, fix("html-job-accepted"))...)
	c := g.client()
	job := jobOf(t)(c.HTMLToPDF.CreateJob(bg, bytes.NewReader(htmlBytes), HTMLModeGenerateFix, WithFileName("page.html"), WithContentSelector("main"), WithExplicitLanguage("en"), WithExplicitTitle("Sample page")))
	if job.Kind != JobKindHTMLToPDF || job.ContentSelector != "main" {
		t.Errorf("job = %+v", job)
	}
	g.expect(steps(uploadHTML, fix("html-job-analyze"))...)
	analyze := jobOf(t)(c.HTMLToPDF.CreateJob(bg, bytes.NewReader(htmlBytes), HTMLModeGenerateAnalyze, WithFileName("page.html")))
	if analyze.Mode != JobModeGenerateAnalyze {
		t.Errorf("mode = %q", analyze.Mode)
	}
}

func TestHTMLToPDFRefusesRetiredModeBlankSelectorAndPDFOnlyOptions(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	_, err := c.HTMLToPDF.CreateJob(bg, bytes.NewReader(htmlBytes), "generate_safe_fix")
	wantConfig(t, err, "generate_analyze, generate_fix")
	_, err = c.HTMLToPDF.CreateJob(bg, bytes.NewReader(htmlBytes), HTMLModeGenerateFix, WithContentSelector("   "))
	wantConfig(t, err, "content selector")
	_, err = c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithContentSelector("main"))
	wantConfig(t, err, "WithContentSelector")
	_, err = c.Web.CreateScan(bg, scanURL, WithExplicitTitle("x"))
	wantConfig(t, err, "WithExplicitTitle")
}

func TestWebScanSendsTheURLAndRefusesOtherSchemesLocally(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("web-job-accepted"))
	job := jobOf(t)(g.client().Web.CreateScan(bg, scanURL))
	if job.TargetURL != scanURL || job.Mode != JobModeScan {
		t.Errorf("job = %+v", job)
	}
	for _, bad := range []string{"file:///etc/passwd", "example.com", "https://"} {
		_, err := g.client().Web.CreateScan(bg, bad)
		wantConfig(t, err, "http")
	}
}

func TestValidatesModeLanguageTitleAndFileBeforeAnyRequest(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	pdf := func(mode PDFMode, opts ...CallOption) error {
		_, err := c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), mode, opts...)
		return err
	}
	wantConfig(t, pdf(PDFModeAnalyze, WithExplicitLanguage("english!")), "BCP 47")
	wantConfig(t, pdf(PDFModeFix, WithExplicitTitle("  ")), "explicit title must be 1-500 printable characters")
	wantConfig(t, pdf(PDFModeFix, WithExplicitTitle(strings.Repeat("x", 501))), "explicit title")
	wantConfig(t, pdf(PDFModeFix, WithExplicitTitle("a\x00b")), "explicit title")
	_, err := c.PDF.CreateJob(bg, bytes.NewReader(nil), PDFModeAnalyze)
	wantConfig(t, err, "empty")
	_, err = c.PDF.CreateJob(bg, nil, PDFModeAnalyze)
	wantConfig(t, err, "reader")
	if g.count() != 0 {
		t.Errorf("%d requests were sent", g.count())
	}
}

func TestATitleOf500CodePointsIsAccepted(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, reply{status: 202, body: jobJSON(nil)})...)
	title := strings.Repeat("\U0001F600", 500)
	jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithExplicitTitle(title)))
	if got := g.last(1)[0].json(t)["explicitTitle"].(string); got != title {
		t.Error("the 500 code point title was altered")
	}
}

func TestTakesTheFileNameFromAnOSFile(t *testing.T) {
	g := newGolden(t)
	path := filepath.Join(t.TempDir(), "sample.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g.expect(steps(uploadPDF, fix("pdf-job-accepted"))...)
	jobOf(t)(g.client().PDF.CreateJob(bg, f, PDFModeAnalyze))
	if got := g.last(3)[0].json(t)["fileName"]; got != "sample.pdf" {
		t.Errorf("fileName = %v", got)
	}
}

func TestAFileLargerThanTheTicketLimitIsRefusedAfterTheTicket(t *testing.T) {
	g := newGolden(t)
	ticket := map[string]any{}
	for k, v := range map[string]any{"uploadId": "11111111-1111-4111-8111-111111111111", "method": "PUT", "uploadUrl": g.srv.URL + "/x", "expiresAt": "2026-10-05T10:10:00Z", "maxBytes": 4} {
		ticket[k] = v
	}
	g.expect(reply{status: 201, body: ticket})
	_, err := g.client().Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf")
	wantConfig(t, err, "ticket limit")
	if g.count() != 1 {
		t.Errorf("%d requests", g.count())
	}
}

func TestUploadValidatesContentTypeAndFileName(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	_, err := c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), "text/plain", "a.txt")
	wantConfig(t, err, "content type")
	_, err = c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "")
	wantConfig(t, err, "file name")
	_, err = c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, strings.Repeat("n", 256))
	wantConfig(t, err, "file name")
}

// ---- structured documents ----------------------------------------------------------------------

func TestDocumentsPostTheDocumentItselfUnwrapped(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("document-job-accepted"))
	doc := Document{
		Title:    "Invoice 2026-0042",
		Language: "en",
		Body: []Block{
			Heading{Level: 1, Text: "Invoice 2026-0042"},
			Paragraph{Content: []Inline{Text("Thank you "), Strong("very much"), Text(".")}},
			Table{Caption: "Items billed", Header: [][]string{{"Description", "Amount"}}, Rows: [][]string{{"Consulting", "1000.00"}}},
			Image{UploadID: "33333333-3333-4333-8333-333333333333", Alt: "Company logo: a blue square"},
		},
	}
	job := jobOf(t)(g.client().Documents.CreateJob(bg, doc))
	if job.Kind != JobKindDocumentToPDF || job.Mode != JobModeGenerateAnalyze {
		t.Errorf("job = %+v", job)
	}
}

func TestDocumentsAcceptTheirOwnFixturesDecodedFromJSON(t *testing.T) {
	g := newGolden(t)
	for _, name := range []string{"document-job-accepted", "document-job-prefer-wait-finished"} {
		var doc Document
		if err := doc.UnmarshalJSON(fixtures[name].Request.Body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g.expect(fix(name))
		var opts []CallOption
		if name == "document-job-prefer-wait-finished" {
			opts = append(opts, WithWait(30))
		}
		if _, err := g.client().Documents.CreateJob(bg, doc, opts...); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUploadImageReturnsTheTicketWhoseUploadIDAnImageBlockUses(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("upload-ticket-image-png"), fix("upload-put-image-png"))
	ticket, err := g.client().Documents.UploadImage(bg, bytes.NewReader(pngBytes), WithContentType(ContentTypePNG), WithFileName("logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	if ticket.UploadID != "33333333-3333-4333-8333-333333333333" || ticket.MaxBytes != nil {
		t.Errorf("ticket = %+v", ticket)
	}
	if g.last(1)[0].header.Get("Authorization") != "" {
		t.Error("the image PUT carried the bearer")
	}
}

func TestUploadImageTakesTheTypeFromTheFileNameAndRefusesAnythingElse(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	g.expect(fix("upload-ticket-image-png"), fix("upload-put-image-png"))
	if _, err := c.Documents.UploadImage(bg, bytes.NewReader(pngBytes), WithFileName("logo.png")); err != nil {
		t.Fatal(err)
	}
	_, err := c.Documents.UploadImage(bg, bytes.NewReader(pngBytes))
	wantConfig(t, err, "image content type")
	_, err = c.Documents.UploadImage(bg, bytes.NewReader(pngBytes), WithContentType(ContentTypePDF))
	wantConfig(t, err, "image content type")
	_, err = c.Documents.UploadImage(bg, bytes.NewReader(pngBytes), WithFileName("notes.txt"))
	wantConfig(t, err, "image content type")
}

func TestARejectedDocumentCarriesEveryProblemInErrors(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("document-job-validation-errors"))
	doc := Document{Title: "T", Language: "en", Body: []Block{Heading{Level: 3, Text: "Skipped"}, Image{UploadID: "33333333-3333-4333-8333-333333333333"}}}
	_, err := g.client().Documents.CreateJob(bg, doc)
	needErr(t, err)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v", err, err)
	}
	if validation.Code != "VALIDATION_ERROR" {
		t.Errorf("code = %q", validation.Code)
	}
	want := []ProblemError{
		{Path: "/body/0/level", Code: "HEADING_LEVEL_SKIPPED", Message: "The first heading must be level 1 or 2."},
		{Path: "/body/1/alt", Code: "IMAGE_ALT_MISSING", Message: "An image needs a description."},
	}
	if !reflect.DeepEqual(validation.Errors, want) {
		t.Errorf("errors = %+v", validation.Errors)
	}
}

func TestAFailedImageReferenceIsReportedWithItsJSONPointer(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("document-job-image-upload-invalid"))
	var doc Document
	if err := doc.UnmarshalJSON(fixtures["document-job-image-upload-invalid"].Request.Body); err != nil {
		t.Fatal(err)
	}
	_, err := g.client().Documents.CreateJob(bg, doc)
	needErr(t, err)
	var api *APIError
	if !errors.As(err, &api) || len(api.Errors) == 0 || api.Errors[0].Path != "/body/3/uploadId" || api.Errors[0].Code != "IMAGE_UPLOAD_INVALID" {
		t.Fatalf("error = %#v", err)
	}
}

// ---- wait and Prefer ---------------------------------------------------------------------------

func TestWaitSendsPreferAndA200ReturnsTheFinishedJob(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-prefer-wait-finished"))...)
	var metas []ResponseMeta
	done := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithWait(60), WithOnResponse(func(m ResponseMeta) { metas = append(metas, m) })))
	if done.State != JobStateCompleted || done.FinalValidation == nil || done.FinalValidation.Status != ValidationPassed {
		t.Errorf("job = %+v", done)
	}
	want := []ResponseMeta{{Status: 200, Location: "/v1/jobs/" + jobID, PreferenceApplied: "wait=60"}}
	if !reflect.DeepEqual(metas, want) {
		t.Errorf("metas = %+v", metas)
	}
}

func TestA202AfterTheWaitIsReturnedUnfinishedAndNothingPollsOnItsOwn(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-prefer-wait-timeout"))...)
	var metas []ResponseMeta
	pending := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithWait(5), WithOnResponse(func(m ResponseMeta) { metas = append(metas, m) })))
	if pending.State != JobStateRunning || pending.FinishedAt != nil || pending.IsFinished() {
		t.Errorf("job = %+v", pending)
	}
	if metas[0].Status != 202 || metas[0].PreferenceApplied != "wait=5" || g.count() != 3 {
		t.Errorf("metas = %+v, exchanges = %d", metas, g.count())
	}
}

func TestUntilFinishedContinuesWithJobsWaitAfterA202(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-prefer-wait-timeout"), fix("job-completed-fix"))...)
	done := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithWait(5), WithUntilFinished()))
	if done.State != JobStateCompleted || g.last(1)[0].method != "GET" {
		t.Errorf("job = %+v", done)
	}
}

func TestUntilFinishedDoesNotPollWhenTheWaitAlreadyFinishedTheJob(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-prefer-wait-finished"))...)
	done := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithWait(60), WithUntilFinished()))
	if done.State != JobStateCompleted {
		t.Errorf("state = %q", done.State)
	}
}

func TestUntilFinishedWithoutWaitPollsAfterAPlain202AndPassesPollOptions(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 202, body: jobJSON(map[string]any{"kind": "web"})}, reply{status: 200, body: jobJSON(map[string]any{"kind": "web", "state": "completed"})})
	var updates int
	done := jobOf(t)(g.client().Web.CreateScan(bg, scanURL, WithUntilFinished(WithOnUpdate(func(*Job) { updates++ }))))
	ex := g.last(2)
	if done.State != JobStateCompleted || updates != 1 || ex[0].method != "POST" || ex[1].method != "GET" || ex[1].uri != "/v1/jobs/"+jobID || ex[0].header.Get("Prefer") != "" {
		t.Errorf("job = %+v, updates = %d, exchanges = %+v", done, updates, ex)
	}
}

func TestDocumentsAndReadingOrderCorrectionsTakeWaitToo(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	g.expect(fix("document-job-prefer-wait-finished"))
	var doc Document
	if err := doc.UnmarshalJSON(fixtures["document-job-prefer-wait-finished"].Request.Body); err != nil {
		t.Fatal(err)
	}
	if job := jobOf(t)(c.Documents.CreateJob(bg, doc, WithWait(30))); job.State != JobStateCompleted {
		t.Errorf("state = %q", job.State)
	}
	g.expect(fix("remediation-without-reviewer"))
	body := requestBody("remediation-without-reviewer")
	approval := ReadingOrderRemediation{
		BaseArtifactSHA256: body["baseArtifactSha256"].(string),
		SnapshotVersion:    1,
		Target:             RemediationTargetPDFTags,
		OrderedNodeIDs:     []string{"n2b3c4d5e6f708190", "n1a2b3c4d5e6f7089"},
		Acknowledged:       true,
	}
	fixed := jobOf(t)(c.Jobs.RemediateReadingOrder(bg, jobID, approval, WithWait(60)))
	if fixed.Kind != JobKindOrderPDF || fixed.State != JobStateCompleted {
		t.Errorf("job = %+v", fixed)
	}
}

func TestRejectsAWaitOutsideOneToNineHundredSecondsBeforeAnyRequest(t *testing.T) {
	g := newGolden(t)
	for _, wait := range []int{0, -1, 901} {
		_, err := g.client().Web.CreateScan(bg, scanURL, WithWait(wait))
		wantConfig(t, err, "wait must be")
	}
	if g.count() != 0 {
		t.Errorf("%d requests", g.count())
	}
}

func TestExposesAnIdempotentReplay(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-idempotent-replay"))...)
	var meta ResponseMeta
	jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithIdempotencyKey("retry-1"), WithOnResponse(func(m ResponseMeta) { meta = m })))
	if meta.Status != 202 || !meta.IdempotentReplayed {
		t.Errorf("meta = %+v", meta)
	}
}

func TestStretchesTheRequestTimeoutOverTheWait(t *testing.T) {
	g := newGolden(t)
	c := g.client(WithTimeout(5 * time.Second))
	g.expect(fix("web-job-accepted"), fix("web-job-accepted"))
	jobOf(t)(c.Web.CreateScan(bg, scanURL, WithWait(60)))
	jobOf(t)(c.Web.CreateScan(bg, scanURL))
	g.mu.Lock()
	defer g.mu.Unlock()
	waited, plain := g.budgets[0], g.budgets[1]
	if waited < 65*time.Second || waited > 70*time.Second || plain < 4*time.Second || plain > 5*time.Second {
		t.Errorf("budgets = %v, %v; want about 70s and 5s", waited, plain)
	}
}

// ---- polling and reads -------------------------------------------------------------------------

func TestJobsWaitHonoursRetryAfterAndReturnsTheTerminalJob(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-queued-retry-after"), fix("job-running"), fix("job-completed"))
	var seen []JobState
	done, err := g.client().Jobs.Wait(bg, jobID, WithOnUpdate(func(j *Job) { seen = append(seen, j.State) }))
	if err != nil {
		t.Fatal(err)
	}
	if done.State != JobStateCompleted || !reflect.DeepEqual(seen, []JobState{JobStateQueued, JobStateRunning, JobStateCompleted}) {
		t.Errorf("done = %+v, seen = %v", done, seen)
	}
	sleeps := g.slept()
	if len(sleeps) != 2 || sleeps[0] <= 0 || sleeps[0] > 10*time.Second {
		t.Errorf("sleeps = %v", sleeps)
	}
}

func TestWithoutRetryAfterTheDelayDoublesUpToTenSeconds(t *testing.T) {
	g := newGolden(t)
	running := reply{status: 200, body: jobJSON(map[string]any{"state": "running"})}
	script := make([]any, 0, 7)
	for i := 0; i < 6; i++ {
		script = append(script, running)
	}
	script = append(script, reply{status: 200, body: jobJSON(map[string]any{"state": "completed"})})
	g.expect(script...)
	if _, err := g.client().Jobs.Wait(bg, jobID); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	if got := g.slept(); !reflect.DeepEqual(got, want) {
		t.Errorf("sleeps = %v; want %v", got, want)
	}
}

func TestAFailedJobIsReturnedNotRaised(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-failed"))
	job, err := g.client().Jobs.Wait(bg, jobID)
	if err != nil {
		t.Fatalf("a failed job must not be an error: %v", err)
	}
	if job.State != JobStateFailed || job.Error == nil || job.Error.Code == "" {
		t.Errorf("job = %+v", job)
	}
}

func TestWaitFailsWhenTheBudgetCannotCoverTheNextPoll(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 200, body: jobJSON(map[string]any{"state": "running"})})
	_, err := g.client().Jobs.Wait(bg, jobID, WithPollTimeout(500*time.Millisecond))
	var timeout *TimeoutError
	if !errors.As(err, &timeout) || timeout.JobID != jobID {
		t.Fatalf("error = %v", err)
	}
	if len(g.slept()) != 0 {
		t.Errorf("sleeps = %v", g.slept())
	}
}

func TestListsFindingsActionsAndArtifacts(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("findings"), fix("actions"), fix("artifacts"))
	c := g.client()
	findings, err := c.Jobs.Findings(bg, jobID)
	if err != nil || len(findings) != 2 {
		t.Fatalf("findings = %+v, %v", findings, err)
	}
	if findings[0].Resolution != ResolutionFixedInCopy || findings[1].Resolution != "" || findings[0].NeedsHumanReview || !findings[1].NeedsHumanReview {
		t.Errorf("findings = %+v", findings)
	}
	actions, err := c.Jobs.Actions(bg, jobID)
	if err != nil || len(actions) != 2 || actions[0].Outcome != ActionApplied || actions[1].Outcome != ActionRolledBack {
		t.Fatalf("actions = %+v, %v", actions, err)
	}
	if actions[0].Before != nil || actions[0].After == nil || *actions[0].After != "sample" {
		t.Errorf("before/after = %v / %v", actions[0].Before, actions[0].After)
	}
	artifacts, err := c.Jobs.Artifacts(bg, jobID)
	if err != nil || len(artifacts) != 2 || artifacts[0].Kind != ArtifactOriginal || artifacts[1].Kind != ArtifactRemediated || artifacts[0].SizeBytes != 45 {
		t.Fatalf("artifacts = %+v, %v", artifacts, err)
	}
	if artifacts[0].ExpiresAt.IsZero() {
		t.Error("expiresAt was not parsed")
	}
}

func TestReadsLowercaseTheJobIDAndRejectAMalformedOneLocally(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-completed"))
	c := g.client()
	if _, err := c.Jobs.Get(bg, strings.ToUpper(jobID)); err != nil {
		t.Fatal(err)
	}
	if uri := g.last(1)[0].uri; uri != "/v1/jobs/"+jobID {
		t.Errorf("uri = %q", uri)
	}
	for _, bad := range []string{"../organizations", "", "not-a-uuid", jobID + "/x", strings.Repeat("g", 36)} {
		_, err := c.Jobs.Get(bg, bad)
		wantConfig(t, err, "job id")
	}
}

func TestReadsAJobOfASession(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-completed-session"))
	job := jobOf(t)(g.client(WithToken(sessionToken)).Jobs.Get(bg, jobID))
	if job.Validation == nil || job.Validation.Status != ValidationFailed {
		t.Errorf("job = %+v", job)
	}
}

func TestAJobWithoutArtifactsHasAnEmptyList(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifacts-empty"))
	artifacts, err := g.client().Jobs.Artifacts(bg, otherJobID)
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, %v", artifacts, err)
	}
}

// ---- artifact downloads ------------------------------------------------------------------------

func artifactFor(g *golden, name string) Artifact {
	f := fixtures[name]
	size := int64(1)
	if f.Response.RawBody != nil {
		size = int64(len(rawResponse(name)))
	}
	sum := f.Response.SHA256
	if sum == "" {
		sum = strings.Repeat("0", 64)
	}
	return Artifact{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1", Kind: ArtifactOriginal, SHA256: sum, SizeBytes: size, DownloadURL: g.srv.URL + f.Request.Path}
}

func TestDownloadReturnsTheBytesWhenSizeAndHashMatchWithoutTheBearer(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"), fix("artifact-download-original"), fix("artifact-download-original"))
	c := g.client()
	a := artifactFor(g, "artifact-download-original")
	data, err := c.Artifacts.Download(bg, a)
	if err != nil || !bytes.Equal(data, pdfBytes) {
		t.Fatalf("data = %q, %v", data, err)
	}
	if g.last(1)[0].header.Get("Authorization") != "" {
		t.Error("the download carried the bearer")
	}
	var sink bytes.Buffer
	if err := c.Artifacts.DownloadTo(bg, a, &sink); err != nil || !bytes.Equal(sink.Bytes(), pdfBytes) {
		t.Fatalf("DownloadTo: %v", err)
	}
	target := filepath.Join(t.TempDir(), "out.pdf")
	if err := c.Artifacts.DownloadToFile(bg, a, target); err != nil {
		t.Fatal(err)
	}
	if written, _ := os.ReadFile(target); !bytes.Equal(written, pdfBytes) {
		t.Error("the file differs")
	}
}

func TestDownloadOfAWrongHashReturnsAndWritesNothing(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"), fix("artifact-download-original"), fix("artifact-download-original"))
	c := g.client()
	a := artifactFor(g, "artifact-download-original")
	a.SHA256 = strings.Repeat("0", 64)
	target := filepath.Join(t.TempDir(), "out.pdf")
	err := c.Artifacts.DownloadToFile(bg, a, target)
	var storage *StorageError
	if !errors.As(err, &storage) || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("a file was left behind")
	}
	var sink bytes.Buffer
	if err := c.Artifacts.DownloadTo(bg, a, &sink); err == nil || sink.Len() != 0 {
		t.Errorf("DownloadTo err = %v, wrote %d bytes", err, sink.Len())
	}
	if data, err := c.Artifacts.Download(bg, a); err == nil || data != nil {
		t.Errorf("Download returned %q, %v", data, err)
	}
}

func TestDownloadOfAWrongSizeFailsEvenWhenTheHashWouldMatch(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"))
	a := artifactFor(g, "artifact-download-original")
	a.SizeBytes++
	data, err := g.client().Artifacts.Download(bg, a)
	var storage *StorageError
	if !errors.As(err, &storage) || !strings.Contains(err.Error(), "bytes") || data != nil {
		t.Fatalf("data = %q, error = %v", data, err)
	}
}

func TestDownloadFailsWhenMoreBytesArriveThanDeclared(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"))
	a := artifactFor(g, "artifact-download-original")
	a.SizeBytes = 10
	_, err := g.client().Artifacts.Download(bg, a)
	var storage *StorageError
	if !errors.As(err, &storage) || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("error = %v", err)
	}
}

func TestTheHashComparisonIgnoresCase(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"))
	a := artifactFor(g, "artifact-download-original")
	a.SHA256 = strings.ToUpper(a.SHA256)
	if data, err := g.client().Artifacts.Download(bg, a); err != nil || !bytes.Equal(data, pdfBytes) {
		t.Fatalf("data = %q, %v", data, err)
	}
}

func TestDownloadsTheRemediatedCopyWithItsOwnHash(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-remediated"))
	a := artifactFor(g, "artifact-download-remediated")
	a.Kind = ArtifactRemediated
	data, err := g.client().Artifacts.Download(bg, a)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != a.SHA256 {
		t.Error("hash differs")
	}
}

func TestARefusedDownloadIsAStorageErrorWithTheProblemCode(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-signature-invalid"), fix("artifact-download-not-found"))
	c := g.client()
	for _, want := range []struct {
		name   string
		status int
		code   string
	}{{"artifact-download-signature-invalid", 403, "SIGNATURE_INVALID"}, {"artifact-download-not-found", 404, "NOT_FOUND"}} {
		_, err := c.Artifacts.Download(bg, artifactFor(g, want.name))
		var storage *StorageError
		if !errors.As(err, &storage) || storage.Status != want.status || storage.Code != want.code {
			t.Errorf("%s: error = %#v", want.name, err)
		}
	}
}

func TestRefusedUploadsAreStorageErrorsWithTheProblemCode(t *testing.T) {
	for _, want := range []struct {
		put    string
		status int
		code   string
	}{
		{"upload-put-checksum-mismatch", 422, "CHECKSUM_MISMATCH"},
		{"upload-put-expired", 410, "UPLOAD_EXPIRED"},
		{"upload-put-signature-invalid", 403, "SIGNATURE_INVALID"},
	} {
		g := newGolden(t)
		g.expect(fix("upload-ticket-pdf"), fix(want.put))
		_, err := g.client().Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf")
		var storage *StorageError
		if !errors.As(err, &storage) || storage.Status != want.status || storage.Code != want.code {
			t.Errorf("%s: error = %#v", want.put, err)
		}
	}
}

// ---- reading order -----------------------------------------------------------------------------

func approval() ReadingOrderRemediation {
	body := requestBody("remediation-accepted")
	return ReadingOrderRemediation{
		BaseArtifactSHA256: body["baseArtifactSha256"].(string),
		SnapshotVersion:    1,
		Target:             RemediationTargetPDFTags,
		OrderedNodeIDs:     []string{"n2b3c4d5e6f708190", "n1a2b3c4d5e6f7089"},
		Acknowledged:       true,
		Reviewer:           "reviewer@example.com",
	}
}

func TestReturnsTheSnapshotAndQueuesAnAcknowledgedCorrectionWithAReviewer(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("reading-order"), fix("remediation-accepted"))
	c := g.client()
	snapshot, err := c.Jobs.ReadingOrder(bg, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.ReviewRequired || snapshot.ReadingOrderReview != "needs_review" || snapshot.FocusOrderReview != "not_run" {
		t.Errorf("snapshot = %+v", snapshot)
	}
	if snapshot.Nodes[0].BoundingBox != nil || snapshot.Nodes[1].BoundingBox == nil || *snapshot.Nodes[1].BoundingBox != (BoundingBox{X: 72, Y: 720, Width: 200, Height: 12}) {
		t.Errorf("bounding boxes = %+v / %+v", snapshot.Nodes[0].BoundingBox, snapshot.Nodes[1].BoundingBox)
	}
	if snapshot.Nodes[0].ParentID != "" || snapshot.Nodes[1].ParentID != "n0f1e2d3c4b5a6978" || snapshot.ArtifactHashes["pdf_structure"] == "" {
		t.Errorf("nodes = %+v", snapshot.Nodes)
	}
	job := jobOf(t)(c.Jobs.RemediateReadingOrder(bg, jobID, approval()))
	if job.Kind != JobKindOrderPDF || job.Mode != JobModeReorder {
		t.Errorf("job = %+v", job)
	}
}

func TestTheReviewerIsOmittedWhenAbsentAndSentTrimmed(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("remediation-without-reviewer"))
	a := approval()
	a.Reviewer = ""
	jobOf(t)(g.client().Jobs.RemediateReadingOrder(bg, jobID, a, WithWait(60)))
	g.expect(reply{status: 202, body: jobJSON(map[string]any{"kind": "order_pdf"})})
	a.Reviewer = " " + strings.Repeat("r", 200) + " "
	jobOf(t)(g.client().Jobs.RemediateReadingOrder(bg, jobID, a))
	if got := g.last(1)[0].json(t)["reviewer"]; got != strings.Repeat("r", 200) {
		t.Errorf("reviewer = %v", got)
	}
	for _, bad := range []string{strings.Repeat("r", 201), "   ", "a\nb"} {
		a.Reviewer = bad
		_, err := g.client().Jobs.RemediateReadingOrder(bg, jobID, a)
		wantConfig(t, err, "reviewer")
	}
}

func TestRefusesAnUnacknowledgedOrMalformedApprovalBeforeAnyRequest(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	try := func(change func(*ReadingOrderRemediation)) error {
		a := approval()
		change(&a)
		_, err := c.Jobs.RemediateReadingOrder(bg, jobID, a)
		return err
	}
	wantConfig(t, try(func(a *ReadingOrderRemediation) { a.Acknowledged = false }), "acknowledged")
	wantConfig(t, try(func(a *ReadingOrderRemediation) { a.OrderedNodeIDs = []string{"n1", "n1"} }), "distinct")
	wantConfig(t, try(func(a *ReadingOrderRemediation) { a.OrderedNodeIDs = []string{"n1"} }), "distinct")
	wantConfig(t, try(func(a *ReadingOrderRemediation) { a.BaseArtifactSHA256 = "nope" }), "hash")
	wantConfig(t, try(func(a *ReadingOrderRemediation) { a.Target = "pdf" }), "target")
	if g.count() != 0 {
		t.Errorf("%d requests", g.count())
	}
}

func TestStaleExpiredUnsafeAndMissingMapToTypedErrorsWithTheCode(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("remediation-stale"), fix("remediation-artifact-expired"), fix("remediation-unsupported"), fix("reading-order-not-found"))
	c := g.client()
	a := approval()
	a.Reviewer = ""
	_, err := c.Jobs.RemediateReadingOrder(bg, jobID, a)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Code != "STALE_READING_ORDER_SNAPSHOT" || !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %#v", err)
	}
	_, err = c.Jobs.RemediateReadingOrder(bg, jobID, a)
	if !errors.As(err, &conflict) || conflict.Code != "ARTIFACT_EXPIRED" {
		t.Fatalf("expired: %#v", err)
	}
	a.Target = RemediationTargetSourceHTML
	_, err = c.Jobs.RemediateReadingOrder(bg, jobID, a)
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "ORDER_FIX_UNSUPPORTED" {
		t.Fatalf("unsupported: %#v", err)
	}
	_, err = c.Jobs.ReadingOrder(bg, otherJobID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %#v", err)
	}
}

// ---- client sessions ---------------------------------------------------------------------------

func TestMintsAScopedSessionWithTheRequestShapeOfCore(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("client-session"))
	minted, err := g.client().ClientSessions.Create(bg, ClientSessionRequest{
		AllowedOrigin: "https://app.example.com",
		Scopes:        []SessionScope{ScopeJobsCreate, ScopeJobsRead},
		MaxJobs:       3,
		MaxUploads:    6,
		TTLSeconds:    600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(minted.SessionToken, "v1.") || !minted.ExpiresAt.Equal(time.Date(2026, 10, 5, 10, 10, 0, 0, time.UTC)) {
		t.Errorf("session = %+v", minted)
	}
}

func TestSendsOnlyTheAllowedOriginWhenNothingElseIsSet(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("client-session-defaults"))
	if _, err := g.client().ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesAnOriginWithAPathLocallyAndMapsTheServersRefusals(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	_, err := c.ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com/path"})
	wantConfig(t, err, "origin")
	g.expect(fix("client-session-forbidden"))
	_, err = g.client(WithToken(sessionToken)).ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com"})
	var denied *PermissionError
	if !errors.As(err, &denied) || denied.Code != "FORBIDDEN" {
		t.Fatalf("forbidden: %#v", err)
	}
	invalid := fixtures["client-session-invalid"].Response
	var body map[string]any
	if jsonErr := json.Unmarshal(invalid.Body, &body); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	g.expect(reply{status: invalid.Status, body: body}, fix("client-session-unsupported-media-type"))
	_, err = c.ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com", MaxJobs: 5000})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("invalid: %#v", err)
	}
	_, err = c.ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com"})
	var media *UnsupportedMediaTypeError
	if !errors.As(err, &media) {
		t.Fatalf("media type: %#v", err)
	}
}

func TestASessionTokenWorksAsTheBearerForJobCreation(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("upload-ticket-session"), fix("upload-put-pdf"), fix("pdf-job-session"))
	jobOf(t)(g.client(WithToken(sessionToken)).PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeAnalyze, WithFileName("sample.pdf")))
}

// ---- problems ----------------------------------------------------------------------------------

func TestPDFCreationProblemsMapToTheirErrorTypes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		mode   PDFMode
		opts   []CallOption
		is     error
		as     func(error) bool
	}{
		{"pdf-job-unauthorized", 401, "UNAUTHORIZED", PDFModeFix, nil, ErrUnauthorized, func(e error) bool { var x *AuthenticationError; return errors.As(e, &x) }},
		{"pdf-job-feature-off", 404, "NOT_FOUND", PDFModeFix, nil, ErrNotFound, func(e error) bool { var x *NotFoundError; return errors.As(e, &x) }},
		{"pdf-job-idempotency-key-reused", 409, "IDEMPOTENCY_KEY_REUSED", PDFModeAnalyze, nil, ErrConflict, func(e error) bool { var x *ConflictError; return errors.As(e, &x) }},
		{"pdf-job-unsupported-input", 415, "UNSUPPORTED_INPUT", PDFModeFix, nil, ErrUnsupportedMediaType, func(e error) bool { var x *UnsupportedMediaTypeError; return errors.As(e, &x) }},
		{"pdf-job-validation-error", 422, "VALIDATION_ERROR", PDFModeAnalyze, []CallOption{WithExplicitTitle("x")}, ErrValidation, func(e error) bool { var x *ValidationError; return errors.As(e, &x) }},
		{"pdf-job-invalid-json", 400, "INVALID_JSON", PDFModeFix, nil, ErrValidation, func(e error) bool { var x *ValidationError; return errors.As(e, &x) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGolden(t)
			g.expect(steps(uploadPDF, fix(c.name))...)
			opts := append([]CallOption{WithFileName("sample.pdf")}, c.opts...)
			_, err := g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), c.mode, opts...)
			var api *APIError
			if !errors.As(err, &api) || api.Status != c.status || api.Code != c.code {
				t.Fatalf("error = %#v", err)
			}
			if !errors.Is(err, c.is) || !c.as(err) {
				t.Errorf("%T does not classify as %v", err, c.is)
			}
		})
	}
}

func TestInsufficientScopeOnACreationAndOnARead(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("upload-ticket-session"), fix("upload-put-pdf"), fix("pdf-job-insufficient-scope"), fix("job-insufficient-scope"))
	c := g.client(WithToken(sessionToken))
	_, err := c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"))
	var denied *PermissionError
	if !errors.As(err, &denied) || denied.Code != "INSUFFICIENT_SCOPE" || !errors.Is(err, ErrForbidden) {
		t.Fatalf("creation: %#v", err)
	}
	_, err = c.Jobs.Get(bg, jobID)
	if !errors.As(err, &denied) || denied.Code != "INSUFFICIENT_SCOPE" {
		t.Fatalf("read: %#v", err)
	}
}

func Test401And404OnARead(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-unauthorized"), fix("job-not-found"))
	c := g.client()
	if _, err := c.Jobs.Get(bg, jobID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401: %#v", err)
	}
	if _, err := c.Jobs.Get(bg, otherJobID); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %#v", err)
	}
}

func Test421IsAHostNotAllowedError(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("web-job-host-not-allowed"))
	_, err := g.client().Web.CreateScan(bg, scanURL)
	var host *HostNotAllowedError
	if !errors.As(err, &host) || host.Code != "HOST_NOT_ALLOWED" || !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("error = %#v", err)
	}
}

func TestARejectedScanURLIsAValidationError(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("web-job-url-rejected"))
	_, err := g.client().Web.CreateScan(bg, "https://internal.invalid/")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %#v", err)
	}
}

func TestSessionLimitsAreRateLimitErrorsWithoutRetryAfterAndNeverRetried(t *testing.T) {
	g := newGolden(t)
	c := g.client(WithToken(sessionToken))
	g.expect(fix("upload-ticket-session"), fix("upload-put-pdf"), fix("pdf-job-session-job-limit"))
	_, err := c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"))
	var limit *RateLimitError
	if !errors.As(err, &limit) || limit.Code != "SESSION_JOB_LIMIT" || limit.RetryAfter != nil || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("job limit: %#v", err)
	}
	g.expect(fix("upload-ticket-session-limit"))
	_, err = c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"))
	if !errors.As(err, &limit) || limit.Code != "SESSION_UPLOAD_LIMIT" {
		t.Fatalf("upload limit: %#v", err)
	}
	if len(g.slept()) != 0 {
		t.Errorf("sleeps = %v", g.slept())
	}
}

func TestQueueFullIsAServiceUnavailableErrorWithRetryAfterAndIsNotRetried(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, fix("pdf-job-queue-full"))...)
	_, err := g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"))
	var unavailable *ServiceUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %#v", err)
	}
	var server *ServerError
	if !errors.As(err, &server) || !errors.Is(err, ErrServer) || !errors.Is(err, ErrUnavailable) {
		t.Errorf("a 503 must also be a server error: %#v", err)
	}
	if unavailable.Code != "QUEUE_FULL" || unavailable.RetryAfter == nil || *unavailable.RetryAfter != 30*time.Second {
		t.Errorf("error = %+v", unavailable)
	}
}

func Test422FileTooLargeOnTheUploadTicket(t *testing.T) {
	g := newGolden(t)
	answer := fixtures["upload-ticket-too-large"].Response
	var body map[string]any
	if err := json.Unmarshal(answer.Body, &body); err != nil {
		t.Fatal(err)
	}
	g.expect(reply{status: answer.Status, body: body})
	_, err := g.client().Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "huge.pdf")
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "FILE_TOO_LARGE" {
		t.Fatalf("error = %#v", err)
	}
}

func TestErrorTextsNeverContainTheCredential(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("job-unauthorized"))
	_, err := g.client().Jobs.Get(bg, jobID)
	var api *APIError
	if !errors.As(err, &api) || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), secretKey) {
		t.Fatalf("error text contains the token: %v", err)
	}
	g.expect(fail{errors.New("cannot connect with " + secretKey)}, fail{errors.New("cannot connect with " + secretKey)}, fail{errors.New("cannot connect with " + secretKey)})
	_, err = g.client().Info(bg)
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("network error text = %v", err)
	}
}

// ---- newer core --------------------------------------------------------------------------------

func TestUnknownEnumValuesAreKeptAsTheyAre(t *testing.T) {
	g := newGolden(t)
	g.fallback = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": "a", "kind": "stamped_copy", "sha256": strings.Repeat("0", 64), "sizeBytes": 1, "downloadUrl": "https://x.invalid/a", "expiresAt": "2030-01-01T00:00:00Z"}}}, nil)
		case strings.HasSuffix(r.URL.Path, "/findings"):
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"source": "epub", "ruleId": "X", "message": "m", "needsHumanReview": true, "resolution": "needs_a_person"}}}, nil)
		case strings.HasSuffix(r.URL.Path, "/actions"):
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"ruleId": "R", "outcome": "deferred", "reason": "r"}}}, nil)
		case strings.HasSuffix(r.URL.Path, "/v1/info"):
			writeJSON(w, 200, map[string]any{"features": []string{"pdf", "ocr"}}, nil)
		default:
			validation := map[string]any{"profile": "pdfua2", "status": "partial", "failedRules": []any{}}
			writeJSON(w, 200, jobJSON(map[string]any{"kind": "epub_to_pdf", "mode": "polish", "state": "paused", "remediationStatus": "queued_for_review", "validation": validation, "futureField": 1}), nil)
		}
	}
	c := g.client()
	job, err := c.Jobs.Get(bg, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "epub_to_pdf" || job.Mode != "polish" || job.State != "paused" || job.RemediationStatus != "queued_for_review" || job.Validation.Status != "partial" {
		t.Errorf("job = %+v", job)
	}
	artifacts, _ := c.Jobs.Artifacts(bg, jobID)
	findings, _ := c.Jobs.Findings(bg, jobID)
	actions, _ := c.Jobs.Actions(bg, jobID)
	info, _ := c.Info(bg)
	if artifacts[0].Kind != "stamped_copy" || findings[0].Source != "epub" || findings[0].Resolution != "needs_a_person" || actions[0].Outcome != "deferred" || len(info.Features) != 2 || info.Features[1] != "ocr" {
		t.Errorf("artifacts = %+v, findings = %+v, actions = %+v, info = %+v", artifacts, findings, actions, info)
	}
}

func TestWaitTreatsAnUnknownStateAsFinalInsteadOfPollingForever(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 200, body: jobJSON(map[string]any{"state": "paused"})})
	done, err := g.client().Jobs.Wait(bg, jobID)
	if err != nil || done.State != "paused" || !done.IsFinished() {
		t.Fatalf("done = %+v, %v", done, err)
	}
}

func TestAnUnknownProblemCodeOrStatusStillBecomesAnAPIErrorThatKeepsTheCode(t *testing.T) {
	g := newGolden(t)
	body := problemJSON(418, "BREWING", map[string]any{"errors": []any{map[string]any{"path": "/x", "code": "NEW_RULE", "message": "m"}, map[string]any{"nonsense": true}}})
	g.expect(reply{status: 418, body: body, headers: map[string]string{"Content-Type": "application/problem+json"}})
	_, err := g.client().Jobs.Get(bg, jobID)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 418 || api.Code != "BREWING" {
		t.Fatalf("error = %#v", err)
	}
	if !reflect.DeepEqual(api.Errors, []ProblemError{{Path: "/x", Code: "NEW_RULE", Message: "m"}}) {
		t.Errorf("errors = %+v", api.Errors)
	}
	if errors.Is(err, ErrServer) || errors.Is(err, ErrValidation) {
		t.Error("a 418 must not match a status class sentinel")
	}
}

func TestABodyThatIsNotAProblemStillBecomesAServerError(t *testing.T) {
	g := newGolden(t)
	g.fallback = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}
	_, err := g.client().Jobs.Get(bg, jobID)
	var server *ServerError
	if !errors.As(err, &server) || server.Status != 502 || server.Code != "INTERNAL_ERROR" {
		t.Fatalf("error = %#v", err)
	}
}

func TestASuccessBodyThatIsNotAJobIsAServerError(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 200, body: map[string]any{"unexpected": true}}, reply{status: 200})
	for i := 0; i < 2; i++ {
		_, err := g.client().Jobs.Get(bg, jobID)
		var server *ServerError
		if !errors.As(err, &server) || !strings.Contains(err.Error(), "Malformed") {
			t.Fatalf("attempt %d: error = %#v", i, err)
		}
	}
}

// ---- retries -----------------------------------------------------------------------------------

func TestACreationRepeatedAfterALostResponseCarriesTheSameKeyAndTheServerReplays(t *testing.T) {
	g := newGolden(t)
	g.expect(steps(uploadPDF, lost("pdf-job-idempotent-replay"), fix("pdf-job-idempotent-replay"))...)
	var metas []ResponseMeta
	job := jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithOnResponse(func(m ResponseMeta) { metas = append(metas, m) })))
	if job.ID != jobID {
		t.Errorf("job = %+v", job)
	}
	ex := g.last(2)
	if ex[0].uri != "/v1/pdf/jobs" || ex[1].uri != "/v1/pdf/jobs" {
		t.Errorf("uris = %q %q", ex[0].uri, ex[1].uri)
	}
	if ex[0].header.Get("Idempotency-Key") == "" || ex[0].header.Get("Idempotency-Key") != ex[1].header.Get("Idempotency-Key") || !bytes.Equal(ex[0].body, ex[1].body) {
		t.Error("the repeat must carry the same key and body")
	}
	if len(metas) != 1 || !metas[0].IdempotentReplayed || !reflect.DeepEqual(g.slept(), []time.Duration{250 * time.Millisecond}) {
		t.Errorf("metas = %+v, sleeps = %v", metas, g.slept())
	}
}

func TestGivesUpAfterTwoRepeatsAndSurfacesTheNetworkError(t *testing.T) {
	g := newGolden(t)
	down := errors.New("dial tcp: connection refused")
	g.expect(fail{down}, fail{down}, fail{down})
	_, err := g.client().Web.CreateScan(bg, scanURL)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %v", err)
	}
	if g.count() != 3 || !reflect.DeepEqual(g.slept(), []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}) {
		t.Errorf("exchanges = %d, sleeps = %v", g.count(), g.slept())
	}
}

func TestEveryCreationCarriesAKeySoAFailedFirstAttemptIsRepeated(t *testing.T) {
	g := newGolden(t)
	g.expect(fail{errors.New("down")}, fix("web-job-accepted"))
	jobOf(t)(g.client().Web.CreateScan(bg, scanURL))
	for _, ex := range g.last(2) {
		if ex.header.Get("Idempotency-Key") == "" {
			t.Error("a creation went out without a key")
		}
	}
}

func TestAClientSessionMintHasNoKeyAndIsNotRepeated(t *testing.T) {
	g := newGolden(t)
	g.expect(fail{errors.New("down")})
	_, err := g.client().ClientSessions.Create(bg, ClientSessionRequest{AllowedOrigin: "https://app.example.com"})
	if err == nil || g.count() != 1 {
		t.Fatalf("error = %v, exchanges = %d", err, g.count())
	}
}

func TestReadsRetryAfterA503WithRetryAfterButCreationsNever(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 503, body: problemJSON(503, "QUEUE_FULL", nil), headers: map[string]string{"Retry-After": "0"}}, fix("job-completed"))
	job := jobOf(t)(g.client().Jobs.Get(bg, jobID))
	if job.State != JobStateCompleted || !reflect.DeepEqual(g.slept(), []time.Duration{0}) {
		t.Errorf("job = %+v, sleeps = %v", job, g.slept())
	}
	g.expect(reply{status: 503, body: problemJSON(503, "QUEUE_FULL", nil)})
	before := g.count()
	_, err := g.client().Web.CreateScan(bg, scanURL)
	if !errors.Is(err, ErrUnavailable) || g.count()-before != 1 {
		t.Errorf("error = %v, exchanges = %d", err, g.count()-before)
	}
}

func TestAReadGivesUpAfterTwoBusyRetries(t *testing.T) {
	g := newGolden(t)
	busy := reply{status: 429, body: problemJSON(429, "SESSION_JOB_LIMIT", nil), headers: map[string]string{"Retry-After": "3"}}
	g.expect(busy, busy, busy)
	_, err := g.client().Jobs.Get(bg, jobID)
	if !errors.Is(err, ErrRateLimited) || !reflect.DeepEqual(g.slept(), []time.Duration{3 * time.Second, 3 * time.Second}) {
		t.Errorf("error = %v, sleeps = %v", err, g.slept())
	}
}

func TestRetryAfterMayBeAnHTTPDateAndIsCappedBetweenRetries(t *testing.T) {
	g := newGolden(t)
	g.expect(reply{status: 503, body: problemJSON(503, "QUEUE_FULL", nil), headers: map[string]string{"Retry-After": "300"}}, fix("job-completed"))
	jobOf(t)(g.client().Jobs.Get(bg, jobID))
	if !reflect.DeepEqual(g.slept(), []time.Duration{10 * time.Second}) {
		t.Errorf("sleeps = %v", g.slept())
	}
	g.expect(reply{status: 503, body: problemJSON(503, "QUEUE_FULL", nil), headers: map[string]string{"Retry-After": "Wed, 21 Oct 2015 07:28:00 GMT"}})
	_, err := g.client().Web.CreateScan(bg, scanURL)
	var unavailable *ServiceUnavailableError
	if !errors.As(err, &unavailable) || unavailable.RetryAfter == nil || *unavailable.RetryAfter != 0 {
		t.Fatalf("error = %#v", err)
	}
}

func TestIdempotencyKeysOfOneTo255PrintableASCIIAndDerivedUploadKeysStayWithinTheLimit(t *testing.T) {
	g := newGolden(t)
	key := strings.Repeat("k", 255)
	g.expect(steps(uploadPDF, fix("pdf-job-accepted"))...)
	jobOf(t)(g.client().PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeAnalyze, WithFileName("sample.pdf"), WithIdempotencyKey(key)))
	ex := g.last(3)
	ticketKey := ex[0].header.Get("Idempotency-Key")
	if ex[2].header.Get("Idempotency-Key") != key || len(ticketKey) > 255 || ticketKey == key {
		t.Errorf("job key = %q, ticket key = %q", ex[2].header.Get("Idempotency-Key"), ticketKey)
	}
}

func TestRejectsUnusableIdempotencyKeysBeforeAnyRequest(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	for _, bad := range []string{"", "   ", strings.Repeat("k", 256), "tab\t", "é", "new\nline"} {
		_, err := c.Web.CreateScan(bg, scanURL, WithIdempotencyKey(bad))
		wantConfig(t, err, "idempotency key")
		_, err = c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithIdempotencyKey(bad))
		wantConfig(t, err, "idempotency key")
	}
	if g.count() != 0 {
		t.Errorf("%d requests", g.count())
	}
}

func TestInteriorSpacesAreKeptAndTheKeyIsSentTrimmed(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("web-job-accepted"), fix("web-job-accepted"))
	c := g.client()
	jobOf(t)(c.Web.CreateScan(bg, scanURL, WithIdempotencyKey("two words-1")))
	jobOf(t)(c.Web.CreateScan(bg, scanURL, WithIdempotencyKey("  padded  ")))
	ex := g.last(2)
	if ex[0].header.Get("Idempotency-Key") != "two words-1" || ex[1].header.Get("Idempotency-Key") != "padded" {
		t.Errorf("keys = %q, %q", ex[0].header.Get("Idempotency-Key"), ex[1].header.Get("Idempotency-Key"))
	}
}

func TestACallerRetryWithTheSameKeySurvivesAReplayedUploadTicket(t *testing.T) {
	g := newGolden(t)
	key := "caller-retry-key"
	c := g.client()
	g.expect(steps(uploadPDF, fix("pdf-job-queue-full"))...)
	_, err := c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithIdempotencyKey(key))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first attempt: %v", err)
	}
	var ticket map[string]any
	if jsonErr := json.Unmarshal([]byte(g.resolve(string(fixtures["upload-ticket-pdf"].Response.Body))), &ticket); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	g.expect(reply{status: 201, body: ticket, headers: map[string]string{"Idempotent-Replayed": "true"}}, fix("upload-put-already-completed"), fix("pdf-job-idempotent-replay"))
	second := jobOf(t)(c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeFix, WithFileName("sample.pdf"), WithIdempotencyKey(key)))
	if second.State != JobStateQueued {
		t.Errorf("state = %q", second.State)
	}
	var keys []string
	g.mu.Lock()
	for _, ex := range g.exchanges {
		if ex.uri == "/v1/uploads" {
			keys = append(keys, ex.header.Get("Idempotency-Key"))
		}
	}
	g.mu.Unlock()
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Errorf("ticket keys = %q", keys)
	}
}

func TestA409OnThePutWithoutAReplayedTicketIsStillAnError(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("upload-ticket-pdf"), fix("upload-put-already-completed"))
	_, err := g.client().Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf")
	var storage *StorageError
	if !errors.As(err, &storage) || storage.Code != "UPLOAD_ALREADY_COMPLETED" {
		t.Fatalf("error = %#v", err)
	}
}

func TestAPostWithWaitIsNotRepeatedAfterATimeoutButAfterAConnectionFailure(t *testing.T) {
	g := newGolden(t)
	c := g.client()
	g.expect(fail{context.DeadlineExceeded})
	if _, err := c.Web.CreateScan(bg, scanURL, WithWait(5)); err == nil {
		t.Fatal("expected an error")
	}
	if g.count() != 1 {
		t.Errorf("a held request that timed out was repeated: %d exchanges", g.count())
	}
	before := g.count()
	g.expect(fail{context.DeadlineExceeded}, fail{context.DeadlineExceeded}, fail{context.DeadlineExceeded})
	if _, err := c.Web.CreateScan(bg, scanURL); err == nil {
		t.Fatal("expected an error")
	}
	if g.count()-before != 3 {
		t.Errorf("timeouts without wait: %d attempts, want 3", g.count()-before)
	}
	before = g.count()
	g.expect(fail{errors.New("dial tcp: refused")}, fail{errors.New("dial tcp: refused")}, fail{errors.New("dial tcp: refused")})
	if _, err := c.Web.CreateScan(bg, scanURL, WithWait(5)); err == nil {
		t.Fatal("expected an error")
	}
	if g.count()-before != 3 {
		t.Errorf("connection failures with wait: %d attempts, want 3", g.count()-before)
	}
}

func TestACancelledContextStopsAtOnceWithoutRetrying(t *testing.T) {
	g := newGolden(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := g.client().Jobs.Get(ctx, jobID)
	if !errors.Is(err, context.Canceled) || g.count() != 0 {
		t.Errorf("error = %v, exchanges = %d", err, g.count())
	}
}

func TestALostTicketAnswerIsRepeatedWithTheSameKeyAndTheSameRequest(t *testing.T) {
	g := newGolden(t)
	g.expect(lost("upload-ticket-pdf"), fix("upload-ticket-pdf"), fix("upload-put-pdf"))
	ticket, err := g.client().Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf")
	if err != nil || ticket.UploadID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("ticket = %+v, %v", ticket, err)
	}
	ex := g.last(3)
	if ex[0].header.Get("Idempotency-Key") != ex[1].header.Get("Idempotency-Key") || !bytes.Equal(ex[0].body, ex[1].body) {
		t.Error("the repeat differs from the first request")
	}
	if !reflect.DeepEqual(g.slept(), []time.Duration{250 * time.Millisecond}) {
		t.Errorf("sleeps = %v", g.slept())
	}
}

func TestTheGoldenUploadBytesMatchTheHashInTheTicketRequest(t *testing.T) {
	sum := sha256.Sum256(pdfBytes)
	if hex.EncodeToString(sum[:]) != requestBody("upload-ticket-pdf")["sha256"] {
		t.Error("the golden bytes do not match the golden hash")
	}
}

func TestEveryGoldenFixtureIsReplayedBySomeTest(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var sources strings.Builder
	for _, f := range files {
		data, _ := os.ReadFile(f)
		sources.Write(data)
	}
	for name := range fixtures {
		if !strings.Contains(sources.String(), `"`+name+`"`) {
			t.Errorf("fixture %q is not replayed by any test", name)
		}
	}
}

// ---- storage isolation (fix round 1) -------------------------------------------------------------

type recordingTransport struct {
	mu      sync.Mutex
	uris    []string
	auth    []string
	budgets []time.Duration
	err     error
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.uris = append(r.uris, req.URL.RequestURI())
	r.auth = append(r.auth, req.Header.Get("Authorization"))
	if d, ok := req.Context().Deadline(); ok {
		r.budgets = append(r.budgets, time.Until(d))
	}
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestStorageRequestsNeverUseTheCallersHTTPClient(t *testing.T) {
	g := newGolden(t)
	api := &recordingTransport{}
	g.expect(steps(uploadPDF, fix("pdf-job-accepted"), fix("artifact-download-original"))...)
	c := g.client(WithHTTPClient(&http.Client{Transport: api}))
	jobOf(t)(c.PDF.CreateJob(bg, bytes.NewReader(pdfBytes), PDFModeAnalyze, WithFileName("sample.pdf")))
	if _, err := c.Artifacts.Download(bg, artifactFor(g, "artifact-download-original")); err != nil {
		t.Fatal(err)
	}
	if len(api.uris) != 2 {
		t.Fatalf("the API client saw %v; want only the ticket and the job", api.uris)
	}
	for _, uri := range api.uris {
		if strings.Contains(uri, "sig=") {
			t.Errorf("a signed URL went through the API client: %s", uri)
		}
	}
	for _, ex := range g.last(4) {
		if strings.Contains(ex.uri, "sig=") && ex.header.Get("Authorization") != "" {
			t.Errorf("storage request carried Authorization: %s", ex.uri)
		}
	}
}

func TestStorageUsesItsOwnClientAndTimeout(t *testing.T) {
	g := newGolden(t)
	store := &recordingTransport{}
	g.expect(steps(uploadPDF)...)
	c := g.client(WithTimeout(5*time.Second), WithStorageHTTPClient(&http.Client{Transport: store}))
	if _, err := c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf"); err != nil {
		t.Fatal(err)
	}
	if len(store.uris) != 1 || !strings.Contains(store.uris[0], "sig=") || store.auth[0] != "" {
		t.Fatalf("storage saw %v auth %v", store.uris, store.auth)
	}
	if b := store.budgets[0]; b < 9*time.Minute || b > 10*time.Minute {
		t.Errorf("default storage budget = %v; want about 10m", b)
	}
	store2 := &recordingTransport{}
	g.expect(steps(uploadPDF)...)
	c = g.client(WithStorageTimeout(7*time.Minute), WithStorageHTTPClient(&http.Client{Transport: store2}))
	if _, err := c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf"); err != nil {
		t.Fatal(err)
	}
	if b := store2.budgets[0]; b < 6*time.Minute || b > 7*time.Minute {
		t.Errorf("storage budget = %v; want about 7m", b)
	}
	_, err := NewClient(WithBaseURL("http://core.test"), WithToken("x"), WithStorageTimeout(0))
	wantConfig(t, err, "storage timeout")
	_, err = NewClient(WithBaseURL("http://core.test"), WithToken("x"), WithStorageHTTPClient(nil))
	wantConfig(t, err, "storage HTTP client")
}

func TestAStorageErrorNeverContainsTheSignedURL(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("upload-ticket-pdf"))
	c := g.client(WithStorageHTTPClient(&http.Client{Transport: &recordingTransport{err: errors.New("boom")}}))
	_, err := c.Uploads.Upload(bg, bytes.NewReader(pdfBytes), ContentTypePDF, "sample.pdf")
	var storage *StorageError
	if !errors.As(err, &storage) || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "sig=") || strings.Contains(err.Error(), "/v1/uploads/") {
		t.Fatalf("error = %v", err)
	}
	a := Artifact{Kind: ArtifactOriginal, SizeBytes: 1, DownloadURL: g.srv.URL + "/x?exp=1&sig=SECRETSIG"}
	_, err = c.Artifacts.Download(bg, a)
	if err == nil || strings.Contains(err.Error(), "SECRETSIG") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("download error = %v", err)
	}
}

func TestDownloadToFileIsAtomicAndNeverOverwrites(t *testing.T) {
	g := newGolden(t)
	g.expect(fix("artifact-download-original"), fix("artifact-download-original"))
	c := g.client()
	a := artifactFor(g, "artifact-download-original")
	dir := t.TempDir()
	target := filepath.Join(dir, "out.pdf")
	if err := c.Artifacts.DownloadToFile(bg, a, target); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
	// an existing target fails and stays intact; the check happens before any download
	err := c.Artifacts.DownloadToFile(bg, a, target)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v; want ErrExist", err)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, pdfBytes) {
		t.Error("the existing file was changed")
	}
	// a mismatch leaves neither target nor temp file
	bad := a
	bad.SHA256 = strings.Repeat("0", 64)
	dir2 := t.TempDir()
	if err := c.Artifacts.DownloadToFile(bg, bad, filepath.Join(dir2, "x.pdf")); err == nil {
		t.Fatal("expected a mismatch")
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}
