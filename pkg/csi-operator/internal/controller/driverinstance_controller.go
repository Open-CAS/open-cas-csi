/*
Copyright 2024 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	opencasv1alpha1 "opencas-csi/pkg/csi-operator/api/v1alpha1"
	"opencas-csi/pkg/csi-operator/internal/helpers"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
)

// DriverInstanceReconciler reconciles a DriverInstance object
type DriverInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Mutex  sync.Mutex
}

//+kubebuilder:rbac:groups=csi.open-cas.com,resources=driverinstances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=csi.open-cas.com,resources=driverinstances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=csi.open-cas.com,resources=driverinstances/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *DriverInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	di := &opencasv1alpha1.DriverInstance{}

	if err := r.Get(ctx, req.NamespacedName, di); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}
	isDeleted := !di.GetDeletionTimestamp().IsZero()

	logger := log.FromContext(ctx).WithName("driver-instance-reconcile")
	if isDeleted {
		logger.Info("instance cleanup started")
	} else {
		logger.Info("loop started")
	}

	// add finalizer if cache resource is not being deleted
	if !isDeleted {
		if err := r.AddFinalizer(ctx, di); err != nil {
			logger.Error(err, fmt.Sprintf("failed to add finalizer to cache resource %q", di.GetName()))
		}
	}

	// DRIVER
	objects := r.getObjectsList(di)
	if isDeleted {
		// if deleted reverse order of handling resources
		slices.Reverse(objects)
	}
	for _, obj := range objects {
		logger.Info(fmt.Sprintf("%s/%s: %s", obj.GetNamespace(), obj.GetName(), obj.GetObjectKind().GroupVersionKind().String()))
		if err := r.handleObject(ctx, di, obj); err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		logger.Info(fmt.Sprintf("%v", obj.GetOwnerReferences()))
	}

	// remove finalizer if no errors occurred
	if isDeleted {
		if err := r.RemoveFinalizer(ctx, di); err != nil {
			logger.Error(err, "failed to remove finalizer from driver instance resource")
			return ctrl.Result{RequeueAfter: helpers.DefaultRequequeAfter}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *DriverInstanceReconciler) getObjectsList(di *opencasv1alpha1.DriverInstance) []client.Object {
	// prepare objects to handle in order
	objects := []client.Object{}

	objects = append(objects, helpers.GetNamespaceConfiguration(di))
	objects = append(objects, helpers.GetServiceAccountConfiguration(di))
	for _, role := range helpers.GetRolesConfiguration(di) {
		objects = append(objects, role)
	}
	for _, roleBinding := range helpers.GetRoleBindingsConfiguration(di) {
		objects = append(objects, roleBinding)
	}
	for _, clusterRole := range helpers.GetClusterRolesConfiguration(di) {
		objects = append(objects, clusterRole)
	}
	for _, clusterRoleBinding := range helpers.GetClusterRoleBindingsConfiguration(di) {
		objects = append(objects, clusterRoleBinding)
	}
	objects = append(objects, helpers.GetCSIDriverConfiguration(di))
	objects = append(objects, helpers.GetConfigMapConfiguration(di))
	objects = append(objects, helpers.GetControllerDeploymentConfiguration(di))
	objects = append(objects, helpers.GetNodeDaemonSetConfiguration(di))

	return objects
}

func (r *DriverInstanceReconciler) handleObject(ctx context.Context, di *opencasv1alpha1.DriverInstance, obj client.Object) error {
	isDeleted := !di.GetDeletionTimestamp().IsZero()

	if err := ctrl.SetControllerReference(di, obj, r.Scheme); err != nil {
		return err
	}

	switch obj.(type) {
	case *appsv1.DaemonSet, *appsv1.Deployment:
	default:
		controllerutil.AddFinalizer(obj, helpers.Finalizer)
	}

	getObj := helpers.GetNewObject(obj)
	if err := r.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, getObj); err != nil {
		if apierrors.IsNotFound(err) {
			if isDeleted {
				return nil
			}

			// create owned object
			return r.Create(ctx, obj, &client.CreateOptions{})
		}

		return err
	}

	if isDeleted {
		switch obj.(type) {
		case *appsv1.DaemonSet, *appsv1.Deployment:
			return nil
		default:
			if controllerutil.RemoveFinalizer(getObj, helpers.Finalizer) {
				return r.Update(ctx, getObj)
			}
			return nil
		}
	}

	// apply current configuration
	obj.SetResourceVersion(getObj.GetResourceVersion())
	return r.Patch(ctx, obj, client.MergeFrom(getObj))
}

func (r *DriverInstanceReconciler) RemoveFinalizer(ctx context.Context, di *opencasv1alpha1.DriverInstance) error {
	patch := client.MergeFrom(di.DeepCopy())
	if controllerutil.RemoveFinalizer(di, helpers.Finalizer) {
		return r.Patch(ctx, di, patch)
	}
	return nil
}

func (r *DriverInstanceReconciler) AddFinalizer(ctx context.Context, di *opencasv1alpha1.DriverInstance) error {
	patch := client.MergeFrom(di.DeepCopy())
	if controllerutil.AddFinalizer(di, helpers.Finalizer) {
		return r.Patch(ctx, di, patch)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DriverInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opencasv1alpha1.DriverInstance{}).
		Owns(&corev1.Namespace{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&rbacv1.ClusterRole{}).
		Owns(&rbacv1.ClusterRoleBinding{}).
		Owns(&storagev1.CSIDriver{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.DaemonSet{}).
		WithEventFilter(
			predicate.And(
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					predicate.LabelChangedPredicate{}),
				predicate.Funcs{
					CreateFunc: func(ce event.CreateEvent) bool {
						_, ok := ce.Object.(*opencasv1alpha1.DriverInstance)
						return ok
					},
				})).
		Complete(r)
}
