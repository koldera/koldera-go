package koldera

import "time"

// PDFMode selects what a PDF job does.
type PDFMode string

// PDF job modes. The profile is always ua1.
const (
	// PDFModeAnalyze validates the PDF and reports; it writes nothing.
	PDFModeAnalyze PDFMode = "analyze"
	// PDFModeFix also writes a separate repaired copy (artifact kind remediated). The original is never modified.
	PDFModeFix PDFMode = "fix"
)

// HTMLMode selects what an HTML-to-PDF job does.
type HTMLMode string

// HTML-to-PDF job modes.
const (
	// HTMLModeGenerateAnalyze renders the page to a tagged PDF and validates it.
	HTMLModeGenerateAnalyze HTMLMode = "generate_analyze"
	// HTMLModeGenerateFix also repairs the generated PDF.
	HTMLModeGenerateFix HTMLMode = "generate_fix"
)

// JobKind is the kind of a job. Values a newer core sends are kept as they are.
type JobKind string

// Known job kinds.
const (
	JobKindPDF           JobKind = "pdf"
	JobKindHTMLToPDF     JobKind = "html_to_pdf"
	JobKindDocumentToPDF JobKind = "document_to_pdf"
	JobKindWeb           JobKind = "web"
	JobKindOrderPDF      JobKind = "order_pdf"
	JobKindOrderHTML     JobKind = "order_html"
)

// JobState is the execution state of a job; it says nothing about the result of the checks.
type JobState string

// Known job states. Any other value counts as finished.
const (
	JobStateQueued    JobState = "queued"
	JobStateRunning   JobState = "running"
	JobStateCompleted JobState = "completed"
	JobStateFailed    JobState = "failed"
)

// JobMode is the mode a job ran in.
type JobMode string

// Known job modes.
const (
	JobModeAnalyze         JobMode = "analyze"
	JobModeFix             JobMode = "fix"
	JobModeGenerateAnalyze JobMode = "generate_analyze"
	JobModeGenerateFix     JobMode = "generate_fix"
	JobModeScan            JobMode = "scan"
	JobModeReorder         JobMode = "reorder"
)

// RemediationStatus tells what a fix did to the copy.
type RemediationStatus string

// Known remediation statuses.
const (
	RemediationNotRequested    RemediationStatus = "not_requested"
	RemediationNoApplicableFix RemediationStatus = "no_applicable_fix"
	RemediationApplied         RemediationStatus = "applied"
	RemediationSkipped         RemediationStatus = "skipped"
	RemediationRolledBack      RemediationStatus = "rolled_back"
)

// ValidationStatus is the outcome of one machine validation.
type ValidationStatus string

// Known validation statuses.
const (
	ValidationPassed  ValidationStatus = "passed"
	ValidationFailed  ValidationStatus = "failed"
	ValidationErrored ValidationStatus = "error"
	ValidationNotRun  ValidationStatus = "not_run"
)

// FailedRule is one failing machine rule and how often it fired.
type FailedRule struct {
	RuleID string `json:"ruleId"`
	Count  int    `json:"count"`
}

// MachineValidation is an automated validation result. It is a machine check, never a statement of conformance.
type MachineValidation struct {
	Profile     string           `json:"profile"`
	Status      ValidationStatus `json:"status"`
	FailedRules []FailedRule     `json:"failedRules"`
}

// JobError says why a job failed, for example WORKER_TIMEOUT or INVALID_INPUT. It is not a problem code.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Job is a job as the API reports it. A null in the response is the zero value here (empty string, nil pointer).
// State is execution state only; ManualReviewRequired stays true because nothing a job reports states conformance.
type Job struct {
	ID                   string             `json:"id"`
	Kind                 JobKind            `json:"kind"`
	Mode                 JobMode            `json:"mode"`
	State                JobState           `json:"state"`
	CreatedAt            time.Time          `json:"createdAt"`
	StartedAt            *time.Time         `json:"startedAt"`
	FinishedAt           *time.Time         `json:"finishedAt"`
	Validation           *MachineValidation `json:"validation"`
	BaselineValidation   *MachineValidation `json:"baselineValidation"`
	FinalValidation      *MachineValidation `json:"finalValidation"`
	RemediationStatus    RemediationStatus  `json:"remediationStatus"`
	ManualReviewRequired bool               `json:"manualReviewRequired"`
	EngineVersion        string             `json:"engineVersion"`
	Error                *JobError          `json:"error"`
	// TargetURL is set on web jobs, without query or fragment.
	TargetURL string `json:"targetUrl"`
	// ContentSelector is set on HTML jobs that printed only the elements matching it.
	ContentSelector string `json:"contentSelector"`
}

