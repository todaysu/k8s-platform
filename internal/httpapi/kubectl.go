package httpapi

import (
	"context"
	"encoding/json"
	"example.com/k8s-platform/internal/cluster"
	"net/http"
)

type kubectlRunner interface {
	RunKubectl(context.Context, string, []string, string) (cluster.KubectlResult, error)
}

func runClusterKubectl(manager any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runner, ok := manager.(kubectlRunner)
		if !ok {
			writeJSON(w, 503, response{Error: errorResponse{Code: "KUBECTL_UNAVAILABLE", Message: "当前服务不支持 kubectl"}})
			return
		}
		var body struct {
			Args  []string `json:"args"`
			Stdin string   `json:"stdin"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1100*1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || cluster.ValidateKubectlArgs(body.Args, body.Stdin) != nil {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_KUBECTL_COMMAND", Message: "命令或参数不支持；YAML 请使用 -f - 并填写输入内容。"}})
			return
		}
		result, err := runner.RunKubectl(r.Context(), r.PathValue("id"), body.Args, body.Stdin)
		if err != nil {
			writeJSON(w, 503, response{Error: errorResponse{Code: "KUBECTL_UNAVAILABLE", Message: "无法启动 kubectl，请检查服务端 kubectl 安装、集群凭据或并发会话。"}})
			return
		}
		writeJSON(w, 200, response{Data: result})
	}
}
