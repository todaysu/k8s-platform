package reports

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/k8s-platform/internal/cluster"
)

func reportCipher(t *testing.T) cluster.CredentialCipher {
	t.Helper()
	value, err := cluster.NewAESGCMCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestModelKeyIsEncryptedAndAIReceivesOnlyBoundedEvidence(t *testing.T) {
	db := openReportDB(t, "reports-ai")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}

	var receivedAuthorization, receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedAuthorization = request.Header.Get("Authorization")
		body, _ := io.ReadAll(request.Body)
		receivedBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"{\"summary\":\"需关注调度失败\",\"observations\":[\"本周 Warning 有重复发生\"],\"actions\":[\"核对调度约束\"],\"limitations\":[\"仅基于聚合数据\"]}"}}]}`)
	}))
	defer server.Close()

	saved, err := service.SaveModelConfig(context.Background(), ModelConfig{Endpoint: server.URL, Model: "internal-model", APIKey: "top-secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.HasAPIKey || saved.APIKey != "" {
		t.Fatalf("saved config leaked key: %+v", saved)
	}
	var encrypted string
	if err := db.QueryRow(`SELECT encrypted_key FROM report_model_config WHERE id=1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if encrypted == "top-secret-key" || strings.Contains(encrypted, "top-secret-key") {
		t.Fatal("model key was stored as plaintext")
	}
	config, encrypted, err := service.loadModelConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clusters := make([]ClusterSummary, 30)
	for i := range clusters {
		clusters[i] = ClusterSummary{Name: "cluster"}
	}
	analysis, err := service.analyze(context.Background(), config, encrypted, Evidence{Clusters: clusters, Warning: 3}, Window{Label: "周报"})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Summary == "" || receivedAuthorization != "Bearer top-secret-key" {
		t.Fatalf("analysis/auth = %+v / %q", analysis, receivedAuthorization)
	}
	if strings.Contains(receivedBody, "top-secret-key") {
		t.Fatal("model key was included in the request body")
	}
	var requestPayload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(receivedBody), &requestPayload); err != nil {
		t.Fatal(err)
	}
	if len(requestPayload.Messages) != 2 || strings.Count(requestPayload.Messages[1].Content, `"name":"cluster"`) != 20 {
		t.Fatal("AI evidence was not bounded to 20 cluster summaries")
	}
}
