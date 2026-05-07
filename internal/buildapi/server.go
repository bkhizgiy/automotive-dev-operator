package buildapi

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
	"github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/catalog"
	"github.com/centos-automotive-suite/automotive-dev-operator/internal/common/labels"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var apiTracer = otel.Tracer("build-api")

func spanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

type traceIDContextKey struct{}

func extractTraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	if id, ok := ctx.Value(traceIDContextKey{}).(string); ok && id != "" {
		return id
	}
	return ""
}

const (
	// Build phase constants — aliases for readability; canonical values in api/v1alpha1
	phaseCancelled = automotivev1alpha1.ImageBuildPhaseCancelled
	phaseCompleted = automotivev1alpha1.ImageBuildPhaseCompleted
	phaseFailed    = automotivev1alpha1.ImageBuildPhaseFailed
	phasePending   = automotivev1alpha1.ImageBuildPhasePending
	phaseUploading = automotivev1alpha1.ImageBuildPhaseUploading
	phaseBuilding  = automotivev1alpha1.ImageBuildPhaseBuilding
	phasePushing   = automotivev1alpha1.ImageBuildPhasePushing
	phaseFlashing  = automotivev1alpha1.ImageBuildPhaseFlashing
	phaseRunning   = "Running"

	// Image format constants
	formatImage    = "image"
	formatQcow2    = "qcow2"
	extensionRaw   = ".raw"
	extensionQcow2 = ".qcow2"
	statusUnknown  = "unknown"
	statusMissing  = "MISSING"
	buildAPIName   = "ado-build-api"

	// maxManifestSize is the maximum allowed manifest size in bytes.
	// Manifests are stored in ConfigMaps, which are limited by etcd's ~1MB object size.
	maxManifestSize = 900 * 1024
)

var getClientFromRequestFn = getClientFromRequest
var getRESTConfigFromRequestFn = getRESTConfigFromRequest
var createInternalRegistrySecretFn = createInternalRegistrySecret
var newPodExecExecutorFn = func(
	config *rest.Config,
	namespace, podName, containerName string,
	cmd []string,
) (remotecommand.Executor, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	req := clientset.CoreV1().RESTClient().Post().Resource("pods").Name(podName).Namespace(namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, kscheme.ParameterCodec)
	return remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
}
var loadOperatorConfigFn = func(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
) (*automotivev1alpha1.OperatorConfig, error) {
	operatorConfig := &automotivev1alpha1.OperatorConfig{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "config",
	}, operatorConfig); err != nil {
		return nil, err
	}
	return operatorConfig, nil
}

var loadTargetDefaultsFn = func(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
) (map[string]TargetDefaults, error) {
	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "aib-target-defaults",
	}, cm); err != nil {
		return nil, err
	}

	data, ok := cm.Data["target-defaults.yaml"]
	if !ok {
		return nil, nil
	}

	var parsed struct {
		Targets map[string]struct {
			Architecture  string   `yaml:"architecture"`
			ExtraArgs     []string `yaml:"extraArgs"`
			DefaultFormat string   `yaml:"defaultFormat"`
		} `yaml:"targets"`
	}
	if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse target-defaults.yaml: %w", err)
	}

	result := make(map[string]TargetDefaults, len(parsed.Targets))
	for name, t := range parsed.Targets {
		result[name] = TargetDefaults{
			Architecture:  t.Architecture,
			ExtraArgs:     t.ExtraArgs,
			DefaultFormat: t.DefaultFormat,
		}
	}
	return result, nil
}

// APILimits holds configurable limits for the API server
type APILimits struct {
	MaxUploadFileSize           int64
	MaxTotalUploadSize          int64
	MaxLogStreamDurationMinutes int32
	ClientTokenExpiryDays       int32
}

// DefaultAPILimits returns the default limits
func DefaultAPILimits() APILimits {
	return APILimits{
		MaxUploadFileSize:           1 * 1024 * 1024 * 1024, // 1GB
		MaxTotalUploadSize:          2 * 1024 * 1024 * 1024, // 2GB
		MaxLogStreamDurationMinutes: 120,                    // 2 hours
		ClientTokenExpiryDays:       automotivev1alpha1.DefaultClientTokenExpiryDays,
	}
}

// APIServer provides the REST API for build operations.
type APIServer struct {
	server              *http.Server
	router              *gin.Engine
	addr                string
	log                 logr.Logger
	limits              APILimits
	internalJWT         *internalJWTConfig
	externalJWT         authenticator.Token
	internalPrefix      string
	authConfig          *AuthenticationConfiguration // Store raw config for API exposure
	oidcClientID        string
	authConfigMu        sync.RWMutex // Protects externalJWT, authConfig, internalPrefix, oidcClientID
	lastAuthConfigCheck time.Time    // Last time we checked OperatorConfig
	progressCache       map[string]progressCacheEntry
	progressCacheMu     sync.RWMutex
}

//go:embed openapi.yaml
var embeddedOpenAPI []byte

// NewAPIServer creates a new API server
func NewAPIServer(addr string, logger logr.Logger) *APIServer {
	return NewAPIServerWithLimits(addr, logger, DefaultAPILimits())
}

// NewAPIServerWithLimits creates a new API server with custom limits
func NewAPIServerWithLimits(addr string, logger logr.Logger, limits APILimits) *APIServer {
	// Gin mode should be controlled by environment, not by which constructor is used
	if os.Getenv("GIN_MODE") == "" {
		// Default to release mode for production safety
		gin.SetMode(gin.ReleaseMode)
	}

	a := &APIServer{addr: addr, log: logger, limits: limits}
	if clientID := strings.TrimSpace(os.Getenv("BUILD_API_OIDC_CLIENT_ID")); clientID != "" {
		a.oidcClientID = clientID
	}
	if cfg, err := loadInternalJWTConfig(); err != nil {
		logger.Error(err, "internal JWT configuration is invalid; internal JWT auth disabled")
	} else if cfg != nil {
		a.internalJWT = cfg
		logger.Info("internal JWT auth enabled", "issuer", cfg.issuer, "audience", cfg.audience)
	}

	// Try to load authentication configuration directly from OperatorConfig CRD
	namespace := resolveNamespace()
	logger.Info("attempting to load authentication config from OperatorConfig", "namespace", namespace)
	k8sClient, err := a.getCatalogClient()
	if err == nil {
		// IMPORTANT: Use context.Background() without cancel - the OIDC authenticator does lazy
		// initialization in the background and needs the context to remain valid after this function returns.
		// Using a cancellable context would kill the background JWKS fetch.
		cfg, authn, prefix, err := loadAuthenticationConfigurationFromOperatorConfig(context.Background(), k8sClient, namespace)
		if err != nil {
			// If OperatorConfig doesn't exist or can't be read, log and continue without OIDC
			// This allows kubeconfig fallback to work
			logger.Info("failed to load authentication config from OperatorConfig, will use kubeconfig fallback", "namespace", namespace, "error", err)
		} else if cfg != nil {
			a.authConfig = cfg
			a.externalJWT = authn
			a.internalPrefix = prefix
			if cfg.ClientID != "" {
				a.oidcClientID = cfg.ClientID
			}
			if len(cfg.JWT) > 0 {
				if authn != nil {
					logger.Info("loaded authentication config from OperatorConfig", "jwt_count", len(cfg.JWT), "namespace", namespace, "client_id", cfg.ClientID)
				} else {
					logger.Info("OIDC configured in OperatorConfig but initialization failed, externalJWT set to nil to enable kubeconfig fallback", "jwt_count", len(cfg.JWT), "namespace", namespace)
					// Ensure externalJWT is nil so clients don't try to use OIDC tokens
					a.externalJWT = nil
				}
			} else {
				logger.Info("authentication config loaded from OperatorConfig but no JWT issuers configured", "namespace", namespace)
			}
		} else {
			logger.Info("no authentication config in OperatorConfig, will use kubeconfig fallback", "namespace", namespace)
		}
	} else {
		logger.Info("failed to create k8s client for OperatorConfig, will use kubeconfig fallback", "error", err)
	}
	a.router = a.createRouter()
	a.server = &http.Server{Addr: addr, Handler: otelhttp.NewHandler(a.router, "build-api")}
	return a
}

