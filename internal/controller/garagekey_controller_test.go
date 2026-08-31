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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"

	storagev1alpha1 "github.com/tJouve/garage-provisioner-operator/api/v1alpha1"
)

const (
	testKeyNamespace = "storage-key"
	testKeyName      = "app-key"
	testKeyBucketRef = "missing-bucket"
)

var _ = Describe("GarageKey Controller", func() {
	Context("When bucket reference is missing", func() {
		It("should set BucketNotFound condition", func() {
			ctx := context.Background()

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testKeyNamespace}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), namespace)
			})

			key := &storagev1alpha1.GarageKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testKeyName,
					Namespace: testKeyNamespace,
				},
				Spec: storagev1alpha1.GarageKeySpec{
					BucketRef: storagev1alpha1.BucketRef{Name: testKeyBucketRef},
					BucketPermissions: storagev1alpha1.BucketPermissions{
						Read: true,
					},
				},
			}
			Expect(k8sClient.Create(ctx, key)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), key)
			})

			reconciler := &GarageKeyReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testKeyNamespace, Name: testKeyName}})
			Expect(err).NotTo(HaveOccurred())

			fetched := &storagev1alpha1.GarageKey{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testKeyNamespace, Name: testKeyName}, fetched)).To(Succeed())

			condition := meta.FindStatusCondition(fetched.Status.Conditions, readyConditionType)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal("BucketNotFound"))
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
