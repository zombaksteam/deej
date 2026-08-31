package deej

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyFileWriter_CreationAndWriting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "deej-log-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockTime := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	writer := &dailyFileWriter{
		dir:           tmpDir,
		retentionDays: 30,
		nowFunc:       func() time.Time { return mockTime },
	}

	if err := writer.rotateIfNeeded(); err != nil {
		t.Fatalf("rotateIfNeeded failed: %v", err)
	}
	defer writer.Close()

	expectedFile := filepath.Join(tmpDir, "2026-08-31.log")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("Expected file %s does not exist", expectedFile)
	}

	msg := "Hello deej log line\n"
	if _, err := writer.Write([]byte(msg)); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	_ = writer.Sync()

	content, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(content), msg) {
		t.Errorf("Expected log content %q, got %q", msg, string(content))
	}
}

func TestDailyFileWriter_MidnightRotation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "deej-log-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	currentMockTime := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	writer := &dailyFileWriter{
		dir:           tmpDir,
		retentionDays: 30,
		nowFunc:       func() time.Time { return currentMockTime },
	}

	if err := writer.rotateIfNeeded(); err != nil {
		t.Fatalf("rotateIfNeeded failed: %v", err)
	}
	defer writer.Close()

	// Write day 1
	if _, err := writer.Write([]byte("Day 1 log\n")); err != nil {
		t.Fatalf("Write day 1 failed: %v", err)
	}

	// Advance time to next day (past midnight)
	currentMockTime = time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)

	// Write day 2
	if _, err := writer.Write([]byte("Day 2 log\n")); err != nil {
		t.Fatalf("Write day 2 failed: %v", err)
	}
	_ = writer.Sync()

	// Verify both files exist
	day1File := filepath.Join(tmpDir, "2026-08-31.log")
	day2File := filepath.Join(tmpDir, "2026-09-01.log")

	c1, err := os.ReadFile(day1File)
	if err != nil {
		t.Fatalf("ReadFile day 1 failed: %v", err)
	}
	if !strings.Contains(string(c1), "Day 1 log") {
		t.Errorf("Day 1 file missing expected content, got %q", string(c1))
	}

	c2, err := os.ReadFile(day2File)
	if err != nil {
		t.Fatalf("ReadFile day 2 failed: %v", err)
	}
	if !strings.Contains(string(c2), "Day 2 log") {
		t.Errorf("Day 2 file missing expected content, got %q", string(c2))
	}
}

func TestDailyFileWriter_RetentionCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "deej-log-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create fake log files: one 40 days old, one 10 days old, and one today
	todayTime := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	oldLog := filepath.Join(tmpDir, "2026-07-20.log")     // 42 days old -> should be deleted
	recentLog := filepath.Join(tmpDir, "2026-08-20.log")  // 11 days old -> should be kept
	todayLog := filepath.Join(tmpDir, "2026-08-31.log")   // 0 days old -> should be kept
	unrelatedFile := filepath.Join(tmpDir, "readme.txt") // non-log -> should be kept

	_ = os.WriteFile(oldLog, []byte("old"), 0644)
	_ = os.WriteFile(recentLog, []byte("recent"), 0644)
	_ = os.WriteFile(todayLog, []byte("today"), 0644)
	_ = os.WriteFile(unrelatedFile, []byte("text"), 0644)

	writer := &dailyFileWriter{
		dir:           tmpDir,
		retentionDays: 30,
		nowFunc:       func() time.Time { return todayTime },
	}

	writer.cleanOldLogs()

	if _, err := os.Stat(oldLog); !os.IsNotExist(err) {
		t.Errorf("Expected old log %s to be deleted, but it still exists", oldLog)
	}
	if _, err := os.Stat(recentLog); os.IsNotExist(err) {
		t.Errorf("Expected recent log %s to be kept, but it was deleted", recentLog)
	}
	if _, err := os.Stat(todayLog); os.IsNotExist(err) {
		t.Errorf("Expected today log %s to be kept, but it was deleted", todayLog)
	}
	if _, err := os.Stat(unrelatedFile); os.IsNotExist(err) {
		t.Errorf("Expected unrelated file %s to be kept, but it was deleted", unrelatedFile)
	}
}

func TestNewLogger(t *testing.T) {
	// Test dev logger
	devLogger, err := NewLogger(buildTypeDev)
	if err != nil {
		t.Fatalf("NewLogger(dev) failed: %v", err)
	}
	devLogger.Debug("Test dev log message")

	// Test release/default logger
	prodLogger, err := NewLogger(buildTypeRelease)
	if err != nil {
		t.Fatalf("NewLogger(release) failed: %v", err)
	}
	prodLogger.Info("Test prod log message")
}
