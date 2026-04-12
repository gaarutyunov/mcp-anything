//go:build e2e

package e2e_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestMain starts the shared k3s cluster before running all E2E tests and
// terminates it once all tests have completed.
func TestMain(m *testing.M) {
	ctx := context.Background()
	total := time.Now()

	slog.Info("TestMain: starting k3s cluster")

	start := time.Now()
	slog.Info("k3s: startup begin")
	k3sResult, k3sErr := startSharedK3s(ctx)
	if k3sErr != nil {
		slog.Error("k3s: startup failed", "elapsed", time.Since(start).Round(time.Millisecond), "error", k3sErr)
	} else {
		slog.Info("k3s: startup done", "elapsed", time.Since(start).Round(time.Millisecond))
	}

	slog.Info("TestMain: k3s started", "total_startup", time.Since(total).Round(time.Millisecond))

	if k3sErr != nil {
		slog.Warn("shared k3s cluster unavailable; E2E tests will be skipped", "error", k3sErr)
	} else {
		globalK3s = k3sResult
	}

	slog.Info("TestMain: running tests")
	runStart := time.Now()
	code := m.Run()
	slog.Info("TestMain: tests finished", "elapsed", time.Since(runStart).Round(time.Millisecond), "exit_code", code)

	slog.Info("TestMain: terminating shared containers")
	if globalK3s != nil {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		termStart := time.Now()
		if termErr := globalK3s.container.Terminate(termCtx); termErr != nil {
			slog.Warn("terminate shared k3s", "error", termErr, "elapsed", time.Since(termStart).Round(time.Millisecond))
		} else {
			slog.Info("k3s: terminated", "elapsed", time.Since(termStart).Round(time.Millisecond))
		}
	}

	os.Exit(code)
}
