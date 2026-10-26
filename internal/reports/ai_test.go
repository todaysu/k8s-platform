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

// Saving must refuse the values that silently break every future report: the
// stored key is never shown again, so a sentence pasted into the API Key field
// would otherwise only surface as a 401 hours later.
func TestSaveModelConfigRejectsUnusableAPIKey(t *testing.T) {
	db := openReportDB(t, "reports-key-guard")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]ModelConfig{
		"cjk sentence":  {Endpoint: "https://model.internal/v1", Model: "m", APIKey: "想在平台里的ai模型中增加一个对话的功能，"},
		"too short":     {Endpoint: "https://model.internal/v1", Model: "m", APIKey: "abc"},
		"newline":       {Endpoint: "https://model.internal/v1", Model: "m", APIKey: "sk-key\nline2"},
		"overlong":      {Endpoint: "https://model.internal/v1", Model: "m", APIKey: strings.Repeat("k", 8193)},
		"missing model": {Endpoint: "https://model.internal/v1", APIKey: "sk-valid-key-1234"},
	}
	for name, config := range cases {
		if _, err := service.SaveModelConfig(context.Background(), config); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM report_model_config WHERE id=1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected configs must not be persisted")
	}
	if _, err := service.SaveModelConfig(context.Background(), ModelConfig{Endpoint: "https://model.internal/v1", Model: "m", APIKey: "sk-valid-key-1234"}); err != nil {
		t.Fatal(err)
	}
}

func TestProbeModelReportsReusableFailureReason(t *testing.T) {
	db := openReportDB(t, "reports-probe")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":{"message":"invalid token","type":"new_api_error"}}`)
	}))
	defer server.Close()

	if err := service.ProbeModel(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m", APIKey: "typed-key"}); err == nil {
		t.Fatal("expected probe failure")
	} else if message := ModelFailureMessage(err); !strings.Contains(message, "API Key 无效或已过期") || !strings.Contains(message, "invalid token") {
		t.Fatalf("probe reason not actionable: %s", message)
	}
	if seen != "Bearer typed-key" {
		t.Fatalf("probe ignored the unsaved key, sent %q", seen)
	}
	if err := service.ProbeModel(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m", APIKey: "想在平台里增加一个对话功能"}); err == nil {
		t.Fatal("probe must not send a sentence as a credential")
	}
	if err := service.ProbeModel(context.Background(), ModelConfig{Model: "m"}); err == nil {
		t.Fatal("probe requires an endpoint")
	}
}

func TestProbeModelFallsBackToStoredKey(t *testing.T) {
	db := openReportDB(t, "reports-probe-stored")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()
	if _, err := service.SaveModelConfig(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m", APIKey: "stored-key-1234"}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProbeModel(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer stored-key-1234" {
		t.Fatalf("expected stored key, sent %q", seen)
	}
}

// A reasoning model bills its thinking trace against max_tokens, so a budget
// sized for the JSON alone comes back with an empty body and finish_reason
// "length". Reporting that as "invalid JSON" sends operators after the wrong bug.
func TestAnalyzeExplainsExhaustedReasoningBudget(t *testing.T) {
	db := openReportDB(t, "reports-reasoning-budget")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	var requestedMax float64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			MaxTokens float64 `json:"max_tokens"`
		}
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &payload)
		requestedMax = payload.MaxTokens
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"思考中…"}}]}`)
	}))
	defer server.Close()

	config := ModelConfig{Endpoint: server.URL, Model: "reasoning-model"}
	_, err = service.analyze(context.Background(), config, "", Evidence{}, Window{})
	if err == nil {
		t.Fatal("expected an explicit budget error")
	}
	if message := ModelFailureMessage(err); !strings.Contains(message, "推理型") {
		t.Fatalf("failure reason did not name the real cause: %s", message)
	}
	// The budget must leave room for the trace, not just the answer.
	if requestedMax < 8000 {
		t.Fatalf("max_tokens=%v is too small for a reasoning model", requestedMax)
	}
}

func TestExtractJSONObjectAcceptsCommonModelShapes(t *testing.T) {
	want := `{"summary":"ok"}`
	cases := []string{
		`{"summary":"ok"}`,
		"```json\n{\"summary\":\"ok\"}\n```",
		"```\n{\"summary\":\"ok\"}\n```",
		"以下是报告：\n```json\n{\"summary\":\"ok\"}\n```\n希望有帮助。",
		"报告如下：{\"summary\":\"ok\"}",
		"<think>先分析 {\"noise\":true}</think>\n{\"summary\":\"ok\"}",
	}
	for _, input := range cases {
		if got := extractJSONObject(input); got != want {
			t.Fatalf("extractJSONObject(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseAnalysisResponseAcceptsReasoningAndWrappers(t *testing.T) {
	cases := []string{
		`<think>证据中有 {"warning":3}</think>` + "\n```json\n" + `{"summary":"成功","observations":["重复问题"],"actions":["处理"],"limitations":["样本少"]}` + "\n```",
		`{"analysis":{"summary":"成功","observations":[],"actions":[],"limitations":[]}}`,
		`{"metadata":{"version":1}}` + "\n" + `{"summary":"成功","observations":[],"actions":[],"limitations":[]}`,
	}
	for _, input := range cases {
		analysis, err := parseAnalysisResponse(input)
		if err != nil || analysis.Summary != "成功" {
			t.Fatalf("parseAnalysisResponse(%q) = %+v, %v", input, analysis, err)
		}
	}
}

func TestParseAnalysisResponseRejectsIncompleteObjects(t *testing.T) {
	for _, input := range []string{`{"observations":["x"]}`, `{"summary":""}`, `<think>{"summary":"伪报告"}</think>`} {
		if _, err := parseAnalysisResponse(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

// The model can report finish_reason=stop and still emit malformed JSON, which
// fails roughly a quarter of calls on deepseek-v4.1-flash. One bad body must not
// cost the whole report.
func TestAnalyzeRetriesMalformedJSON(t *testing.T) {
	db := openReportDB(t, "reports-retry-json")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = io.WriteString(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"summary\": \"ok\",,}"}}]}`)
			return
		}
		_, _ = io.WriteString(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"summary\":\"重试后成功\",\"observations\":[\"a\"],\"actions\":[\"b\"],\"limitations\":[\"c\"]}"}}]}`)
	}))
	defer server.Close()

	analysis, err := service.analyze(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m"}, "", Evidence{Warning: 1}, Window{})
	if err != nil {
		t.Fatalf("a malformed first body should not fail the analysis: %v", err)
	}
	if calls != 2 || analysis.Summary != "重试后成功" {
		t.Fatalf("calls=%d summary=%q", calls, analysis.Summary)
	}
}

// A rejected key or a wrong model name will never succeed on a second try, and
// retrying would only double the wait before the operator sees the real cause.
func TestAnalyzeDoesNotRetryUnusableConfig(t *testing.T) {
	db := openReportDB(t, "reports-retry-auth")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, reportCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":{"message":"Invalid token"}}`)
	}))
	defer server.Close()

	if _, err := service.analyze(context.Background(), ModelConfig{Endpoint: server.URL, Model: "m"}, "", Evidence{Warning: 1}, Window{}); err == nil {
		t.Fatal("expected auth failure")
	}
	if calls != 1 {
		t.Fatalf("auth failure was retried %d times", calls)
	}
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
