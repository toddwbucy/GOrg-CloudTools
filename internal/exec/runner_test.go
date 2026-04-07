package exec

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ── test helpers ──────────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	// SQLite :memory: databases are per-connection. Without this, GORM's
	// connection pool opens a second connection and sees an empty database.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(
		&models.Change{},
		&models.Tool{},
		&models.Script{},
		&models.ExecutionSession{},
		&models.ExecutionBatch{},
		&models.Execution{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// mockSSMExecutor implements RemoteExecutor with injectable behaviour.
type mockSSMExecutor struct {
	sendFn      func(ctx context.Context, instanceIDs []string, script, platform string) (string, error)
	waitForDone func(ctx context.Context, commandID, instanceID string) (*InvocationResult, error)
}

func (m *mockSSMExecutor) Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error) {
	return m.sendFn(ctx, instanceIDs, script, platform)
}

func (m *mockSSMExecutor) WaitForDone(ctx context.Context, commandID, instanceID string) (*InvocationResult, error) {
	return m.waitForDone(ctx, commandID, instanceID)
}

// successMock returns a RemoteExecutor that always succeeds.
func successMock() *mockSSMExecutor {
	return &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, _ string) (string, error) {
			return "cmd-test-ok", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			return &InvocationResult{
				Status:   "Success",
				Output:   "all good",
				ExitCode: 0,
				Done:     true,
			}, nil
		},
	}
}

// seedScript inserts a non-ephemeral script and returns it.
func seedScript(t *testing.T, db *gorm.DB) *models.Script {
	t.Helper()
	s := &models.Script{
		Name:        "test-script",
		Content:     "echo hello",
		ScriptType:  "bash",
		Interpreter: "bash",
	}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	return s
}

// baseReq returns a minimal valid ScriptRequest for a named script.
func baseReq(scriptID uint, instanceIDs ...string) ScriptRequest {
	ids := instanceIDs
	if len(ids) == 0 {
		ids = []string{"i-aabbccdd"}
	}
	return ScriptRequest{
		ScriptID:    &scriptID,
		Platform:    "linux",
		InstanceIDs: ids,
		AccountID:   "123456789012",
		Region:      "us-east-1",
	}
}

// waitForBatch polls until the batch reaches a terminal status or the test times out.
func waitForBatch(t *testing.T, db *gorm.DB, batchID uint, timeout time.Duration) models.ExecutionBatch {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var b models.ExecutionBatch
		if err := db.First(&b, batchID).Error; err != nil {
			t.Fatalf("load batch %d: %v", batchID, err)
		}
		if b.Status == models.BatchStatusCompleted || b.Status == models.BatchStatusFailed {
			return b
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("batch %d did not reach terminal status within %v", batchID, timeout)
	return models.ExecutionBatch{}
}

// ── input validation ──────────────────────────────────────────────────────────

func TestStart_BothScriptIDAndInlineScriptIsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	id := uint(1)
	_, err := r.start(context.Background(), successMock(), ScriptRequest{
		ScriptID:    &id,
		InlineScript: "echo hi",
		InstanceIDs: []string{"i-123"},
	})
	if err == nil {
		t.Fatal("expected error for both script_id and inline_script, got nil")
	}
}

func TestStart_EmptyInstanceIDsIsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)
	_, err := r.start(context.Background(), successMock(), ScriptRequest{
		ScriptID:    &s.ID,
		InstanceIDs: []string{},
	})
	if err == nil {
		t.Fatal("expected error for empty instance_ids, got nil")
	}
}

func TestStart_NeitherScriptIDNorInlineIsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	_, err := r.start(context.Background(), successMock(), ScriptRequest{
		InstanceIDs: []string{"i-123"},
	})
	if err == nil {
		t.Fatal("expected error when neither script_id nor inline_script is set, got nil")
	}
}

// ── platform validation ───────────────────────────────────────────────────────

