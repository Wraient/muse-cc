package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsHandlerIsOpenAIAndAnthropicCompatible(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{{
				"id":      "muse-spark-1.3-contributor",
				"object":  "model",
				"created": 123,
			}},
		})
	}))
	defer upstream.Close()

	handler := ModelsHandler(http.DefaultClient, func() string { return upstream.URL }, func() string { return "key" })
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object  string `json:"object"`
		HasMore bool   `json:"has_more"`
		Data    []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Object    string `json:"object"`
			CreatedAt int64  `json:"created_at"`
			Created   int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Object != "list" || body.HasMore {
		t.Errorf("unexpected list metadata: %+v", body)
	}
	if len(body.Data) != 1 {
		t.Fatalf("models = %+v", body.Data)
	}
	model := body.Data[0]
	if model.ID != "muse-spark-1.3-contributor" || model.Type != "model" || model.Object != "model" ||
		model.CreatedAt != 123 || model.Created != 123 {
		t.Errorf("unexpected model: %+v", model)
	}
}
