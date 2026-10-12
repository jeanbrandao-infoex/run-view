package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"infoex.local/run/internal/jobs"
)

type Result struct {
	Outputs []jobs.Output
	Summary string
}
type Runner interface {
	Run(context.Context, jobs.Job) (Result, error)
}
type Docker struct {
	Root  string
	Image string
}

var ErrCleanup = errors.New("sandbox_cleanup_failed")
var imagePattern = regexp.MustCompile(`^(sha256:[a-f0-9]{64}|[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64})$`)

// boundedWriter discards excess stdout without blocking or accumulating memory.
type boundedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *boundedWriter) Write(p []byte) (int, error) {
	length := len(p)
	remaining := writer.limit - writer.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		writer.buffer.Write(p)
	}
	return length, nil
}

func command(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output := &boundedWriter{limit: jobs.MaxLogBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	return output.buffer.String(), err
}
func (docker *Docker) Ready(ctx context.Context) error {
	if !imagePattern.MatchString(docker.Image) {
		return errors.New("sandbox image must be a digest or sha256 image ID")
	}
	if strings.ContainsAny(docker.Root, ",\n\r") {
		return errors.New("invalid spool path")
	}
	output, err := command(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return errors.New("docker unavailable")
	}
	if !strings.Contains(output, `"runsc"`) {
		return errors.New("runsc is mandatory")
	}
	if _, err = command(ctx, "image", "inspect", docker.Image); err != nil {
		return errors.New("sandbox image must already be present")
	}
	return nil
}
func (docker *Docker) Cleanup(ctx context.Context) error {
	ids, err := command(ctx, "ps", "-aq", "--filter", "label=infoex.run.managed=true")
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(ids) {
		if _, err = command(ctx, "rm", "-f", id); err != nil {
			return err
		}
	}
	return nil
}
func (docker *Docker) Run(ctx context.Context, job jobs.Job) (result Result, runErr error) {
	if err := job.Request.Validate(); err != nil {
		return Result{}, err
	}
	dir := filepath.Join(docker.Root, job.ID)
	input, output, err := prepareStaging(dir, job.Request)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(input)
	name := "infoex-run-" + job.ID
	// Always force-remove even if create/start is interrupted. Docker daemon outlives CLI.
	created := false
	defer func() {
		if !created {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := command(cleanup, "rm", "-f", name); err != nil {
			result = Result{}
			runErr = ErrCleanup
		}
	}()
	args := []string{"create", "--name", name, "--label", "infoex.run.managed=true", "--runtime=runsc", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65532:65532", "--cpus=1", "--memory=512m", "--memory-swap=512m", "--pids-limit=64", "--ulimit=nofile=256:256", "--ulimit=fsize=134217728:134217728", "--log-driver=none", "--tmpfs=/workspace:rw,noexec,nosuid,nodev,size=134217728,mode=1777", "--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=33554432,mode=1777", "--shm-size=16m", "--mount", "type=bind,src=" + input + ",dst=/input,readonly", "--workdir=/workspace", "--env=HOME=/tmp", "--env=TMPDIR=/tmp", "--env=OPENBLAS_NUM_THREADS=1", "--env=OMP_NUM_THREADS=1", "--entrypoint=python3", docker.Image, "-I", "-B", "/opt/runner.py", "/input/main.py"}
	// Mark before create: a cancelled CLI can leave a container in the daemon.
	created = true
	if _, err := command(ctx, args...); err != nil {
		return Result{}, errors.New("sandbox_create_failed")
	}
	if _, err := command(ctx, "start", name); err != nil {
		return Result{}, errors.New("sandbox_execution_failed")
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-ticker.C:
		}
		status, err := command(ctx, "exec", name, "python3", "-I", "-c", "from pathlib import Path; p=Path('/tmp/status.json'); print(p.read_text() if p.exists() else '{}')")
		if err != nil {
			return Result{}, errors.New("sandbox_unavailable")
		}
		var state struct {
			Done    bool   `json:"done"`
			Exit    int    `json:"exit"`
			Summary string `json:"summary"`
		}
		if err = json.Unmarshal([]byte(status), &state); err != nil {
			return Result{}, errors.New("invalid sandbox status")
		}
		if !state.Done {
			continue
		}
		if state.Exit != 0 {
			return Result{}, errors.New("analysis_failed")
		}
		// Collect while harness is still alive: tmpfs is lost when container stops.
		return docker.collect(ctx, name, output, limitSummary(state.Summary))
	}
}

func prepareStaging(dir string, request jobs.Request) (input, output string, err error) {
	input = filepath.Join(dir, "input")
	output = filepath.Join(dir, "output")
	if err := os.MkdirAll(input, 0755); err != nil {
		return "", "", err
	}
	if err := os.Chmod(input, 0755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(output, 0700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(input, "main.py"), []byte(request.Code), 0444); err != nil {
		return "", "", err
	}
	for _, file := range request.Inputs {
		data, err := base64.StdEncoding.Strict().DecodeString(file.Data)
		if err != nil {
			return "", "", err
		}
		if err = os.WriteFile(filepath.Join(input, file.Name), data, 0444); err != nil {
			return "", "", err
		}
	}
	return input, output, nil
}

func limitSummary(summary string) string {
	summary = strings.ToValidUTF8(summary, "�")
	if len(summary) <= jobs.MaxSummaryBytes {
		return summary
	}
	limit := jobs.MaxSummaryBytes
	for !utf8.ValidString(summary[:limit]) {
		limit--
	}
	return summary[:limit]
}

func (docker *Docker) collect(ctx context.Context, name, output, summary string) (Result, error) {
	cmd := exec.CommandContext(ctx, "docker", "cp", name+":/workspace/.", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	discard := &boundedWriter{limit: 4096}
	cmd.Stderr = discard
	if err = cmd.Start(); err != nil {
		return Result{}, err
	}
	result, extractErr := ExtractTar(stdout, output)
	if extractErr != nil {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if extractErr != nil {
		return Result{}, extractErr
	}
	if waitErr != nil {
		return Result{}, errors.New("output_collection_failed")
	}
	result.Summary = summary
	return result, nil
}

// ExtractTar accepts only plain files in a flat output namespace. No symlinks,
// hard links, devices, traversal, duplicate names or unbounded archive output.
func ExtractTar(reader io.Reader, output string) (Result, error) {
	archive := tar.NewReader(io.LimitReader(reader, jobs.MaxOutputBytes+(2<<20)))
	total := int64(0)
	result := Result{Outputs: []jobs.Output{}}
	names := map[string]bool{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir && (name == "" || name == "." || name == "workspace/" || name == "workspace") {
			continue
		}
		// Docker archive cp may prepend the source directory.
		name = strings.TrimPrefix(name, "workspace/")
		if header.Typeflag != tar.TypeReg || !jobs.ValidOutput(name) || names[name] || len(names) >= jobs.MaxOutputs || header.Size < 0 {
			return Result{}, errors.New("unsafe output archive")
		}
		total += header.Size
		if total > jobs.MaxOutputBytes {
			return Result{}, errors.New("output size exceeded")
		}
		names[name] = true
		data, err := io.ReadAll(io.LimitReader(archive, header.Size))
		if err != nil {
			return Result{}, err
		}
		if int64(len(data)) != header.Size {
			return Result{}, errors.New("incomplete output")
		}
		if err = os.WriteFile(filepath.Join(output, name), data, 0600); err != nil {
			return Result{}, err
		}
		result.Outputs = append(result.Outputs, jobs.Output{Name: name, Size: header.Size, SHA256: jobs.Hash(data)})
	}
	return result, nil
}
