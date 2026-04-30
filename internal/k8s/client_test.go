package k8s_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/errorprobe/errorprobe/internal/k8s"
)

// ---------------------------------------------------------------------------
// Stub that implements K8sAPI for interface-level tests
// ---------------------------------------------------------------------------

type stubK8sAPI struct {
	pingErr error
}

func (s *stubK8sAPI) Ping(_ context.Context) error { return s.pingErr }
func (s *stubK8sAPI) IsAvailable(ctx context.Context) bool {
	return s.Ping(ctx) == nil
}
func (s *stubK8sAPI) ListPods(_ context.Context) ([]k8s.PodInfo, error) { return nil, nil }

// ---------------------------------------------------------------------------
// T5.10 — K8s client tests
// ---------------------------------------------------------------------------

// TestPing_Available_NoError: fake clientset; ServerVersion succeeds → nil error.
func TestPing_Available_NoError(t *testing.T) {
	c := k8s.NewClientWithFake(k8sfake.NewSimpleClientset())
	err := c.Ping(context.Background())
	require.NoError(t, err)
}

// TestPing_Unavailable_Error: stub K8sAPI returns error from Ping.
func TestPing_Unavailable_Error(t *testing.T) {
	stub := &stubK8sAPI{pingErr: errors.New("connection refused")}
	err := stub.Ping(context.Background())
	assert.Error(t, err)
}

// TestIsAvailable_True: Ping succeeds via fake clientset; IsAvailable returns true.
func TestIsAvailable_True(t *testing.T) {
	c := k8s.NewClientWithFake(k8sfake.NewSimpleClientset())
	assert.True(t, c.IsAvailable(context.Background()))
}

// TestIsAvailable_False: stub Ping fails; IsAvailable returns false, no panic.
func TestIsAvailable_False(t *testing.T) {
	stub := &stubK8sAPI{pingErr: errors.New("unreachable")}
	assert.False(t, stub.IsAvailable(context.Background()))
}

// TestListPods_FakeClientset: fake clientset with a pod; ListPods returns it.
func TestListPods_FakeClientset(t *testing.T) {
	fake := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	})
	c := k8s.NewClientWithFake(fake)
	pods, err := c.ListPods(context.Background())
	require.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, "my-pod", pods[0].Name)
}

// TestStartedAt_ReturnsFirstRunningTime: first container's StartedAt is returned.
func TestStartedAt_ReturnsFirstRunningTime(t *testing.T) {
	now := time.Now()
	containers := []k8s.ContainerInfo{
		{Name: "a", Running: true, StartedAt: now},
		{Name: "b", Running: true, StartedAt: now.Add(-time.Hour)},
	}
	result := k8s.StartedAt(containers)
	assert.Equal(t, now, result)
}

// TestStartedAt_NoneRunning_ReturnsZero: no running containers → zero time.
func TestStartedAt_NoneRunning_ReturnsZero(t *testing.T) {
	containers := []k8s.ContainerInfo{
		{Name: "a", Running: false},
	}
	result := k8s.StartedAt(containers)
	assert.True(t, result.IsZero())
}

// TestStartedAt_Empty_ReturnsZero: empty list → zero time.
func TestStartedAt_Empty_ReturnsZero(t *testing.T) {
	result := k8s.StartedAt(nil)
	assert.True(t, result.IsZero())
}
func TestListPods_WithContainerStatus(t *testing.T) {
	fake := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "myapp:1.0"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					Ready:        true,
					RestartCount: 3,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	})
	c := k8s.NewClientWithFake(fake)
	pods, err := c.ListPods(context.Background())
	require.NoError(t, err)
	require.Len(t, pods, 1)
	require.Len(t, pods[0].Containers, 1)
	ci := pods[0].Containers[0]
	assert.Equal(t, "app", ci.Name)
	assert.Equal(t, "myapp:1.0", ci.Image)
	assert.True(t, ci.Ready)
	assert.Equal(t, 3, ci.RestartCount)
	assert.True(t, ci.Running)
}
