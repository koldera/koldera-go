# koldera-go

Go SDK for koldera-core, the commercially licensed, self-hosted service that checks PDFs against
the PDF/UA-1 machine rules, writes fixed copies, renders HTML and structured documents to tagged PDF, and scans web
pages. Module `github.com/koldera/koldera-go`, package `koldera`, standard library only, Go 1.24 or later. Every call
takes a `context.Context` first.

Everything koldera-core reports is a **machine check**. A person still has to review the result:
`job.ManualReviewRequired` is always `true`, and this SDK never turns a passing machine check into a statement that a
document is accessible.

Status: early release (0.1). The API may change before 1.0.

## Install

```sh
go get github.com/koldera/koldera-go
```

The API reference is the package documentation (`go doc github.com/koldera/koldera-go`, or pkg.go.dev once the
repository is public). Examples for every call are in [`example_test.go`](example_test.go); the snippets below are
taken from them.

## Quick start

Point the client at your koldera-core instance. `KOLDERA_URL` and `KOLDERA_TOKEN` are read from the environment when
`WithBaseURL` and `WithToken` are not given.

```go
client, err := koldera.NewClient(
	koldera.WithBaseURL("https://koldera.example.com"),
	koldera.WithToken(os.Getenv("KOLDERA_TOKEN")),
)
if err != nil {
	log.Fatal(err)
}
info, err := client.Info(context.Background())
if err != nil {
	log.Fatal(err)
}
fmt.Println(info.Version, info.Features)
```

## Check or fix a PDF, read the findings, download the copy

The mode is `koldera.PDFModeAnalyze` (validate only) or `koldera.PDFModeFix` (validate, write a separate repaired copy,
validate again). Your original file is never modified. For HTML the modes are `koldera.HTMLModeGenerateAnalyze` and
`koldera.HTMLModeGenerateFix` (`client.HTMLToPDF.CreateJob`).

```go
file, err := os.Open("report.pdf")
if err != nil {
	log.Fatal(err)
}
defer file.Close()

ctx := context.Background()
job, err := client.PDF.CreateJob(ctx, file, koldera.PDFModeFix,
	koldera.WithExplicitLanguage("en"),
	koldera.WithWait(60),        // hold the request for up to 60 seconds
	koldera.WithUntilFinished(), // poll afterwards if the job is still running
)
if err != nil {
	log.Fatal(err)
}
if job.State == koldera.JobStateFailed {
	fmt.Println("failed:", job.Error.Code, job.Error.Message)
	return
}

findings, err := client.Jobs.Findings(ctx, job.ID)
if err != nil {
	log.Fatal(err)
}
for _, f := range findings {
	fmt.Println(f.RuleID, f.Message, f.NeedsHumanReview) // machine checks; some are flagged for a person
}

artifacts, err := client.Jobs.Artifacts(ctx, job.ID)
if err != nil {
	log.Fatal(err)
}
for _, a := range artifacts {
	if a.Kind == koldera.ArtifactRemediated {
		// Size and SHA-256 are verified before anything is written.
		if err := client.Artifacts.DownloadToFile(ctx, a, "report.fixed.pdf"); err != nil {
			log.Fatal(err)
		}
	}
}
```

Other calls: `client.Web.CreateScan(ctx, url)`, `client.Documents.CreateJob(ctx, doc)`, `client.Jobs.Get`,
`client.Jobs.Actions`, `client.Jobs.ReadingOrder`, `client.Jobs.RemediateReadingOrder`, `client.Health` and
`client.ClientSessions.Create`. See `example_test.go`.

## Waiting for a job

`WithWait(seconds)` (1 to 900) is sent as `Prefer: wait=N`: the server holds the request and answers with the job in
its real state. When the job is still running after the wait, the call returns it as is. Add `WithUntilFinished()` to
keep polling, or call `client.Jobs.Wait(ctx, id)` yourself. A failed job is returned, not an error: read `job.Error`.
Polling stops when the job is no longer queued or running, honours `Retry-After`, and returns `*koldera.TimeoutError`
when your own limit (`WithPollTimeout`) runs out; the job keeps going on the server.
`WithUntilFinished` takes the same options as `Jobs.Wait`, for example
`WithUntilFinished(koldera.WithPollTimeout(5*time.Minute))`.

```go
job, err := client.Jobs.Wait(ctx, jobID,
	koldera.WithPollTimeout(5*time.Minute),
	koldera.WithOnUpdate(func(j *koldera.Job) { fmt.Println("state:", j.State) }),
)
var timeout *koldera.TimeoutError
if errors.As(err, &timeout) {
	fmt.Println("still running; the job keeps going on the server")
} else if err == nil {
	fmt.Println(job.State)
}
```

## Structured documents

A structured document declares semantic blocks with typed values. Structure is declared by you, never inferred. Images
are uploaded first and referenced by upload id with alternative text.

```go
logo, err := os.Open("logo.png")
if err != nil {
	log.Fatal(err)
}
defer logo.Close()
ticket, err := client.Documents.UploadImage(ctx, logo) // the type comes from the file name
if err != nil {
	log.Fatal(err)
}

doc := koldera.Document{
	Title:    "Invoice 2026-0042",
	Language: "en",
	Body: []koldera.Block{
		koldera.Heading{Level: 1, Text: "Invoice 2026-0042"},
		koldera.Paragraph{Content: []koldera.Inline{koldera.Text("Thank you "), koldera.Strong("very much"), koldera.Text(".")}},
		koldera.Table{
			Caption: "Items billed",
			Header:  [][]string{{"Description", "Amount"}},
			Rows:    [][]string{{"Consulting", "1000.00"}},
		},
		koldera.Image{UploadID: ticket.UploadID, Alt: "Company logo: a blue square"},
	},
}
job, err := client.Documents.CreateJob(ctx, doc, koldera.WithWait(60), koldera.WithUntilFinished())
if err != nil {
	log.Fatal(err)
}
fmt.Println(job.State)
```

