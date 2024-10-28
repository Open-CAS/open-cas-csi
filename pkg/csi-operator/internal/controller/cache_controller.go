/*
Copyright 2024The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package controller

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	opencasv1alpha1 "opencas-csi/pkg/csi-operator/api/v1alpha1"
	"opencas-csi/pkg/csi-operator/internal/helpers"
	"opencas-csi/pkg/k8sutils"
	"opencas-csi/pkg/opencas"
)

// CacheReconciler reconciles a Cache object
type CacheReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	NodeMutex keymutex.KeyMutex
}

//+kubebuilder:rbac:groups=csi.open-cas.com,resources=caches,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=csi.open-cas.com,resources=caches/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=csi.open-cas.com,resources=caches/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

func (r *CacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.NodeMutex.LockKey(req.NamespacedName.Name)
	defer r.NodeMutex.UnlockKey(req.NamespacedName.Name)

	cacheDef := &opencasv1alpha1.Cache{}
	if err := r.Get(ctx, req.NamespacedName, cacheDef); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	logger := log.FromContext(ctx).WithName("cache-reconcile")
	logger.Info("loop started")

	logger.V(5).Info("target cache", cacheDef.Spec)

	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: cacheDef.GetNamespace(), Name: cacheDef.GetName()}, node); err != nil {
		if errors.IsNotFound(err) {
			if cacheDef.GetDeletionTimestamp().IsZero() {
				logger.Info("cache name should be equal to node name")
			}
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// add finalizer if cache resource is not being deleted
	if cacheDef.GetDeletionTimestamp().IsZero() {
		if err := r.AddFinalizer(ctx, cacheDef); err != nil {
			logger.Error(err, fmt.Sprintf("failed to add finalizer to cache resource %q", cacheDef.GetName()))
		}
	}

	// initiate cache management
	casadmImage := os.Getenv(k8sutils.CasadmImageEnv)
	if casadmImage == "" {
		return ctrl.Result{RequeueAfter: helpers.LongRequeueAfter}, fmt.Errorf("casadm image not configured, cannot reconcile caches")
	}

	cas, err := opencas.NewOpenCasManager(context.TODO(), cacheDef.GetName(), os.Getenv(k8sutils.OperatorNamespaceEnv))
	if err != nil {
		return ctrl.Result{Requeue: true}, fmt.Errorf("cannot create Open-CAS manager on node %q\nerr: %s",
			cacheDef.GetName(), err.Error(),
		)
	}

	// get current cache state
	instances, err := cas.GetCacheInstances()
	if err != nil {
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to get caches on node %q\nerr: %s",
			cacheDef.GetName(), err.Error(),
		)
	}
	logger.V(5).Info("current cache", "instances", instances)

	// determine operations to bring cache to the desired state
	removeInstances := []*opencas.CacheInstance{}
	var modifyInstance *opencas.CacheInstance
	errorsRequiringRequeueOccurred := false

	for _, instance := range instances {
		if instance.Cache.DevicePath != cacheDef.Spec.DeviceById ||
			!cacheDef.GetDeletionTimestamp().IsZero() {
			removeInstances = append(removeInstances, instance)
		} else {
			cls, err := cas.GetCacheLineSize(instance.Cache)
			if err != nil {
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to get cache line size on node %q\nerr: %s",
					cacheDef.GetName(), err.Error(),
				)
			}

			targetCls := opencas.CLS_4k
			if cacheDef.Spec.CacheLizeSize != 0 {
				targetCls = opencas.CacheLineSize(cacheDef.Spec.CacheLizeSize)
			}
			if cls != targetCls {
				removeInstances = append(removeInstances, instance)
				continue
			}

			// if cache doesn't require restart
			modifyInstance = instance
		}
	}

	// execute determined operations
	// 'remove' actions executed first, so the released devices can be reused
	// gather errors (may not affect modify/start)
	labelOperatingCacheId, ok := node.GetLabels()[k8sutils.OperatingCacheLabelKey]
	for _, instance := range removeInstances {
		// if cache set on node as used, unset
		if ok && labelOperatingCacheId == fmt.Sprintf("%v", instance.Cache.CacheId) && cacheDef.GetDeletionTimestamp().IsZero() {
			if err := r.SetOperatingCacheLabel(ctx, node, ""); err != nil {
				logger.Error(err, "failed to set empty label on node")
			}
		}

		if len(instance.Cache.Volumes) == 0 {
			logger.Info(fmt.Sprintf("stopping cache %q", instance.Cache.DevicePath))
			if err := cas.StopCache(instance.Cache); err != nil {
				logger.Error(err, fmt.Sprintf("cannot stop cache %q, requeue", instance.Cache.DevicePath))
				errorsRequiringRequeueOccurred = true
			} else {
				logger.Info(fmt.Sprintf("cache %q stopped", instance.Cache.DevicePath))
			}
			continue
		}

		logger.Info(fmt.Sprintf(
			"cache volumes present for cache %q - stopping cache deferred until they are no longer present",
			instance.Cache.DevicePath,
		))
		// requeue if cache is in use unless cache is being deleted
		if cacheDef.GetDeletionTimestamp().IsZero() {
			errorsRequiringRequeueOccurred = true
		}
	}

	if modifyInstance != nil {
		// modify instance if already running
		targetMode := opencas.WriteThrough
		if cacheDef.Spec.CacheMode != "" {
			targetMode = opencas.CacheMode(cacheDef.Spec.CacheMode)
		}
		if modifyInstance.Cache.CacheMode != targetMode {
			err := cas.SetCacheMode(modifyInstance.Cache, targetMode)
			if err != nil {
				logger.Error(err, fmt.Sprintf("cannot change cache mode for cache %d on node, requeue", modifyInstance.Cache.CacheId))
				errorsRequiringRequeueOccurred = true
			}
		}
	} else if cacheDef.GetDeletionTimestamp().IsZero() {
		// start cache
		devicePath := cacheDef.Spec.DeviceById
		logger.Info(fmt.Sprintf("starting cache %q", devicePath))
		cache, err := cas.StartCache(devicePath, opencas.CacheMode(cacheDef.Spec.CacheMode), opencas.CacheLineSize(cacheDef.Spec.CacheLizeSize))
		if err != nil {
			logger.Error(err, fmt.Sprintf("cannot start cache %q, requeue", devicePath))
			return ctrl.Result{RequeueAfter: helpers.DefaultRequequeAfter}, nil
		}

		// cache is running - set it as the one used on the node
		if err := r.SetOperatingCacheLabel(ctx, node, fmt.Sprintf("%v", cache.CacheId)); err != nil {
			logger.Error(err, "failed to set label on node")
		}
	}

	// remove finalizer if cache resource is being deleted and no errors occurred
	if !cacheDef.GetDeletionTimestamp().IsZero() && !errorsRequiringRequeueOccurred {
		if err := r.RemoveFinalizer(ctx, cacheDef); err != nil {
			logger.Error(err, "failed to remove finalizer from cache resource")
			errorsRequiringRequeueOccurred = true
		} else {
			// operating cache id no longer maintained by controller
			if err := r.DeleteOperatingCacheLabel(ctx, node); err != nil {
				logger.Error(err, "failed to remove label from node")
				errorsRequiringRequeueOccurred = true
			}
		}
	}
	// those errors did not affect starting/modifying cache, but we still want to retry cleaning
	if errorsRequiringRequeueOccurred {
		logger.Info("target cache set up successfully; non-critical error(s) during previous cache removal occurred, requeue for cleanup")
		return ctrl.Result{RequeueAfter: helpers.DefaultRequequeAfter}, nil
	}

	return ctrl.Result{}, nil
}

func (r *CacheReconciler) RemoveFinalizer(ctx context.Context, c *opencasv1alpha1.Cache) error {
	patch := client.MergeFrom(c.DeepCopy())
	if controllerutil.RemoveFinalizer(c, helpers.Finalizer) {
		return r.Patch(ctx, c, patch)
	}
	return nil
}

func (r *CacheReconciler) AddFinalizer(ctx context.Context, c *opencasv1alpha1.Cache) error {
	patch := client.MergeFrom(c.DeepCopy())
	if controllerutil.AddFinalizer(c, helpers.Finalizer) {
		return r.Patch(ctx, c, patch)
	}
	return nil
}

func (r *CacheReconciler) SetOperatingCacheLabel(ctx context.Context, n *corev1.Node, value string) error {
	patch := client.MergeFrom(n.DeepCopy())
	nodeLabels := n.GetLabels()
	nodeLabels[k8sutils.OperatingCacheLabelKey] = value
	n.SetLabels(nodeLabels)
	return r.Patch(ctx, n, patch)
}

func (r *CacheReconciler) DeleteOperatingCacheLabel(ctx context.Context, n *corev1.Node) error {
	patch := client.MergeFrom(n.DeepCopy())
	nodeLabels := n.GetLabels()
	delete(nodeLabels, k8sutils.OperatingCacheLabelKey)
	n.SetLabels(nodeLabels)
	return r.Patch(ctx, n, patch)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opencasv1alpha1.Cache{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, node client.Object) []reconcile.Request {
				cacheList := &opencasv1alpha1.CacheList{}
				if err := mgr.GetClient().List(ctx, cacheList); err != nil {
					mgr.GetLogger().Error(err, "while listing Caches")
					return nil
				}

				reqs := make([]ctrl.Request, 0, len(cacheList.Items))
				for _, cache := range cacheList.Items {
					if cache.GetName() == node.GetName() {
						mgr.GetLogger().Info("watcher initiated reconciliation", "node", node.GetName())
						reqs = append(reqs, ctrl.Request{
							NamespacedName: types.NamespacedName{
								Namespace: cache.GetNamespace(),
								Name:      cache.GetName(),
							},
						})
					}
				}
				return reqs
			}),
			builder.WithPredicates(predicate.Funcs{UpdateFunc: func(ue event.UpdateEvent) bool {
				newNode := ue.ObjectNew.(*corev1.Node)
				oldNode := ue.ObjectOld.(*corev1.Node)

				for _, newCond := range newNode.Status.Conditions {
					if newCond.Type == corev1.NodeReady {
						for _, oldCond := range oldNode.Status.Conditions {
							if oldCond.Type == corev1.NodeReady {
								return oldCond.Status != newCond.Status && newCond.Status == corev1.ConditionTrue
							}
						}
					}
				}
				return false
			}}),
		).
		Complete(r)
}
