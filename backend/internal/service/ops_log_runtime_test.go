package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestDefaultOpsRuntimeLogConfig_ZeroValueConfigKeepsBaseline(t *testing.T) {
	cfg := &config.Config{}

	got := defaultOpsRuntimeLogConfig(cfg)

	if got.Level != "info" {
		t.Fatalf("expected level info, got %q", got.Level)
	}
	if got.EnableSampling {
		t.Fatalf("expected enable_sampling false, got true")
	}
	if got.SamplingInitial != 100 {
		t.Fatalf("expected sampling_initial 100, got %d", got.SamplingInitial)
	}
	if got.SamplingNext != 100 {
		t.Fatalf("expected sampling_thereafter 100, got %d", got.SamplingNext)
	}
	if !got.Caller {
		t.Fatalf("expected caller true, got false")
	}
	if got.StacktraceLevel != "error" {
		t.Fatalf("expected stacktrace_level error, got %q", got.StacktraceLevel)
	}
	if got.RetentionDays != 30 {
		t.Fatalf("expected retention_days 30, got %d", got.RetentionDays)
	}
}

func TestDefaultOpsRuntimeLogConfig_UsesConfiguredValues(t *testing.T) {
	cfg := &config.Config{}
	cfg.Log.Level = "WARN"
	cfg.Log.Sampling.Enabled = true
	cfg.Log.Sampling.Initial = 17
	cfg.Log.Sampling.Thereafter = 9
	cfg.Log.Caller = false
	cfg.Log.StacktraceLevel = "fatal"
	cfg.Log.Rotation.MaxAgeDays = 45

	got := defaultOpsRuntimeLogConfig(cfg)

	if got.Level != "warn" {
		t.Fatalf("expected level warn, got %q", got.Level)
	}
	if !got.EnableSampling {
		t.Fatalf("expected enable_sampling true, got false")
	}
	if got.SamplingInitial != 17 {
		t.Fatalf("expected sampling_initial 17, got %d", got.SamplingInitial)
	}
	if got.SamplingNext != 9 {
		t.Fatalf("expected sampling_thereafter 9, got %d", got.SamplingNext)
	}
	if got.Caller {
		t.Fatalf("expected caller false, got true")
	}
	if got.StacktraceLevel != "fatal" {
		t.Fatalf("expected stacktrace_level fatal, got %q", got.StacktraceLevel)
	}
	if got.RetentionDays != 45 {
		t.Fatalf("expected retention_days 45, got %d", got.RetentionDays)
	}
}