// IsFinished reports whether the job is no longer queued or running. A state an older SDK does not know counts as
// finished, so polling never runs forever.
func (j *Job) IsFinished() bool {
	return j.State != JobStateQueued && j.State != JobStateRunning
}

// FindingSource says which checker produced a finding.
type FindingSource string

// Known finding sources.
const (
	FindingSourcePDF  FindingSource = "pdf"
	FindingSourceHTML FindingSource = "html"
	FindingSourceWeb  FindingSource = "web"
)

// FindingResolution is set only on machine checks of a job whose fix was applied.
type FindingResolution string

// Known finding resolutions.
const (
	ResolutionFixedInCopy  FindingResolution = "fixed_in_copy"
	ResolutionStillFailing FindingResolution = "still_failing"
)

// Finding is one result of a job. NeedsHumanReview separates automated checks from items a person must decide.
type Finding struct {
	Source           FindingSource     `json:"source"`
	RuleID           string            `json:"ruleId"`
	Message          string            `json:"message"`
	PageNumber       *int              `json:"pageNumber"`
	Selector         string            `json:"selector"`
	NeedsHumanReview bool              `json:"needsHumanReview"`
	Resolution       FindingResolution `json:"resolution"`
}

// ActionOutcome is what one repair rule did.
type ActionOutcome string

// Known action outcomes.
const (
	ActionApplied    ActionOutcome = "applied"
	ActionSkipped    ActionOutcome = "skipped"
	ActionFailed     ActionOutcome = "failed"
	ActionRolledBack ActionOutcome = "rolled_back"
)

// RemediationAction is one repair rule's outcome on a fix job.
type RemediationAction struct {
	RuleID  string        `json:"ruleId"`
	Outcome ActionOutcome `json:"outcome"`
	Reason  string        `json:"reason"`
	Before  *string       `json:"before"`
	After   *string       `json:"after"`
}

// ArtifactKind is the kind of a downloadable file.
type ArtifactKind string

// Known artifact kinds.
const (
	ArtifactOriginal       ArtifactKind = "original"
	ArtifactRemediated     ArtifactKind = "remediated"
	ArtifactBaselineReport ArtifactKind = "baseline_report"
	ArtifactFinalReport    ArtifactKind = "final_report"
	ArtifactGeneratedPDF   ArtifactKind = "generated_pdf"
	ArtifactSourceHTML     ArtifactKind = "source_html"
	ArtifactWebReport      ArtifactKind = "web_report"
	ArtifactReordered      ArtifactKind = "reordered"
)

// Artifact is a file of a job with a short-lived signed download URL (fetched without any bearer token).
type Artifact struct {
	ID          string       `json:"id"`
	Kind        ArtifactKind `json:"kind"`
	SHA256      string       `json:"sha256"`
	SizeBytes   int64        `json:"sizeBytes"`
	DownloadURL string       `json:"downloadUrl"`
	ExpiresAt   time.Time    `json:"expiresAt"`
}

// UploadContentType is a content type the upload endpoint accepts.
type UploadContentType string

// Accepted upload content types.
const (
	ContentTypePDF  UploadContentType = "application/pdf"
	ContentTypeHTML UploadContentType = "text/html"
	ContentTypePNG  UploadContentType = "image/png"
	ContentTypeJPEG UploadContentType = "image/jpeg"
	ContentTypeSVG  UploadContentType = "image/svg+xml"
)

