package providers

import (
	"encoding/json"
	"testing"
)

func TestMapResponse(t *testing.T) {
	resp := []byte(`{
		"code": 200,
		"data": {
			"webPages": {
				"value": [
					{"name": "Java 官网", "url": "https://java.com"}
				]
			}
		}
	}`)

	mapping := map[string]string{
		"results": "$.data.webPages.value",
	}

	p := &HTTPProvider{}
	mapped, err := p.mapResponse(resp, mapping)
	if err != nil {
		t.Fatalf("mapResponse failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(mapped), &result); err != nil {
		t.Fatalf("mapped result is not valid JSON: %v", err)
	}

	results, ok := result["results"].([]interface{})
	if !ok {
		t.Fatalf("expected results to be array, got %T", result["results"])
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestSanitizeURL(t *testing.T) {
	got := sanitizeURL(" `https://api.tavily.com/search` ")
	if got != "https://api.tavily.com/search" {
		t.Fatalf("expected sanitized URL, got %q", got)
	}
}

func TestParseJSONPath(t *testing.T) {
	parts := parseJSONPath("data.webPages.value[0].name")
	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d: %+v", len(parts), parts)
	}
	expected := []jsonPathPart{
		{Key: "data"},
		{Key: "webPages"},
		{Key: "value"},
		{Index: 0},
		{Key: "name"},
	}
	for i, e := range expected {
		if parts[i].Key != e.Key || parts[i].Index != e.Index {
			t.Errorf("part %d mismatch: expected %+v, got %+v", i, e, parts[i])
		}
	}
}
