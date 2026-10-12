package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"infoex.local/run/internal/jobs"
)

func TestRejectUnsafeArchives(t *testing.T) {
	cases := []struct {
		name string
		kind byte
	}{{"../secret", tar.TypeReg}, {"/etc/passwd", tar.TypeReg}, {"x", tar.TypeSymlink}, {"x", tar.TypeLink}, {"nested/file", tar.TypeReg}}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var b bytes.Buffer
			writer := tar.NewWriter(&b)
			writer.WriteHeader(&tar.Header{Name: item.name, Typeflag: item.kind, Mode: 0600, Linkname: "/etc/passwd"})
			writer.Close()
			if _, err := ExtractTar(&b, t.TempDir()); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestExtractRegularArtifacts(t *testing.T) {
	var b bytes.Buffer
	writer := tar.NewWriter(&b)
	data := []byte("account,value\na,100\n")
	writer.WriteHeader(&tar.Header{Name: "./result.csv", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))})
	writer.Write(data)
	writer.Close()
	root := t.TempDir()
	result, err := ExtractTar(&b, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].SHA256 != jobs.Hash(data) {
		t.Fatal(result)
	}
	got, _ := os.ReadFile(filepath.Join(root, "result.csv"))
	if !bytes.Equal(got, data) {
		t.Fatal("wrong bytes")
	}
}
func TestBoundedOutput(t *testing.T) {
	writer := &boundedWriter{limit: 100}
	count, err := writer.Write([]byte(strings.Repeat("x", 10000)))
	if err != nil || count != 10000 || writer.buffer.Len() != 100 {
		t.Fatal(count, err)
	}
}

func TestLimitSummaryPreservesUTF8AndBound(t *testing.T) {
	summary := strings.Repeat("é", jobs.MaxSummaryBytes)
	limited := limitSummary(summary)
	if len(limited) > jobs.MaxSummaryBytes || !utf8.ValidString(limited) {
		t.Fatalf("invalid summary limit: %d bytes", len(limited))
	}
}

// Explicit opt-in. Requires Docker daemon on this host, runsc and built image.
// This is not executed by the routine suite.
func TestRunscIntegration(t *testing.T) {
	if os.Getenv("INFOEX_RUN_INTEGRATION") != "1" {
		t.Skip("real runsc not requested")
	}
	root := t.TempDir()
	executor := &Docker{Root: root, Image: os.Getenv("INFOEX_RUN_IMAGE")}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := executor.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	code := `from pathlib import Path
import socket
assert not Path('/var/run/docker.sock').exists()
assert not Path('/input/.env').exists()
try:
    socket.create_connection(('1.1.1.1',443), timeout=1)
    raise AssertionError('Network was available')
except OSError:
    pass
try:
    Path('/input/injected.txt').write_text('x')
    raise AssertionError('Read-only input was writable')
except OSError:
    pass
Path('/workspace/result.csv').write_text('account,value\na,100\n')
print('isolation smoke passed')`
	job := jobs.Job{ID: strings.Repeat("a", 64), TenantID: "test", Request: jobs.Request{Code: code, TimeoutSeconds: 30}}
	result, err := executor.Run(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 {
		t.Fatal(result)
	}
	// Timeout and cleanup are mandatory as well.
	limited, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	job.ID = strings.Repeat("b", 64)
	job.Request.Code = "while True: pass"
	if _, err = executor.Run(limited, job); err == nil {
		t.Fatal("timeout not enforced")
	}
}