A rejected document returns `*koldera.ValidationError` whose `Errors` list `Path`, `Code` and `Message` for each
problem. The block rules are in
koldera-core's documents guide.

## Client sessions for browsers

A browser never gets your server token. Your backend authenticates the user, then mints a short-lived session for one
origin with limited scopes and budgets, and hands only `SessionToken` to the page:

```go
session, err := client.ClientSessions.Create(ctx, koldera.ClientSessionRequest{
	AllowedOrigin: "https://app.example.com",
	Scopes:        []koldera.SessionScope{koldera.ScopeJobsCreate, koldera.ScopeJobsRead},
	MaxJobs:       3,
	TTLSeconds:    600,
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(session.ExpiresAt) // hand session.SessionToken to the browser
```

Origin and CORS checks on the server are a second layer, not a secret.

## Errors

Every non-2xx answer becomes an `*koldera.APIError` (or a more specific type that embeds it) with the stable problem
`Code`, `Status` and, when the server sent one, `RetryAfter`. Use `errors.As` for the type and `errors.Is` with a
sentinel such as `koldera.ErrNotFound`. Unknown codes, states and kinds from a newer koldera-core do not crash the
client: they are plain string values.

```go
_, err = client.Jobs.Get(ctx, jobID)

var unavailable *koldera.ServiceUnavailableError
var invalid *koldera.ValidationError
switch {
case errors.As(err, &unavailable) && unavailable.RetryAfter != nil:
	fmt.Println("try again in", *unavailable.RetryAfter)
case errors.As(err, &invalid):
	for _, p := range invalid.Errors {
		fmt.Println(p.Path, p.Code, p.Message)
	}
case errors.Is(err, koldera.ErrNotFound):
	fmt.Println("no such job")
case errors.Is(err, koldera.ErrUnauthorized):
	fmt.Println("check KOLDERA_TOKEN")
}
```

| Type | Sentinel | Status |
| --- | --- | --- |
| `AuthenticationError` | `ErrUnauthorized` | 401 |
| `PermissionError` | `ErrForbidden` | 403 |
| `NotFoundError` | `ErrNotFound` | 404 |
| `ConflictError` | `ErrConflict` | 409 |
| `ValidationError` | `ErrValidation` | 400, 422 |
| `UnsupportedMediaTypeError` | `ErrUnsupportedMediaType` | 415 |
| `HostNotAllowedError` | `ErrHostNotAllowed` | 421 |
| `RateLimitError` | `ErrRateLimited` | 429 |
| `ServiceUnavailableError` | `ErrUnavailable` | 503 |
| `ServerError` | `ErrServer` | other 5xx |

Also: `ErrConfiguration` (the SDK was used wrongly), `*TimeoutError` (polling ran out) and `*StorageError` (a signed
upload or download failed, or a downloaded artifact did not match its size or SHA-256). Error messages never contain
a credential or a signed address.

Creation calls carry an `Idempotency-Key` (generated, or set with `WithIdempotencyKey`). A network failure on a
creation call is repeated with the same key, so a lost response does not create a second job.

## Configuration

| Option | Meaning |
| --- | --- |
| `WithBaseURL` | origin of koldera-core; env `KOLDERA_URL` |
| `WithToken` | server token, any non-empty string the server accepts; env `KOLDERA_TOKEN`; never printed |
| `WithTimeout` | per-request timeout for API calls (default 30 s); a `WithWait` stretches it to cover the wait plus 10 s |
| `WithStorageTimeout` | timeout of one signed upload or download (default 10 minutes) |
| `WithHTTPClient` | your `*http.Client` for API calls |
| `WithStorageHTTPClient` | your `*http.Client` for signed uploads and downloads (for example a proxy); it must not add credentials |
| `WithUserAgentSuffix` | product token appended to the SDK's User-Agent |

Per call on job creation: `WithWait`, `WithUntilFinished`, `WithIdempotencyKey` and `WithOnResponse`. PDF and HTML
calls add `WithFileName`, `WithExplicitLanguage` and `WithExplicitTitle`; HTML adds `WithContentSelector`.

Signed upload and download addresses go through a separate storage client with its own timeout, so they never receive
the `Authorization` header and your `WithHTTPClient` client never sees them.

## Downloads are verified

`Artifacts.Download`, `DownloadTo` and `DownloadToFile` check the size and SHA-256 from the artifact listing. Nothing is
returned or written on a mismatch (`*StorageError`). `DownloadToFile` writes a temporary file beside the target,
verifies it, and moves it into place without overwriting an existing file. Download addresses expire after ten
minutes, so list the artifacts shortly before you download them.

## Compatibility

| SDK | koldera-core | Go |
| --- | --- | --- |
| 0.1.x | 0.1.x | 1.24 or later |

The vendored API description in [`contracts/`](contracts) records the koldera-core commit it was taken from
(`contracts/CORE_VERSION`), and CI checks that the copy matches. Run koldera-core as described in its
README and deployment guide.

## Links

- [Contributing](CONTRIBUTING.md), [security policy](SECURITY.md), [changelog](CHANGELOG.md).

## Licence

This SDK is licensed under the [Apache License 2.0](LICENSE).

koldera-core is commercially licensed, self-hosted software; this SDK is Apache-2.0 and talks to it over its HTTP API.
