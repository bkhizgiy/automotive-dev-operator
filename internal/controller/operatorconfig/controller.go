package operatorconfig

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	automotivev1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1"
)

const (
	operatorNamespace = "automotive-dev-operator-system"
	finalizerName     = "operatorconfig.automotive.sdv.cloud.redhat.com/finalizer"
)

// OperatorConfigReconciler reconciles an OperatorConfig object
type OperatorConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=operatorconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=operatorconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=operatorconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete

func (r *OperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("operatorconfig", req.NamespacedName)
	log.Info("=== Reconciliation started ===")

	config := &automotivev1.OperatorConfig{}
	if err := r.Get(ctx, req.NamespacedName, config); err != nil {
		if errors.IsNotFound(err) {
			log.Info("OperatorConfig not found, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get OperatorConfig")
		return ctrl.Result{}, err
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(config, finalizerName) {
		log.Info("Adding finalizer")
		controllerutil.AddFinalizer(config, finalizerName)
		if err := r.Update(ctx, config); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		log.Info("Finalizer added, requeuing")
		// Requeue to avoid doing more work in this reconciliation
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion
	if !config.DeletionTimestamp.IsZero() {
		log.Info("Handling deletion")
		if err := r.cleanupWebUI(ctx); err != nil {
			log.Error(err, "Failed to cleanup WebUI")
			return ctrl.Result{}, err
		}
		log.Info("Removing finalizer")
		controllerutil.RemoveFinalizer(config, finalizerName)
		if err := r.Update(ctx, config); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
		log.Info("Deletion completed successfully")
		return ctrl.Result{}, nil
	}

	// Reconcile WebUI
	log.Info("Processing WebUI configuration", "webUI", config.Spec.WebUI, "generation", config.Generation)
	statusChanged := false
	if config.Spec.WebUI {
		if err := r.deployWebUI(ctx, config); err != nil {
			log.Error(err, "Failed to deploy WebUI")
			if config.Status.Phase != "Failed" || config.Status.WebUIDeployed {
				config.Status.Phase = "Failed"
				config.Status.Message = fmt.Sprintf("Failed to deploy WebUI: %v", err)
				config.Status.WebUIDeployed = false
				statusChanged = true
			}
			if statusChanged {
				_ = r.Status().Update(ctx, config)
			}
			return ctrl.Result{}, err
		}
		if config.Status.Phase != "Ready" || !config.Status.WebUIDeployed {
			config.Status.Phase = "Ready"
			config.Status.Message = "WebUI deployed successfully"
			config.Status.WebUIDeployed = true
			statusChanged = true
		}
	} else {
		if err := r.cleanupWebUI(ctx); err != nil {
			log.Error(err, "Failed to cleanup WebUI")
			if config.Status.Phase != "Failed" {
				config.Status.Phase = "Failed"
				config.Status.Message = fmt.Sprintf("Failed to cleanup WebUI: %v", err)
				statusChanged = true
			}
			if statusChanged {
				_ = r.Status().Update(ctx, config)
			}
			return ctrl.Result{}, err
		}
		if config.Status.Phase != "Ready" || config.Status.WebUIDeployed {
			config.Status.Phase = "Ready"
			config.Status.Message = "WebUI disabled"
			config.Status.WebUIDeployed = false
			statusChanged = true
		}
	}

	if statusChanged {
		log.Info("Updating status", "phase", config.Status.Phase, "webUIDeployed", config.Status.WebUIDeployed)
		if err := r.Status().Update(ctx, config); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	}

	log.Info("=== Reconciliation completed successfully ===")
	return ctrl.Result{}, nil
}

func (r *OperatorConfigReconciler) deployWebUI(ctx context.Context, owner *automotivev1.OperatorConfig) error {
	r.Log.Info("Starting WebUI deployment")

	// Create cookie secrets for OAuth proxies
	r.Log.Info("Ensuring OAuth secrets")
	if err := r.ensureOAuthSecrets(ctx, owner); err != nil {
		r.Log.Error(err, "Failed to ensure OAuth secrets")
		return fmt.Errorf("failed to ensure OAuth secrets: %w", err)
	}
	r.Log.Info("OAuth secrets ensured successfully")

	// Update ServiceAccount with OAuth redirect annotations
	r.Log.Info("Updating ServiceAccount OAuth annotations")
	if err := r.updateServiceAccountOAuthAnnotations(ctx); err != nil {
		r.Log.Error(err, "Failed to update ServiceAccount OAuth annotations")
		return fmt.Errorf("failed to update ServiceAccount OAuth annotations: %w", err)
	}
	r.Log.Info("ServiceAccount OAuth annotations updated successfully")

	// Create/update nginx ConfigMap
	r.Log.Info("Creating/updating nginx ConfigMap")
	nginxConfigMap := r.buildWebUINginxConfigMap()
	if err := r.createOrUpdate(ctx, nginxConfigMap, owner); err != nil {
		r.Log.Error(err, "Failed to create/update nginx configmap")
		return fmt.Errorf("failed to create/update nginx configmap: %w", err)
	}
	r.Log.Info("Nginx ConfigMap created/updated successfully")

	// Create/update deployment
	r.Log.Info("Creating/updating webui deployment")
	deployment := r.buildWebUIDeployment()
	if err := r.createOrUpdate(ctx, deployment, owner); err != nil {
		r.Log.Error(err, "Failed to create/update webui deployment")
		return fmt.Errorf("failed to create/update webui deployment: %w", err)
	}
	r.Log.Info("WebUI deployment created/updated successfully")

	// Create/update service
	r.Log.Info("Creating/updating webui service")
	service := r.buildWebUIService()
	if err := r.createOrUpdate(ctx, service, owner); err != nil {
		r.Log.Error(err, "Failed to create/update webui service")
		return fmt.Errorf("failed to create/update webui service: %w", err)
	}
	r.Log.Info("WebUI service created/updated successfully")

	// Create/update route
	r.Log.Info("Creating/updating webui route")
	route := r.buildWebUIRoute()
	if err := r.createOrUpdate(ctx, route, owner); err != nil {
		r.Log.Error(err, "Failed to create/update webui route")
		return fmt.Errorf("failed to create/update webui route: %w", err)
	}
	r.Log.Info("WebUI route created/updated successfully")

	// Create/update build-api deployment
	r.Log.Info("Creating/updating build-api deployment")
	buildAPIDeployment := r.buildBuildAPIDeployment()
	if err := r.createOrUpdate(ctx, buildAPIDeployment, owner); err != nil {
		r.Log.Error(err, "Failed to create/update build-api deployment")
		return fmt.Errorf("failed to create/update build-api deployment: %w", err)
	}
	r.Log.Info("Build-API deployment created/updated successfully")

	// Create/update build-api service
	r.Log.Info("Creating/updating build-api service")
	buildAPIService := r.buildBuildAPIService()
	if err := r.createOrUpdate(ctx, buildAPIService, owner); err != nil {
		r.Log.Error(err, "Failed to create/update build-api service")
		return fmt.Errorf("failed to create/update build-api service: %w", err)
	}
	r.Log.Info("Build-API service created/updated successfully")

	// Create/update build-api route
	r.Log.Info("Creating/updating build-api route")
	buildAPIRoute := r.buildBuildAPIRoute()
	if err := r.createOrUpdate(ctx, buildAPIRoute, owner); err != nil {
		r.Log.Error(err, "Failed to create/update build-api route")
		return fmt.Errorf("failed to create/update build-api route: %w", err)
	}
	r.Log.Info("Build-API route created/updated successfully")

	r.Log.Info("WebUI deployment completed successfully")
	return nil
}

func (r *OperatorConfigReconciler) ensureOAuthSecrets(ctx context.Context, owner *automotivev1.OperatorConfig) error {
	secrets := []string{"ado-webui-oauth-proxy", "ado-build-api-oauth-proxy"}

	for _, secretName := range secrets {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: secretName, Namespace: operatorNamespace}, secret)

		if err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to get secret %s: %w", secretName, err)
			}

			// Secret doesn't exist, create it
			secret = r.buildOAuthSecret(secretName)
			// Don't set controller reference - cleanup handled by finalizer
			if err := r.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create secret %s: %w", secretName, err)
			}
			r.Log.Info("Created OAuth secret", "name", secretName)
		}
	}

	return nil
}

