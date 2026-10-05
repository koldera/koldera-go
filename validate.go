package koldera

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxReadBytes = 100 << 20

var (
	languageTag     = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)
	idempotencyForm = regexp.MustCompile(`^[\x20-\x7e]{1,255}$`)
	sha256Form      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	uuidForm        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	originForm      = regexp.MustCompile(`^https?://[^/\s]+$`)
	httpURLForm     = regexp.MustCompile(`(?i)^https?://[^/\s]+`)
)

var imageExtensions = map[string]UploadContentType{
	".png": ContentTypePNG, ".jpg": ContentTypeJPEG, ".jpeg": ContentTypeJPEG, ".svg": ContentTypeSVG,
}

func isImageType(t UploadContentType) bool {
	return t == ContentTypePNG || t == ContentTypeJPEG || t == ContentTypeSVG
}

func isUploadType(t UploadContentType) bool {
	return t == ContentTypePDF || t == ContentTypeHTML || isImageType(t)
}

// keyOrNew returns the trimmed caller key, or a fresh one. A key must be 1-255 printable ASCII characters and not
// blank.
func keyOrNew(key string, given bool) (string, error) {
	if !given {
		return NewIdempotencyKey(), nil
	}
	trimmed := ""
	if idempotencyForm.MatchString(key) {
		trimmed = strings.TrimSpace(key)
	}
	if trimmed == "" {
		return "", configError("idempotency key must be 1-255 printable ASCII characters and not blank")
	}
	return trimmed, nil
}

func (o *callOptions) idempotencyKey() (string, error) {
	return keyOrNew(o.key, contains(o.used, "WithIdempotencyKey"))
}

func contains(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// derivedKey is the key of a companion request of one logical call (the upload before a job), within the limit.
func derivedKey(key, purpose string) string {
	if len(key) > 240 {
		key = key[:240]
	}
	return key + ":" + purpose
}

func checkWait(o *callOptions) error {
	if contains(o.used, "WithWait") && (o.wait < 1 || o.wait > 900) {
		return configError("wait must be a whole number of seconds from 1 to 900")
	}
	return nil
}

func checkLanguage(o *callOptions) error {
	if contains(o.used, "WithExplicitLanguage") && !languageTag.MatchString(o.language) {
		return configError("explicit language must be a well-formed BCP 47 tag")
	}
	return nil
}

func printableText(s string, max int) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > max || strings.ContainsFunc(trimmed, unicode.IsControl) {
		return "", false
	}
	return trimmed, true
}

// checkTitle returns the trimmed title when one was given: 1-500 printable characters counted as code points.
func checkTitle(o *callOptions) (string, error) {
	if !contains(o.used, "WithExplicitTitle") {
		return "", nil
	}
	title, ok := printableText(o.title, 500)
	if !ok {
		return "", configError("explicit title must be 1-500 printable characters")
	}
	return title, nil
}

func checkSelector(o *callOptions) (string, error) {
	if !contains(o.used, "WithContentSelector") {
		return "", nil
	}
	selector, ok := printableText(o.selector, 200)
	if !ok {
		return "", configError("content selector must be 1-200 printable characters (a CSS selector)")
	}
	return selector, nil
}

func checkReviewer(reviewer string) (string, error) {
	if reviewer == "" {
		return "", nil
	}
	trimmed, ok := printableText(reviewer, 200)
	if !ok {
		return "", configError("reviewer must be 1-200 printable characters")
	}
	return trimmed, nil
}

func checkMode(mode string, allowed ...string) error {
	for _, a := range allowed {
		if mode == a {
			return nil
		}
	}
	return configError("mode must be one of %s", strings.Join(allowed, ", "))
}

func checkScanURL(target string) error {
	if !httpURLForm.MatchString(target) {
		return configError("url must be an absolute http(s) URL")
	}
	return nil
}

func checkOrigin(origin string) error {
	if !originForm.MatchString(origin) {
		return configError("allowed origin must be an absolute http(s) origin without a path")
	}
	return nil
}

func jobPath(id string) (string, error) {
	if !uuidForm.MatchString(id) {
		return "", configError("job id must be a UUID")
	}
	return strings.ToLower(id), nil
}

func checkUpload(contentType UploadContentType, fileName string) error {
	if !isUploadType(contentType) {
		return configError("content type must be one of application/pdf, text/html, image/png, image/jpeg, image/svg+xml")
	}
	if n := utf8.RuneCountInString(fileName); n < 1 || n > 255 {
		return configError("file name must be 1-255 characters")
	}
	return nil
}

// nameOf is the base name of a reader that knows its name (an *os.File), else the default.
func nameOf(r io.Reader, fallback string) string {
	if named, ok := r.(interface{ Name() string }); ok {
		if base := filepath.Base(named.Name()); base != "" && base != "." && base != string(os.PathSeparator) {
			return base
		}
	}
	return fallback
}

func fileNameOption(o *callOptions, r io.Reader, fallback string) string {
	if o.fileName != "" {
		return o.fileName
	}
	return nameOf(r, fallback)
}

// imageTypeOf is the explicit type, else the one the file name's extension names.
func imageTypeOf(o *callOptions, r io.Reader) (UploadContentType, error) {
	chosen := o.contentType
	if chosen == "" {
		for _, name := range []string{o.fileName, nameOf(r, "")} {
			if name == "" {
				continue
			}
			if t, ok := imageExtensions[strings.ToLower(filepath.Ext(name))]; ok {
				chosen = t
				break
			}
		}
	}
	if !isImageType(chosen) {
		return "", configError("image content type must be one of image/png, image/jpeg, image/svg+xml")
	}
	return chosen, nil
}

func readSource(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, configError("reader is nil")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, configError("the file is empty")
	}
	if len(data) > maxReadBytes {
		return nil, configError("the file exceeds the SDK limit of %d bytes", maxReadBytes)
	}
	return data, nil
}
