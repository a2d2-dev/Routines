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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

// GitHubTriggerReconciler reconciles a GitHubTrigger object.
type GitHubTriggerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	GatewayURL string
}

// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=githubtriggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=githubtriggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile watches GitHubTrigger CRs, validates the installation secret, and sets
// status conditions. The Gateway watches GitHubTrigger objects and registers the
// GitHub App webhook route dynamically.
func (r *GitHubTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	trigger := &routinesv1alpha1.GitHubTrigger{}
	if err := r.Get(ctx, req.NamespacedName, trigger); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching GitHubTrigger: %w", err)
	}

	if !trigger.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	ready := true
	reason := "Ready"
	message := "GitHubTrigger is ready"

	// Validate that the installation secret exists.
	secretKey := types.NamespacedName{
		Name:      trigger.Spec.InstallationRef.Name,
		Namespace: trigger.Namespace,
	}
	installSecret := &corev1.Secret{}
	if err := r.Get(ctx, secretKey, installSecret); err != nil {
		if errors.IsNotFound(err) {
			ready = false
			reason = "InstallationSecretNotFound"
			message = fmt.Sprintf("installation secret %q not found", trigger.Spec.InstallationRef.Name)
		} else {
			return ctrl.Result{}, fmt.Errorf("fetching installation secret: %w", err)
		}
	}

	// Validate that at least one event type is configured.
	if len(trigger.Spec.Events) == 0 {
		ready = false
		reason = "NoEventsConfigured"
		message = "spec.events must contain at least one event type"
	}

	patch := client.MergeFrom(trigger.DeepCopy())
	trigger.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             conditionStatus(ready),
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		},
	}
	if err := r.Status().Patch(ctx, trigger, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching GitHubTrigger status: %w", err)
	}

	logf.FromContext(ctx).Info("Reconciled GitHubTrigger",
		"name", trigger.Name,
		"ready", ready,
	)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the GitHubTrigger controller.
func (r *GitHubTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routinesv1alpha1.GitHubTrigger{}).
		Named("githubtrigger").
		Complete(r)
}