func (r *OperatorConfigReconciler) updateServiceAccountOAuthAnnotations(ctx context.Context) error {
	sa := &corev1.ServiceAccount{}
	err := r.Get(ctx, client.ObjectKey{Name: "ado-controller-manager", Namespace: operatorNamespace}, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			// ServiceAccount doesn't exist (likely running locally in dev mode)
			r.Log.Info("ServiceAccount not found, skipping OAuth annotation update (running locally?)")
			return nil
		}
		return fmt.Errorf("failed to get ServiceAccount: %w", err)
	}

	if sa.Annotations == nil {
		sa.Annotations = make(map[string]string)
	}

	annotations := map[string]string{
		"serviceaccounts.openshift.io/oauth-redirectreference.webui":    `{"kind":"OAuthRedirectReference","apiVersion":"v1","reference":{"kind":"Route","name":"ado-webui"}}`,
		"serviceaccounts.openshift.io/oauth-redirectreference.buildapi": `{"kind":"OAuthRedirectReference","apiVersion":"v1","reference":{"kind":"Route","name":"ado-build-api"}}`,
	}

	updated := false
	for key, value := range annotations {
		if sa.Annotations[key] != value {
			sa.Annotations[key] = value
			updated = true
		}
	}

	if updated {
		if err := r.Update(ctx, sa); err != nil {
			return fmt.Errorf("failed to update ServiceAccount annotations: %w", err)
		}
		r.Log.Info("Updated ServiceAccount OAuth annotations")
	}

	return nil
}