// LoadLimitsFromConfig loads API limits from OperatorConfig, using defaults for unset values
func LoadLimitsFromConfig(cfg *automotivev1alpha1.BuildAPIConfig) APILimits {
	limits := DefaultAPILimits()
	if cfg == nil {
		return limits
	}
	if cfg.MaxUploadFileSize > 0 {
		limits.MaxUploadFileSize = cfg.MaxUploadFileSize
	}
	if cfg.MaxTotalUploadSize > 0 {
		limits.MaxTotalUploadSize = cfg.MaxTotalUploadSize
	}
	if cfg.MaxLogStreamDurationMinutes > 0 {
		limits.MaxLogStreamDurationMinutes = cfg.MaxLogStreamDurationMinutes
	}
	if cfg.ClientTokenExpiryDays > 0 {
		limits.ClientTokenExpiryDays = cfg.ClientTokenExpiryDays
	}
	return limits
}

// safeFilename validates that a filename is safe for use in shell commands
// It only allows alphanumeric characters, dots, hyphens, underscores, at signs, and single forward slashes for paths
func safeFilename(filename string) bool {
	if filename == "" {
		return false
	}

	// Reject dangerous characters that could be used for command injection
	for _, char := range filename {
		switch char {
		case 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
			'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
			'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z',
			'0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
			'.', '-', '_', '/', '@':
			// Safe characters
			continue
		default:
			// Reject any other character including quotes, semicolons, backticks, pipes, etc.
			return false
		}
	}

	return true
}

// Start implements manager.Runnable
func (a *APIServer) Start(ctx context.Context) error {

	go func() {
		a.log.Info("build-api listening", "addr", a.addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Error(err, "build-api server error")
		}
	}()

	<-ctx.Done()
	a.log.Info("shutting down build-api server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Error(err, "build-api server forced to shutdown")
		return err
	}
	a.log.Info("build-api server exited")
	return nil
}

func (a *APIServer) createRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	router.Use(func(c *gin.Context) {
		reqID := uuid.New().String()
		c.Set("reqID", reqID)

		ctx := c.Request.Context()
		if sc := trace.SpanContextFromContext(ctx); !sc.IsValid() {
			var tid trace.TraceID
			_, _ = rand.Read(tid[:])
			ctx = context.WithValue(ctx, traceIDContextKey{}, tid.String())
			c.Request = c.Request.WithContext(ctx)
		}

		a.log.Info("http request", "method", c.Request.Method, "path", c.Request.URL.Path, "reqID", reqID, "traceID", extractTraceID(ctx))
		c.Next()
	})

	router.GET("/metrics", metricsHandler())

	v1 := router.Group("/v1")
	{
		v1.GET("/healthz", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		v1.GET("/openapi.yaml", func(c *gin.Context) {
			c.Data(http.StatusOK, "application/yaml", embeddedOpenAPI)
		})

		// Auth config endpoint (no auth required - needed for OIDC discovery)
		v1.GET("/auth/config", a.handleGetAuthConfig)

		buildsGroup := v1.Group("/builds")
		buildsGroup.Use(a.authMiddleware())
		{
			buildsGroup.POST("", a.wrapHandler("create build", a.createBuild))
			buildsGroup.GET("", a.wrapHandler("list builds", listBuilds))
			buildsGroup.GET("/:name", a.wrapNamedHandler("get build", a.getBuild))
			buildsGroup.GET("/:name/logs", a.wrapNamedHandler("logs requested", a.streamLogs))
			buildsGroup.GET("/:name/progress", a.handleGetProgress)
			buildsGroup.GET("/:name/template", a.wrapNamedHandler("template requested", getBuildTemplate))
			buildsGroup.POST("/:name/uploads", a.wrapNamedHandler("uploads", a.uploadFiles))
			buildsGroup.POST("/:name/token", a.handleCreateBuildToken)
			buildsGroup.POST("/:name/cancel", a.wrapNamedHandler("cancel build", a.cancelBuild))
			buildsGroup.DELETE("/:name", a.wrapNamedHandler("delete build", a.deleteBuild))
		}

		flashGroup := v1.Group("/flash")
		flashGroup.Use(flashMetricsMiddleware(), a.authMiddleware())
		{
			flashGroup.POST("", a.wrapHandler("create flash", a.createFlash))
			flashGroup.GET("", a.wrapHandler("list flash jobs", a.listFlash))
			flashGroup.GET("/:name", a.wrapNamedHandler("get flash", a.getFlash))
			flashGroup.GET("/:name/logs", a.wrapNamedHandler("flash logs requested", a.streamFlashLogs))
		}

		configGroup := v1.Group("/config")
		configGroup.Use(a.authMiddleware())
		{
			configGroup.GET("", a.handleGetOperatorConfig)
		}

		containerBuildsGroup := v1.Group("/container-builds")
		containerBuildsGroup.Use(a.authMiddleware())
		{
			containerBuildsGroup.POST("", a.wrapHandler("create container build", a.createContainerBuild))
			containerBuildsGroup.GET("", a.wrapHandler("list container builds", listContainerBuilds))
			containerBuildsGroup.GET("/:name", a.wrapNamedHandler("get container build", a.getContainerBuild))
			containerBuildsGroup.POST("/:name/upload", a.wrapNamedHandler("container build upload", a.uploadContainerBuildContext))
			containerBuildsGroup.GET("/:name/logs", a.wrapNamedHandler("container build logs", a.streamContainerBuildLogs))
		}

		a.registerSealedRoutes(v1)

		a.registerWorkspaceRoutes(v1)

		// Register catalog routes with authentication
		catalogClient, err := a.getCatalogClient()
		if err != nil {
			a.log.Error(err, "failed to create catalog client, catalog routes will not be available")
		} else if catalogClient != nil {
			a.log.Info("registering catalog routes")
			catalog.RegisterRoutes(v1, catalogClient, a.log)
		}
	}

	return router
}

// StartServer starts the REST API server on the given address in a goroutine and returns the server
func StartServer(addr string, logger logr.Logger) (*http.Server, error) {
	api := NewAPIServer(addr, logger)
	server := api.server
	go func() {
		if err := api.Start(context.Background()); err != nil {
			logger.Error(err, "failed to start build-api server")
		}
	}()
	return server, nil
}

// getCatalogClient returns a Kubernetes client for catalog operations
func (a *APIServer) getCatalogClient() (client.Client, error) {
	var cfg *rest.Config
	var err error
	cfg, err = rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build kube config: %w", err)
		}
	}

	scheme := runtime.NewScheme()
	if err := automotivev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add automotive scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}
	return k8sClient, nil
}

// authError represents an authentication failure with a reason
type authError struct {
	Reason  string `json:"reason"`
	Details string `json:"details,omitempty"`
}

func (a *APIServer) handleCreateBuildToken(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("token requested", "build", name, "reqID", c.GetString("reqID"))

	namespace := resolveNamespace()
	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	ctx := c.Request.Context()
	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(ctx, c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}

	// Verify the requesting user owns this build
	requester := a.resolveRequester(c)
	owner := build.Annotations[labels.RequestedBy]
	if owner != requester {
		c.JSON(http.StatusForbidden, gin.H{"error": "you can only request tokens for your own builds"})
		return
	}

	if !build.Spec.GetUseServiceAccountAuth() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "build does not use the internal registry"})
		return
	}

	if build.Status.Phase != phaseCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("build is not completed (current: %s)", build.Status.Phase)})
		return
	}

	// Determine the image ref first — only mint tokens if there's an internal image
	imageRef := build.Spec.GetExportOCI()
	if imageRef == "" {
		imageRef = build.Spec.GetContainerPush()
	}
	if imageRef == "" || !strings.HasPrefix(imageRef, defaultInternalRegistryURL+"/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "build has no image in the internal registry"})
		return
	}

	tokenLifetime := resolveTokenLifetime(ctx, k8sClient, namespace)
	token, expiresAt, err := a.mintRegistryToken(ctx, c, namespace, tokenLifetime)
	if err != nil {
		a.log.Error(err, "failed to mint registry token", "build", name)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to mint registry token: %v", err)})
		return
	}

	registryHost := ""
	externalRoute, routeErr := getExternalRegistryRoute(ctx, k8sClient, namespace)
	if routeErr == nil && externalRoute != "" {
		imageRef = translateToExternalURL(imageRef, externalRoute)
		registryHost = externalRoute
	} else {
		registryHost = strings.SplitN(imageRef, "/", 2)[0]
	}

	writeJSON(c, http.StatusOK, TokenResponse{
		Registry:  registryHost,
		Username:  "serviceaccount",
		Token:     token,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		Image:     imageRef,
	})
}

