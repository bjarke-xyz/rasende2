package config

import (
	"testing"
	"time"
)

func TestConfiguredBuildTimestampUsesBuildValue(t *testing.T) {
	t.Setenv("BUILD_TIME", "")
	previous := buildTimestamp
	t.Cleanup(func() { buildTimestamp = previous })
	buildTimestamp = "2026-10-25 10:00:00"

	got := configuredBuildTimestamp()
	want := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("configuredBuildTimestamp() = %v, want %v", got, want)
	}
}

func TestConfiguredBuildTimestampEnvironmentOverridesBuildValue(t *testing.T) {
	t.Setenv("BUILD_TIME", "2026-10-25 11:00:00")
	previous := buildTimestamp
	t.Cleanup(func() { buildTimestamp = previous })
	buildTimestamp = "2026-10-25 10:00:00"

	got := configuredBuildTimestamp()
	want := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("configuredBuildTimestamp() = %v, want %v", got, want)
	}
}
