// Package koldera is the Go client for koldera-core, the self-hosted service that checks PDFs against the PDF/UA-1
// machine rules, writes fixed copies, renders HTML and structured documents to tagged PDF, and scans web pages.
// Everything the server reports is a machine check: Job.ManualReviewRequired is always true, and a person still has to
// review the result. Create one Client with NewClient, configured by WithBaseURL and WithToken or by KOLDERA_URL and
// KOLDERA_TOKEN; every call takes a context.Context first. Server errors are *APIError values of a more specific type.
package koldera
