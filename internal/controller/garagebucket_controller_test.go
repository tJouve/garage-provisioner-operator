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
	testNamespace  = "storage"
	testStoreName  = "garage"
	testBucketName = "bucket-a"
)

var _ = Describe("GarageBucket Controller", func() {
	Context("When reconciling with non-ready store", func() {
		It("should set StoreNotReady condition", func() {
			ctx := context.Background()

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), namespace)
			})

			store := &storagev1alpha1.GarageStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testStoreName,
					Namespace: testNamespace,
				},
				Spec: storagev1alpha1.GarageStoreSpec{
					AdminURL:      "https://garage-admin.example",
					Zone:          "garage",
					S3EndpointURL: "https://garage-s3.example",
					AdminSecretRef: storagev1alpha1.AdminSecretRef{
						Name: "garage-admin-token",
					},
				},
				Status: storagev1alpha1.GarageStoreStatus{Ready: false},
			}
			Expect(k8sClient.Create(ctx, store)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), store)
			})

			bucket := &storagev1alpha1.GarageBucket{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testBucketName,
					Namespace: testNamespace,
				},
				Spec: storagev1alpha1.GarageBucketSpec{
					GarageStoreRef: storagev1alpha1.GarageStoreRef{
						Name: testStoreName,
						Kind: storagev1alpha1.GarageStoreRefKindGarageStore,
					},
				},
			}
			Expect(k8sClient.Create(ctx, bucket)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), bucket)
			})

			reconciler := &GarageBucketReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testBucketName}})
			Expect(err).NotTo(HaveOccurred())

			fetched := &storagev1alpha1.GarageBucket{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testBucketName}, fetched)).To(Succeed())

			condition := meta.FindStatusCondition(fetched.Status.Conditions, readyConditionType)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal("StoreNotReady"))
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
