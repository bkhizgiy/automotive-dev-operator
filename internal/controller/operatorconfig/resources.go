package operatorconfig

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultOperatorImage = "quay.io/rh-sdv-cloud/automotive-dev-operator:latest"
	buildControllerName  = "ado-build-controller"
)

// getOperatorImage returns the operator image from env var or default
func getOperatorImage() string {
	if img := os.Getenv("OPERATOR_IMAGE"); img != "" {
		return img
	}
	return defaultOperatorImage
}

// buildBuildAPIContainers builds the container list for build-API deployment, conditionally including oauth-proxy
func (r *OperatorConfigReconciler) buildBuildAPIContainers(isOpenShift bool) []corev1.Container {
	buildAPIEnv := []corev1.EnvVar{
		{
			Name: "BUILD_API_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name: "INTERNAL_JWT_ISSUER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: internalJWTSecretName,
					},
					Key: "issuer",
				},
			},
		},
		{
			Name: "INTERNAL_JWT_AUDIENCE",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: internalJWTSecretName,
					},
					Key: "audience",
				},
			},
		},
		{
			Name: "INTERNAL_JWT_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: internalJWTSecretName,
					},
					Key: "signing-key",
				},
			},
		},
	}
	containers := []corev1.Container{
		{
			Name:    "build-api",
			Image:   getOperatorImage(),
			Command: []string{"/build-api"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
			Env: buildAPIEnv,
			Ports: []corev1.ContainerPort{
				{
					Name:          "http",
					ContainerPort: 8080,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			// No volume mounts needed - Build API reads directly from OperatorConfig CRD
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
			},
		},
	}

	// Only add oauth-proxy on OpenShift
	if isOpenShift {
		containers = append(containers, corev1.Container{
			Name:  "oauth-proxy",
			Image: "registry.redhat.io/openshift4/ose-oauth-proxy:latest",
			Args: []string{
				"--provider=openshift",
				"--https-address=",
				"--http-address=:8081",
				"--upstream=http://localhost:8080",
				"--openshift-service-account=ado-operator",
				"--cookie-secret=$(COOKIE_SECRET)",
				"--cookie-secure=false",
				"--pass-access-token=true",
				"--pass-user-bearer-token=true",
				"--pass-user-headers=true",
				"--request-logging=true",
				"--skip-auth-regex=^/healthz",
				"--skip-auth-regex=^/v1/",
				"--skip-auth-regex=/v1/",
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
								Name: "ado-build-api-oauth-proxy",
							},
							Key: "cookie-secret",
						},
					},
				},
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "proxy-http",
					ContainerPort: 8081,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
			},
		})
	}

	return containers
}

func (r *OperatorConfigReconciler) buildBuildAPIDeployment(isOpenShift bool) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-build-api",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "automotive-dev-operator",
					"app.kubernetes.io/component": "build-api",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "automotive-dev-operator",
						"app.kubernetes.io/component": "build-api",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "ado-operator",
					InitContainers: []corev1.Container{
						{
							Name:    "init-secrets",
							Image:   getOperatorImage(),
							Command: []string{"/init-secrets"},
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
					Containers: r.buildBuildAPIContainers(isOpenShift),
					// No volumes needed - Build API reads directly from OperatorConfig CRD
				},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildAPIService(isOpenShift bool) *corev1.Service {
	// Always expose port 8080 (direct access to build-api)
	ports := []corev1.ServicePort{
		{
			Name:       "http",
			Port:       8080,
			TargetPort: intstr.FromInt(8080),
			Protocol:   corev1.ProtocolTCP,
		},
	}

	// On OpenShift, also expose port 8081 (oauth-proxy)
	if isOpenShift {
		ports = append(ports, corev1.ServicePort{
			Name:       "proxy",
			Port:       8081,
			TargetPort: intstr.FromInt(8081),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-build-api",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
			},
			Ports: ports,
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildAPIRoute() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-build-api",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "ado-build-api",
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("proxy"),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
			WildcardPolicy: routev1.WildcardPolicyNone,
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildAPIIngress() *networkingv1.Ingress {
	pathTypePrefix := networkingv1.PathTypePrefix
	ingressClassName := "nginx"

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-build-api",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTP",
				"nginx.ingress.kubernetes.io/ssl-redirect":     "false",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "ado-build-api",
											Port: networkingv1.ServiceBackendPort{
												// Use port name "http" - matches the service definition on all platforms
												Name: "http",
											},
										},
									},
								},
							},
						},
					},
				},
			},
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

