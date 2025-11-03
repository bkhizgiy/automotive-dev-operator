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
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete

func (r *OperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("operatorconfig", req.NamespacedName)

	config := &automotivev1.OperatorConfig{}
	if err := r.Get(ctx, req.NamespacedName, config); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(config, finalizerName) {
		controllerutil.AddFinalizer(config, finalizerName)
		if err := r.Update(ctx, config); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Handle deletion
	if !config.DeletionTimestamp.IsZero() {
		if err := r.cleanupWebUI(ctx); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(config, finalizerName)
		if err := r.Update(ctx, config); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Reconcile WebUI
	if config.Spec.WebUI {
		if err := r.deployWebUI(ctx, config); err != nil {
			log.Error(err, "Failed to deploy WebUI")
			config.Status.Phase = "Failed"
			config.Status.Message = fmt.Sprintf("Failed to deploy WebUI: %v", err)
			config.Status.WebUIDeployed = false
			_ = r.Status().Update(ctx, config)
			return ctrl.Result{}, err
		}
		config.Status.Phase = "Ready"
		config.Status.Message = "WebUI deployed successfully"
		config.Status.WebUIDeployed = true
	} else {
		if err := r.cleanupWebUI(ctx); err != nil {
			log.Error(err, "Failed to cleanup WebUI")
			config.Status.Phase = "Failed"
			config.Status.Message = fmt.Sprintf("Failed to cleanup WebUI: %v", err)
			_ = r.Status().Update(ctx, config)
			return ctrl.Result{}, err
		}
		config.Status.Phase = "Ready"
		config.Status.Message = "WebUI disabled"
		config.Status.WebUIDeployed = false
	}

	if err := r.Status().Update(ctx, config); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *OperatorConfigReconciler) deployWebUI(ctx context.Context, owner *automotivev1.OperatorConfig) error {
	// Create/update deployment
	deployment := r.buildWebUIDeployment()
	if err := r.createOrUpdate(ctx, deployment, owner); err != nil {
		return fmt.Errorf("failed to create/update webui deployment: %w", err)
	}

	// Create/update service
	service := r.buildWebUIService()
	if err := r.createOrUpdate(ctx, service, owner); err != nil {
		return fmt.Errorf("failed to create/update webui service: %w", err)
	}

	// Create/update route
	route := r.buildWebUIRoute()
	if err := r.createOrUpdate(ctx, route, owner); err != nil {
		return fmt.Errorf("failed to create/update webui route: %w", err)
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

	return nil
}

func (r *OperatorConfigReconciler) createOrUpdate(ctx context.Context, obj client.Object, owner *automotivev1.OperatorConfig) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)

	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		// Create new resource
		if err := ctrl.SetControllerReference(owner, obj, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, obj)
	}

	// Update existing resource
	obj.SetResourceVersion(existing.GetResourceVersion())
	if err := ctrl.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return err
	}
	return r.Update(ctx, obj)
}

func (r *OperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.OperatorConfig{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&routev1.Route{}).
		Complete(r)
}
