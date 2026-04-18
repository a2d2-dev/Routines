/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

// RoutineReconciler reconciles a Routine object.
type RoutineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile watches Routine CRs and logs reconcile events.
// Full StatefulSet / PVC / Gateway lifecycle management is implemented in Phase 4.
func (r *RoutineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Routine instance.
	routine := &routinesv1alpha1.Routine{}
	if err := r.Get(ctx, req.NamespacedName, routine); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Routine not found — may have been deleted", "name", req.Name, "namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching Routine %s/%s: %w", req.Namespace, req.Name, err)
	}

	log.Info("Reconciling Routine",
		"name", routine.Name,
		"namespace", routine.Namespace,
		"phase", routine.Status.Phase,
		"suspend", routine.Spec.Suspend,
	)

	// Initialise phase on first reconcile.
	if routine.Status.Phase == "" {
		patch := client.MergeFrom(routine.DeepCopy())
		if routine.Spec.Suspend {
			routine.Status.Phase = routinesv1alpha1.RoutinePhaseSuspended
		} else {
			routine.Status.Phase = routinesv1alpha1.RoutinePhasePending
		}
		if err := r.Status().Patch(ctx, routine, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching Routine status: %w", err)
		}
		log.Info("Initialised Routine phase", "phase", routine.Status.Phase)
	}

	// Phase 1: skeleton only — full reconcile logic (StatefulSet, PVC, Gateway registration)
	// is deferred to Phase 4.
	log.Info("Reconcile complete (skeleton)", "name", routine.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RoutineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routinesv1alpha1.Routine{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("routine").
		Complete(r)
}
