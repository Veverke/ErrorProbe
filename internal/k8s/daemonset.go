package k8s

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// daemonSetNamespace is the namespace where ErrorProbe deploys the Vector DaemonSet.
	daemonSetNamespace = "errorprobe"

	// daemonSetName is the name of the Vector DaemonSet and its supporting resources.
	daemonSetName = "errorprobe-vector"

	// configMapName is the name of the ConfigMap holding vector.toml.
	configMapName = "errorprobe-vector-config"
)

// ApplyVectorDaemonSet creates or updates the Vector DaemonSet and supporting
// RBAC resources in the "errorprobe" namespace. It is idempotent.
func (c *Client) ApplyVectorDaemonSet(ctx context.Context, image, vectorConfigTOML string) error {
	if err := c.applyNamespace(ctx); err != nil {
		return fmt.Errorf("applying namespace: %w", err)
	}
	if err := c.applyServiceAccount(ctx); err != nil {
		return fmt.Errorf("applying service account: %w", err)
	}
	if err := c.applyClusterRole(ctx); err != nil {
		return fmt.Errorf("applying cluster role: %w", err)
	}
	if err := c.applyClusterRoleBinding(ctx); err != nil {
		return fmt.Errorf("applying cluster role binding: %w", err)
	}
	if err := c.applyConfigMap(ctx, vectorConfigTOML); err != nil {
		return fmt.Errorf("applying config map: %w", err)
	}
	if err := c.applyDaemonSet(ctx, image); err != nil {
		return fmt.Errorf("applying daemonset: %w", err)
	}
	return nil
}

// DeleteVectorDaemonSet removes the Vector DaemonSet and supporting resources,
// then polls until the DaemonSet is fully gone from the API server. This
// prevents ApplyVectorDaemonSet from racing against a still-terminating
// DaemonSet (which has DeletionTimestamp set) and silently re-using it only
// to have K8s delete it moments later once all pods have terminated.
func (c *Client) DeleteVectorDaemonSet(ctx context.Context) error {
	del := metav1.DeletePropagationForeground
	opts := metav1.DeleteOptions{PropagationPolicy: &del}

	_ = c.cs.AppsV1().DaemonSets(daemonSetNamespace).Delete(ctx, daemonSetName, opts)
	_ = c.cs.CoreV1().ConfigMaps(daemonSetNamespace).Delete(ctx, configMapName, metav1.DeleteOptions{})
	_ = c.cs.RbacV1().ClusterRoleBindings().Delete(ctx, daemonSetName, metav1.DeleteOptions{})
	_ = c.cs.RbacV1().ClusterRoles().Delete(ctx, daemonSetName, metav1.DeleteOptions{})
	_ = c.cs.CoreV1().ServiceAccounts(daemonSetNamespace).Delete(ctx, daemonSetName, metav1.DeleteOptions{})

	// Poll until the DaemonSet is gone (404) so callers can safely re-create it.
	for {
		_, err := c.cs.AppsV1().DaemonSets(daemonSetNamespace).Get(ctx, daemonSetName, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			break
		}
		select {
		case <-ctx.Done():
			return nil // best-effort: caller context expired, proceed anyway
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil
}

func (c *Client) applyNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   daemonSetNamespace,
			Labels: managedByLabel(),
		},
	}
	_, err := c.cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *Client) applyServiceAccount(ctx context.Context) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      daemonSetName,
			Namespace: daemonSetNamespace,
			Labels:    managedByLabel(),
		},
	}
	_, err := c.cs.CoreV1().ServiceAccounts(daemonSetNamespace).Create(ctx, sa, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *Client) applyClusterRole(ctx context.Context) error {
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   daemonSetName,
			Labels: managedByLabel(),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods", "namespaces", "nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
	_, err := c.cs.RbacV1().ClusterRoles().Create(ctx, cr, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		_, err = c.cs.RbacV1().ClusterRoles().Update(ctx, cr, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) applyClusterRoleBinding(ctx context.Context) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   daemonSetName,
			Labels: managedByLabel(),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     daemonSetName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      daemonSetName,
				Namespace: daemonSetNamespace,
			},
		},
	}
	_, err := c.cs.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		_, err = c.cs.RbacV1().ClusterRoleBindings().Update(ctx, crb, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) applyConfigMap(ctx context.Context, vectorConfigTOML string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: daemonSetNamespace,
			Labels:    managedByLabel(),
		},
		Data: map[string]string{
			"vector.toml": vectorConfigTOML,
		},
	}
	_, err := c.cs.CoreV1().ConfigMaps(daemonSetNamespace).Create(ctx, cm, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		_, err = c.cs.CoreV1().ConfigMaps(daemonSetNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) applyDaemonSet(ctx context.Context, image string) error {
	hostPathDir := corev1.HostPathDirectory
	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      daemonSetName,
			Namespace: daemonSetNamespace,
			Labels:    managedByLabel(),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": daemonSetName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": daemonSetName},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: daemonSetName,
					Tolerations: []corev1.Toleration{
						// Run on all nodes including control-plane.
						{Operator: corev1.TolerationOpExists},
					},
					Containers: []corev1.Container{
						{
							Name:  "vector",
							Image: image,
							Args:  []string{"--config", "/etc/vector/vector.toml"},
							Env: []corev1.EnvVar{
								{
									Name: "HOST_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.hostIP",
										},
									},
								},
								{
									Name: "VECTOR_SELF_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{Name: "VECTOR_LOG", Value: "warn"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "vector-config", MountPath: "/etc/vector", ReadOnly: true},
								{Name: "varlogpods", MountPath: "/var/log/pods", ReadOnly: true},
								{Name: "varlibdockercontainers", MountPath: "/var/lib/docker/containers", ReadOnly: true},
								{Name: "vectordata", MountPath: "/var/lib/vector"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "vector-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
								},
							},
						},
						{
							Name: "varlogpods",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/log/pods",
									Type: &hostPathDir,
								},
							},
						},
						{
							Name: "varlibdockercontainers",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/docker/containers",
									Type: &hostPathDir,
								},
							},
						},
						{
							Name: "vectordata",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/errorprobe-vector",
									Type: &hostPathDirOrCreate,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := c.cs.AppsV1().DaemonSets(daemonSetNamespace).Create(ctx, ds, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		existing, getErr := c.cs.AppsV1().DaemonSets(daemonSetNamespace).Get(ctx, daemonSetName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		ds.ResourceVersion = existing.ResourceVersion
		_, err = c.cs.AppsV1().DaemonSets(daemonSetNamespace).Update(ctx, ds, metav1.UpdateOptions{})
	}
	return err
}

// managedByLabel returns the standard label applied to all ErrorProbe K8s resources.
func managedByLabel() map[string]string {
	return map[string]string{"managed-by": "errorprobe"}
}