func (a *APIServer) deleteBuild(c *gin.Context, name string) {
	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	namespace := resolveNamespace()
	ctx := c.Request.Context()

	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(ctx, c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}

	requester := a.resolveRequester(c)
	owner := build.Annotations[labels.RequestedBy]
	if owner != requester {
		c.JSON(http.StatusForbidden, gin.H{"error": "you can only delete your own builds"})
		return
	}

	// Clean up ImageStream tags created by this build before deleting
	// Only delete the specific tags this build created; if the stream becomes
	// empty afterwards, delete the whole ImageStream.
	if build.Spec.GetUseServiceAccountAuth() {
		streamName, tags := resolveImageStreamRefs(build)
		if streamName != "" {
			for _, tag := range tags {
				if delErr := deleteImageStreamTag(ctx, k8sClient, namespace, streamName, tag); delErr != nil {
					if !k8serrors.IsNotFound(delErr) {
						a.log.Error(delErr, "failed to delete ImageStreamTag", "imageStreamTag", streamName+":"+tag)
					}
				}
			}
			// If no tags remain, clean up the empty ImageStream
			hasTags, err := imageStreamHasTags(ctx, k8sClient, namespace, streamName)
			if err != nil {
				if !k8serrors.IsNotFound(err) {
					a.log.Error(err, "failed to check ImageStream tags", "imageStream", streamName)
				}
			} else if !hasTags {
				if delErr := deleteImageStream(ctx, k8sClient, namespace, streamName); delErr != nil {
					if !k8serrors.IsNotFound(delErr) {
						a.log.Error(delErr, "failed to delete empty ImageStream", "imageStream", streamName)
					}
				}
			}
		}
	}

	// Delete the ImageBuild CR — Kubernetes cascading delete handles owned resources
	// (PipelineRuns, TaskRuns, PVCs, Secrets, Pods, Services, ConfigMaps)
	if err := k8sClient.Delete(ctx, build); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete build: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("build %q deleted", name)})
}

func (a *APIServer) cancelBuild(c *gin.Context, name string) {
	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	namespace := resolveNamespace()
	ctx := c.Request.Context()

	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(ctx, c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}

	requester := a.resolveRequester(c)
	owner := build.Annotations[labels.RequestedBy]
	if owner != requester {
		c.JSON(http.StatusForbidden, gin.H{"error": "you can only cancel your own builds"})
		return
	}

	switch build.Status.Phase {
	case "", phasePending, phaseUploading, phaseBuilding, phasePushing, phaseFlashing:
		// cancellable
	default:
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("build is in %q phase and cannot be cancelled", build.Status.Phase),
		})
		return
	}

	if build.Status.PipelineRunName != "" {
		pipelineRun := &tektonv1.PipelineRun{}
		prKey := types.NamespacedName{Name: build.Status.PipelineRunName, Namespace: namespace}
		if err := k8sClient.Get(ctx, prKey, pipelineRun); err != nil {
			if !k8serrors.IsNotFound(err) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching PipelineRun: %v", err)})
				return
			}
		} else if pipelineRun.Status.CompletionTime != nil {
			c.JSON(http.StatusConflict, gin.H{
				"error": "build has already completed; refresh and retry",
			})
			return
		} else {
			pipelineRun.Spec.Status = tektonv1.PipelineRunSpecStatusCancelled
			if err := k8sClient.Update(ctx, pipelineRun); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to cancel PipelineRun: %v", err)})
				return
			}
		}
	}

	build.Status.Phase = phaseCancelled
	build.Status.Message = "Build cancelled by user"
	now := metav1.Now()
	if build.Status.CompletionTime == nil {
		build.Status.CompletionTime = &now
	}
	if err := k8sClient.Status().Update(ctx, build); err != nil {
		// Controller may have already set phase to Cancelled after seeing the PipelineRun cancel
		if k8serrors.IsConflict(err) {
			c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("build %q cancelled", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update build status: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("build %q cancelled", name)})
}

// validateBuildRequest validates the build request, sanitizes the name, and applies defaults
func validateBuildRequest(req *BuildRequest) error {
	if err := validateBuildName(req.Name); err != nil {
		return err
	}
	req.Name = sanitizeBuildNameForValidation(req.Name)

	if len(req.Manifest) > maxManifestSize {
		return fmt.Errorf("manifest too large: %d bytes exceeds %d byte limit (ConfigMap/etcd constraint)",
			len(req.Manifest), maxManifestSize)
	}

	if req.Mode == ModeDisk {
		if req.ContainerRef == "" {
			return fmt.Errorf("container-ref is required for disk mode")
		}
		if err := validateContainerRef(req.ContainerRef); err != nil {
			return err
		}
	} else if req.Manifest == "" {
		return fmt.Errorf("manifest is required")
	}

	for field, value := range map[string]string{"container-push": req.ContainerPush, "export-oci": req.ExportOCI} {
		if err := validateContainerRef(value); err != nil {
			return fmt.Errorf("invalid %s: %v", field, err)
		}
	}

	if req.Reproducible && !req.SecureBuild {
		return fmt.Errorf("reproducible builds require secureBuild to be true")
	}

	return nil
}

// resolveAndClampTTL validates the requested TTL and enforces MaxBuildTTL if configured.
func resolveAndClampTTL(ctx context.Context, k8sClient client.Client, namespace, requestedTTL string) (string, error) {
	if requestedTTL == "" {
		return requestedTTL, nil
	}
	if requestedTTL != "0" {
		dur, err := time.ParseDuration(requestedTTL)
		if err != nil {
			return "", fmt.Errorf("invalid TTL %q: %w", requestedTTL, err)
		}
		if dur < 0 {
			return "", fmt.Errorf("TTL must not be negative")
		}
	}
	operatorCfg, cfgErr := loadOperatorConfigFn(ctx, k8sClient, namespace)
	if cfgErr != nil && !k8serrors.IsNotFound(cfgErr) {
		return "", fmt.Errorf("failed to load OperatorConfig: %w", cfgErr)
	}
	if operatorCfg != nil && operatorCfg.Spec.OSBuilds != nil {
		if maxStr := operatorCfg.Spec.OSBuilds.GetMaxBuildTTL(); maxStr != "" && maxStr != "0" {
			maxDur, parseErr := time.ParseDuration(maxStr)
			if parseErr != nil {
				return "", fmt.Errorf("invalid MaxBuildTTL %q in OperatorConfig: %w", maxStr, parseErr)
			}
			if maxDur <= 0 {
				return "", fmt.Errorf("MaxBuildTTL must be positive, got %q", maxStr)
			}
			if requestedTTL == "0" {
				return "", fmt.Errorf("no-expiry (TTL \"0\") is not allowed when MaxBuildTTL is set (%s)", maxStr)
			}
			dur, _ := time.ParseDuration(requestedTTL)
			if dur > maxDur {
				return "", fmt.Errorf("requested TTL %q exceeds maximum %q", requestedTTL, maxStr)
			}
		}
	}
	return requestedTTL, nil
}

// applyBuildDefaults sets default values for build request fields
func applyBuildDefaults(req *BuildRequest) error {
	if req.Distro == "" {
		req.Distro = "autosd"
	}
	if req.Target == "" {
		req.Target = "qemu"
	}
	if req.Architecture == "" {
		req.Architecture = "arm64"
	}
	if req.ExportFormat == "" {
		req.ExportFormat = formatImage
	}
	if req.Mode == "" {
		req.Mode = ModeBootc
	}
	if strings.TrimSpace(string(req.Compression)) == "" {
		req.Compression = CompressionGzip
	}
	if !req.Compression.IsValid() {
		return fmt.Errorf("invalid compression %q: must be lz4, gzip, or xz", req.Compression)
	}
	if !req.Distro.IsValid() {
		return fmt.Errorf("distro cannot be empty")
	}
	if !req.Target.IsValid() {
		return fmt.Errorf("target cannot be empty")
	}
	if !req.Architecture.IsValid() {
		return fmt.Errorf("architecture cannot be empty")
	}
	// ExportFormat validation removed - allow AIB to handle format validation
	if !req.Mode.IsValid() {
		return fmt.Errorf("mode cannot be empty")
	}
	if req.AutomotiveImageBuilder == "" {
		req.AutomotiveImageBuilder = automotivev1alpha1.DefaultAutomotiveImageBuilderImage
	}
	if req.ManifestFileName == "" {
		req.ManifestFileName = "manifest.aib.yml"
	}
	return nil
}