func (r *OperatorConfigReconciler) cleanupWebUI(ctx context.Context) error {
	// Delete deployment
	deployment := &appsv1.Deployment{}
	deployment.Name = "ado-webui"
	deployment.Namespace = operatorNamespace
	if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete webui deployment: %w", err)
	}

	// Delete service
	service := &corev1.Service{}
	service.Name = "ado-webui"
	service.Namespace = operatorNamespace
	if err := r.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete webui service: %w", err)
	}

	// Delete route
	route := &routev1.Route{}
	route.Name = "ado-webui"
	route.Namespace = operatorNamespace
	if err := r.Delete(ctx, route); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete webui route: %w", err)
	}

	// Delete nginx ConfigMap
	configMap := &corev1.ConfigMap{}
	configMap.Name = "ado-webui-nginx-config"
	configMap.Namespace = operatorNamespace
	if err := r.Delete(ctx, configMap); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete nginx configmap: %w", err)
	}

	// Delete build-api deployment
	buildAPIDeployment := &appsv1.Deployment{}
	buildAPIDeployment.Name = "ado-build-api"
	buildAPIDeployment.Namespace = operatorNamespace
	if err := r.Delete(ctx, buildAPIDeployment); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete build-api deployment: %w", err)
	}

	// Delete build-api service
	buildAPIService := &corev1.Service{}
	buildAPIService.Name = "ado-build-api"
	buildAPIService.Namespace = operatorNamespace
	if err := r.Delete(ctx, buildAPIService); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete build-api service: %w", err)
	}

	// Delete build-api route
	buildAPIRoute := &routev1.Route{}
	buildAPIRoute.Name = "ado-build-api"
	buildAPIRoute.Namespace = operatorNamespace
	if err := r.Delete(ctx, buildAPIRoute); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete build-api route: %w", err)
	}

	// Delete OAuth secrets
	secrets := []string{"ado-webui-oauth-proxy", "ado-build-api-oauth-proxy"}
	for _, secretName := range secrets {
		secret := &corev1.Secret{}
		secret.Name = secretName
		secret.Namespace = operatorNamespace
		if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete secret %s: %w", secretName, err)
		}
	}

	return nil
}

func (r *OperatorConfigReconciler) createOrUpdate(ctx context.Context, obj client.Object, owner *automotivev1.OperatorConfig) error {
	// Try to get the existing resource
	key := client.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(client.Object)

	err := r.Get(ctx, key, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new resource
			return r.Create(ctx, obj)
		}
		return err
	}

	// Resource exists, update it
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

func (r *OperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.OperatorConfig{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
