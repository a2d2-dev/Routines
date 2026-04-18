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
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	scheduleTriggerFinalizerName = "routines.a2d2.dev/schedule-finalizer"
	defaultCronJobImage          = "curlimages/curl:latest"
	labelScheduleTriggerName     = "routines.a2d2.dev/schedule-trigger"
)

// ScheduleTriggerReconciler reconciles a ScheduleTrigger object.
type ScheduleTriggerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	GatewayURL string
}

// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=scheduletriggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=scheduletriggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=scheduletriggers/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete

func (r *ScheduleTriggerReconciler) gatewayURL() string {
	if r.GatewayURL != "" {
		return r.GatewayURL
	}
	return defaultGatewayURL
}

// Reconcile watches ScheduleTrigger CRs and manages a CronJob that calls Gateway /v1/enqueue.
func (r *ScheduleTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	trigger := &routinesv1alpha1.ScheduleTrigger{}
	if err := r.Get(ctx, req.NamespacedName, trigger); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching ScheduleTrigger: %w", err)
	}

	if !trigger.DeletionTimestamp.IsZero() {
		return r.handleScheduleTriggerDeletion(ctx, trigger)
	}

	if !controllerutil.ContainsFinalizer(trigger, scheduleTriggerFinalizerName) {
		patch := client.MergeFrom(trigger.DeepCopy())
		controllerutil.AddFinalizer(trigger, scheduleTriggerFinalizerName)
		if err := r.Patch(ctx, trigger, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer to ScheduleTrigger: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve Routine UIDs for all refs.
	routineUIDs, err := r.resolveRoutineUIDs(ctx, trigger)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileCronJob(ctx, trigger, routineUIDs); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.updateScheduleTriggerStatus(ctx, trigger, true, "CronJobReady", "CronJob is configured")
}

func (r *ScheduleTriggerReconciler) handleScheduleTriggerDeletion(
	ctx context.Context,
	trigger *routinesv1alpha1.ScheduleTrigger,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(trigger, scheduleTriggerFinalizerName) {
		return ctrl.Result{}, nil
	}
	patch := client.MergeFrom(trigger.DeepCopy())
	controllerutil.RemoveFinalizer(trigger, scheduleTriggerFinalizerName)
	if err := r.Patch(ctx, trigger, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing ScheduleTrigger finalizer: %w", err)
	}
	logf.FromContext(ctx).Info("ScheduleTrigger deleted", "name", trigger.Name)
	return ctrl.Result{}, nil
}

// resolveRoutineUIDs looks up each RoutineRef and returns the Kubernetes UIDs.
func (r *ScheduleTriggerReconciler) resolveRoutineUIDs(
	ctx context.Context,
	trigger *routinesv1alpha1.ScheduleTrigger,
) ([]string, error) {
	var uids []string
	for _, ref := range trigger.Spec.RoutineRefs {
		ns := ref.Namespace
		if ns == "" {
			ns = trigger.Namespace
		}
		routine := &routinesv1alpha1.Routine{}
		key := types.NamespacedName{Name: ref.Name, Namespace: ns}
		if err := r.Get(ctx, key, routine); err != nil {
			if errors.IsNotFound(err) {
				logf.FromContext(ctx).Info("Routine not found for ScheduleTrigger, skipping", "routine", ref.Name)
				continue
			}
			return nil, fmt.Errorf("fetching Routine %s: %w", ref.Name, err)
		}
		uids = append(uids, string(routine.UID))
	}
	return uids, nil
}

// reconcileCronJob creates or updates the CronJob for this ScheduleTrigger.
func (r *ScheduleTriggerReconciler) reconcileCronJob(
	ctx context.Context,
	trigger *routinesv1alpha1.ScheduleTrigger,
	routineUIDs []string,
) error {
	desired := r.buildCronJob(trigger, routineUIDs)

	existing := &batchv1.CronJob{}
	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	err := r.Get(ctx, key, existing)

	if errors.IsNotFound(err) {
		if setErr := ctrl.SetControllerReference(trigger, desired, r.Scheme); setErr != nil {
			return fmt.Errorf("setting controller reference on CronJob: %w", setErr)
		}
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("creating CronJob: %w", createErr)
		}
		logf.FromContext(ctx).Info("Created CronJob", "name", trigger.Name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching CronJob: %w", err)
	}

	// Update schedule and env if changed.
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.Schedule = trigger.Spec.Cron
	existing.Spec.TimeZone = &trigger.Spec.Timezone
	existing.Spec.JobTemplate = desired.Spec.JobTemplate
	if patchErr := r.Patch(ctx, existing, patch); patchErr != nil {
		return fmt.Errorf("patching CronJob: %w", patchErr)
	}
	return nil
}

// buildCronJob constructs the CronJob that enqueues messages into the Gateway.
func (r *ScheduleTriggerReconciler) buildCronJob(
	trigger *routinesv1alpha1.ScheduleTrigger,
	routineUIDs []string,
) *batchv1.CronJob {
	labels := map[string]string{
		labelScheduleTriggerName: trigger.Name,
	}

	// Build a shell script that POSTs to Gateway /v1/enqueue for each Routine UID.
	var enqueueLines []string
	for _, uid := range routineUIDs {
		line := fmt.Sprintf(
			`curl -sf -X POST "%s/v1/enqueue" -H "Content-Type: application/json" -d '{"routineUID":"%s","source":"schedule","triggerName":"%s"}' || true`,
			r.gatewayURL(), uid, trigger.Name,
		)
		enqueueLines = append(enqueueLines, line)
	}
	if len(enqueueLines) == 0 {
		enqueueLines = []string{"echo 'No routines to enqueue'"}
	}
	script := strings.Join(enqueueLines, "\n")

	tz := trigger.Spec.Timezone
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      trigger.Name,
			Namespace: trigger.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: trigger.Spec.Cron,
			TimeZone: &tz,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{
								{
									Name:    "enqueue",
									Image:   defaultCronJobImage,
									Command: []string{"sh", "-c"},
									Args:    []string{script},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *ScheduleTriggerReconciler) updateScheduleTriggerStatus(
	ctx context.Context,
	trigger *routinesv1alpha1.ScheduleTrigger,
	ready bool,
	reason, message string,
) error {
	status := metav1.ConditionTrue
	if !ready {
		status = metav1.ConditionFalse
	}
	patch := client.MergeFrom(trigger.DeepCopy())
	trigger.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		},
	}
	if err := r.Status().Patch(ctx, trigger, patch); err != nil {
		return fmt.Errorf("patching ScheduleTrigger status: %w", err)
	}
	return nil
}

// SetupWithManager sets up the ScheduleTrigger controller.
func (r *ScheduleTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routinesv1alpha1.ScheduleTrigger{}).
		Owns(&batchv1.CronJob{}).
		Named("scheduletrigger").
		Complete(r)
}