// resolveRegistryForBuild handles registry setup for both internal and external registry builds.
// It returns envSecretRef, pushSecretName, and an error (non-nil means the response was already written).
func (a *APIServer) resolveRegistryForBuild(
	ctx context.Context, c *gin.Context, k8sClient client.Client,
	namespace string, req *BuildRequest,
) (string, string, error) {
	if req.UseInternalRegistry {
		_, pushSecretName, err := a.setupInternalRegistryBuild(ctx, c, k8sClient, namespace, req)
		if err != nil {
			return "", "", err
		}

		// Hybrid: container pushed to external registry, disk to internal.
		// Create external registry secret for the container push workspace.
		if req.ContainerPush != "" && req.RegistryCredentials != nil && req.RegistryCredentials.Enabled {
			envSecretRef, err := createRegistrySecret(ctx, k8sClient, namespace, req.Name, req.RegistryCredentials)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return "", "", err
			}
			return envSecretRef, pushSecretName, nil
		}

		return pushSecretName, pushSecretName, nil
	}

	envSecretRef, pushSecretName, err := setupBuildSecrets(ctx, k8sClient, namespace, req)
	if err != nil {
		if errors.Is(err, errRegistryCredentialsRequiredForPush) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else if k8serrors.IsAlreadyExists(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return "", "", err
	}
	return envSecretRef, pushSecretName, nil
}

// setupInternalRegistryBuild validates and configures internal registry push,
// returning ("", pushSecretName, nil) on success.
func (a *APIServer) setupInternalRegistryBuild(
	ctx context.Context, c *gin.Context, k8sClient client.Client,
	namespace string, req *BuildRequest,
) (string, string, error) {
	// Validate: internal registry handles the disk push, so exportOci must not be set.
	// containerPush (and registryCredentials) MAY be set for hybrid builds where
	// the bootc container is pushed to an external registry.
	if req.ExportOCI != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "useInternalRegistry cannot be used with exportOci"})
		return "", "", fmt.Errorf("validation error")
	}
	if req.Reproducible {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reproducible builds cannot use internal registry (OCI referrers not supported)"})
		return "", "", fmt.Errorf("validation error")
	}
	// Resolve external route (validates registry is reachable)
	if _, err := getExternalRegistryRoute(ctx, k8sClient, namespace); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", "", err
	}

	// Generate image name and tag
	imageName := req.InternalRegistryImageName
	if imageName == "" {
		imageName = req.Name
	}
	tag := req.InternalRegistryTag

	// Set concrete URLs based on build mode.
	// When ContainerPush is already set (hybrid: external container push),
	// keep it and only generate internal URLs for what's missing.
	externalContainerPush := req.ContainerPush != ""
	if req.Mode.IsBootc() {
		if !externalContainerPush {
			bootcTag := tag
			if bootcTag == "" {
				bootcTag = "bootc"
			}
			req.ContainerPush = generateRegistryImageRef(defaultInternalRegistryURL, namespace, imageName, bootcTag)
		}
		// Flash requires a disk image
		if req.FlashEnabled && !req.BuildDiskImage {
			req.BuildDiskImage = true
		}
		if req.BuildDiskImage {
			diskTag := tag
			if diskTag == "" {
				diskTag = "disk"
			}
			req.ExportOCI = generateRegistryImageRef(defaultInternalRegistryURL, namespace, imageName, diskTag)
		}
	} else {
		// Traditional/disk modes: push disk image as OCI artifact
		diskTag := tag
		if diskTag == "" {
			diskTag = "disk"
		}
		req.ExportOCI = generateRegistryImageRef(defaultInternalRegistryURL, namespace, imageName, diskTag)
	}

	// Pre-create ImageStream for internal registry pushes.
	// All images (bootc container and disk) share the same ImageStream,
	// distinguished by tag (:bootc, :disk).
	needsImageStream := !externalContainerPush || req.BuildDiskImage || !req.Mode.IsBootc()
	if needsImageStream {
		if _, err := ensureImageStream(ctx, k8sClient, namespace, imageName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating ImageStream: %v", err)})
			return "", "", err
		}
	}

	// Create auth secret from SA token
	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error getting REST config: %v", err)})
		return "", "", err
	}
	tokenLifetime := resolveTokenLifetime(ctx, k8sClient, namespace)
	secretName, err := createInternalRegistrySecret(ctx, restCfg, namespace, req.Name, tokenLifetime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", "", err
	}
	// Return as both envSecretRef (for pipeline registry-auth workspace + WhenExpression)
	// and pushSecretName (for push credential binding)
	return secretName, secretName, nil
}

// buildExportSpec creates ExportSpec configuration from build request
func buildExportSpec(req *BuildRequest) *automotivev1alpha1.ExportSpec {
	export := &automotivev1alpha1.ExportSpec{
		Format:                string(req.ExportFormat),
		Compression:           string(req.Compression),
		BuildDiskImage:        req.BuildDiskImage,
		Container:             req.ContainerPush,
		UseServiceAccountAuth: req.UseInternalRegistry,
	}

	// Set disk export if OCI URL is specified
	if req.ExportOCI != "" {
		export.Disk = &automotivev1alpha1.DiskExport{
			OCI: req.ExportOCI,
		}
	}

	return export
}

// resolveExtraRepos processes --extra-repo flags (workspace:path pairs), starts HTTP
// servers in the workspace pods, and injects extra_repos into the build's CustomDefs.
func (a *APIServer) resolveExtraRepos(ctx context.Context, k8sClient client.Client, restCfg *rest.Config, req *BuildRequest) error {
	if len(req.ExtraRepos) == 0 {
		return nil
	}

	namespace := resolveNamespace()
	basePort := 8080

	type repoEntry struct {
		ID      string `json:"id"`
		BaseURL string `json:"baseurl"`
	}
	repos := make([]repoEntry, 0, len(req.ExtraRepos))

	for i, entry := range req.ExtraRepos {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid --extra-repo %q: must be workspace-name:/path", entry)
		}
		wsName, repoPath := parts[0], parts[1]
		port := basePort + i

		// Look up the workspace pod
		ws := &automotivev1alpha1.Workspace{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: wsName}, ws); err != nil {
			return fmt.Errorf("workspace %q not found: %w", wsName, err)
		}
		if ws.Status.Phase != phaseRunning {
			return fmt.Errorf("workspace %q is not running (phase: %s)", wsName, ws.Status.Phase)
		}

		pod := &corev1.Pod{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ws.Status.PodName}, pod); err != nil {
			return fmt.Errorf("workspace pod %q not found: %w", ws.Status.PodName, err)
		}
		podIP := pod.Status.PodIP
		if podIP == "" {
			return fmt.Errorf("workspace pod %q has no IP", ws.Status.PodName)
		}

		// Start HTTP server in the workspace (background, fire-and-forget).
		// Redirect shell's own FDs first so runc exec doesn't block waiting for SPDY pipes.
		cmd := []string{"/bin/sh", "-c",
			fmt.Sprintf("exec 0</dev/null 1>/dev/null 2>/dev/null; cd %s && python3 -m http.server %d &", shellQuote(repoPath), port)}
		if err := podExec(ctx, restCfg, namespace, ws.Status.PodName, workspaceContainerName, cmd, io.Discard); err != nil {
			return fmt.Errorf("starting HTTP server in workspace %q: %w", wsName, err)
		}

		repoURL := fmt.Sprintf("http://%s:%d", podIP, port)
		repos = append(repos, repoEntry{
			ID:      fmt.Sprintf("workspace-%s", wsName),
			BaseURL: repoURL,
		})
		a.log.Info("Extra repo configured", "workspace", wsName, "url", repoURL)
	}

	reposJSON, err := json.Marshal(repos)
	if err != nil {
		return fmt.Errorf("marshaling extra_repos: %w", err)
	}
	req.CustomDefs = append(req.CustomDefs, fmt.Sprintf("extra_repos=%s", string(reposJSON)))
	return nil
}

