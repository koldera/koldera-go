# Contributing

Thanks for helping. Please read this page and the [Code of Conduct](CODE_OF_CONDUCT.md) first.

## Before you start

- For a bug or a small fix, open a pull request or an issue.
- For a new feature or a change to the public API, open an issue first so we can agree on the shape before you write
  code.
- Report security problems privately, see [SECURITY.md](SECURITY.md).
- Server behaviour and the HTTP API belong to [koldera-core](https://github.com/koldera/koldera-core). This repository
  only wraps them. Changes must not make the SDK claim more than a machine check: no claim that a document is
  "accessible" or conformant, no generated alternative text, no guessed structure.

## Licence of contributions

The project is licensed under Apache-2.0. By submitting a contribution you agree that it is licensed under the same
terms (section 5 of the licence). There is no CLA and no DCO sign-off.

## Setup

You need Go 1.24 or later and Node.js 20 or later for the repository scripts. The module has no dependencies.

```bash
gofmt -l .                                               # must print nothing
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...   # the version CI uses
go test -race ./...                                      # the race detector needs cgo
node scripts/check-source-hygiene.mjs                    # must exit 0
node --test scripts/check-source-hygiene.test.mjs
```

### Dependencies

The client uses the standard library only. CI fails if `go.mod` gains a `require` line; a dependency needs an issue
and an agreement first, because it would also need a licence check.

### Contracts

`contracts/openapi.yaml` and the schemas next to it are copied from koldera-core at the commit named in
`contracts/CORE_VERSION`, and the golden fixtures drive the SDK tests (every fixture needs a replay case). When
koldera-core changes its API, update the copy and the commit id together; CI fetches the files at that commit and fails
on a difference (it is skipped with a notice while koldera-core is private and not readable with the workflow token).

### Source hygiene

The hygiene check scans for internal planning references, hosted-service hostnames, long comment blocks and wording that
claims more than a machine check can. Say what was checked and that human review is still required.

## Making a change

- **Test first.** Write a failing test for the behaviour, then the smallest change that passes it.
- **Keep the public surface honest.** Every exported identifier gets a doc comment, and the README and
  `example_test.go` show the change.
- **Open values.** Kinds, states, modes and problem codes are strings with constants, never closed sets.
- **No real documents or credentials.** Fixtures are synthetic; examples use `example.com`. Never commit tokens, keys
  or personal data.
- **Credentials stay out of reach.** The API token is sent only to the configured base URL; signed addresses never get
  it. Keep it out of messages, `String` results and logs.

## Commit style

Small focused commits, imperative mood, plain messages that say what changed and why when it is not obvious:

```text
Verify the SHA-256 before DownloadToFile moves the file
Keep the request on a redacted network error
Document the compatibility table
```

Avoid messages like "update" or "fix".

## Pull requests

Describe what changed and why, list the commands you ran with their results, and note anything you could not run. Keep
a pull request to one concern. The Koldera team reviews it; expect questions about tests and about what a change could
make the SDK claim. The repository is squash-merged.

CI runs one job on Ubuntu and is skipped for changes that only touch the changelog, the community files or the issue
and PR templates; the README is checked against the examples by review, so a README change runs CI. The release
workflow runs the whole CI job again for the tagged commit and adds a test run on the oldest supported Go release.

## Releasing

Releases are made by the maintainers. A Go module has no registry upload: the tag is the release, and the Go module
proxy fetches it on demand.

1. Make sure `main` is green. Until koldera-core is public the CI contract check is skipped, so run it by hand: fetch
   `contracts/*` from koldera-core at the commit in `contracts/CORE_VERSION` and diff them against this repository.
2. Move the changelog entry from "Unreleased" to the version and date, commit and merge that change through a pull
   request.
3. Tag the merged commit and push the tag:

   ```bash
   git tag v0.1.0 <commit-on-main>
   git push origin v0.1.0
   ```

4. `release.yml` verifies that the tag is a semantic version on `main`, runs the whole CI workflow and a test run on
   `oldstable`, creates the GitHub Release with generated notes (it is skipped when the release exists), and finally
   asks `proxy.golang.org` for the version so that `go get` and pkg.go.dev see it. That last step cannot succeed while
   the repository is private (the job is allowed to fail); it works once the repository is public.

Never move or reuse a published tag: the module proxy caches the first content it saw, so a fix is a new patch version.
If the release job fails after the tag exists, fix the cause and re-run the failed jobs.