func TestStart_InvalidPlatformReturnsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)
	_, err := r.start(context.Background(), successMock(), ScriptRequest{
		ScriptID:    &s.ID,
		Platform:    "amiga",
		InstanceIDs: []string{"i-123"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("expected unsupported platform error, got: %v", err)
	}
}

func TestStart_EmptyPlatformDefaultsToLinux(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	// Buffered channel: sendFn runs in a goroutine; the channel provides
	// explicit happens-before between the write and the test read.
	platformCh := make(chan string, 1)
	mock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, platform string) (string, error) {
			platformCh <- platform
			return "cmd-id", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			return &InvocationResult{Status: "Success", Done: true}, nil
		},
	}

	req := baseReq(s.ID)
	req.Platform = "" // empty — should default to linux
	batchID, err := r.start(context.Background(), mock, req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForBatch(t, db, batchID, 2*time.Second)
	got := <-platformCh
	if got != "linux" {
		t.Errorf("expected platform linux, got %q", got)
	}
}

func TestStart_WindowsPlatformCaseInsensitive(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	platformCh := make(chan string, 1)
	mock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, platform string) (string, error) {
			platformCh <- platform
			return "cmd-id", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			return &InvocationResult{Status: "Success", Done: true}, nil
		},
	}

	req := baseReq(s.ID)
	req.Platform = "Windows" // mixed case
	batchID, err := r.start(context.Background(), mock, req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForBatch(t, db, batchID, 2*time.Second)
	got := <-platformCh
	if got != "windows" {
		t.Errorf("expected normalised platform windows, got %q", got)
	}
}

// ── ephemeral / inline scripts ────────────────────────────────────────────────

func TestStart_InlineScriptIsPersistedAsEphemeral(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)

	batchID, err := r.start(context.Background(), successMock(), ScriptRequest{
		InlineScript: "echo ephemeral",
		Platform:     "linux",
		InstanceIDs:  []string{"i-aabb"},
		AccountID:    "123",
		Region:       "us-east-1",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForBatch(t, db, batchID, 2*time.Second)

	// The ephemeral script must exist in the DB (FK is satisfied) but must be
	// invisible to the public scripts query (ephemeral = false filter).
	var total int64
	if err := db.Model(&models.Script{}).Where("ephemeral = ?", true).Count(&total).Error; err != nil {
		t.Fatalf("counting ephemeral scripts: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 ephemeral script, found %d", total)
	}
	var publicTotal int64
	if err := db.Model(&models.Script{}).Where("ephemeral = ?", false).Count(&publicTotal).Error; err != nil {
		t.Fatalf("counting public scripts: %v", err)
	}
	if publicTotal != 0 {
		t.Errorf("expected 0 public scripts, found %d", publicTotal)
	}
}

func TestStart_InlineScriptWindowsUsesCorrectType(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)

	batchID, err := r.start(context.Background(), successMock(), ScriptRequest{
		InlineScript: "Write-Output hello",
		Platform:     "windows",
		InstanceIDs:  []string{"i-win1"},
		AccountID:    "123",
		Region:       "us-east-1",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForBatch(t, db, batchID, 2*time.Second)

	var s models.Script
	if res := db.Where("ephemeral = ?", true).First(&s); res.Error != nil {
		t.Fatalf("querying ephemeral script: %v", res.Error)
	}
	if s.ScriptType != "powershell" {
		t.Errorf("expected script_type powershell, got %q", s.ScriptType)
	}
	if s.Interpreter != "powershell" {
		t.Errorf("expected interpreter powershell, got %q", s.Interpreter)
	}
}

func TestStart_EphemeralScriptCannotBeExecutedByID(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)

	// Create an ephemeral script directly (simulates a past inline run).
	s := &models.Script{
		Name:        "_inline",
		Content:     "echo secret",
		ScriptType:  "bash",
		Interpreter: "bash",
		Ephemeral:   true,
	}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("seeding ephemeral script: %v", err)
	}

	_, err := r.start(context.Background(), successMock(), ScriptRequest{
		ScriptID:    &s.ID,
		Platform:    "linux",
		InstanceIDs: []string{"i-123"},
	})
	if err == nil {
		t.Fatal("expected error loading ephemeral script by ID, got nil")
	}
}