// resolveWorkspaceForBuild resolves a workspace reference for a build:
// - Finds the workspace or auto-creates it if it doesn't exist
// - Creates/finds a build-cache PVC for osbuild checkpoint persistence
// - Forwards the workspace's lease if the build has flash enabled but no explicit lease
// - Starts an HTTP file server in the workspace pod and injects workspace_url as a custom define
// Returns the build-cache PVC name.
func (a *APIServer) resolveWorkspaceForBuild(ctx context.Context, k8sClient client.Client, restCfg *rest.Config, namespace, wsName, requester string, req *BuildRequest) (string, error) {
	operatorConfig, _ := loadOperatorConfigFn(ctx, k8sClient, namespace)
	var wsConfig *automotivev1alpha1.WorkspacesConfig
	if operatorConfig != nil {
		wsConfig = operatorConfig.Spec.Workspaces
	}

	ws := &automotivev1alpha1.Workspace{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: wsName}, ws)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return "", fmt.Errorf("checking workspace %q: %w", wsName, err)
		}
		// Auto-create workspace with defaults from OperatorConfig
		ws = &automotivev1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      wsName,
				Namespace: namespace,
			},
			Spec: automotivev1alpha1.WorkspaceSpec{
				Owner:        requester,
				PVCSize:      wsConfig.GetPVCSize(),
				StorageClass: wsConfig.GetStorageClass(),
				NodeSelector: wsConfig.GetNodeSelector(),
			},
		}
		if err := k8sClient.Create(ctx, ws); err != nil {
			return "", fmt.Errorf("creating workspace %q: %w", wsName, err)
		}
		a.log.Info("Auto-created workspace for build", "workspace", wsName, "requester", requester)
	} else {
		if ws.Spec.Owner != requester {
			return "", fmt.Errorf("workspace %q not found", wsName)
		}
	}

	// Forward workspace lease if flash is enabled and no explicit lease was provided
	if req.FlashEnabled && req.FlashLeaseName == "" && ws.Spec.LeaseID != "" {
		req.FlashLeaseName = ws.Spec.LeaseID
	}

	// Start file server in workspace pod and inject workspace_url for manifest use
	// (e.g. add_files: [{path: /usr/bin/foo, url: $workspace_url/my-binary}])
	if ws.Status.Phase == phaseRunning && ws.Status.PodName != "" && restCfg != nil {
		pod := &corev1.Pod{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ws.Status.PodName}, pod); err == nil && pod.Status.PodIP != "" {
			const wsFileServerPort = 9090
			// Redirect shell's own FDs first so runc exec doesn't block waiting for SPDY pipes.
			cmd := []string{"/bin/sh", "-c",
				fmt.Sprintf("exec 0</dev/null 1>/dev/null 2>/dev/null; python3 -m http.server %d -d /workspace &", wsFileServerPort)}
			if err := podExec(ctx, restCfg, namespace, ws.Status.PodName, workspaceContainerName, cmd, io.Discard); err != nil {
				a.log.Error(err, "Failed to start workspace file server", "workspace", wsName)
			} else {
				wsURL := fmt.Sprintf("http://%s:%d", pod.Status.PodIP, wsFileServerPort)
				req.CustomDefs = append(req.CustomDefs, fmt.Sprintf("workspace_url=%s", wsURL))
				a.log.Info("Workspace file server started", "workspace", wsName, "url", wsURL)
			}
		}
	}

	// Find existing build-cache PVC via labels, or create a new one
	buildCacheLabels := map[string]string{
		labels.Workspace: wsName,
		labels.Component: "build-cache",
	}
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := k8sClient.List(ctx, pvcList,
		client.InNamespace(namespace),
		client.MatchingLabels(buildCacheLabels),
	); err != nil {
		return "", fmt.Errorf("listing build-cache PVCs: %w", err)
	}
	for i := range pvcList.Items {
		if pvcList.Items[i].DeletionTimestamp == nil {
			return pvcList.Items[i].Name, nil
		}
	}

	// Determine cache PVC size and storage class from OperatorConfig
	cacheSize := "20Gi"
	var storageClassName *string
	if wsConfig != nil {
		if wsConfig.BuildCacheSize != "" {
			cacheSize = wsConfig.BuildCacheSize
		}
		if sc := wsConfig.GetStorageClass(); sc != "" {
			storageClassName = &sc
		}
	}
	cacheSizeQty, err := resource.ParseQuantity(cacheSize)
	if err != nil {
		return "", fmt.Errorf("invalid buildCacheSize %q: %w", cacheSize, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: wsName + "-build-cache-",
			Namespace:    namespace,
			Labels:       buildCacheLabels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: automotivev1alpha1.GroupVersion.String(),
					Kind:       "Workspace",
					Name:       ws.Name,
					UID:        ws.UID,
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: cacheSizeQty,
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, pvc); err != nil {
		return "", fmt.Errorf("creating build-cache PVC: %w", err)
	}

	a.log.Info("Created build-cache PVC", "workspace", wsName, "pvc", pvc.Name, "size", cacheSize)
	return pvc.Name, nil
}

// buildAIBSpec creates AIBSpec configuration from build request
func buildAIBSpec(req *BuildRequest, manifest, manifestFileName string, inputFilesServer bool) *automotivev1alpha1.AIBSpec {
	return &automotivev1alpha1.AIBSpec{
		Distro:           string(req.Distro),
		Target:           string(req.Target),
		Mode:             string(req.Mode),
		Manifest:         manifest,
		ManifestFileName: manifestFileName,
		Image:            req.AutomotiveImageBuilder,
		BuilderImage:     req.BuilderImage,
		RebuildBuilder:   req.RebuildBuilder,
		InputFilesServer: inputFilesServer,
		ContainerRef:     req.ContainerRef,
		CustomDefs:       req.CustomDefs,
		AIBExtraArgs:     req.AIBExtraArgs,
	}
}

// digestPinnedRef matches an OCI reference with a sha256 digest: image@sha256:<64 hex chars>
var digestPinnedRef = regexp.MustCompile(`^.+@sha256:[a-fA-F0-9]{64}$`)

// validateSecureBuild checks that the OperatorConfig has a valid digest-pinned taskBundleRef.
// Returns the validated ref, an HTTP status code, and error.
func resolveTaskBundleRef(ctx context.Context, k8sClient client.Client, namespace string, req *BuildRequest) (string, int, error) {
	if !req.SecureBuild {
		return "", 0, nil
	}
	if req.TaskBundleRef != "" {
		ref := strings.TrimSpace(req.TaskBundleRef)
		if !digestPinnedRef.MatchString(ref) {
			return "", http.StatusBadRequest, fmt.Errorf("taskBundleRef must be digest-pinned (image@sha256:<64 hex>), got %q", ref)
		}
		return ref, 0, nil
	}
	return validateSecureBuild(ctx, k8sClient, namespace)
}

func validateRestoreSourcesRef(req *BuildRequest) error {
	if req.RestoreSourcesRef == "" {
		return nil
	}
	ref := strings.TrimSpace(req.RestoreSourcesRef)
	if !digestPinnedRef.MatchString(ref) {
		return fmt.Errorf("restoreSourcesRef must be digest-pinned (image@sha256:<64 hex>), got %q", ref)
	}
	req.RestoreSourcesRef = ref
	return nil
}

func validateSecureBuild(ctx context.Context, k8sClient client.Client, namespace string) (string, int, error) {
	operatorConfig := &automotivev1alpha1.OperatorConfig{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "config", Namespace: namespace}, operatorConfig); err != nil {
		return "", http.StatusInternalServerError, fmt.Errorf("secureBuild requested but OperatorConfig could not be read: %v", err)
	}
	if operatorConfig.Spec.OSBuilds == nil || operatorConfig.Spec.OSBuilds.TaskBundleRef == "" {
		return "", http.StatusBadRequest, fmt.Errorf("secureBuild requested but OperatorConfig.spec.osBuilds.taskBundleRef is not set")
	}
	ref := strings.TrimSpace(operatorConfig.Spec.OSBuilds.TaskBundleRef)
	if !digestPinnedRef.MatchString(ref) {
		return "", http.StatusBadRequest, fmt.Errorf("secureBuild requires a digest-pinned taskBundleRef (must match image@sha256:<64 hex>), got %q", ref)
	}
	return ref, 0, nil
}

