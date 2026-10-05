# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project follows [Semantic Versioning](https://semver.org/) (0.x releases may change the API).

## 0.1.0 — Unreleased

Initial release of `github.com/koldera/koldera-go`, for koldera-core 0.1.

- `koldera.Client` over `net/http`, standard library only, Go 1.24 or later; every call takes a `context.Context`.
  Configured with options or with `KOLDERA_URL` and `KOLDERA_TOKEN`.
- Resources: `PDF` and `HTMLToPDF` jobs (`analyze` and `fix`, `generate_analyze` and `generate_fix`), `Web` scans,
  `Documents` (typed structured documents and image uploads), `Jobs` (get, wait, findings, actions, artifacts,
  reading-order evidence and approved corrections), `Artifacts`, `Uploads`, `ClientSessions` (scoped, short-lived
  browser sessions minted by your backend), `Info` and `Health`.
- `WithWait` (sent as `Prefer: wait=N`) and `WithUntilFinished` polling on every creation call; `Jobs.Wait` treats any
  state other than queued or running as final and honours `Retry-After`.
- Open values: unknown states, kinds, modes and problem codes from a newer koldera-core stay plain strings.
- Typed errors per HTTP status with the problem `Code`, per-problem `Errors` and `RetryAfter`, usable with
  `errors.As` and `errors.Is`.
- Idempotency keys on creation calls; network failures are repeated with the same key.
- Artifact downloads verify size and SHA-256 before returning or writing a byte (`DownloadToFile` writes a temporary
  file and moves it into place without overwriting).
- Signed upload and download addresses use a separate SDK-owned client with its own storage timeout, never receive the
  API token, and never appear in error messages.
- Apache-2.0 licence; CI with contract, secret and source-hygiene checks; a tag-driven release workflow.
