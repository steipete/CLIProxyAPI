package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func BenchmarkFileRequestLoggerConcurrentErrorCleanup(b *testing.B) {
	logsDir := b.TempDir()
	for i := range 1000 {
		name := filepath.Join(logsDir, fmt.Sprintf("error-v1-messages-%04d.log", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			b.Fatalf("write fixture: %v", err)
		}
	}
	logger := NewFileRequestLogger(false, logsDir, "", 1000)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := logger.cleanupOldErrorLogs(); err != nil {
				b.Errorf("cleanupOldErrorLogs: %v", err)
			}
		}
	})
	b.StopTimer()
	deadline := time.Now().Add(2 * time.Second)
	for {
		logger.errorCleanupMu.Lock()
		running := logger.errorCleanupRunning
		logger.errorCleanupMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			b.Fatal("error cleanup did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFileRequestLoggerConcurrentErrorCleanupKeepsNewestFiles(t *testing.T) {
	logsDir := t.TempDir()
	for i := range 100 {
		name := filepath.Join(logsDir, fmt.Sprintf("error-v1-messages-%04d.log", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	logger := NewFileRequestLogger(false, logsDir, "", 10)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := logger.cleanupOldErrorLogs(); err != nil {
				t.Errorf("cleanupOldErrorLogs: %v", err)
			}
		}()
	}
	wg.Wait()
	waitForErrorCleanupIdle(t, logger)
	assertErrorLogCount(t, logsDir, 10)
}

func TestFileRequestLoggerConcurrentErrorCleanupRescansLateFiles(t *testing.T) {
	logsDir := t.TempDir()
	for i := range 2000 {
		name := filepath.Join(logsDir, fmt.Sprintf("error-initial-%04d.log", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatalf("write initial fixture: %v", err)
		}
	}
	logger := NewFileRequestLogger(false, logsDir, "", 10)

	done := make(chan error, 1)
	go func() { done <- logger.cleanupOldErrorLogs() }()
	deadline := time.Now().Add(time.Second)
	for {
		logger.errorCleanupMu.Lock()
		running := logger.errorCleanupRunning
		logger.errorCleanupMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not start")
		}
		time.Sleep(time.Millisecond)
	}

	for i := range 100 {
		name := filepath.Join(logsDir, fmt.Sprintf("error-late-%04d.log", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatalf("write late fixture: %v", err)
		}
		if err := logger.cleanupOldErrorLogs(); err != nil {
			t.Fatalf("queue late cleanup: %v", err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("cleanupOldErrorLogs: %v", err)
	}
	waitForErrorCleanupIdle(t, logger)
	assertErrorLogCount(t, logsDir, 10)
}

func waitForErrorCleanupIdle(t *testing.T, logger *FileRequestLogger) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		logger.errorCleanupMu.Lock()
		running := logger.errorCleanupRunning
		logger.errorCleanupMu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("error cleanup did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertErrorLogCount(t *testing.T, logsDir string, maxFiles int) {
	t.Helper()
	entries, errRead := os.ReadDir(logsDir)
	if errRead != nil {
		t.Fatalf("read logs: %v", errRead)
	}
	if len(entries) > maxFiles {
		t.Fatalf("retained error logs = %d, want at most %d", len(entries), maxFiles)
	}
}