func (a *APIServer) createBuild(c *gin.Context) {
	ctx, span := apiTracer.Start(c.Request.Context(), "createBuild")
	defer span.End()
	c.Request = c.Request.WithContext(ctx)

	var req BuildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spanError(span, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request"})
		return
	}

	needsUpload := req.HasLocalFiles || manifestNeedsUpload(req.Manifest)

	if err := validateBuildRequest(&req); err != nil {
		spanError(span, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := applyBuildDefaults(&req); err != nil {
		spanError(span, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		spanError(span, err)
		return
	}

	namespace := resolveNamespace()

	effectiveTTL, ttlErr := resolveAndClampTTL(ctx, k8sClient, namespace, req.TTL)
	if ttlErr != nil {
		spanError(span, ttlErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": ttlErr.Error()})
		return
	}

	// Resolve --extra-repo workspace:path pairs into extra_repos custom defines
	if len(req.ExtraRepos) > 0 {
		restCfgForRepos, err := getRESTConfigFromRequest(c)
		if err != nil {
			spanError(span, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get kubernetes config"})
			return
		}
		if err := a.resolveExtraRepos(ctx, k8sClient, restCfgForRepos, &req); err != nil {
			spanError(span, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// Append a short random suffix to ensure unique names for parallel builds
	req.Name = fmt.Sprintf("%s-%s", req.Name, uuid.New().String()[:5])
	span.SetAttributes(attribute.String("build.name", req.Name))

	requestedBy := a.resolveRequester(c)

	taskBundleRef, bundleStatus, bundleErr := resolveTaskBundleRef(ctx, k8sClient, namespace, &req)
	if bundleErr != nil {
		spanError(span, bundleErr)
		c.JSON(bundleStatus, gin.H{"error": bundleErr.Error()})
		return
	}

	if err := validateRestoreSourcesRef(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve --workspace: create/find build-cache PVC, forward lease, start file server
	var buildCachePVCName string
	if req.Workspace != "" {
		restCfg, restErr := getRESTConfigFromRequest(c)
		if restErr != nil {
			spanError(span, restErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get kubernetes config"})
			return
		}
		pvcName, wsErr := a.resolveWorkspaceForBuild(ctx, k8sClient, restCfg, namespace, req.Workspace, requestedBy, &req)
		if wsErr != nil {
			spanError(span, wsErr)
			c.JSON(http.StatusBadRequest, gin.H{"error": wsErr.Error()})
			return
		}
		buildCachePVCName = pvcName
	}

	existing := &automotivev1alpha1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: namespace}, existing); err == nil {
		conflictErr := fmt.Errorf("ImageBuild %s already exists", req.Name)
		spanError(span, conflictErr)
		c.JSON(http.StatusConflict, gin.H{"error": conflictErr.Error()})
		return
	} else if !k8serrors.IsNotFound(err) {
		spanError(span, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error checking existing build: %v", err)})
		return
	}

	buildLabels := map[string]string{
		labels.ManagedBy:    labels.ValueBuildAPI,
		labels.PartOf:       labels.ValueAutomotiveDev,
		labels.CreatedBy:    labels.ValueBuildAPICreator,
		labels.Distro:       string(req.Distro),
		labels.Target:       string(req.Target),
		labels.Architecture: string(req.Architecture),
	}

	envSecretRef, pushSecretName, apiErr := a.resolveRegistryForBuild(ctx, c, k8sClient, namespace, &req)
	if apiErr != nil {
		spanError(span, apiErr)
		return
	}

	var flashSpec *automotivev1alpha1.FlashSpec
	var flashSecretName string
	if req.FlashEnabled {
		if req.FlashClientConfig == "" {
			flashErr := fmt.Errorf("flash enabled but client config is required")
			spanError(span, flashErr)
			c.JSON(http.StatusBadRequest, gin.H{"error": flashErr.Error()})
			return
		}
		flashSecretName = req.Name + "-jumpstarter-client"
		if err := createFlashClientSecret(ctx, k8sClient, namespace, flashSecretName, req.FlashClientConfig); err != nil {
			spanError(span, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating flash client secret: %v", err)})
			return
		}
		flashSpec = &automotivev1alpha1.FlashSpec{
			ClientConfigSecretRef: flashSecretName,
			LeaseDuration:         req.FlashLeaseDuration,
			LeaseName:             req.FlashLeaseName,
			FlashCmd:              req.FlashCmd,
			ExporterSelector:      req.FlashExporterSelector,
		}
	}

	traceID := extractTraceID(ctx)
	annotations := map[string]string{
		automotivev1alpha1.AnnotationRequestedBy: requestedBy,
		automotivev1alpha1.AnnotationTraceID:     traceID,
	}
	if req.Reproducible && taskBundleRef != "" {
		annotations[automotivev1alpha1.AnnotationTaskBundleRef] = taskBundleRef
	}

	imageBuild := &automotivev1alpha1.ImageBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   namespace,
			Labels:      buildLabels,
			Annotations: annotations,
		},
		Spec: automotivev1alpha1.ImageBuildSpec{
			Architecture:      string(req.Architecture),
			StorageClass:      req.StorageClass,
			SecretRef:         envSecretRef,
			PushSecretRef:     pushSecretName,
			AIB:               buildAIBSpec(&req, req.Manifest, req.ManifestFileName, needsUpload),
			Export:            buildExportSpec(&req),
			Flash:             flashSpec,
			BuildCachePVC:     buildCachePVCName,
			Workspace:         req.Workspace,
			SecureBuild:       req.SecureBuild,
			Reproducible:      req.Reproducible,
			TaskBundleRef:     taskBundleRef,
			RestoreSourcesRef: req.RestoreSourcesRef,
			TTL:               effectiveTTL,
		},
	}
	if err := k8sClient.Create(ctx, imageBuild); err != nil {
		spanError(span, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating ImageBuild: %v", err)})
		return
	}

	// Set owner references for cascading deletion
	if envSecretRef != "" {
		if err := setSecretOwnerRef(ctx, k8sClient, namespace, envSecretRef, imageBuild); err != nil {
			log.Printf(
				"WARNING: failed to set owner reference on registry secret %s: %v "+
					"(cleanup may require manual intervention)",
				envSecretRef, err,
			)
		}
	}

	if pushSecretName != "" {
		if err := setSecretOwnerRef(ctx, k8sClient, namespace, pushSecretName, imageBuild); err != nil {
			log.Printf(
				"WARNING: failed to set owner reference on push secret %s: %v "+
					"(cleanup may require manual intervention)",
				pushSecretName, err,
			)
		}
	}

	if flashSecretName != "" {
		if err := setSecretOwnerRef(ctx, k8sClient, namespace, flashSecretName, imageBuild); err != nil {
			log.Printf(
				"WARNING: failed to set owner reference on flash client secret %s: %v "+
					"(cleanup may require manual intervention)",
				flashSecretName, err,
			)
		}
	}

	writeJSON(c, http.StatusAccepted, BuildResponse{
		Name:        req.Name,
		Phase:       phaseBuilding,
		Message:     "Build triggered",
		RequestedBy: requestedBy,
		TraceID:     traceID,
	})
}

func listBuilds(c *gin.Context) {
	namespace := resolveNamespace()
	limit, offset := parsePagination(c)

	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	ctx := c.Request.Context()
	list := &automotivev1alpha1.ImageBuildList{}
	if err := k8sClient.List(ctx, list, client.InNamespace(namespace)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing builds: %v", err)})
		return
	}

	// Sort by creation time, newest first
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[j].CreationTimestamp.Before(&list.Items[i].CreationTimestamp)
	})

	// Paginate before doing per-item work (external route lookup, etc.)
	page := applyPagination(list.Items, limit, offset)

	// Resolve external route once for translating internal registry URLs
	externalRoute, _ := getExternalRegistryRoute(ctx, k8sClient, namespace)

	resp := make([]BuildListItem, 0, len(page))
	for _, b := range page {
		var startStr, compStr string
		if b.Status.StartTime != nil {
			startStr = b.Status.StartTime.Format(time.RFC3339)
		}
		if b.Status.CompletionTime != nil {
			compStr = b.Status.CompletionTime.Format(time.RFC3339)
		}

		containerImage := b.Spec.GetContainerPush()
		diskImage := b.Spec.GetExportOCI()
		if b.Spec.GetUseServiceAccountAuth() && externalRoute != "" {
			if containerImage != "" {
				containerImage = translateToExternalURL(containerImage, externalRoute)
			}
			if diskImage != "" {
				diskImage = translateToExternalURL(diskImage, externalRoute)
			}
		}

		resp = append(resp, BuildListItem{
			Name:           b.Name,
			Phase:          b.Status.Phase,
			Message:        b.Status.Message,
			RequestedBy:    b.Annotations[labels.RequestedBy],
			CreatedAt:      b.CreationTimestamp.Format(time.RFC3339),
			StartTime:      startStr,
			CompletionTime: compStr,
			ContainerImage: containerImage,
			DiskImage:      diskImage,
		})
	}
	writeJSON(c, http.StatusOK, resp)
}

func (a *APIServer) getBuild(c *gin.Context, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	ctx := c.Request.Context()
	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(ctx, c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}

	containerImage := build.Spec.GetContainerPush()
	diskImage := build.Spec.GetExportOCI()
	var warning string

	if build.Spec.GetUseServiceAccountAuth() {
		externalRoute, err := getExternalRegistryRoute(ctx, k8sClient, namespace)
		if err != nil {
			a.log.Error(err, "failed to resolve external registry route, returning internal URLs", "build", name)
			warning = fmt.Sprintf("external registry route lookup failed: %v; returning internal URLs", err)
		} else if externalRoute != "" {
			if containerImage != "" {
				containerImage = translateToExternalURL(containerImage, externalRoute)
			}
			if diskImage != "" {
				diskImage = translateToExternalURL(diskImage, externalRoute)
			}
		}
	}

	// For terminal builds, include Jumpstarter mapping so the CLI can show
	// manual flash guidance after successful or failed flash attempts.
	var jumpstarterInfo *JumpstarterInfo
	if isTerminalPhase(build.Status.Phase) {
		operatorConfig := &automotivev1alpha1.OperatorConfig{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "config", Namespace: namespace}, operatorConfig); err == nil {
			if operatorConfig.Status.JumpstarterAvailable {
				jumpstarterInfo = &JumpstarterInfo{Available: true}
				// Include lease ID if flash was executed
				if build.Status.LeaseID != "" {
					jumpstarterInfo.LeaseID = build.Status.LeaseID
				}
				if operatorConfig.Spec.Jumpstarter != nil {
					if mapping, ok := operatorConfig.Spec.Jumpstarter.TargetMappings[build.Spec.GetTarget()]; ok {
						jumpstarterInfo.ExporterSelector = mapping.Selector
						flashCmd := build.Spec.GetFlashCmd()
						if flashCmd == "" {
							flashCmd = mapping.FlashCmd
						}
						// Replace placeholders in flash command using translated URLs
						if flashCmd != "" {
							imageURI := diskImage
							if imageURI == "" {
								imageURI = containerImage
							}
							if imageURI != "" {
								flashCmd = strings.ReplaceAll(flashCmd, "{image_uri}", imageURI)
								flashCmd = strings.ReplaceAll(flashCmd, "{artifact_url}", imageURI)
							}
						}
						jumpstarterInfo.FlashCmd = flashCmd
					}
				}
			}
		}
	}

	// Mint a fresh registry token only for completed/failed internal registry builds
	// that belong to the requesting user
	var registryToken string
	requester := a.resolveRequester(c)
	buildOwner := build.Annotations[labels.RequestedBy]
	if requester == buildOwner &&
		build.Spec.GetUseServiceAccountAuth() &&
		isTerminalPhase(build.Status.Phase) {
		var tokenErr error
		tokenLifetime := resolveTokenLifetime(ctx, k8sClient, namespace)
		registryToken, _, tokenErr = a.mintRegistryToken(ctx, c, namespace, tokenLifetime)
		if tokenErr != nil {
			a.log.Error(tokenErr, "failed to mint registry token", "build", name)
			tokenWarning := fmt.Sprintf("failed to mint registry token: %v", tokenErr)
			if warning != "" {
				warning = warning + "; " + tokenWarning
			} else {
				warning = tokenWarning
			}
		}
	}

	writeJSON(c, http.StatusOK, BuildResponse{
		Name:        build.Name,
		Phase:       build.Status.Phase,
		Message:     build.Status.Message,
		RequestedBy: build.Annotations[labels.RequestedBy],
		TraceID:     build.Annotations[automotivev1alpha1.AnnotationTraceID],
		StartTime: func() string {
			if build.Status.StartTime != nil {
				return build.Status.StartTime.Format(time.RFC3339)
			}
			return ""
		}(),
		CompletionTime: func() string {
			if build.Status.CompletionTime != nil {
				return build.Status.CompletionTime.Format(time.RFC3339)
			}
			return ""
		}(),
		ContainerImage: containerImage,
		DiskImage:      diskImage,
		RegistryToken:  registryToken,
		Warning:        warning,
		ExpiresAt: func() string {
			if build.Status.ExpiresAt != nil {
				return build.Status.ExpiresAt.Format(time.RFC3339)
			}
			return ""
		}(),
		Jumpstarter: jumpstarterInfo,
		Parameters: &BuildParameters{
			Architecture:           build.Spec.Architecture,
			Distro:                 build.Spec.GetDistro(),
			Target:                 build.Spec.GetTarget(),
			Mode:                   build.Spec.GetMode(),
			ExportFormat:           build.Spec.GetExportFormat(),
			Compression:            build.Spec.GetCompression(),
			StorageClass:           build.Spec.StorageClass,
			AutomotiveImageBuilder: build.Spec.GetAIBImage(),
			BuilderImage:           build.Spec.GetBuilderImage(),
			ContainerRef:           build.Spec.GetContainerRef(),
			BuildDiskImage:         build.Spec.GetBuildDiskImage(),
			FlashEnabled:           build.Spec.IsFlashEnabled(),
			FlashLeaseDuration:     build.Spec.GetFlashLeaseDuration(),
			FlashLeaseName:         build.Spec.GetFlashLeaseName(),
			UseServiceAccountAuth:  build.Spec.GetUseServiceAccountAuth(),
		},
	})
}

// getBuildTemplate returns a BuildRequest-like struct representing the inputs that produced a given build
func getBuildTemplate(c *gin.Context, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}

	ctx := c.Request.Context()
	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(ctx, c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}

	manifest := build.Spec.GetManifest()
	manifestFileName := build.Spec.GetManifestFileName()
	if manifestFileName == "" {
		manifestFileName = "manifest.aib.yml"
	}

	sourceFiles := extractManifestSourceFiles(manifest)

	writeJSON(c, http.StatusOK, BuildTemplateResponse{
		BuildRequest: BuildRequest{
			Name:                   build.Name,
			Manifest:               manifest,
			ManifestFileName:       manifestFileName,
			Distro:                 Distro(build.Spec.GetDistro()),
			Target:                 Target(build.Spec.GetTarget()),
			Architecture:           Architecture(build.Spec.Architecture),
			ExportFormat:           ExportFormat(build.Spec.GetExportFormat()),
			Mode:                   Mode(build.Spec.GetMode()),
			AutomotiveImageBuilder: build.Spec.GetAIBImage(),
			CustomDefs:             build.Spec.GetCustomDefs(),
			AIBExtraArgs:           build.Spec.GetAIBExtraArgs(),
			Compression:            Compression(build.Spec.GetCompression()),
			SecureBuild:            build.Spec.SecureBuild,
			Reproducible:           build.Spec.Reproducible,
			TaskBundleRef:          build.Spec.TaskBundleRef,
			RestoreSourcesRef:      build.Spec.RestoreSourcesRef,
			TTL:                    build.Spec.GetTTL(),
		},
		SourceFiles: sourceFiles,
	})
}

// manifestAddFile represents a single add_files entry from an AIB manifest.
type manifestAddFile struct {
	SourcePath string `yaml:"source_path"`
	SourceGlob string `yaml:"source_glob"`
	Source     string `yaml:"source"`
}

// manifestContent represents the content section of an AIB manifest.
type manifestContent struct {
	AddFiles []manifestAddFile `yaml:"add_files"`
}

// manifestSchema is a minimal schema for parsing add_files from AIB manifests.
type manifestSchema struct {
	Content manifestContent `yaml:"content"`
	QM      struct {
		Content manifestContent `yaml:"content"`
	} `yaml:"qm"`
}

// manifestNeedsUpload parses the manifest YAML and returns true if any
// add_files entry references local files via source_path.
// Note: source_glob is intentionally excluded — only the client can determine
// whether a glob expands to actual files. This fallback exists for backward
// compatibility with older clients that don't send HasLocalFiles.
func manifestNeedsUpload(manifest string) bool {
	var m manifestSchema
	if err := yaml.Unmarshal([]byte(manifest), &m); err != nil {
		log.Printf("warning: failed to parse manifest for upload detection: %v", err)
		return false
	}
	for _, sections := range [][]manifestAddFile{m.Content.AddFiles, m.QM.Content.AddFiles} {
		for _, f := range sections {
			if f.SourcePath != "" {
				return true
			}
		}
	}
	return false
}

// extractManifestSourceFiles parses the manifest YAML and returns the list of
// relative, non-HTTP source references (source, source_path, source_glob).
func extractManifestSourceFiles(manifest string) []string {
	var m manifestSchema
	if err := yaml.Unmarshal([]byte(manifest), &m); err != nil {
		return nil
	}
	var files []string
	for _, sections := range [][]manifestAddFile{m.Content.AddFiles, m.QM.Content.AddFiles} {
		for _, f := range sections {
			for _, p := range []string{f.Source, f.SourcePath, f.SourceGlob} {
				if p != "" && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "http") {
					files = append(files, p)
				}
			}
		}
	}
	return files
}

// uploadContext holds the context needed for file upload operations.
type uploadContext struct {
	ctx       context.Context
	restCfg   *rest.Config
	namespace string
	podName   string
	container string
	limits    *APILimits
}

// processFilePartResult contains the result of processing a single file part.
type processFilePartResult struct {
	bytesWritten int64
}

// validateDestPath checks if the destination path is safe for upload.
func validateDestPath(dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("missing destination filename")
	}
	if !safeFilename(dest) {
		return "", fmt.Errorf("invalid destination filename: %s", dest)
	}
	// Root the path so path.Clean resolves all ".." without escaping,
	// then strip the leading "/" to make it relative to /workspace/shared/.
	cleanDest := strings.TrimPrefix(path.Clean("/"+dest), "/")
	if cleanDest == "" || cleanDest == "." {
		return "", fmt.Errorf("invalid destination path: %s", dest)
	}
	return cleanDest, nil
}

// processFilePart handles a single file part from the multipart upload.
func processFilePart(part *multipart.Part, pendingPath string, uctx *uploadContext) (processFilePartResult, error) {
	dest := pendingPath
	if dest == "" {
		dest = strings.TrimSpace(part.FileName())
	}

	cleanDest, err := validateDestPath(dest)
	if err != nil {
		return processFilePartResult{}, err
	}

	tmp, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return processFilePartResult{}, err
	}
	tmpName := tmp.Name()
	defer func() {
		if closeErr := tmp.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close temp file: %v\n", closeErr)
		}
	}()
	defer func() {
		if removeErr := os.Remove(tmpName); removeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove temp file: %v\n", removeErr)
		}
	}()

	limitedReader := io.LimitReader(part, uctx.limits.MaxUploadFileSize+1)
	n, err := io.Copy(tmp, limitedReader)
	if err != nil {
		return processFilePartResult{}, err
	}
	if n > uctx.limits.MaxUploadFileSize {
		return processFilePartResult{}, fmt.Errorf("file %s exceeds maximum size (%d bytes)", dest, uctx.limits.MaxUploadFileSize)
	}

	destPath := "/workspace/shared/" + cleanDest
	if err := copyFileToPod(uctx.ctx, uctx.restCfg, uctx.namespace, uctx.podName, uctx.container, tmpName, destPath); err != nil {
		return processFilePartResult{}, fmt.Errorf("stream to pod failed: %w", err)
	}

	return processFilePartResult{bytesWritten: n}, nil
}

