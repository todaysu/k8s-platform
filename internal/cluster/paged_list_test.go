package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestPagedListRequiresCompleteSnapshot(t *testing.T) {
	calls := 0
	visited := 0
	err := visitPages(context.Background(), func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		calls++
		if options.Limit != 500 || options.ResourceVersion != "" {
			t.Fatal("unsafe unbounded or RV=0 list")
		}
		if calls == 1 {
			return &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "second"}, Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "first"}}}}, nil
		}
		if options.Continue != "second" {
			t.Fatal("continue lost")
		}
		return nil, errors.New("page failed")
	}, func(pod *corev1.Pod) error { visited++; return nil })
	if err == nil || visited != 1 || calls != 2 {
		t.Fatalf("partial snapshot accepted: %d %d %v", calls, visited, err)
	}
}
func TestPagedListRejectsIgnoredLimitAndRepeatedToken(t *testing.T) {
	err := visitPages(context.Background(), func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return &corev1.PodList{Items: make([]corev1.Pod, 501)}, nil
	}, func(*corev1.Pod) error { return nil })
	if ErrorCode(err) != CodeCollectionLimitExceeded {
		t.Fatalf("ignored limit accepted: %v", err)
	}
	err = visitPages(context.Background(), func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "repeat"}}, nil
	}, func(*corev1.Pod) error { return nil })
	if ErrorCode(err) != CodeCollectionLimitExceeded {
		t.Fatalf("endless pagination: %v", err)
	}
}
func TestListResponseBodyHasByteBudget(t *testing.T) {
	body := &listBudgetBody{ReadCloser: io.NopCloser(strings.NewReader("123456789")), remaining: 8}
	if _, err := io.ReadAll(body); !errors.Is(err, errListBudget) {
		t.Fatalf("body budget bypassed: %v", err)
	}
}

var _ http.RoundTripper = listBudgetTransport{}

func TestEventMessageTruncationPreservesUTF8(t *testing.T) {
	event := normalizedCoreEvent(&corev1.Event{Message: strings.Repeat("中", 2000)})
	if !utf8.ValidString(event.Message) || len(event.Message) > 4120 || !strings.Contains(event.Message, "[truncated]") {
		t.Fatalf("invalid bounded event message: bytes=%d", len(event.Message))
	}
}
