package operatorconfig

import (
	"crypto/rand"
	"encoding/base64"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

func (r *OperatorConfigReconciler) buildWebUINginxConfigMap() *corev1.ConfigMap {
	nginxConf := `events {
  worker_connections 1024;
}

http {
  include /etc/nginx/mime.types;
  default_type application/octet-stream;

  sendfile on;
  keepalive_timeout 65;
  access_log /dev/stdout;
  error_log /dev/stderr notice;

  server {
    listen 8080;
    server_name localhost;

    root /usr/share/nginx/html;
    index index.html;

    location = /index.html {
      add_header Cache-Control "no-cache, no-store, must-revalidate" always;
      add_header Pragma "no-cache" always;
      add_header Expires "0" always;
    }

    location / {
      try_files $uri $uri/ /index.html;
    }

    location /oauth/ {
      proxy_pass http://127.0.0.1:8081;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_set_header X-Forwarded-Host $server_name;
    }

    location = /config.js {
      default_type application/javascript;
      add_header Cache-Control "no-cache, no-store, must-revalidate" always;
      add_header Pragma "no-cache" always;
      add_header Expires "0" always;
      return 200 "window.__API_BASE = '';\n";
    }

    location ~ ^/v1/builds/.*/logs/sse$ {
      proxy_pass http://ado-build-api:8080;
      proxy_set_header Authorization "Bearer $http_x_forwarded_access_token";
      proxy_set_header X-Forwarded-Access-Token $http_x_forwarded_access_token;
      proxy_set_header X-Forwarded-User $http_x_forwarded_user;
      proxy_set_header X-Forwarded-Email $http_x_forwarded_email;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;

      proxy_buffering off;
      proxy_cache off;
      proxy_http_version 1.1;
      proxy_set_header Connection "";
      chunked_transfer_encoding off;
      proxy_read_timeout 86400s;
      proxy_send_timeout 86400s;
      proxy_connect_timeout 10s;
    }

    location /v1/ {
      proxy_pass http://ado-build-api:8080;
      proxy_set_header Authorization "Bearer $http_x_forwarded_access_token";
      proxy_set_header X-Forwarded-Access-Token $http_x_forwarded_access_token;
      proxy_set_header X-Forwarded-User $http_x_forwarded_user;
      proxy_set_header X-Forwarded-Email $http_x_forwarded_email;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;

      proxy_buffering off;
      proxy_cache off;
      proxy_read_timeout 1800s;
      proxy_send_timeout 1800s;
      proxy_connect_timeout 60s;
    }

    location ~* \.(?:js|css|png|jpg|jpeg|gif|svg|ico|woff2?)$ {
      add_header Cache-Control "public, max-age=31536000, immutable" always;
      expires 1y;
    }

    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
  }
}`

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-webui-nginx-config",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "ado-webui",
				"app.kubernetes.io/component": "webui",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
			},
		},
		Data: map[string]string{
			"nginx.conf": nginxConf,
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildAPIDeployment() *appsv1.Deployment {
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
					ServiceAccountName: "ado-controller-manager",
					InitContainers: []corev1.Container{
						{
							Name:            "init-secrets",
							Image:           "quay.io/rh-sdv-cloud/automotive-dev-operator:latest",
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
							Name:            "build-api",
							Image:           "quay.io/rh-sdv-cloud/automotive-dev-operator:latest",
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"/build-api"},
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
							Env: []corev1.EnvVar{
								{
									Name: "BUILD_API_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
							},
						},
						{
							Name:            "oauth-proxy",
							Image:           "registry.redhat.io/openshift4/ose-oauth-proxy:latest",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args: []string{
								"--provider=openshift",
								"--https-address=",
								"--http-address=:8081",
								"--upstream=http://localhost:8080",
								"--openshift-service-account=ado-controller-manager",
								"--cookie-secret=$(COOKIE_SECRET)",
								"--cookie-secure=false",
								"--pass-access-token=true",
								"--pass-user-headers=true",
								"--request-logging=true",
								"--skip-auth-regex=^/healthz",
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
						},
					},
				},
			},
		},
	}
}

func (r *OperatorConfigReconciler) buildBuildAPIService() *corev1.Service {
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
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "proxy",
					Port:       8081,
					TargetPort: intstr.FromInt(8081),
					Protocol:   corev1.ProtocolTCP,
				},
			},
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

func (r *OperatorConfigReconciler) buildWebUIIngress() *networkingv1.Ingress {
	pathTypePrefix := networkingv1.PathTypePrefix
	ingressClassName := "nginx"

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ado-webui",
			Namespace: operatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "ado-webui",
				"app.kubernetes.io/part-of":   "automotive-dev-operator",
				"app.kubernetes.io/component": "webui",
			},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTP",
				"nginx.ingress.kubernetes.io/ssl-redirect":     "true",
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
											Name: "ado-webui",
											Port: networkingv1.ServiceBackendPort{
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
				"nginx.ingress.kubernetes.io/ssl-redirect":     "true",
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
												Name: "proxy",
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

func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}
