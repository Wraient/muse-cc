package store

import (
	"context"
	"testing"
	"time"
)

func TestRequestStatsIncludeCacheTokens(t *testing.T) {
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now()
	row := &RequestRow{
		Ts:                       now,
		Method:                   "POST",
		Path:                     "/v1/messages",
		IncomingModel:            "client-model",
		TargetModel:              "target-model",
		Status:                   200,
		InputTokens:              100,
		OutputTokens:             20,
		CachedInputTokens:        70,
		CacheCreationInputTokens: 15,
	}
	if err := st.InsertRequest(context.Background(), row); err != nil {
		t.Fatalf("insert request: %v", err)
	}

	rows, err := st.ListRequests(context.Background(), 10)
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(rows) != 1 ||
		rows[0].CachedInputTokens != 70 ||
		rows[0].CacheCreationInputTokens != 15 {
		t.Fatalf("request rows = %+v", rows)
	}

	summary, err := st.Summary(context.Background())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.TotalCachedInputTok != 70 || summary.TotalCacheCreationInputTok != 15 {
		t.Fatalf("summary = %+v", summary)
	}

	hourly, err := st.HourlySeries(context.Background(), 1)
	if err != nil {
		t.Fatalf("hourly: %v", err)
	}
	if len(hourly) != 1 ||
		hourly[0].CachedInputTokens != 70 ||
		hourly[0].CacheCreationInputTokens != 15 {
		t.Fatalf("hourly = %+v", hourly)
	}

	models, err := st.ModelUsage(context.Background(), now.Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatalf("model usage: %v", err)
	}
	if len(models) != 1 || models[0].CachedInputTokens != 70 {
		t.Fatalf("models = %+v", models)
	}
}
