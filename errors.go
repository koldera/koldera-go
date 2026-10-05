package koldera

import (
	"errors"
	"fmt"
	"time"
)

// ErrConfiguration marks a misuse of the SDK: a bad option, a malformed argument, an unacknowledged correction.
// It is raised before any request is made.
var ErrConfiguration = errors.New("koldera: invalid configuration or argument")

// Sentinels for errors.Is; every API error matches the sentinels of its status class.
var (
	ErrUnauthorized         = errors.New("koldera: unauthorized (401)")
	ErrForbidden            = errors.New("koldera: forbidden (403)")
	ErrNotFound             = errors.New("koldera: not found (404)")
	ErrConflict             = errors.New("koldera: conflict (409)")
	ErrValidation           = errors.New("koldera: invalid request or input (400, 422)")
	ErrUnsupportedMediaType = errors.New("koldera: unsupported media type (415)")
	ErrHostNotAllowed       = errors.New("koldera: host not allowed (421)")
	ErrRateLimited          = errors.New("koldera: rate limited (429)")
	ErrUnavailable          = errors.New("koldera: service unavailable (503)")
	ErrServer               = errors.New("koldera: server error (5xx)")
)

// ProblemError is one problem of a rejected structured document; Path is a JSON Pointer.
type ProblemError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is any non-2xx answer of the API. Code is the stable problem code (QUEUE_FULL,
// STALE_READING_ORDER_SNAPSHOT, ...), also for codes a newer core adds. The concrete type of the error tells what
// to do (see the other error types); use errors.As with *APIError to reach the fields of any of them. Messages never
// include a credential.
type APIError struct {
	Status int
	Code   string
	Title  string
	Detail string
	// Errors lists the problems of a rejected structured document; empty otherwise.
	Errors []ProblemError
	// RetryAfter is what the Retry-After header asked for; nil when absent.
	RetryAfter *time.Duration
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s (%s, HTTP %d): %s", e.Title, e.Code, e.Status, e.Detail)
	}
	return fmt.Sprintf("%s (%s, HTTP %d)", e.Title, e.Code, e.Status)
}

// Is matches the sentinels of the status class.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == 401
	case ErrForbidden:
		return e.Status == 403
	case ErrNotFound:
		return e.Status == 404
	case ErrConflict:
		return e.Status == 409
	case ErrValidation:
		return e.Status == 400 || e.Status == 422
	case ErrUnsupportedMediaType:
		return e.Status == 415
	case ErrHostNotAllowed:
		return e.Status == 421
	case ErrRateLimited:
		return e.Status == 429
	case ErrUnavailable:
		return e.Status == 503
	case ErrServer:
		return e.Status >= 500
	}
	return false
}

// AuthenticationError is a 401: the token is missing, malformed, expired or unknown.
type AuthenticationError struct{ APIError }

// PermissionError is a 403: the token is valid but lacks the scope or the right (INSUFFICIENT_SCOPE, FORBIDDEN,
// SIGNATURE_INVALID).
type PermissionError struct{ APIError }

// NotFoundError is a 404: the resource does not exist, is not visible to this token, or its feature is switched off.
type NotFoundError struct{ APIError }

// ConflictError is a 409: an idempotency key reused with another body, a stale reading-order snapshot, an expired
// artifact.
type ConflictError struct{ APIError }

// ValidationError is a 422, or a 400 for a malformed body. See Code and Errors.
type ValidationError struct{ APIError }

// UnsupportedMediaTypeError is a 415: the body is not JSON, or the input type is unsupported.
type UnsupportedMediaTypeError struct{ APIError }

// HostNotAllowedError is a 421: the request reached a host the server does not serve.
type HostNotAllowedError struct{ APIError }

// RateLimitError is a 429: a client-session budget is used up (SESSION_JOB_LIMIT, SESSION_UPLOAD_LIMIT). Mint a new
// session.
type RateLimitError struct{ APIError }

// ServerError is any 5xx.
type ServerError struct{ APIError }

// ServiceUnavailableError is a 503, including QUEUE_FULL; RetryAfter says when to try again. It is also a
// ServerError.
type ServiceUnavailableError struct{ ServerError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *AuthenticationError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *PermissionError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *NotFoundError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *ConflictError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *ValidationError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *UnsupportedMediaTypeError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *HostNotAllowedError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *RateLimitError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *APIError.
func (e *ServerError) Unwrap() error { return &e.APIError }

// Unwrap lets errors.As reach the embedded *ServerError and, through it, the *APIError.
func (e *ServiceUnavailableError) Unwrap() error { return &e.ServerError }

// TimeoutError is returned by Jobs.Wait when the job did not finish within the poll timeout. The job keeps running on
// the server.
type TimeoutError struct {
	JobID   string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("job %s did not finish within %s", e.JobID, e.Timeout)
}

// StorageError is a failed signed storage exchange (upload PUT or artifact download), or downloaded bytes that did
// not match the declared size or SHA-256.
type StorageError struct {
	Message string
	// Status is the HTTP status of the storage answer; 0 when there was none.
	Status int
	// Code is the problem code of the storage answer (SIGNATURE_INVALID, UPLOAD_EXPIRED, ...), when it sent one.
	Code string
}

func (e *StorageError) Error() string { return e.Message }

// problem is the RFC 9457 body of the API.
type problem struct {
	Type   string
	Title  string
	Status int
	Code   string
	Detail string
	Errors []ProblemError
}

func errorFromProblem(p problem, retryAfter *time.Duration) error {
	base := APIError{Status: p.Status, Code: p.Code, Title: p.Title, Detail: p.Detail, Errors: p.Errors, RetryAfter: retryAfter}
	switch {
	case p.Status == 400 || p.Status == 422:
		return &ValidationError{base}
	case p.Status == 401:
		return &AuthenticationError{base}
	case p.Status == 403:
		return &PermissionError{base}
	case p.Status == 404:
		return &NotFoundError{base}
	case p.Status == 409:
		return &ConflictError{base}
	case p.Status == 415:
		return &UnsupportedMediaTypeError{base}
	case p.Status == 421:
		return &HostNotAllowedError{base}
	case p.Status == 429:
		return &RateLimitError{base}
	case p.Status == 503:
		return &ServiceUnavailableError{ServerError{base}}
	case p.Status >= 500:
		return &ServerError{base}
	}
	return &base
}

func configError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConfiguration, fmt.Sprintf(format, args...))
}

// Is also matches ErrServer for a malformed success body, which carries the 2xx status it arrived with.
func (e *ServerError) Is(target error) bool {
	return target == ErrServer || e.APIError.Is(target)
}
