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
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

const (
	routineFinalizerName = "routines.a2d2.dev/finalizer"
	defaultAgentImage    = "ghcr.io/a2d2-dev/sandbox:latest"
	defaultGatewayURL    = "http://routines-gateway:8080"
	workPVCSize          = "5Gi"
	workMountPath        = "/work"
	labelRoutineName     = "routines.a2d2.dev/routine"
)

// RoutineReconciler reconciles a Routine object.
type RoutineReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	AgentImage string // overrides AGENT_IMAGE env var
	GatewayURL string // overrides AGENT_GATEWAY_URL env var
}

// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=routines/finalizers,verbs=update
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=connectorbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *RoutineReconciler) agentImage() string {
	if r.AgentImage != "" {
		return r.AgentImage
	}
	if img := os.Getenv("AGENT_IMAGE"); img != "" {
		return img
	}
	return defaultAgentImage
}

func (r *RoutineReconciler) gatewayURL() string {
	if r.GatewayURL != "" {
		return r.GatewayURL
	}
	if u := os.Getenv("AGENT_GATEWAY_URL"); u != "" {
		return u
	}
	return defaultGatewayURL
}

// Reconcile manages the full lifecycle of a Routine: StatefulSet, PVC, finalizer, and status.
func (r *RoutineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	routine := &routinesv1alpha1.Routine{}
	if err := r.Get(ctx, req.NamespacedName, routine); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching Routine %s/%s: %w", req.Namespace, req.Name, err)
	}

	// --- Deletion handling ---
	if !routine.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, routine)
	}

	// --- Ensure finalizer ---
	if !controllerutil.ContainsFinalizer(routine, routineFinalizerName) {
		patch := client.MergeFrom(routine.DeepCopy())
		controllerutil.AddFinalizer(routine, routineFinalizerName)
		if err := r.Patch(ctx, routine, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		log.Info("Added finalizer", "name", routine.Name)
		return ctrl.Result{Requeue: true}, nil
	}

	// --- Reconcile PVC ---
	if err := r.reconcilePVC(ctx, routine); err != nil {
		return ctrl.Result{}, err
	}

	// --- Resolve ConnectorBindings ---
	extraEnv, err := r.resolveConnectorBindings(ctx, routine)
	if err != nil {
		return ctrl.Result{}, err
	}

	// --- Reconcile StatefulSet ---
	if err := r.reconcileStatefulSet(ctx, routine, extraEnv); err != nil {
		return ctrl.Result{}, err
	}

	// --- Update status ---
	return ctrl.Result{}, r.updateStatus(ctx, routine)
}

// handleDeletion scales the StatefulSet to 0 and removes the finalizer.
func (r *RoutineReconciler) handleDeletion(ctx context.Context, routine *routinesv1alpha1.Routine) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(routine, routineFinalizerName) {
		return ctrl.Result{}, nil
	}

	// Scale StatefulSet to 0 first.
	ss := &appsv1.StatefulSet{}
	ssKey := types.NamespacedName{Name: routine.Name, Namespace: routine.Namespace}
	if err := r.Get(ctx, ssKey, ss); err == nil {
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			patch := client.MergeFrom(ss.DeepCopy())
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := r.Patch(ctx, ss, patch); err != nil {
				return ctrl.Result{}, fmt.Errorf("scaling StatefulSet to 0 on deletion: %w", err)
			}
			log.Info("Scaled StatefulSet to 0 for deletion", "name", routine.Name)
		}
	} else if !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("fetching StatefulSet for deletion: %w", err)
	}

	// Remove finalizer to allow garbage collection.
	patch := client.MergeFrom(routine.DeepCopy())
	controllerutil.RemoveFinalizer(routine, routineFinalizerName)
	if err := r.Patch(ctx, routine, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	log.Info("Removed finalizer, Routine deleted", "name", routine.Name)
	return ctrl.Result{}, nil
}

// reconcilePVC ensures the work PVC exists for this Routine.
func (r *RoutineReconciler) reconcilePVC(ctx context.Context, routine *routinesv1alpha1.Routine) error {
	pvcName := routine.Name + "-work"
	pvc := &corev1.PersistentVolumeClaim{}
	key := types.NamespacedName{Name: pvcName, Namespace: routine.Namespace}

	err := r.Get(ctx, key, pvc)
	if err == nil {
		return nil // already exists
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("checking PVC %s: %w", pvcName, err)
	}

	storageClass := ""
	desired := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: routine.Namespace,
			Labels:    map[string]string{labelRoutineName: routine.Name},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(workPVCSize),
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(routine, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference on PVC: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("creating PVC %s: %w", pvcName, err)
	}
	logf.FromContext(ctx).Info("Created PVC", "name", pvcName)
	return nil
}