func (r *OperatorConfigReconciler) buildInternalJWTSecret(name string) (*corev1.Secret, error) {
	signingKey, err := generateRandomToken(32)
	if err != nil {
		return nil, err
	}

	issuer := "ado-build-api"
	audience := "ado-build-api"
	expiresAt := time.Now().Add(365 * 24 * time.Hour)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   "internal",
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
		NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	})
	signedToken, err := token.SignedString([]byte(signingKey))
	if err != nil {
		return nil, fmt.Errorf("failed to sign internal JWT: %w", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-api",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"signing-key": signingKey,
			"token":       signedToken,
			"issuer":      issuer,
			"audience":    audience,
			"expires-at":  expiresAt.Format(time.RFC3339),
		},
	}, nil
}

func generateRandomToken(length int) (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	for i := range bytes {
		bytes[i] = charset[int(bytes[i])%len(charset)]
	}
	return string(bytes), nil
}

func (r *OperatorConfigReconciler) buildBuildControllerDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildControllerName,
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "automotive-dev-operator",
					"app.kubernetes.io/component": "build-controller",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "automotive-dev-operator",
						"app.kubernetes.io/component": "build-controller",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: buildControllerName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:    "manager",
							Image:   getOperatorImage(),
							Command: []string{"/manager"},
							Args: []string{
								"--mode=build",
								"--leader-elect",
								"--health-probe-bind-address=:8081",
								"--metrics-bind-address=0",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "OPERATOR_IMAGE",
									Value: getOperatorImage(),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "health",
									ContainerPort: 8081,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(8081),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt(8081),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1000m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildControllerServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildControllerName,
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: buildControllerName,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Rules: []rbacv1.PolicyRule{
			// ImageBuild controller RBAC
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"imagebuilds"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"imagebuilds/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"imagebuilds/finalizers"},
				Verbs:     []string{"update"},
			},
			// Image controller RBAC
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"images"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"images/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"images/finalizers"},
				Verbs:     []string{"update"},
			},
			// CatalogImage controller RBAC
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"catalogimages"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"catalogimages/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"catalogimages/finalizers"},
				Verbs:     []string{"update"},
			},
			// Read-only access to OperatorConfig (for build config)
			{
				APIGroups: []string{"automotive.sdv.cloud.redhat.com"},
				Resources: []string{"operatorconfigs"},
				Verbs:     []string{"get", "list", "watch"},
			},
			// Core resources needed by ImageBuild controller
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"get", "list", "watch", "create"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"serviceaccounts"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"serviceaccounts/token"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods/log"},
				Verbs:     []string{"get"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch"},
			},
			// OpenShift-specific
			{
				APIGroups: []string{"image.openshift.io"},
				Resources: []string{"imagestreams"},
				Verbs:     []string{"get", "create"},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			// Tekton resources
			{
				APIGroups: []string{"tekton.dev"},
				Resources: []string{"tasks", "pipelines", "pipelineruns", "taskruns"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: buildControllerName,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     buildControllerName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      buildControllerName,
				Namespace: operatorNamespace,
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildControllerLeaderElectionRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildControllerName + "-leader-election",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch"},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildControllerLeaderElectionRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildControllerName + "-leader-election",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "automotive-dev-operator",
				"app.kubernetes.io/component": "build-controller",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     buildControllerName + "-leader-election",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      buildControllerName,
				Namespace: operatorNamespace,
			},
		},
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(i int32) *int32 {
	return &i
}
