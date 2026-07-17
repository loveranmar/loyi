package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loveranmar/loyi/internal/provider"
)

func TestStream(t *testing.T) {
	var path, auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ship \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"it\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := &Client{ID: "custom", BaseURL: srv.URL, APIKey: "key-1", Model: "m-1"}
	ch, err := c.Stream(context.Background(), provider.Request{
		System:   "sys",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "go"}},
		Effort:   provider.EffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		sb.WriteString(chunk.Text)
	}
	if sb.String() != "ship it" {
		t.Errorf("text = %q", sb.String())
	}
	if path != "/chat/completions" || auth != "Bearer key-1" {
		t.Errorf("path=%q auth=%q", path, auth)
	}
	if body["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", msgs)
	}
}