// ── execution outcomes ────────────────────────────────────────────────────────

func TestRun_SuccessIncrementsCompletedInstances(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 2, 30)
	s := seedScript(t, db)

	batchID, err := r.start(context.Background(), successMock(), baseReq(s.ID, "i-001", "i-002"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	batch := waitForBatch(t, db, batchID, 3*time.Second)

	if batch.CompletedInstances != 2 {
		t.Errorf("expected completed_instances=2, got %d", batch.CompletedInstances)
	}
	if batch.FailedInstances != 0 {
		t.Errorf("expected failed_instances=0, got %d", batch.FailedInstances)
	}
	if batch.Status != models.BatchStatusCompleted {
		t.Errorf("expected status completed, got %s", batch.Status)
	}
}

func TestRun_SSMSendFailureIncrementsFailedInstances(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	failMock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, _ string) (string, error) {
			return "", fmt.Errorf("ssm: no such instance")
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			t.Error("WaitForDone should not be called after Send failure")
			return nil, nil
		},
	}

	batchID, err := r.start(context.Background(), failMock, baseReq(s.ID, "i-001"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	batch := waitForBatch(t, db, batchID, 2*time.Second)

	if batch.FailedInstances != 1 {
		t.Errorf("expected failed_instances=1, got %d", batch.FailedInstances)
	}
	if batch.CompletedInstances != 0 {
		t.Errorf("expected completed_instances=0, got %d", batch.CompletedInstances)
	}
}

func TestRun_TerminalFailureIncrementsFailedInstances(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	terminalFailMock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, _ string) (string, error) {
			return "cmd-fail", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			return &InvocationResult{
				Status:   "Failed",
				ExitCode: 1,
				Done:     true,
			}, nil
		},
	}

	batchID, err := r.start(context.Background(), terminalFailMock, baseReq(s.ID, "i-001"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	batch := waitForBatch(t, db, batchID, 2*time.Second)

	if batch.FailedInstances != 1 {
		t.Errorf("expected failed_instances=1, got %d", batch.FailedInstances)
	}
	if batch.CompletedInstances != 0 {
		t.Errorf("expected completed_instances=0, got %d", batch.CompletedInstances)
	}
}

// ── worker pool ───────────────────────────────────────────────────────────────

// TestRun_WorkerPoolBoundsConc verifies that no more than maxConc SSM calls
// are in-flight simultaneously. The mock tracks concurrent active Send calls;
// WaitForDone holds each for a short period to create measurable overlap.
func TestRun_WorkerPoolBoundsConc(t *testing.T) {
	const maxConc = 2
	const numInstances = 8

	db := newTestDB(t)
	r := New(db, maxConc, 30)
	s := seedScript(t, db)

	var active atomic.Int32
	var maxSeen atomic.Int32
	var mu sync.Mutex // guards maxSeen CAS

	boundedMock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, _ string) (string, error) {
			cur := active.Add(1)
			mu.Lock()
			if int(cur) > int(maxSeen.Load()) {
				maxSeen.Store(cur)
			}
			mu.Unlock()
			return "cmd-id", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*InvocationResult, error) {
			time.Sleep(20 * time.Millisecond)
			active.Add(-1)
			return &InvocationResult{Status: "Success", Done: true}, nil
		},
	}

	ids := make([]string, numInstances)
	for i := range ids {
		ids[i] = fmt.Sprintf("i-%02d", i)
	}
	req := baseReq(s.ID, ids...)

	batchID, err := r.start(context.Background(), boundedMock, req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForBatch(t, db, batchID, 5*time.Second)

	if int(maxSeen.Load()) > maxConc {
		t.Errorf("worker pool exceeded maxConc=%d; saw %d concurrent active sends", maxConc, maxSeen.Load())
	}
	if maxSeen.Load() == 0 {
		t.Error("no sends were observed — mock may not have been called")
	}
}
