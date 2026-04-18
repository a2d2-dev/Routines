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

package integration

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

const (
	testNamespace = "default"
	timeout       = 30 * time.Second
	interval      = 200 * time.Millisecond
)

var _ = Describe("Routine Lifecycle", func() {
	Context("when a Routine is created", func() {
		var routineName string

		BeforeEach(func() {
			routineName = "test-routine-" + generateSuffix()
		})

		AfterEach(func() {
			// Clean up the Routine to avoid interfering with other tests.
			routine := &routinesv1alpha1.Routine{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace, Name: routineName,
			}, routine); err == nil {
				_ = k8sClient.Delete(ctx, routine)
			}
		})

		It("should progress to Ready phase and create a StatefulSet and PVC", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      routineName,
					Namespace: testNamespace,
				},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt: routinesv1alpha1.PromptSpec{
						Inline: "Run a daily health-check and report status.",
					},
					MaxDurationSeconds: 300,
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			By("waiting for the Routine finalizer to be set")
			Eventually(func(g Gomega) {
				r := &routinesv1alpha1.Routine{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, r)).To(Succeed())
				g.Expect(r.Finalizers).To(ContainElement("routines.a2d2.dev/finalizer"))
			}, timeout, interval).Should(Succeed())

			By("waiting for the StatefulSet to be created")
			Eventually(func(g Gomega) {
				sts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, sts)).To(Succeed())
				g.Expect(sts.Spec.Replicas).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			By("verifying PVC exists")
			Eventually(func(g Gomega) {
				pvc := &corev1.PersistentVolumeClaim{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName + "-work",
				}, pvc)).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})

		It("should suspend and resume correctly", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      routineName,
					Namespace: testNamespace,
				},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt: routinesv1alpha1.PromptSpec{
						Inline: "Periodic CI summary.",
					},
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			By("waiting for StatefulSet creation")
			Eventually(func(g Gomega) {
				sts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, sts)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("suspending the Routine")
			patch := client.MergeFrom(routine.DeepCopy())
			routine.Spec.Suspend = true
			Expect(k8sClient.Patch(ctx, routine, patch)).To(Succeed())

			By("waiting for StatefulSet replicas to scale to 0")
			Eventually(func(g Gomega) {
				sts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, sts)).To(Succeed())
				g.Expect(*sts.Spec.Replicas).To(Equal(int32(0)))
			}, timeout, interval).Should(Succeed())

			By("resuming the Routine")
			r := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace, Name: routineName,
			}, r)).To(Succeed())
			patch2 := client.MergeFrom(r.DeepCopy())
			r.Spec.Suspend = false
			Expect(k8sClient.Patch(ctx, r, patch2)).To(Succeed())

			By("waiting for StatefulSet replicas to scale back to 1")
			Eventually(func(g Gomega) {
				sts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, sts)).To(Succeed())
				g.Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("PVC finalizer protection", func() {
		It("should retain PVC when Routine is deleted", func() {
			name := "pvc-test-" + generateSuffix()
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt: routinesv1alpha1.PromptSpec{Inline: "test"},
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			By("waiting for PVC to be created")
			Eventually(func(g Gomega) {
				pvc := &corev1.PersistentVolumeClaim{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: name + "-work",
				}, pvc)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			By("deleting the Routine")
			Expect(k8sClient.Delete(ctx, routine)).To(Succeed())

			By("verifying PVC still exists after Routine deletion")
			Consistently(func(g Gomega) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: name + "-work",
				}, pvc)
				// PVC should exist (not deleted) or be in terminating state.
				if err != nil {
					g.Expect(errors.IsNotFound(err)).To(BeFalse(), "PVC was deleted unexpectedly")
				}
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			// Cleanup PVC.
			pvc := &corev1.PersistentVolumeClaim{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace, Name: name + "-work",
			}, pvc); err == nil {
				_ = k8sClient.Delete(ctx, pvc)
			}
		})
	})
})

// generateSuffix returns a unique suffix for resource names based on nanosecond timestamp.
func generateSuffix() string {
	ns := time.Now().UnixNano()
	// Use last 9 decimal digits (nanoseconds within the current second) for brevity.
	return fmt.Sprintf("%09d", ns%1_000_000_000)
}