// UploadTicket is the single-use destination of an upload. UploadID is what a job request or an image block refers to.
type UploadTicket struct {
	UploadID  string    `json:"uploadId"`
	Method    string    `json:"method"`
	UploadURL string    `json:"uploadUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
	// MaxBytes is the configured size limit for this content type; nil when the server states none.
	MaxBytes *int64 `json:"maxBytes"`
}

// BoundingBox is the rendered position of a node.
type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// ReadingOrderNode is one node of a reading-order snapshot. Ids are opaque and stable per artifact.
type ReadingOrderNode struct {
	NodeID       string       `json:"nodeId"`
	ParentID     string       `json:"parentId"`
	Role         string       `json:"role"`
	LogicalIndex int          `json:"logicalIndex"`
	PageNumber   *int         `json:"pageNumber"`
	Origin       string       `json:"origin"`
	TextPreview  string       `json:"textPreview"`
	BoundingBox  *BoundingBox `json:"boundingBox"`
}

// ReadingOrderSnapshot is evidence bound to one artifact hash. ReviewRequired is always true: it is never a verdict.
type ReadingOrderSnapshot struct {
	JobID                       string             `json:"jobId"`
	SourceArtifactSHA256        string             `json:"sourceArtifactSha256"`
	SnapshotVersion             int                `json:"snapshotVersion"`
	Nodes                       []ReadingOrderNode `json:"nodes"`
	ReviewRequired              bool               `json:"reviewRequired"`
	ReadingOrderReview          string             `json:"readingOrderReview"`
	FocusOrderReview            string             `json:"focusOrderReview"`
	FocusOrderNodeIDs           []string           `json:"focusOrderNodeIds"`
	VisualOrderCandidateNodeIDs []string           `json:"visualOrderCandidateNodeIds"`
	ArtifactHashes              map[string]string  `json:"artifactHashes"`
}

// RemediationTarget is what a reading-order correction changes.
type RemediationTarget string

// Reading-order correction targets.
const (
	RemediationTargetPDFTags    RemediationTarget = "pdf_tags"
	RemediationTargetSourceHTML RemediationTarget = "source_html"
)

// ReadingOrderRemediation is what a person approved: the complete children of one parent, in the new order.
type ReadingOrderRemediation struct {
	// BaseArtifactSHA256 is the snapshot's artifact hash; a changed artifact is refused with a conflict.
	BaseArtifactSHA256 string
	SnapshotVersion    int
	Target             RemediationTarget
	// OrderedNodeIDs lists every child of one parent in the approved order.
	OrderedNodeIDs []string
	// Acknowledged must be true: a person checked the sequence against the content.
	Acknowledged bool
	// Reviewer is an audit label the caller asserts, not authenticated (1-200 characters, trimmed). Empty sends none.
	Reviewer string
}

// SessionScope is a right a client session can carry.
type SessionScope string

// Client session scopes.
const (
	ScopeJobsCreate SessionScope = "jobs:create"
	ScopeJobsRead   SessionScope = "jobs:read"
)

// ClientSessionRequest asks for a short-lived token a browser can use. Zero values are not sent and take the
// server's defaults.
type ClientSessionRequest struct {
	// AllowedOrigin is an absolute http(s) origin without a path, for example https://app.example.com.
	AllowedOrigin string `json:"allowedOrigin"`
	// Scopes defaults to both scopes.
	Scopes []SessionScope `json:"scopes,omitempty"`
	// MaxJobs is 1-1000 (default 10).
	MaxJobs int `json:"maxJobs,omitempty"`
	// MaxUploads is 1-10000 (default ten per job).
	MaxUploads int `json:"maxUploads,omitempty"`
	// TTLSeconds is 1-3600 (default 900).
	TTLSeconds int `json:"ttlSeconds,omitempty"`
}

// ClientSession is a minted token. Hand SessionToken to the browser, never the server token.
type ClientSession struct {
	SessionToken string    `json:"sessionToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// Feature is a capability the server has switched on.
type Feature string

// Known features.
const (
	FeatureDocument Feature = "document"
	FeatureHTML     Feature = "html"
	FeaturePDF      Feature = "pdf"
	FeatureWeb      Feature = "web"
)

// Info describes the server (GET /v1/info).
type Info struct {
	Version       string    `json:"version"`
	EngineVersion string    `json:"engineVersion"`
	Features      []Feature `json:"features"`
}

// Health is the answer of the liveness probe (GET /healthz).
type Health struct {
	Status string `json:"status"`
}

// ResponseMeta is what a call's response said besides its body.
type ResponseMeta struct {
	Status int
	// Location is where to poll the job, as sent.
	Location string
	// PreferenceApplied is wait=N when the server held the request.
	PreferenceApplied string
	// IdempotentReplayed is true when the stored answer of an earlier request was replayed.
	IdempotentReplayed bool
}
