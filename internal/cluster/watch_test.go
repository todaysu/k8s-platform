package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWatchEventsOutlivesRequestTimeout guards the bug where rest.Config.Timeout
// was applied to the watch HTTP client and silently cut every event watch.
func TestWatchEventsOutlivesRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/apis/events.k8s.io/v1/events" || request.URL.Query().Get("watch") != "true" {
			http.Error(writer, "unexpected request", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		for index := 0; index < 4; index++ {
			fmt.Fprintf(writer, `{"type":"ADDED","object":{"apiVersion":"events.k8s.io/v1","kind":"Event","metadata":{"name":"e%d","namespace":"default","uid":"u%d","resourceVersion":"%d"},"reason":"Test","message":"m","type":"Normal","reportingController":"x","reportingInstance":"y"}}`+"\n", index, index, index)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(40 * time.Millisecond)
		}
	}))
	defer server.Close()

	connector := NewKubernetesConnector(20 * time.Millisecond)
	received := 0
	err := connector.WatchEvents(context.Background(), ParsedKubeconfig{APIServer: server.URL}, "events.k8s.io/v1", "", func(ClusterEvent) error {
		received++
		return nil
	})
	if err != nil && !errors.Is(err, ErrEventWatchClosed) {
		t.Fatalf("watch failed: %v", err)
	}
	if received < 3 {
		t.Fatalf("watch was cut after %d events; request timeout leaked into the watch client", received)
	}
}
