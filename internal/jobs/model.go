package jobs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("job not found")
var ErrConflict = errors.New("idempotency conflict")
var ErrCapacity = errors.New("queue capacity reached")
var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$`)
var safeFile = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,119}$`)

const MaxInputBytes = 24 << 20
const MaxOutputBytes = 128 << 20
const MaxLogBytes = 64 << 10
const MaxSummaryBytes = 8 << 10
const MaxInputs = 20
const MaxOutputs = 20
const DefaultTimeoutSeconds = 120
const MaxTimeoutSeconds = 300

type Input struct {
	Name string `json:"name"`
	Data string `json:"data_base64"`
}
type Request struct {
	Code           string  `json:"code"`
	Inputs         []Input `json:"inputs"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}
type Output struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Job struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	RequestHash string    `json:"request_hash"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Request     Request   `json:"request"`
	Outputs     []Output  `json:"outputs"`
	Summary     string    `json:"summary,omitempty"`
	Error       string    `json:"error,omitempty"`
}

func ValidID(s string) bool { return safeID.MatchString(s) }
func ValidOutput(s string) bool {
	return safeFile.MatchString(s) && !strings.HasPrefix(s, ".") && s != "main.py"
}
func Terminal(s string) bool { return s == "completed" || s == "failed" || s == "cancelled" }
func Hash(b []byte) string   { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func (request *Request) Validate() error {
	if len(request.Code) == 0 || len(request.Code) > 65536 || strings.ContainsRune(request.Code, 0) {
		return errors.New("invalid code size")
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > MaxTimeoutSeconds {
		return errors.New("timeout must be 1..300 seconds")
	}
	if len(request.Inputs) > MaxInputs {
		return errors.New("too many inputs")
	}
	names := map[string]bool{}
	total := 0
	for _, input := range request.Inputs {
		if !ValidOutput(input.Name) || names[input.Name] {
			return errors.New("invalid or duplicate input name")
		}
		names[input.Name] = true
		data, err := base64.StdEncoding.Strict().DecodeString(input.Data)
		if err != nil {
			return errors.New("invalid input encoding")
		}
		total += len(data)
		if total > MaxInputBytes {
			return errors.New("input size exceeded")
		}
	}
	return nil
}
