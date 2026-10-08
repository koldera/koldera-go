package koldera_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/koldera/koldera-go"
)

// TestLiveCore runs only against a real koldera-core:
// KOLDERA_URL=http://localhost:8080 KOLDERA_TOKEN=... go test -run TestLiveCore -v
func TestLiveCore(t *testing.T) {
	if os.Getenv("KOLDERA_URL") == "" || os.Getenv("KOLDERA_TOKEN") == "" {
		t.Skip("set KOLDERA_URL and KOLDERA_TOKEN to run against a real koldera-core")
	}
	ctx := context.Background()
	client, err := koldera.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	wait := []koldera.CallOption{koldera.WithWait(120), koldera.WithUntilFinished()}

	// download fetches the first artifact of the kind and checks its SHA-256 here too, not only in the SDK.
	download := func(t *testing.T, jobID string, kind koldera.ArtifactKind) string {
		t.Helper()
		artifacts, err := client.Jobs.Artifacts(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range artifacts {
			if a.Kind != kind {
				continue
			}
			data, err := client.Artifacts.Download(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != a.SHA256 {
				t.Fatalf("%s: sha256 %s, artifact says %s", kind, got, a.SHA256)
			}
			return a.SHA256
		}
		return ""
	}

	t.Run("health and info", func(t *testing.T) {
		if h, err := client.Health(ctx); err != nil || h.Status != "ok" {
			t.Fatalf("health %v %v", h, err)
		}
		if _, err := client.Info(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("analyze and fix a synthetic PDF", func(t *testing.T) {
		pdf, err := os.ReadFile("testdata/synthetic-report.pdf")
		if err != nil {
			t.Fatal(err)
		}
		analyzed, err := client.PDF.CreateJob(ctx, bytes.NewReader(pdf), koldera.PDFModeAnalyze, wait...)
		if err != nil {
			t.Fatal(err)
		}
		if analyzed.State != koldera.JobStateCompleted || !analyzed.ManualReviewRequired {
			t.Fatalf("analyze: %+v", analyzed)
		}
		if findings, err := client.Jobs.Findings(ctx, analyzed.ID); err != nil || len(findings) == 0 {
			t.Fatalf("findings %d %v", len(findings), err)
		}
		fixed, err := client.PDF.CreateJob(ctx, bytes.NewReader(pdf), koldera.PDFModeFix, append(wait, koldera.WithExplicitLanguage("en"))...)
		if err != nil {
			t.Fatal(err)
		}
		if !fixed.IsFinished() || !fixed.ManualReviewRequired {
			t.Fatalf("fix: %+v", fixed)
		}
		sha := download(t, fixed.ID, koldera.ArtifactRemediated)
		t.Logf("analyze %s %s; fix %s %s %s final %v; remediated sha %s", analyzed.ID, analyzed.Validation.Status, fixed.ID, fixed.State, fixed.RemediationStatus, fixed.FinalValidation, sha)
	})

	t.Run("HTML to tagged PDF", func(t *testing.T) {
		html := `<!doctype html><html lang="en"><head><title>Synthetic page</title></head><body><h1>Synthetic page</h1><p>Invented text.</p></body></html>`
		job, err := client.HTMLToPDF.CreateJob(ctx, strings.NewReader(html), koldera.HTMLModeGenerateAnalyze, wait...)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != koldera.JobStateCompleted || download(t, job.ID, koldera.ArtifactGeneratedPDF) == "" {
			t.Fatalf("html: %+v", job)
		}
		t.Logf("html %s %s", job.ID, job.Validation.Status)
	})

	t.Run("structured document to tagged PDF", func(t *testing.T) {
		doc := koldera.Document{Title: "Synthetic note", Language: "en", Body: []koldera.Block{
			koldera.Heading{Level: 1, Text: "Synthetic note"},
			koldera.Paragraph{Content: []koldera.Inline{koldera.Text("Invented text for a live test.")}},
		}}
		job, err := client.Documents.CreateJob(ctx, doc, wait...)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != koldera.JobStateCompleted || download(t, job.ID, koldera.ArtifactGeneratedPDF) == "" {
			t.Fatalf("document: %+v", job)
		}
		t.Logf("document %s %s", job.ID, job.Validation.Status)
	})
}
