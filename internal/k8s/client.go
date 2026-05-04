package k8s

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps a kubernetes.Interface for use by ErrorProbe.
type Client struct {
	cs kubernetes.Interface
}

// NewClient loads kubeconfig from the given path (empty = auto-detect) and
// verifies connectivity with a Ping before returning.
//
// Auto-detect order:
//  1. kubeconfigPath argument (non-empty)
//  2. KUBECONFIG environment variable
//  3. ~/.kube/config
//  4. In-cluster config (running inside a pod)
func NewClient(kubeconfigPath string) (*Client, error) {
	cfg, err := buildConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s clientset: %w", err)
	}
	c := &Client{cs: cs}
	if err := c.Ping(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// newClientWithCS creates a Client using a provided kubernetes.Interface (for testing).
func newClientWithCS(cs kubernetes.Interface) *Client {
	return &Client{cs: cs}
}

// Ping verifies cluster connectivity by calling ServerVersion.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cs.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("pinging k8s cluster: %w", err)
	}
	return nil
}

// IsAvailable returns true when Ping succeeds; never propagates errors.
func (c *Client) IsAvailable(ctx context.Context) bool {
	return c.Ping(ctx) == nil
}

// ListPods returns all pods in all namespaces.
func (c *Client) ListPods(ctx context.Context) ([]PodInfo, error) {
	list, err := c.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}
	out := make([]PodInfo, 0, len(list.Items))
	for _, pod := range list.Items {
		out = append(out, podToInfo(pod))
	}
	return out, nil
}

// buildConfig resolves a rest.Config using the standard kubeconfig precedence.
func buildConfig(explicitPath string) (*rest.Config, error) {
	// 1. Explicit path provided by caller.
	if explicitPath != "" {
		return clientcmd.BuildConfigFromFlags("", explicitPath)
	}
	// 2–3. KUBECONFIG env var (supports colon/semicolon-separated path lists)
	//      and ~/.kube/config fallback — both handled by NewDefaultClientConfigLoadingRules.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	if cfg, err := cc.ClientConfig(); err == nil {
		return cfg, nil
	}
	// 4. In-cluster config (running inside a pod).
	return rest.InClusterConfig()
}

// podToInfo converts a corev1.Pod to our PodInfo.
func podToInfo(pod corev1.Pod) PodInfo {
	info := PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Node:      pod.Spec.NodeName,
		Phase:     string(pod.Status.Phase),
		Labels:    pod.Labels,
	}

	// Build a map of container statuses keyed by name for O(1) lookup.
	statusByName := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		statusByName[cs.Name] = cs
	}

	for _, c := range pod.Spec.Containers {
		ci := ContainerInfo{
			Name:  c.Name,
			Image: c.Image,
		}
		if cs, ok := statusByName[c.Name]; ok {
			ci.Ready = cs.Ready
			ci.RestartCount = int(cs.RestartCount)
			if cs.State.Running != nil {
				ci.Running = true
				ci.StartedAt = cs.State.Running.StartedAt.Time
			}
		}
		info.Containers = append(info.Containers, ci)
	}
	return info
}

// startedAt returns the earliest running container's StartedAt time, or zero.
func startedAt(containers []ContainerInfo) time.Time {
	var earliest time.Time
	for _, c := range containers {
		if !c.Running || c.StartedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || c.StartedAt.Before(earliest) {
			earliest = c.StartedAt
		}
	}
	return earliest
}

// GetPreviousLogs fetches up to tailLines lines from the previous terminated
// container instance. Returns an empty string when no previous log exists
// (e.g. the container has never restarted, or the node has been recycled).
func (c *Client) GetPreviousLogs(ctx context.Context, namespace, podName, containerName string, tailLines int) (string, error) {
	tail := int64(tailLines)
	req := c.cs.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		Previous:  true,
		TailLines: &tail,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("streaming previous logs for %s/%s/%s: %w", namespace, podName, containerName, err)
	}
	defer stream.Close()
	var buf strings.Builder
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", fmt.Errorf("reading previous logs for %s/%s/%s: %w", namespace, podName, containerName, err)
	}
	return buf.String(), nil
}
