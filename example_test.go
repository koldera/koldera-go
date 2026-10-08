package koldera_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/koldera/koldera-go"
)

func ExampleNewClient() {
	client, err := koldera.NewClient(
		koldera.WithBaseURL("https://koldera.example.com"),
		koldera.WithToken(os.Getenv("KOLDERA_TOKEN")),
		koldera.WithTimeout(30*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	info, err := client.Info(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(info.Version, info.Features)
}

func ExamplePDF_CreateJob() {
	client, err := koldera.NewClient() // KOLDERA_URL and KOLDERA_TOKEN
	if err != nil {
		log.Fatal(err)
	}
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
	fmt.Println(job.State, job.ManualReviewRequired)
}

func ExampleHTMLToPDF_CreateJob() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	file, err := os.Open("page.html")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	job, err := client.HTMLToPDF.CreateJob(context.Background(), file, koldera.HTMLModeGenerateFix,
		koldera.WithContentSelector("main"),
		koldera.WithExplicitTitle("Quarterly summary"),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(job.ID)
}

func ExampleWeb_CreateScan() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	job, err := client.Web.CreateScan(context.Background(), "https://example.com/", koldera.WithWait(120), koldera.WithUntilFinished())
	if err != nil {
		log.Fatal(err)
	}
	findings, err := client.Jobs.Findings(context.Background(), job.ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range findings {
		fmt.Println(f.RuleID, f.Message, f.NeedsHumanReview)
	}
}

func ExampleDocuments_CreateJob() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

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
}

func ExampleDocument() {
	doc := koldera.Document{
		Title:    "Notes",
		Language: "en",
		Body: []koldera.Block{
			koldera.Heading{Level: 1, Text: "Notes"},
			koldera.Paragraph{Content: []koldera.Inline{koldera.Text("Read "), koldera.InlineLink{Text: "the guide", URL: "https://example.com/guide"}}},
		},
	}
	out, err := json.Marshal(doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(out))
	// Output:
	// {"title":"Notes","language":"en","body":[{"type":"heading","level":1,"text":"Notes"},{"type":"paragraph","content":["Read ",{"link":{"text":"the guide","url":"https://example.com/guide"}}]}]}
}

func ExampleJobs_Wait() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	job, err := client.Jobs.Wait(context.Background(), "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01",
		koldera.WithPollTimeout(5*time.Minute),
		koldera.WithOnUpdate(func(j *koldera.Job) { fmt.Println("state:", j.State) }),
	)
	var timeout *koldera.TimeoutError
	switch {
	case errors.As(err, &timeout):
		fmt.Println("still running; the job keeps going on the server")
	case err != nil:
		log.Fatal(err)
	case job.State == koldera.JobStateFailed:
		fmt.Println("failed:", job.Error.Code, job.Error.Message)
	}
}

func ExampleArtifacts_DownloadToFile() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	jobID := "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01"
	artifacts, err := client.Jobs.Artifacts(ctx, jobID)
	if err != nil {
		log.Fatal(err)
	}
	for _, a := range artifacts {
		if a.Kind == koldera.ArtifactRemediated {
			// Size and SHA-256 are verified before anything is written.
			if err := client.Artifacts.DownloadToFile(ctx, a, "fixed.pdf"); err != nil {
				log.Fatal(err)
			}
		}
	}
}

func ExampleJobs_RemediateReadingOrder() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	jobID := "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01"
	snapshot, err := client.Jobs.ReadingOrder(ctx, jobID)
	if err != nil {
		log.Fatal(err)
	}
	// A person reviewed the sequence and approved this order of the children of one parent.
	job, err := client.Jobs.RemediateReadingOrder(ctx, jobID, koldera.ReadingOrderRemediation{
		BaseArtifactSHA256: snapshot.SourceArtifactSHA256,
		SnapshotVersion:    snapshot.SnapshotVersion,
		Target:             koldera.RemediationTargetPDFTags,
		OrderedNodeIDs:     []string{"n2b3c4d5e6f708190", "n1a2b3c4d5e6f7089"},
		Acknowledged:       true,
		Reviewer:           "reviewer@example.com",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(job.ID)
}

func ExampleClientSessions_Create() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	session, err := client.ClientSessions.Create(context.Background(), koldera.ClientSessionRequest{
		AllowedOrigin: "https://app.example.com",
		Scopes:        []koldera.SessionScope{koldera.ScopeJobsCreate, koldera.ScopeJobsRead},
		MaxJobs:       3,
		TTLSeconds:    600,
	})
	if err != nil {
		log.Fatal(err)
	}
	// Hand session.SessionToken to the browser; never the server token.
	fmt.Println(session.ExpiresAt)
}

func ExampleAPIError() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	_, err = client.Jobs.Get(context.Background(), "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01")

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
}

func ExampleClient_Health() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	health, err := client.Health(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%+v\n", *health)
}

func ExampleArtifacts_Download() {
	client, err := koldera.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	artifacts, err := client.Jobs.Artifacts(ctx, "4d2d5a6e-2b1c-4e4a-9d6b-1f3c5e7a9b01")
	if err != nil {
		log.Fatal(err)
	}
	for _, a := range artifacts {
		data, err := client.Artifacts.Download(ctx, a) // verified by size and SHA-256 before it is returned
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(a.Kind, len(data))
	}
}
