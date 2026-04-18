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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

// cleanupRoutine removes a Routine and its owned resources, stripping finalizers so envtest can delete them.
func cleanupRoutine(ctx context.Context, name, namespace string) {
	routine := &routinesv1alpha1.Routine{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, routine); err == nil {
		routine.Finalizers = nil
		_ = k8sClient.Update(ctx, routine)
		_ = k8sClient.Delete(ctx, routine)
	}
	ss := &appsv1.StatefulSet{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ss); err == nil {
		_ = k8sClient.Delete(ctx, ss)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name + "-work", Namespace: namespace}, pvc); err == nil {
		pvc.Finalizers = nil
		_ = k8sClient.Update(ctx, pvc)
		_ = k8sClient.Delete(ctx, pvc)
	}
}

var _ = Describe("Routine Controller", func() {
	const testNamespace = "default"

	var (
		ctx        context.Context
		reconciler *RoutineReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		reconciler = &RoutineReconciler{
			Client:     k8sClient,
			Scheme:     k8sClient.Scheme(),
			GatewayURL: "http://test-gateway:8080",
			AgentImage: "test/agent:latest",
		}
	})

	Describe("Finalizer management", func() {
		const routineName = "test-finalizer"

		AfterEach(func() { cleanupRoutine(ctx, routineName, testNamespace) })

		It("should add a finalizer on first reconcile", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec:       routinesv1alpha1.RoutineSpec{Prompt: routinesv1alpha1.PromptSpec{Inline: "hello"}},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(routineFinalizerName))
		})
	})

	Describe("StatefulSet and PVC creation", func() {
		const routineName = "test-create"

		AfterEach(func() { cleanupRoutine(ctx, routineName, testNamespace) })

		It("should create a StatefulSet with 1 replica and a PVC", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec:       routinesv1alpha1.RoutineSpec{Prompt: routinesv1alpha1.PromptSpec{Inline: "test prompt"}},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			// First reconcile adds finalizer, second creates resources.
			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("checking StatefulSet")
			ss := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, ss)).To(Succeed())
			Expect(ss.Spec.Replicas).NotTo(BeNil())
			Expect(*ss.Spec.Replicas).To(Equal(int32(1)))
			Expect(ss.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(ss.Spec.Template.Spec.Containers[0].Image).To(Equal("test/agent:latest"))

			By("checking env vars")
			envMap := make(map[string]string)
			for _, e := range ss.Spec.Template.Spec.Containers[0].Env {
				envMap[e.Name] = e.Value
			}
			Expect(envMap["AGENT_GATEWAY_URL"]).To(Equal("http://test-gateway:8080"))
			Expect(envMap).To(HaveKey("AGENT_ROUTINE_UID"))

			By("checking /work volume mount")
			Expect(ss.Spec.Template.Spec.Containers[0].VolumeMounts).To(ContainElement(
				corev1.VolumeMount{Name: "work", MountPath: "/work"},
			))

			By("checking PVC")
			pvc := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName + "-work", Namespace: testNamespace}, pvc)).To(Succeed())
			Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteOnce))

			By("checking status phase is Ready")
			updated := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(routinesv1alpha1.RoutinePhaseReady))
		})
	})

	Describe("Suspend lifecycle", func() {
		const routineName = "test-suspend"

		AfterEach(func() { cleanupRoutine(ctx, routineName, testNamespace) })

		It("should create StatefulSet with 0 replicas when suspend=true", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt:  routinesv1alpha1.PromptSpec{Inline: "test"},
					Suspend: true,
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			ss := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, ss)).To(Succeed())
			Expect(*ss.Spec.Replicas).To(Equal(int32(0)))

			updated := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(routinesv1alpha1.RoutinePhaseSuspended))
		})

		It("should scale from 0 to 1 when unsuspended", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt:  routinesv1alpha1.PromptSpec{Inline: "test"},
					Suspend: true,
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())
			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			// Unsuspend.
			updated := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, updated)).To(Succeed())
			updated.Spec.Suspend = false
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, ss)).To(Succeed())
			Expect(*ss.Spec.Replicas).To(Equal(int32(1)))
		})
	})

	Describe("Deletion", func() {
		const routineName = "test-delete"

		It("should remove finalizer on deletion allowing garbage collection", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec:       routinesv1alpha1.RoutineSpec{Prompt: routinesv1alpha1.PromptSpec{Inline: "test"}},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())
			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			current := &routinesv1alpha1.Routine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, current)).To(Succeed())
			Expect(k8sClient.Delete(ctx, current)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			deleted := &routinesv1alpha1.Routine{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, deleted)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// Cleanup owned resources.
			ss := &appsv1.StatefulSet{}
			if err2 := k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, ss); err2 == nil {
				_ = k8sClient.Delete(ctx, ss)
			}
			pvc := &corev1.PersistentVolumeClaim{}
			if err2 := k8sClient.Get(ctx, types.NamespacedName{Name: routineName + "-work", Namespace: testNamespace}, pvc); err2 == nil {
				pvc.Finalizers = nil
				_ = k8sClient.Update(ctx, pvc)
				_ = k8sClient.Delete(ctx, pvc)
			}
		})
	})

	Describe("ConnectorBinding env injection", func() {
		const routineName = "test-connector"
		const secretName = "connector-secret"
		const bindingName = "connector-binding"

		BeforeEach(func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace},
				Data:       map[string][]byte{"API_KEY": []byte("super-secret")},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			cb := &routinesv1alpha1.ConnectorBinding{
				ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: testNamespace},
				Spec: routinesv1alpha1.ConnectorBindingSpec{
					SecretRef: routinesv1alpha1.SecretRef{Name: secretName},
					Scope:     routinesv1alpha1.ConnectorScopeReadOnly,
					Inject: []routinesv1alpha1.InjectRule{
						{As: routinesv1alpha1.InjectAsEnv, Key: "API_KEY", EnvName: "MY_API_KEY"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cb)).To(Succeed())
		})

		AfterEach(func() {
			cleanupRoutine(ctx, routineName, testNamespace)
			cb := &routinesv1alpha1.ConnectorBinding{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: bindingName, Namespace: testNamespace}, cb); err == nil {
				_ = k8sClient.Delete(ctx, cb)
			}
			sec := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: testNamespace}, sec); err == nil {
				_ = k8sClient.Delete(ctx, sec)
			}
		})

		It("should inject ConnectorBinding env vars into StatefulSet pod spec", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{Name: routineName, Namespace: testNamespace},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt: routinesv1alpha1.PromptSpec{Inline: "test"},
					ConnectorBindingRefs: []routinesv1alpha1.ConnectorBindingRef{
						{Name: bindingName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: routineName, Namespace: testNamespace},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			ss := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: routineName, Namespace: testNamespace}, ss)).To(Succeed())
			Expect(ss.Spec.Template.Spec.Containers).To(HaveLen(1))

			var found bool
			for _, env := range ss.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "MY_API_KEY" {
					found = true
					Expect(env.ValueFrom).NotTo(BeNil())
					Expect(env.ValueFrom.SecretKeyRef).NotTo(BeNil())
					Expect(env.ValueFrom.SecretKeyRef.Name).To(Equal(secretName))
					Expect(env.ValueFrom.SecretKeyRef.Key).To(Equal("API_KEY"))
				}
			}
			Expect(found).To(BeTrue(), "expected MY_API_KEY env var in StatefulSet pod spec")
		})
	})
})
