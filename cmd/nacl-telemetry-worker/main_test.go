package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTelemetryParsing(t *testing.T) {
	payload := `{
		"execution_id": "test-execution-123",
		"timestamp_utc": "2026-06-15T12:00:00Z",
		"nacl_engine_version": "v1.4.2",
		"environment": "staging",
		"lineage_epoch_hash": "abc123hash",
		"tenant_id": "tenant-xyz",
		"compiler_metrics": {
			"frontend_parse_us": 100,
			"topological_sort_us": 200,
			"ddl_generation_us": 300,
			"total_compilation_us": 600
		},
		"blast_radius": {
			"risk_level": "High",
			"risk_score": 4.5,
			"mutated_tables_count": 3,
			"heavy_rewrites_detected": true,
			"infrastructure_exclusive_locks": ["table_users", "table_orders"]
		}
	}`

	var event TelemetryEvent
	err := json.Unmarshal([]byte(payload), &event)
	if err != nil {
		t.Fatalf("Failed to parse valid telemetry JSON: %v", err)
	}

	if event.ExecutionID != "test-execution-123" {
		t.Errorf("Expected ExecutionID 'test-execution-123', got '%s'", event.ExecutionID)
	}
	if event.TenantID != "tenant-xyz" {
		t.Errorf("Expected TenantID 'tenant-xyz', got '%s'", event.TenantID)
	}
	if event.CompilerMetrics == nil {
		t.Fatal("CompilerMetrics was nil")
	}
	if event.CompilerMetrics.TotalCompilationUs != 600 {
		t.Errorf("Expected TotalCompilationUs 600, got %d", event.CompilerMetrics.TotalCompilationUs)
	}
	if event.BlastRadius == nil {
		t.Fatal("BlastRadius was nil")
	}
	if event.BlastRadius.RiskLevel != "High" {
		t.Errorf("Expected RiskLevel 'High', got '%s'", event.BlastRadius.RiskLevel)
	}
	if event.BlastRadius.RiskScore != 4.5 {
		t.Errorf("Expected RiskScore 4.5, got %f", event.BlastRadius.RiskScore)
	}
	if event.BlastRadius.MutatedTablesCount != 3 {
		t.Errorf("Expected MutatedTablesCount 3, got %d", event.BlastRadius.MutatedTablesCount)
	}
	if !event.BlastRadius.HeavyRewritesDetected {
		t.Errorf("Expected HeavyRewritesDetected true, got false")
	}

	// Verify locks parsed
	var locks []string
	err = json.Unmarshal(event.BlastRadius.InfrastructureExclusiveLocks, &locks)
	if err != nil {
		t.Fatalf("Failed to parse locks: %v", err)
	}
	if len(locks) != 2 || locks[0] != "table_users" || locks[1] != "table_orders" {
		t.Errorf("Unexpected locks array: %v", locks)
	}
}

func TestTimeParsingFallback(t *testing.T) {
	invalidTimeStr := "not-a-time"
	_, err := time.Parse(time.RFC3339, invalidTimeStr)
	if err == nil {
		t.Error("Expected time.Parse to fail on invalid string")
	}
}

func TestStructPoolReset(t *testing.T) {
	event := eventPool.Get().(*TelemetryEvent)
	event.ExecutionID = "dirty"
	event.CompilerMetrics.TotalCompilationUs = 999
	event.BlastRadius.RiskLevel = "dirty"

	event.Reset()

	if event.ExecutionID != "" {
		t.Errorf("Reset did not clear ExecutionID")
	}
	if event.CompilerMetrics.TotalCompilationUs != 0 {
		t.Errorf("Reset did not clear CompilerMetrics.TotalCompilationUs")
	}
	if event.BlastRadius.RiskLevel != "" {
		t.Errorf("Reset did not clear BlastRadius.RiskLevel")
	}
	eventPool.Put(event)
}

func TestVersionParsing(t *testing.T) {
	testCases := []struct {
		filename   string
		expected   int64
		shouldFail bool
	}{
		{"20260608124638_init.sql", 20260608124638, false},
		{"20260609224500_sprint5_finops.sql", 20260609224500, false},
		{"invalid_filename.sql", 0, true},
		{"not_even_numbers_init.sql", 0, true},
	}

	for _, tc := range testCases {
		parts := strings.SplitN(tc.filename, "_", 2)
		if len(parts) < 2 {
			if !tc.shouldFail {
				t.Errorf("Expected success for %s, but splitting failed", tc.filename)
			}
			continue
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			if !tc.shouldFail {
				t.Errorf("Expected success for %s, but ParseInt failed: %v", tc.filename, err)
			}
			continue
		}
		if tc.shouldFail {
			t.Errorf("Expected failure for %s, but it parsed to %d", tc.filename, version)
		} else if version != tc.expected {
			t.Errorf("Expected %d, got %d for %s", tc.expected, version, tc.filename)
		}
	}
}
