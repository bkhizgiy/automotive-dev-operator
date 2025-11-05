package operatorconfig

import (
	"crypto/rand"
	"encoding/base64"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultWebUIImage    = "quay.io/rh-sdv-cloud/aib-webui:latest"
	defaultOperatorImage = "quay.io/rh-sdv-cloud/automotive-dev-operator:latest"
)

func (r *OperatorConfigReconciler) buildWebUIDeployment() *appsv1.Deployment {
	replicas := int32(1)
	labels := map[string]string{
		"app.kubernetes.io/name":      "ado-webui",
		"app.kubernetes.io/part-of":   "automotive-dev-operator",
		"app.kubernetes.io/component": "webui",
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-webui",
			Namespace: operatorNamespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":    "ado-webui",
					"app.kubernetes.io/part-of": "automotive-dev-operator",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "ado-controller-manager",
					InitContainers: []corev1.Container{
						{
							Name:            "init-secrets",
							Image:           defaultOperatorImage,
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"/init-secrets"},
							Env: []corev1.EnvVar{
								{
									Name: "POD_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "webui",
							Image:           defaultWebUIImage,
							ImagePullPolicy: corev1.PullAlways,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Name:          "http",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								ReadOnlyRootFilesystem: boolPtr(true),
								RunAsNonRoot:           boolPtr(true),
								RunAsUser:              int64Ptr(1001),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "nginx-config",
									MountPath: "/etc/nginx/nginx.conf",
									SubPath:   "nginx.conf",
								},
								{
									Name:      "nginx-cache",
									MountPath: "/var/cache/nginx",
								},
								{
									Name:      "nginx-run",
									MountPath: "/var/run",
								},
							},
						},
						{
							Name:            "oauth-proxy",
							Image:           "registry.redhat.io/openshift4/ose-oauth-proxy:latest",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args: []string{
								"--provider=openshift",
								"--http-address=:8081",
								"--https-address=",
								"--upstream=http://127.0.0.1:8080",
								"--openshift-service-account=ado-controller-manager",
								"--cookie-secret=$(COOKIE_SECRET)",
								"--pass-user-bearer-token=true",
								"--pass-access-token=true",
								"--request-logging=true",
								"--email-domain=*",
								"--skip-provider-button=true",
								"--upstream-timeout=0",
							},
							Env: []corev1.EnvVar{
								{
									Name: "COOKIE_SECRET",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "ado-webui-oauth-proxy",
											},
											Key: "cookie-secret",
										},
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8081,
									Name:          "proxy-http",
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/oauth/healthz",
										Port: intstr.FromString("proxy-http"),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/oauth/healthz",
										Port: intstr.FromString("proxy-http"),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								ReadOnlyRootFilesystem: boolPtr(true),
								RunAsNonRoot:           boolPtr(true),
								RunAsUser:              int64Ptr(1001),
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "nginx-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "ado-webui-nginx-config",
									},
								},
							},
						},
						{
							Name: "nginx-cache",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "nginx-run",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildWebUIService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-webui",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "ado-webui",
				"app.kubernetes.io/part-of": "automotive-dev-operator",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app.kubernetes.io/name":    "ado-webui",
				"app.kubernetes.io/part-of": "automotive-dev-operator",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8081,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromString("proxy-http"),
				},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildWebUIRoute() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-webui",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "ado-webui",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
				"app.kubernetes.io/component": "webui",
			},
			Annotations: map[string]string{
				"haproxy.router.openshift.io/timeout": "24h",
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "ado-webui",
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("http"),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
			WildcardPolicy: routev1.WildcardPolicyNone,
		},
	}
}

func (r *OperatorConfigReconciler) buildOAuthSecret(name string) *corev1.Secret {
	// Generate a random 32-byte cookie secret for AES-256
	cookieSecret := make([]byte, 32)
	if _, err := rand.Read(cookieSecret); err != nil {
		// Fallback to a static secret if random generation fails
		// This should never happen in practice
		cookieSecret = []byte("fallback-secret-change-me-32bit")
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "automotive-dev-operator",
				"app.kubernetes.io/part-of": "automotive-dev-operator",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"cookie-secret": []byte(base64.StdEncoding.EncodeToString(cookieSecret)[:32]),
		},
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func int64Ptr(i int64) *int64 {
	return &i
}