// findRunningUploadPod finds a running upload pod for the given build.
func findRunningUploadPod(ctx context.Context, k8sClient client.Client, namespace, buildName string) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			labels.ImageBuildName: buildName,
			labels.Name:           "upload-pod",
		},
	); err != nil {
		return nil, fmt.Errorf("error listing upload pods: %w", err)
	}
	for i := range podList.Items {
		p := &podList.Items[i]
		if p.Status.Phase == corev1.PodRunning {
			return p, nil
		}
	}
	return nil, nil
}

func (a *APIServer) uploadFiles(c *gin.Context, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getK8sClientOrFail(c)
	if err != nil {
		return
	}
	build := &automotivev1alpha1.ImageBuild{}
	if err := getResourceOrFail(c.Request.Context(), c, k8sClient, name, namespace, build, "build"); err != nil {
		return
	}
	uploadPod, err := findRunningUploadPod(c.Request.Context(), k8sClient, namespace, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if uploadPod == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "upload pod not ready"})
		return
	}

	if c.Request.ContentLength > a.limits.MaxTotalUploadSize {
		errMsg := fmt.Sprintf("upload too large (max %d bytes)", a.limits.MaxTotalUploadSize)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": errMsg})
		return
	}

	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid multipart: %v", err)})
		return
	}

	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rest config: %v", err)})
		return
	}

	uctx := &uploadContext{
		ctx:       c.Request.Context(),
		restCfg:   restCfg,
		namespace: namespace,
		podName:   uploadPod.Name,
		container: uploadPod.Spec.Containers[0].Name,
		limits:    &a.limits,
	}

	var totalBytesUploaded int64
	var pendingPath string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("read part: %v", err)})
			return
		}

		// Handle "path" field - stores the destination path for the next file
		if part.FormName() == "path" {
			pathBytes, err := io.ReadAll(io.LimitReader(part, 4096))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("read path: %v", err)})
				return
			}
			pendingPath = strings.TrimSpace(string(pathBytes))
			continue
		}

		if part.FormName() != "file" {
			continue
		}

		result, err := processFilePart(part, pendingPath, uctx)
		pendingPath = ""
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		totalBytesUploaded += result.bytesWritten
		if totalBytesUploaded > a.limits.MaxTotalUploadSize {
			errMsg := fmt.Sprintf("total upload size exceeds maximum (%d bytes)", a.limits.MaxTotalUploadSize)
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": errMsg})
			return
		}
	}

	original := build
	patched := original.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations[labels.UploadsComplete] = labels.ValueTrue
	if err := k8sClient.Patch(c.Request.Context(), patched, client.MergeFrom(original)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("mark complete failed: %v", err)})
		return
	}
	writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func copyFileToPod(ctx context.Context, config *rest.Config, namespace, podName, containerName, localPath, podPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close file: %v\n", err)
		}
	}()

	// Stream raw file bytes via stdin; the pod-side command writes them directly.
	// Uses only sh + cat (available in ubi-minimal), no tar dependency.
	cmd := []string{"/bin/sh", "-c", "mkdir -p \"$(dirname \"$1\")\" && cat > \"$1\" && chmod 0600 \"$1\"", "--", podPath}

	executor, err := newPodExecExecutorFn(config, namespace, podName, containerName, cmd)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	streamOpts := remotecommand.StreamOptions{Stdin: f, Stdout: io.Discard, Stderr: &stderr}
	if err := executor.StreamWithContext(ctx, streamOpts); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("copy to pod: %w (stderr: %s)", err, stderr.String())
		}
		return err
	}
	return nil
}
func (a *APIServer) handleGetOperatorConfig(c *gin.Context) {
	ctx := c.Request.Context()
	reqID, _ := c.Get("reqID")

	a.log.Info("getting operator config", "reqID", reqID)

	k8sClient, err := getClientFromRequestFn(c)
	if err != nil {
		a.log.Error(err, "failed to get k8s client", "reqID", reqID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create Kubernetes client"})
		return
	}

	namespace := resolveNamespace()

	operatorConfig, err := loadOperatorConfigFn(ctx, k8sClient, namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			a.log.Info("OperatorConfig not found; returning empty operator config response", "reqID", reqID, "namespace", namespace)
			c.JSON(http.StatusOK, OperatorConfigResponse{})
			return
		}
		a.log.Error(err, "failed to get OperatorConfig", "reqID", reqID, "namespace", namespace)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get operator configuration"})
		return
	}

	// Build the response with Jumpstarter target mappings (flash-specific, from CRD)
	response := OperatorConfigResponse{}

	if operatorConfig.Spec.Jumpstarter != nil && len(operatorConfig.Spec.Jumpstarter.TargetMappings) > 0 {
		response.JumpstarterTargets = make(map[string]JumpstarterTarget)
		for target, mapping := range operatorConfig.Spec.Jumpstarter.TargetMappings {
			response.JumpstarterTargets[target] = JumpstarterTarget{
				Selector: mapping.Selector,
				FlashCmd: mapping.FlashCmd,
			}
		}
	}

	// Load build defaults from target-defaults ConfigMap
	targetDefaults, err := loadTargetDefaultsFn(ctx, k8sClient, namespace)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			a.log.Error(err, "failed to load target defaults ConfigMap", "reqID", reqID, "namespace", namespace)
		}
		// Non-fatal: continue without target defaults
	} else if len(targetDefaults) > 0 {
		response.TargetDefaults = targetDefaults
	}

	c.JSON(http.StatusOK, response)
}