// resolveConnectorBindings fetches ConnectorBinding objects referenced by this Routine
// and returns env vars to inject into the agent pod.
func (r *RoutineReconciler) resolveConnectorBindings(
	ctx context.Context,
	routine *routinesv1alpha1.Routine,
) ([]corev1.EnvVar, error) {
	var envVars []corev1.EnvVar

	for _, ref := range routine.Spec.ConnectorBindingRefs {
		cb := &routinesv1alpha1.ConnectorBinding{}
		key := types.NamespacedName{Name: ref.Name, Namespace: routine.Namespace}
		if err := r.Get(ctx, key, cb); err != nil {
			if errors.IsNotFound(err) {
				logf.FromContext(ctx).Info("ConnectorBinding not found, skipping", "name", ref.Name)
				continue
			}
			return nil, fmt.Errorf("fetching ConnectorBinding %s: %w", ref.Name, err)
		}

		for _, rule := range cb.Spec.Inject {
			if rule.As != routinesv1alpha1.InjectAsEnv {
				continue // file injection is a future enhancement
			}
			envName := rule.EnvName
			if envName == "" {
				envName = rule.Key
			}
			envVars = append(envVars, corev1.EnvVar{
				Name: envName,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cb.Spec.SecretRef.Name,
						},
						Key: rule.Key,
					},
				},
			})
		}
	}

	return envVars, nil
}

// reconcileStatefulSet ensures the StatefulSet for this Routine exists with the correct spec.
func (r *RoutineReconciler) reconcileStatefulSet(
	ctx context.Context,
	routine *routinesv1alpha1.Routine,
	extraEnv []corev1.EnvVar,
) error {
	replicas := int32(1)
	if routine.Spec.Suspend {
		replicas = 0
	}

	desired := r.buildStatefulSet(routine, replicas, extraEnv)

	existing := &appsv1.StatefulSet{}
	key := types.NamespacedName{Name: routine.Name, Namespace: routine.Namespace}
	err := r.Get(ctx, key, existing)

	if errors.IsNotFound(err) {
		if setErr := ctrl.SetControllerReference(routine, desired, r.Scheme); setErr != nil {
			return fmt.Errorf("setting controller reference on StatefulSet: %w", setErr)
		}
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("creating StatefulSet: %w", createErr)
		}
		logf.FromContext(ctx).Info("Created StatefulSet", "name", routine.Name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching StatefulSet: %w", err)
	}

	// Patch replicas and pod template on drift.
	currentReplicas := int32(1)
	if existing.Spec.Replicas != nil {
		currentReplicas = *existing.Spec.Replicas
	}
	if currentReplicas != replicas {
		patch := client.MergeFrom(existing.DeepCopy())
		existing.Spec.Replicas = &replicas
		existing.Spec.Template = desired.Spec.Template
		if patchErr := r.Patch(ctx, existing, patch); patchErr != nil {
			return fmt.Errorf("patching StatefulSet: %w", patchErr)
		}
		logf.FromContext(ctx).Info("Updated StatefulSet replicas", "name", routine.Name, "replicas", replicas)
	}
	return nil
}

// buildStatefulSet constructs the desired StatefulSet spec for a Routine.
func (r *RoutineReconciler) buildStatefulSet(
	routine *routinesv1alpha1.Routine,
	replicas int32,
	extraEnv []corev1.EnvVar,
) *appsv1.StatefulSet {
	labels := map[string]string{
		labelRoutineName: routine.Name,
	}

	pvcName := routine.Name + "-work"

	envVars := []corev1.EnvVar{
		{
			Name:  "AGENT_GATEWAY_URL",
			Value: r.gatewayURL(),
		},
		{
			Name:  "AGENT_ROUTINE_UID",
			Value: string(routine.UID),
		},
	}
	envVars = append(envVars, extraEnv...)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routine.Name,
			Namespace: routine.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: routine.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "agent",
							Image: r.agentImage(),
							Env:   envVars,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "work",
									MountPath: workMountPath,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "work",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
								},
							},
						},
					},
				},
			},
		},
	}
}

// updateStatus patches the Routine's status based on current observed state.
func (r *RoutineReconciler) updateStatus(ctx context.Context, routine *routinesv1alpha1.Routine) error {
	desiredPhase := routinesv1alpha1.RoutinePhaseReady
	if routine.Spec.Suspend {
		desiredPhase = routinesv1alpha1.RoutinePhaseSuspended
	}

	ss := &appsv1.StatefulSet{}
	key := types.NamespacedName{Name: routine.Name, Namespace: routine.Namespace}
	podReady := false
	if err := r.Get(ctx, key, ss); err == nil {
		podReady = ss.Status.ReadyReplicas > 0
	}

	now := metav1.Now()
	readyStatus := metav1.ConditionFalse
	readyReason := "StatefulSetNotReady"
	readyMsg := "StatefulSet is not ready"
	if podReady {
		readyStatus = metav1.ConditionTrue
		readyReason = "StatefulSetReady"
		readyMsg = "StatefulSet is ready"
	}
	if routine.Spec.Suspend {
		readyStatus = metav1.ConditionFalse
		readyReason = "Suspended"
		readyMsg = "Routine is suspended"
	}

	patch := client.MergeFrom(routine.DeepCopy())
	routine.Status.Phase = desiredPhase
	routine.Status.PodReady = podReady
	routine.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             readyStatus,
			Reason:             readyReason,
			Message:            readyMsg,
			LastTransitionTime: now,
		},
	}
	if err := r.Status().Patch(ctx, routine, patch); err != nil {
		return fmt.Errorf("patching Routine status: %w", err)
	}
	logf.FromContext(ctx).Info("Updated Routine status",
		"name", routine.Name,
		"phase", desiredPhase,
		"podReady", podReady,
	)
	return nil
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
