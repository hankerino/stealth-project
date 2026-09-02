package main

// Workload executor (Phase 4). The agent stays stdlib-only — no Kafka
// client — so instead of consuming `node-jobs` it polls the settlement
// job control plane over HTTP and reports signed status transitions:
//
//	poll   GET  {JOBS_URL}/v1/jobs/poll?node_id=&limit=
//	report POST {JOBS_URL}/v1/jobs/{id}/status   (Ed25519-signed)
//
// Status signature (v1): base64 Ed25519 over
// sha256(node_id || job_id || status || decimal(sent_at_unix_ms)) — the
// heartbeat scheme extended with job_id+status (must match settlement's
// verification byte-for-byte).
//
// EXECUTOR_MODE=mock (default) simulates the run: report started, sleep
// the spec's mock_duration_seconds (or MOCK_JOB_DURATION), report
// completed — works with zero docker/GPU. EXECUTOR_MODE=docker runs
// `docker run --rm <image> <cmd...>` when the spec carries an image AND a
// docker CLI exists; anything missing falls back to mock with a log line.
// Docker is never a hard dependency.
//
// Idempotency: in-process dedupe (a job is executed once per agent run)
// plus server-side atomic transitions — a duplicate report is a clean
// 409, treated as "already handled".

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// workloadSpec is the jobs.workload JSONB payload (draft shape).
type workloadSpec struct {
	Image               string   `json:"image"`
	Command             []string `json:"command"`
	MockDurationSeconds int64    `json:"mock_duration_seconds"`
}

// agentJob mirrors one element of the poll response.
type agentJob struct {
	JobID    string          `json:"job_id"`
	TradeID  string          `json:"trade_id"`
	Symbol   string          `json:"symbol"`
	Workload json.RawMessage `json:"workload"`
}

func (j agentJob) spec() workloadSpec {
	var s workloadSpec
	if len(j.Workload) > 0 {
		if err := json.Unmarshal(j.Workload, &s); err != nil {
			log.Printf("job %s: bad workload spec (%v); using defaults", j.JobID, err)
		}
	}
	return s
}

// jobStatusMessage is the signed byte string (must match settlement's).
func jobStatusMessage(nodeID, jobID, status string, sentAtUnixMs int64) []byte {
	return []byte(nodeID + jobID + status + strconv.FormatInt(sentAtUnixMs, 10))
}

type jobClient struct {
	base   string
	signer *Signer
	client *http.Client
}

func newJobClient(jobsURL string, signer *Signer) *jobClient {
	return &jobClient{
		base:   strings.TrimRight(jobsURL, "/"),
		signer: signer,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *jobClient) poll(ctx context.Context, nodeID string, limit int) ([]agentJob, error) {
	url := fmt.Sprintf("%s/v1/jobs/poll?node_id=%s&limit=%d", c.base, nodeID, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("poll: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Jobs []agentJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("poll: decode: %w", err)
	}
	return out.Jobs, nil
}

var errJobConflict = errors.New("job conflict (already transitioned)")

// report sends one signed status transition. A 409 means the job is no
// longer in the expected state (duplicate/out-of-order) — not an error
// the agent can fix, so it is reported back as errJobConflict.
func (c *jobClient) report(ctx context.Context, nodeID, jobID, status, reason string) error {
	sentAt := nowUnixMs()
	body, err := json.Marshal(map[string]any{
		"node_id":        nodeID,
		"status":         status,
		"sent_at_unix_ms": sentAt,
		"signature":      c.signer.SignSha256(jobStatusMessage(nodeID, jobID, status, sentAt)),
		"reason":         reason,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/v1/jobs/"+jobID+"/status", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusConflict {
		return errJobConflict
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("report %s: status %d: %s", status, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

type executor struct {
	client       *jobClient
	nodeID       string
	mode         string // mock | docker
	mockDuration time.Duration
	pollInterval time.Duration

	mu       sync.Mutex
	inFlight map[string]bool // job_ids executing in this process
}

func newExecutor(jobsURL, nodeID string, signer *Signer, mode string, mockDuration, pollInterval time.Duration) *executor {
	return &executor{
		client:       newJobClient(jobsURL, signer),
		nodeID:       nodeID,
		mode:         mode,
		mockDuration: mockDuration,
		pollInterval: pollInterval,
		inFlight:     map[string]bool{},
	}
}

// run polls for queued jobs until ctx is done. Each accepted job executes
// in its own goroutine; poll errors are logged and retried next tick.
func (e *executor) run(ctx context.Context, limit int) {
	log.Printf("executor: polling %s every %s (mode=%s, mock=%s)", e.client.base, e.pollInterval, e.mode, e.mockDuration)
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()
	for {
		e.pollOnce(ctx, limit)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *executor) pollOnce(ctx context.Context, limit int) {
	jobs, err := e.client.poll(ctx, e.nodeID, limit)
	if err != nil {
		log.Printf("executor poll: %v (retrying next tick)", err)
		return
	}
	for _, j := range jobs {
		if !e.claim(j.JobID) {
			continue // already executing in this process
		}
		go e.execute(ctx, j)
	}
}

func (e *executor) claim(jobID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inFlight[jobID] {
		return false
	}
	e.inFlight[jobID] = true
	return true
}

func (e *executor) release(jobID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.inFlight, jobID)
}

// execute runs one job: started -> (mock|docker run) -> completed|failed.
func (e *executor) execute(ctx context.Context, j agentJob) {
	defer e.release(j.JobID)
	spec := j.spec()

	if err := e.client.report(ctx, e.nodeID, j.JobID, "started", ""); err != nil {
		if !errors.Is(err, errJobConflict) {
			log.Printf("job %s: report started: %v", j.JobID, err)
		}
		return
	}
	log.Printf("job %s: STARTED (%s, image=%q mode=%s)", j.JobID, j.Symbol, spec.Image, e.mode)

	runErr := e.runWorkload(ctx, j, spec)
	if runErr != nil {
		log.Printf("job %s: execution error: %v", j.JobID, runErr)
		if err := e.client.report(ctx, e.nodeID, j.JobID, "failed", "EXECUTION_ERROR"); err != nil {
			log.Printf("job %s: report failed: %v", j.JobID, err)
		}
		return
	}

	if err := e.client.report(ctx, e.nodeID, j.JobID, "completed", ""); err != nil {
		log.Printf("job %s: report completed: %v", j.JobID, err)
		return
	}
	log.Printf("job %s: COMPLETED", j.JobID)
}

// runWorkload performs the actual (or simulated) work.
func (e *executor) runWorkload(ctx context.Context, j agentJob, spec workloadSpec) error {
	if e.mode == "docker" && spec.Image != "" {
		if path, err := exec.LookPath("docker"); err == nil {
			return runDockerJob(ctx, path, spec)
		}
		log.Printf("job %s: EXECUTOR_MODE=docker but no docker CLI; falling back to mock", j.JobID)
	}
	d := e.mockDuration
	if spec.MockDurationSeconds > 0 {
		d = time.Duration(spec.MockDurationSeconds) * time.Second
	}
	log.Printf("job %s: mock run for %s", j.JobID, d)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// runDockerJob executes the workload image. Bounded by a hard timeout so a
// wedged container cannot hold a job forever.
func runDockerJob(ctx context.Context, dockerPath string, spec workloadSpec) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	args := append([]string{"run", "--rm", spec.Image}, spec.Command...)
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run: %w: %.200s", err, out)
	}
	log.Printf("docker job finished: %.200s", out)
	return nil
}
