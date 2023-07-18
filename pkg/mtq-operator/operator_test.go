/*
Copyright 2023 The MTQ Authors.

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
package mtq_operator

import (
	"context"
	generrors "errors"
	"fmt"
	"kubevirt.io/containerized-data-importer/pkg/common"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/cert"
	"kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
	"reflect"
	"time"

	"kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk/callbacks"

	sdkapi "kubevirt.io/controller-lifecycle-operator-sdk/api"
	sdkr "kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk/reconciler"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secv1 "github.com/openshift/api/security/v1"
	conditions "github.com/openshift/custom-resource-status/conditions/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterResources "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/cluster"
	namespaceResources "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
	utils "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	mtqv1 "kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
)

const (
	version       = "v1.5.0"
	mtqNamespace  = "mtq"
	configMapName = "mtq-config"

	normalCreateSuccess = "Normal CreateResourceSuccess Successfully created resource"
)

type args struct {
	mtq        *v1alpha1.MTQ
	client     client.Client
	reconciler *ReconcileMTQ
}

func init() {
	schemeInitFuncs := []func(*runtime.Scheme) error{
		mtqv1.AddToScheme,
		extv1.AddToScheme,
	}

	for _, f := range schemeInitFuncs {
		if err := f(scheme.Scheme); err != nil {
			panic(fmt.Errorf("failed to initiate the scheme %w", err))
		}
	}
}

type modifyResource func(toModify client.Object) (client.Object, client.Object, error)
type isModifySubject func(resource client.Object) bool
type isUpgraded func(postUpgradeObj client.Object, deisredObj client.Object) bool

type createUnusedObject func() (client.Object, error)

var _ = Describe("Controller", func() {
	DescribeTable("check can create types", func(obj client.Object) {
		client := createClient(obj)

		_, err := getObject(client, obj)
		Expect(err).ToNot(HaveOccurred())
	},
		Entry("MTQ type", createMTQ("mtq", "good uid")),
		Entry("CRD type", &extv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "crd"}}),
	)

	Describe("Deploying MTQ", func() {
		Context("MTQ lifecycle", func() {
			It("should get deployed", func() {
				args := createArgs()
				doReconcile(args)
				setDeploymentsReady(args)

				Expect(args.mtq.Status.OperatorVersion).Should(Equal(version))
				Expect(args.mtq.Status.TargetVersion).Should(Equal(version))
				Expect(args.mtq.Status.ObservedVersion).Should(Equal(version))

				Expect(args.mtq.Status.Conditions).Should(HaveLen(3))
				Expect(conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionAvailable)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionProgressing)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionDegraded)).To(BeTrue())

				Expect(args.mtq.Finalizers).Should(HaveLen(1))

				validateEvents(args.reconciler, createReadyEventValidationMap())
			})

			It("should create configmap", func() {
				args := createArgs()
				doReconcile(args)

				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: mtqNamespace, Name: configMapName}}
				obj, err := getObject(args.client, cm)
				Expect(err).ToNot(HaveOccurred())

				cm = obj.(*corev1.ConfigMap)
				Expect(cm.OwnerReferences[0].UID).Should(Equal(args.mtq.UID))
				validateEvents(args.reconciler, createNotReadyEventValidationMap())
			})

			It("should create requeue when configmap exists with another owner", func() {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: mtqNamespace,
						Name:      configMapName,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: mtqv1.SchemeGroupVersion.String(),
								Kind:       "MTQ",
								Name:       "mtq",
								UID:        "badUID",
							},
						},
					},
				}

				args := createArgs()

				err := args.client.Create(context.TODO(), cm)
				Expect(err).ToNot(HaveOccurred())

				doReconcileRequeue(args)
			})

			It("should create requeue when configmap has deletion timestamp", func() {
				t := metav1.Now()
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         mtqNamespace,
						Name:              configMapName,
						DeletionTimestamp: &t,
					},
				}

				args := createArgs()

				err := args.client.Create(context.TODO(), cm)
				Expect(err).ToNot(HaveOccurred())

				doReconcileRequeue(args)
			})

			It("should create requeue when a resource exists", func() {
				args := createArgs()
				resources, err := getAllResources(args.reconciler)
				Expect(err).ToNot(HaveOccurred())

				err = args.client.Create(context.TODO(), resources[0])
				Expect(err).ToNot(HaveOccurred())

				doReconcileRequeue(args)
			})

			It("should create all resources", func() {
				args := createArgs()
				doReconcile(args)

				resources, err := getAllResources(args.reconciler)
				Expect(err).ToNot(HaveOccurred())

				for _, r := range resources {
					_, err := getObject(args.client, r)
					Expect(err).ToNot(HaveOccurred())
				}
				validateEvents(args.reconciler, createNotReadyEventValidationMap())
			})

			It("can become become ready, un-ready, and ready again", func() {
				var deployment *appsv1.Deployment

				args := createArgs()
				doReconcile(args)

				resources, err := getAllResources(args.reconciler)
				Expect(err).ToNot(HaveOccurred())

				for _, r := range resources {
					d, ok := r.(*appsv1.Deployment)
					if !ok {
						continue
					}

					dd, err := getDeployment(args.client, d)
					Expect(err).ToNot(HaveOccurred())

					dd.Status.Replicas = *dd.Spec.Replicas
					dd.Status.ReadyReplicas = dd.Status.Replicas

					err = args.client.Update(context.TODO(), dd)
					Expect(err).ToNot(HaveOccurred())
				}

				doReconcile(args)

				Expect(args.mtq.Status.Conditions).Should(HaveLen(3))
				Expect(conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionAvailable)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionProgressing)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionDegraded)).To(BeTrue())

				for _, r := range resources {
					var ok bool
					deployment, ok = r.(*appsv1.Deployment)
					if ok {
						break
					}
				}

				deployment, err = getDeployment(args.client, deployment)
				Expect(err).ToNot(HaveOccurred())
				deployment.Status.ReadyReplicas = 0
				err = args.client.Update(context.TODO(), deployment)
				Expect(err).ToNot(HaveOccurred())

				doReconcile(args)

				Expect(args.mtq.Status.Conditions).Should(HaveLen(3))
				Expect(conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionAvailable)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionProgressing)).To(BeTrue())
				// Application should be degraded due to missing deployment pods (set to 0)
				Expect(conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionDegraded)).To(BeTrue())

				deployment, err = getDeployment(args.client, deployment)
				Expect(err).ToNot(HaveOccurred())
				deployment.Status.ReadyReplicas = deployment.Status.Replicas
				err = args.client.Update(context.TODO(), deployment)
				Expect(err).ToNot(HaveOccurred())

				doReconcile(args)

				Expect(args.mtq.Status.Conditions).Should(HaveLen(3))
				Expect(conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionAvailable)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionProgressing)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionDegraded)).To(BeTrue())
				validateEvents(args.reconciler, createReadyEventValidationMap())
			})

			It("should be an error when creating another MTQ instance", func() {
				args := createArgs()
				doReconcile(args)

				newInstance := createMTQ("bad", "bad")
				err := args.client.Create(context.TODO(), newInstance)
				Expect(err).ToNot(HaveOccurred())

				result, err := args.reconciler.Reconcile(context.TODO(), reconcileRequest(newInstance.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(result.Requeue).To(BeFalse())

				newInstance, err = getMTQ(args.client, newInstance)
				Expect(err).ToNot(HaveOccurred())

				Expect(newInstance.Status.Phase).Should(Equal(sdkapi.PhaseError))
				Expect(newInstance.Status.Conditions).Should(HaveLen(3))
				Expect(conditions.IsStatusConditionFalse(newInstance.Status.Conditions, conditions.ConditionAvailable)).To(BeTrue())
				Expect(conditions.IsStatusConditionFalse(newInstance.Status.Conditions, conditions.ConditionProgressing)).To(BeTrue())
				Expect(conditions.IsStatusConditionTrue(newInstance.Status.Conditions, conditions.ConditionDegraded)).To(BeTrue())
				validateEvents(args.reconciler, createErrorMTQEventValidationMap())
			})

			It("should succeed when we delete MTQ", func() {
				args := createArgs()
				doReconcile(args)
				args.mtq.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				err := args.client.Update(context.TODO(), args.mtq)
				Expect(err).ToNot(HaveOccurred())
				doReconcileExpectDelete(args)
				validateEvents(args.reconciler, createNotReadyEventValidationMap())
			})
		})
	})

	DescribeTable("should allow override", func(o mtqOverride) {
		args := createArgs()

		o.Set(args.mtq)
		err := args.client.Update(context.TODO(), args.mtq)
		Expect(err).ToNot(HaveOccurred())

		doReconcile(args)

		resources, err := getAllResources(args.reconciler)
		Expect(err).ToNot(HaveOccurred())

		for _, r := range resources {
			d, ok := r.(*appsv1.Deployment)
			if !ok {
				continue
			}

			d, err = getDeployment(args.client, d)
			Expect(err).ToNot(HaveOccurred())

			o.Check(d)
		}
		validateEvents(args.reconciler, createNotReadyEventValidationMap())
	},
		Entry("Pull override", &pullOverride{corev1.PullNever}),
	)

	Describe("Upgrading MTQ", func() {

		DescribeTable("check detects upgrade correctly", func(prevVersion, newVersion string, shouldUpgrade, shouldError bool) {
			//verify on int version is set
			args := createFromArgs(newVersion)
			doReconcile(args)
			setDeploymentsReady(args)

			Expect(args.mtq.Status.ObservedVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.OperatorVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.TargetVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			//Modify CRD to be of previousVersion
			err := crSetVersion(args.reconciler.reconciler, args.mtq, prevVersion)
			Expect(err).ToNot(HaveOccurred())

			if shouldError {
				doReconcileError(args)
				return
			}

			setDeploymentsDegraded(args)
			doReconcile(args)

			if shouldUpgrade {
				//verify upgraded has started
				Expect(args.mtq.Status.OperatorVersion).Should(Equal(newVersion))
				Expect(args.mtq.Status.ObservedVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.TargetVersion).Should(Equal(newVersion))
				Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseUpgrading))
			} else {
				//verify upgraded hasn't started
				Expect(args.mtq.Status.OperatorVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.ObservedVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.TargetVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))
			}

			//change deployment to ready
			isReady := setDeploymentsReady(args)
			Expect(isReady).Should(BeTrue())

			//now should be upgraded
			if shouldUpgrade {
				//verify versions were updated
				Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))
				Expect(args.mtq.Status.OperatorVersion).Should(Equal(newVersion))
				Expect(args.mtq.Status.TargetVersion).Should(Equal(newVersion))
				Expect(args.mtq.Status.ObservedVersion).Should(Equal(newVersion))
			} else {
				//verify versions remained unchaged
				Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))
				Expect(args.mtq.Status.OperatorVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.TargetVersion).Should(Equal(prevVersion))
				Expect(args.mtq.Status.ObservedVersion).Should(Equal(prevVersion))
			}
		},
			Entry("increasing semver ", "v1.9.5", "v1.10.0", true, false),
			Entry("decreasing semver", "v1.10.0", "v1.9.5", false, true),
			Entry("identical semver", "v1.10.0", "v1.10.0", false, false),
			Entry("invalid semver", "devel", "v1.9.5", true, false),
			Entry("increasing  semver no prefix", "1.9.5", "1.10.0", true, false),
			Entry("decreasing  semver no prefix", "1.10.0", "1.9.5", false, true),
			Entry("identical  semver no prefix", "1.10.0", "1.10.0", false, false),
			Entry("invalid  semver with prefix", "devel1.9.5", "devel1.9.5", false, false),
			Entry("invalid  semver no prefix", "devel", "1.9.5", true, false),
			/* having trouble making sense of this test "" should not be valid previous version
			Entry("no current no prefix", "", "invalid", false, false),
			*/
		)

		It("check detects upgrade w/o prev version", func() {
			prevVersion := ""
			newVersion := "v1.2.3"

			args := createFromArgs(prevVersion)
			doReconcile(args)
			setDeploymentsReady(args)

			Expect(args.mtq.Status.ObservedVersion).To(BeEmpty())
			Expect(args.mtq.Status.OperatorVersion).To(BeEmpty())
			Expect(args.mtq.Status.TargetVersion).To(BeEmpty())
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			args.reconciler.namespacedArgs.OperatorVersion = newVersion
			setDeploymentsDegraded(args)
			doReconcile(args)
			Expect(args.mtq.Status.ObservedVersion).To(BeEmpty())
			Expect(args.mtq.Status.OperatorVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.TargetVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseUpgrading))

			//change deployment to ready
			isReady := setDeploymentsReady(args)
			Expect(isReady).To(BeTrue())
			Expect(args.mtq.Status.ObservedVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.OperatorVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.TargetVersion).Should(Equal(newVersion))
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))
		})

		Describe("MTQ CR deletion during upgrade", func() {
			Context("cr deletion during upgrade", func() {
				It("should delete CR if it is marked for deletion and not begin upgrade flow", func() {
					newVersion := "1.10.0"
					prevVersion := "1.9.5"

					args := createFromArgs(newVersion)
					doReconcile(args)

					//set deployment to ready
					isReady := setDeploymentsReady(args)
					Expect(isReady).Should(BeTrue())

					//verify on int version is set
					Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

					//Modify CRD to be of previousVersion
					Expect(crSetVersion(args.reconciler.reconciler, args.mtq, prevVersion)).To(Succeed())
					//marc MTQ CR for deltetion
					args.mtq.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
					args.mtq.Finalizers = append(args.mtq.Finalizers, "keepmearound")
					Expect(args.client.Update(context.TODO(), args.mtq)).To(Succeed())

					doReconcile(args)

					//verify the version cr is deleted and upgrade hasn't started
					Expect(args.mtq.Status.OperatorVersion).Should(Equal(prevVersion))
					Expect(args.mtq.Status.ObservedVersion).Should(Equal(prevVersion))
					Expect(args.mtq.Status.TargetVersion).Should(Equal(prevVersion))
					Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeleted))
				})

				It("should delete CR if it is marked for deletion during upgrade flow", func() {
					newVersion := "1.10.0"
					prevVersion := "1.9.5"

					args := createFromArgs(newVersion)
					doReconcile(args)
					setDeploymentsReady(args)

					//verify on int version is set
					Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

					//Modify CRD to be of previousVersion
					Expect(crSetVersion(args.reconciler.reconciler, args.mtq, prevVersion)).To(Succeed())
					Expect(args.client.Update(context.TODO(), args.mtq)).To(Succeed())
					setDeploymentsDegraded(args)

					//begin upgrade
					doReconcile(args)

					//mark MTQ CR for deltetion
					args.mtq.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
					Expect(args.client.Update(context.TODO(), args.mtq)).To(Succeed())

					doReconcileExpectDelete(args)

					//verify events, this should include an upgrade event
					match := createNotReadyEventValidationMap()
					match["Normal DeployCompleted Deployment Completed"] = false
					match["Normal UpgradeStarted Started upgrade to version 1.10.0"] = false
					validateEvents(args.reconciler, match)
				})
			})
		})

		DescribeTable("Updates objects on upgrade", func(
			modify modifyResource,
			tomodify isModifySubject,
			upgraded isUpgraded) {

			newVersion := "1.10.0"
			prevVersion := "1.9.5"

			args := createFromArgs(newVersion)
			doReconcile(args)
			setDeploymentsReady(args)

			//verify on int version is set
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			//Modify CRD to be of previousVersion
			Expect(crSetVersion(args.reconciler.reconciler, args.mtq, prevVersion)).To(Succeed())
			Expect(args.client.Update(context.TODO(), args.mtq)).To(Succeed())

			setDeploymentsDegraded(args)

			//find the resource to modify
			oOriginal, oModified, err := getModifiedResource(args.reconciler, modify, tomodify)
			Expect(err).ToNot(HaveOccurred())

			//update object via client, with curObject
			Expect(args.client.Update(context.TODO(), oModified)).To(Succeed())

			//verify object is modified
			storedObj, err := getObject(args.client, oModified)
			Expect(err).ToNot(HaveOccurred())

			Expect(reflect.DeepEqual(storedObj, oModified)).Should(BeTrue())

			doReconcile(args)

			//verify upgraded has started
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseUpgrading))

			//change deployment to ready
			Expect(setDeploymentsReady(args)).Should(BeTrue())

			doReconcile(args)
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			//verify that stored object equals to object in getResources
			storedObj, err = getObject(args.client, oModified)
			Expect(err).ToNot(HaveOccurred())

			Expect(upgraded(storedObj, oOriginal)).Should(BeTrue())

		},
			//Deployment update
			Entry("verify - deployment updated on upgrade - annotation changed",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					deploymentOrig, ok := toModify.(*appsv1.Deployment)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					deployment := deploymentOrig.DeepCopy()
					deployment.Annotations["fake.anno.1"] = "fakeannotation1"
					deployment.Annotations["fake.anno.2"] = "fakeannotation2"
					deployment.Annotations["fake.anno.3"] = "fakeannotation3"
					return toModify, deployment, nil
				},
				func(resource client.Object) bool { //find resource for test
					//return true if object is the one we want to test
					_, ok := resource.(*appsv1.Deployment)
					return ok
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					postDep, ok := postUpgradeObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					desiredDep, ok := deisredObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					delete(desiredDep.Annotations, LastAppliedConfigAnnotation)

					for key, ann := range desiredDep.Annotations {
						if postDep.Annotations[key] != ann {
							return false
						}
					}

					return len(desiredDep.Annotations) <= len(postDep.Annotations)
				}),
			Entry("verify - deployment updated on upgrade - labels changed",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					deploymentOrig, ok := toModify.(*appsv1.Deployment)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					deployment := deploymentOrig.DeepCopy()
					deployment.Labels["fake.label.1"] = "fakelabel1"
					deployment.Labels["fake.label.2"] = "fakelabel2"
					deployment.Labels["fake.label.3"] = "fakelabel3"
					return toModify, deployment, nil
				},
				func(resource client.Object) bool { //find resource for test
					//return true if object is the one we want to test
					_, ok := resource.(*appsv1.Deployment)
					return ok
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					postDep, ok := postUpgradeObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					desiredDep, ok := deisredObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					for key, label := range desiredDep.Labels {
						if postDep.Labels[key] != label {
							return false
						}
					}

					return len(desiredDep.Labels) <= len(postDep.Labels)
				}),
			Entry("verify - deployment updated on upgrade - deployment spec changed - modify container",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					deploymentOrig, ok := toModify.(*appsv1.Deployment)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					deployment := deploymentOrig.DeepCopy()

					containers := deployment.Spec.Template.Spec.Containers
					containers[0].Env = []corev1.EnvVar{
						{
							Name:  "FAKE_ENVVAR",
							Value: fmt.Sprintf("%s/%s:%s", "fake_repo", "importerImage", "tag"),
						},
					}

					return toModify, deployment, nil
				},
				func(resource client.Object) bool { //find resource for test
					//search for mtq-deployment - to test ENV virables change
					deployment, ok := resource.(*appsv1.Deployment)
					if !ok {
						return false
					}
					if deployment.Name == "mtq-controller" {
						return true
					}
					return false
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					postDep, ok := postUpgradeObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					desiredDep, ok := deisredObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					for key, envVar := range desiredDep.Spec.Template.Spec.Containers[0].Env {
						if postDep.Spec.Template.Spec.Containers[0].Env[key].Name != envVar.Name {
							return false
						}
					}

					return len(desiredDep.Spec.Template.Spec.Containers[0].Env) == len(postDep.Spec.Template.Spec.Containers[0].Env)
				}),
			Entry("verify - deployment updated on upgrade - deployment spec changed - add new container",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					deploymentOrig, ok := toModify.(*appsv1.Deployment)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					deployment := deploymentOrig.DeepCopy()

					containers := deployment.Spec.Template.Spec.Containers
					container := corev1.Container{
						Name:            "FAKE_CONTAINER",
						Image:           fmt.Sprintf("%s/%s:%s", "fake-repo", "fake-image", "fake-tag"),
						ImagePullPolicy: "FakePullPolicy",
						Args:            []string{"-v=10"},
					}
					deployment.Spec.Template.Spec.Containers = append(containers, container)

					return toModify, deployment, nil
				},
				func(resource client.Object) bool { //find resource for test
					//search for mtq-deployment - to test container change
					deployment, ok := resource.(*appsv1.Deployment)
					if !ok {
						return false
					}
					if deployment.Name == "mtq-controller" {
						return true
					}
					return false
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					postDep, ok := postUpgradeObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					desiredDep, ok := deisredObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					for key, container := range desiredDep.Spec.Template.Spec.Containers {
						if postDep.Spec.Template.Spec.Containers[key].Name != container.Name {
							return false
						}
					}

					return len(desiredDep.Spec.Template.Spec.Containers) <= len(postDep.Spec.Template.Spec.Containers)
				}),
			Entry("verify - deployment updated on upgrade - deployment spec changed - remove existing container",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					deploymentOrig, ok := toModify.(*appsv1.Deployment)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					deployment := deploymentOrig.DeepCopy()

					deployment.Spec.Template.Spec.Containers = nil

					return toModify, deployment, nil
				},
				func(resource client.Object) bool { //find resource for test
					//search for mtq-deployment - to test container change
					deployment, ok := resource.(*appsv1.Deployment)
					if !ok {
						return false
					}
					if deployment.Name == "mtq-controller" {
						return true
					}
					return false
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					postDep, ok := postUpgradeObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					desiredDep, ok := deisredObj.(*appsv1.Deployment)
					if !ok {
						return false
					}

					return (len(postDep.Spec.Template.Spec.Containers) == len(desiredDep.Spec.Template.Spec.Containers))
				}),

			//Services update
			Entry("verify - services updated on upgrade - annotation changed",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					serviceOrig, ok := toModify.(*corev1.Service)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					service := serviceOrig.DeepCopy()
					service.Annotations["fake.anno.1"] = "fakeannotation1"
					service.Annotations["fake.anno.2"] = "fakeannotation2"
					service.Annotations["fake.anno.3"] = "fakeannotation3"
					return toModify, service, nil
				},
				func(resource client.Object) bool { //find resource for test
					//return true if object is the one we want to test
					_, ok := resource.(*corev1.Service)
					return ok
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					post, ok := postUpgradeObj.(*corev1.Service)
					if !ok {
						return false
					}

					desired, ok := deisredObj.(*corev1.Service)
					if !ok {
						return false
					}

					for key, ann := range desired.Annotations {
						if post.Annotations[key] != ann {
							return false
						}
					}

					return len(desired.Annotations) <= len(post.Annotations)
				}),

			Entry("verify - services updated on upgrade - label changed",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					serviceOrig, ok := toModify.(*corev1.Service)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					service := serviceOrig.DeepCopy()
					service.Labels["fake.label.1"] = "fakelabel1"
					service.Labels["fake.label.2"] = "fakelabel2"
					service.Labels["fake.label.3"] = "fakelabel3"
					return toModify, service, nil
				},
				func(resource client.Object) bool { //find resource for test
					//return true if object is the one we want to test
					_, ok := resource.(*corev1.Service)
					return ok
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has the same fields as desired
					post, ok := postUpgradeObj.(*corev1.Service)
					if !ok {
						return false
					}

					desired, ok := deisredObj.(*corev1.Service)
					if !ok {
						return false
					}

					for key, label := range desired.Labels {
						if post.Labels[key] != label {
							return false
						}
					}

					return len(desired.Labels) <= len(post.Labels)
				}),

			Entry("verify - services updated on upgrade - service port changed",
				func(toModify client.Object) (client.Object, client.Object, error) { //Modify
					serviceOrig, ok := toModify.(*corev1.Service)
					if !ok {
						return toModify, toModify, generrors.New("wrong type")
					}
					service := serviceOrig.DeepCopy()
					service.Spec.Ports = []corev1.ServicePort{
						{
							Port:     999999,
							Protocol: corev1.ProtocolUDP,
						},
					}
					return toModify, service, nil
				},
				func(resource client.Object) bool { //find resource for test
					//return true if object is the one we want to test
					_, ok := resource.(*corev1.Service)
					return ok
				},
				func(postUpgradeObj client.Object, deisredObj client.Object) bool { //check resource was upgraded
					//return true if postUpgrade has teh same fields as desired
					post, ok := postUpgradeObj.(*corev1.Service)
					if !ok {
						return false
					}

					desired, ok := deisredObj.(*corev1.Service)
					if !ok {
						return false
					}

					for key, port := range desired.Spec.Ports {
						if post.Spec.Ports[key].Port != port.Port {
							return false
						}
					}

					return len(desired.Spec.Ports) == len(post.Spec.Ports)
				}),
			//CRD update
			// - update CRD label
			// - update CRD annotation
			// - update CRD version
			// - update CRD spec
			// - update CRD status
			// - add new CRD
			// -

			//RBAC update
			// - update RoleBinding/ClusterRoleBinding
			// - Update Role/ClusterRole

			//ServiceAccount upgrade
			// - update ServiceAccount SCC
			// - update ServiceAccount Labels/Annotations

		) //updates objects on upgrade

		DescribeTable("Removes unused objects on upgrade", func(
			createObj createUnusedObject) {

			newVersion := "1.10.0"
			prevVersion := "1.9.5"

			args := createFromArgs(newVersion)
			doReconcile(args)

			setDeploymentsReady(args)

			//verify on int version is set
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			//Modify CRD to be of previousVersion
			Expect(crSetVersion(args.reconciler.reconciler, args.mtq, prevVersion)).To(Succeed())
			Expect(args.client.Update(context.TODO(), args.mtq)).To(Succeed())

			setDeploymentsDegraded(args)
			unusedObj, err := createObj()
			Expect(err).ToNot(HaveOccurred())
			unusedMetaObj := unusedObj.(metav1.Object)
			unusedMetaObj.GetLabels()["operator.mtq.kubevirt.io/createVersion"] = prevVersion
			err = controllerutil.SetControllerReference(args.mtq, unusedMetaObj, scheme.Scheme)
			Expect(err).ToNot(HaveOccurred())

			//add unused object via client, with curObject
			Expect(args.client.Create(context.TODO(), unusedObj)).To(Succeed())

			doReconcile(args)

			//verify upgraded has started
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseUpgrading))

			//verify unused exists before upgrade is done
			_, err = getObject(args.client, unusedObj)
			Expect(err).ToNot(HaveOccurred())

			//change deployment to ready
			Expect(setDeploymentsReady(args)).Should(BeTrue())

			doReconcile(args)
			Expect(args.mtq.Status.Phase).Should(Equal(sdkapi.PhaseDeployed))

			//verify that object no longer exists after upgrade
			_, err = getObject(args.client, unusedObj)
			Expect(errors.IsNotFound(err)).Should(BeTrue())

		},

			Entry("verify - unused deployment deleted",
				func() (client.Object, error) {
					const imagePullSecretName = "fake-registry-key"
					var imagePullSecrets = []corev1.LocalObjectReference{{Name: imagePullSecretName}}
					deployment := utils.CreateDeployment("fake-mtq-deployment", "app", "containerized-data-importer", "fake-sa", imagePullSecrets, int32(1), &sdkapi.NodePlacement{})
					return deployment, nil
				}),
			Entry("verify - unused service deleted",
				func() (client.Object, error) {
					service := utils.ResourceBuilder.CreateService("fake-mtq-service", "fake-service", "fake", nil)
					return service, nil
				}),
			Entry("verify - unused sa deleted",
				func() (client.Object, error) {
					sa := utils.ResourceBuilder.CreateServiceAccount("fake-mtq-sa")
					return sa, nil
				}),

			Entry("verify - unused crd deleted",
				func() (client.Object, error) {
					crd := &extv1.CustomResourceDefinition{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "apiextensions.k8s.io/v1",
							Kind:       "CustomResourceDefinition",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "fake.mtqs.mtq.kubevirt.io",
							Labels: map[string]string{
								"operator.mtq.kubevirt.io": "",
							},
						},
						Spec: extv1.CustomResourceDefinitionSpec{
							Group: "mtq.kubevirt.io",
							Scope: "Cluster",

							Versions: []extv1.CustomResourceDefinitionVersion{
								{
									Name:    "v1beta1",
									Served:  true,
									Storage: true,
									AdditionalPrinterColumns: []extv1.CustomResourceColumnDefinition{
										{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"},
										{Name: "Phase", Type: "string", JSONPath: ".status.phase"},
									},
								},
								{
									Name:    "v1alpha1",
									Served:  true,
									Storage: false,
									AdditionalPrinterColumns: []extv1.CustomResourceColumnDefinition{
										{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"},
										{Name: "Phase", Type: "string", JSONPath: ".status.phase"},
									},
								},
							},
							Names: extv1.CustomResourceDefinitionNames{
								Kind:     "FakeMTQ",
								ListKind: "FakeMTQList",
								Plural:   "fakemtqs",
								Singular: "fakemtq",
								Categories: []string{
									"all",
								},
								ShortNames: []string{"fakemtq", "fakemtqs"},
							},
						},
					}
					return crd, nil
				}),

			Entry("verify - unused role deleted",
				func() (client.Object, error) {
					role := utils.ResourceBuilder.CreateRole("fake-role", nil)
					return role, nil
				}),

			Entry("verify - unused role binding deleted",
				func() (client.Object, error) {
					role := utils.ResourceBuilder.CreateRoleBinding("fake-role", "fake-role", "fake-role", "fake-role")
					return role, nil
				}),
			Entry("verify - unused cluster role deleted",
				func() (client.Object, error) {
					role := utils.ResourceBuilder.CreateClusterRole("fake-cluster-role", nil)
					return role, nil
				}),
			Entry("verify - unused cluster role binding deleted",
				func() (client.Object, error) {
					role := utils.ResourceBuilder.CreateClusterRoleBinding("fake-cluster-role", "fake-cluster-role", "fake-cluster-role", "fake-cluster-role")
					return role, nil
				}),
		)

	})
})

func getModifiedResource(reconciler *ReconcileMTQ, modify modifyResource, tomodify isModifySubject) (client.Object, client.Object, error) {
	resources, err := getAllResources(reconciler)
	if err != nil {
		return nil, nil, err
	}

	//find the resource to modify
	var orig client.Object
	for _, resource := range resources {
		r, err := getObject(reconciler.client, resource)
		Expect(err).ToNot(HaveOccurred())
		if tomodify(r) {
			orig = r
			break
		}
	}
	//apply modify function on resource and return modified one
	return modify(orig)
}

type mtqOverride interface {
	Set(cr *mtqv1.MTQ)
	Check(d *appsv1.Deployment)
}

type pullOverride struct {
	value corev1.PullPolicy
}

func (o *pullOverride) Set(cr *mtqv1.MTQ) {
	cr.Spec.ImagePullPolicy = o.value
}

func (o *pullOverride) Check(d *appsv1.Deployment) {
	pp := d.Spec.Template.Spec.Containers[0].ImagePullPolicy
	Expect(pp).Should(Equal(o.value))
}

func getMTQ(client client.Client, mtq *mtqv1.MTQ) (*mtqv1.MTQ, error) {
	result, err := getObject(client, mtq)
	if err != nil {
		return nil, err
	}
	return result.(*mtqv1.MTQ), nil
}

func getSCC(client client.Client, scc *secv1.SecurityContextConstraints) (*secv1.SecurityContextConstraints, error) {
	result, err := getObject(client, scc)
	if err != nil {
		return nil, err
	}
	return result.(*secv1.SecurityContextConstraints), nil
}

func setDeploymentsReady(args *args) bool {
	resources, err := getAllResources(args.reconciler)
	Expect(err).ToNot(HaveOccurred())
	running := false

	for _, r := range resources {
		d, ok := r.(*appsv1.Deployment)
		if !ok {
			continue
		}

		Expect(running).To(BeFalse())

		d, err := getDeployment(args.client, d)
		Expect(err).ToNot(HaveOccurred())
		if d.Spec.Replicas != nil {
			d.Status.Replicas = *d.Spec.Replicas
			d.Status.ReadyReplicas = d.Status.Replicas
			err = args.client.Update(context.TODO(), d)
			Expect(err).ToNot(HaveOccurred())
		}

		doReconcile(args)

		if len(args.mtq.Status.Conditions) == 3 &&
			conditions.IsStatusConditionTrue(args.mtq.Status.Conditions, conditions.ConditionAvailable) &&
			conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionProgressing) &&
			conditions.IsStatusConditionFalse(args.mtq.Status.Conditions, conditions.ConditionDegraded) {
			running = true
		}
	}

	return running
}

func setDeploymentsDegraded(args *args) {
	resources, err := getAllResources(args.reconciler)
	Expect(err).ToNot(HaveOccurred())

	for _, r := range resources {
		d, ok := r.(*appsv1.Deployment)
		if !ok {
			continue
		}

		d, err := getDeployment(args.client, d)
		Expect(err).ToNot(HaveOccurred())
		if d.Spec.Replicas != nil {
			d.Status.Replicas = int32(0)
			d.Status.ReadyReplicas = d.Status.Replicas
			err = args.client.Update(context.TODO(), d)
			Expect(err).ToNot(HaveOccurred())
		}

	}
	doReconcile(args)
}

func getDeployment(client client.Client, deployment *appsv1.Deployment) (*appsv1.Deployment, error) {
	result, err := getObject(client, deployment)
	if err != nil {
		return nil, err
	}
	return result.(*appsv1.Deployment), nil
}

func getObject(c client.Client, obj client.Object) (client.Object, error) {
	metaObj := obj.(metav1.Object)
	key := client.ObjectKey{Namespace: metaObj.GetNamespace(), Name: metaObj.GetName()}

	typ := reflect.ValueOf(obj).Elem().Type()
	result := reflect.New(typ).Interface().(client.Object)

	if err := c.Get(context.TODO(), key, result); err != nil {
		return nil, err
	}

	return result, nil
}

func getAllResources(reconciler *ReconcileMTQ) ([]client.Object, error) {
	var result []client.Object
	crs, err := clusterResources.CreateAllStaticResources(reconciler.clusterArgs)
	if err != nil {
		return nil, err
	}

	result = append(result, crs...)

	nrs, err := namespaceResources.CreateAllResources(reconciler.namespacedArgs)
	if err != nil {
		return nil, err
	}

	result = append(result, nrs...)

	return result, nil
}

func reconcileRequest(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
}

func createFromArgs(version string) *args {
	mtq := createMTQ("mtq", "good uid")
	client := createClient(mtq)
	reconciler := createReconcilerWithVersion(client, version)

	return &args{
		mtq:        mtq,
		client:     client,
		reconciler: reconciler,
	}
}

func createArgs() *args {
	mtq := createMTQ("mtq", "good uid")
	client := createClient(mtq)
	reconciler := createReconciler(client)

	return &args{
		mtq:        mtq,
		client:     client,
		reconciler: reconciler,
	}
}

func doReconcile(args *args) {
	result, err := args.reconciler.Reconcile(context.TODO(), reconcileRequest(args.mtq.Name))
	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())

	args.mtq, err = getMTQ(args.client, args.mtq)
	Expect(err).ToNot(HaveOccurred())
}

func doReconcileError(args *args) {
	result, err := args.reconciler.Reconcile(context.TODO(), reconcileRequest(args.mtq.Name))
	Expect(err).To(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())

	args.mtq, err = getMTQ(args.client, args.mtq)
	Expect(err).ToNot(HaveOccurred())
}

func doReconcileRequeue(args *args) {
	result, err := args.reconciler.Reconcile(context.TODO(), reconcileRequest(args.mtq.Name))
	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue || result.RequeueAfter > 0).To(BeTrue())

	args.mtq, err = getMTQ(args.client, args.mtq)
	Expect(err).ToNot(HaveOccurred())
}

func doReconcileExpectDelete(args *args) {
	result, err := args.reconciler.Reconcile(context.TODO(), reconcileRequest(args.mtq.Name))
	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())

	_, err = getMTQ(args.client, args.mtq)
	Expect(err).To(HaveOccurred())
	Expect(errors.IsNotFound(err)).To(BeTrue())
}

func createClient(objs ...client.Object) client.Client {
	var runtimeObjs []runtime.Object
	for _, obj := range objs {
		runtimeObjs = append(runtimeObjs, obj)
	}
	return fakeClient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(runtimeObjs...).Build()
}

func createMTQ(name, uid string) *mtqv1.MTQ {
	return &mtqv1.MTQ{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MTQ",
			APIVersion: "mtqs.mtq.kubevirt.io",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(uid),
			Labels: map[string]string{
				common.AppKubernetesManagedByLabel: "tests",
				common.AppKubernetesPartOfLabel:    "testing",
				common.AppKubernetesVersionLabel:   "v0.0.0-tests",
				common.AppKubernetesComponentLabel: "storage",
			},
		},
	}
}

func createReconcilerWithVersion(client client.Client, version string) *ReconcileMTQ {
	r := createReconciler(client)
	r.namespacedArgs.OperatorVersion = version
	return r
}

func createReconciler(client client.Client) *ReconcileMTQ {
	namespace := "mtq"
	clusterArgs := &clusterResources.FactoryArgs{
		Namespace: namespace,
		Client:    client,
		Logger:    log,
	}
	namespacedArgs := &namespaceResources.FactoryArgs{
		OperatorVersion:        version,
		DeployClusterResources: "true",
		ControllerImage:        "mtq-controller",
		MTQLockServerImage:     "mtq-lock-server",
		Verbosity:              "1",
		PullPolicy:             "Always",
		Namespace:              namespace,
	}

	recorder := record.NewFakeRecorder(250)
	r := &ReconcileMTQ{
		client:         client,
		uncachedClient: client,
		scheme:         scheme.Scheme,
		recorder:       recorder,
		namespace:      namespace,
		clusterArgs:    clusterArgs,
		namespacedArgs: namespacedArgs,
		certManager:    newFakeCertManager(client, namespace),
	}
	callbackDispatcher := callbacks.NewCallbackDispatcher(log, client, client, scheme.Scheme, namespace)
	r.reconciler = sdkr.NewReconciler(r, log, client, callbackDispatcher, scheme.Scheme, createVersionLabel, updateVersionLabel, LastAppliedConfigAnnotation, certPollInterval, finalizerName, false, recorder).
		WithWatching(true)

	r.registerHooks()

	return r
}

func crSetVersion(r *sdkr.Reconciler, cr *mtqv1.MTQ, version string) error {
	return r.CrSetVersion(cr, version)
}

func validateEvents(reconciler *ReconcileMTQ, match map[string]bool) {
	events := reconciler.recorder.(*record.FakeRecorder).Events
	// Closing the channel allows me to do non blocking reads of the channel, once the channel runs out of items the loop exits.
	close(events)
	for event := range events {
		val, ok := match[event]
		Expect(ok).To(BeTrue(), "Event [%s] was not expected", event)
		if !val {
			match[event] = true
		}
	}
	for k, v := range match {
		Expect(v).To(BeTrue(), "Event [%s] not observed", k)
	}
}

func createErrorMTQEventValidationMap() map[string]bool {
	match := createNotReadyEventValidationMap()
	match["Warning ConfigError Reconciling to error state, unwanted MTQ object"] = false
	return match
}

func createReadyEventValidationMap() map[string]bool {
	match := createNotReadyEventValidationMap()
	match["Normal DeployCompleted Deployment Completed"] = false
	return match
}

func createNotReadyEventValidationMap() map[string]bool {
	// match is map of strings and if we observed the event.
	// We are not interested in the order of the events, just that the events happen at least once.
	match := make(map[string]bool)
	match["Normal DeployStarted Started Deployment"] = false
	match[normalCreateSuccess+" *v1.ClusterRole mtq-controller"] = false
	match[normalCreateSuccess+" *v1.ClusterRoleBinding mtq-controller"] = false
	match[normalCreateSuccess+" *v1.ClusterRole mtq-lock"] = false
	match[normalCreateSuccess+" *v1.ClusterRoleBinding mtq-lock"] = false
	match[normalCreateSuccess+" *v1.Role mtq-lock"] = false
	match[normalCreateSuccess+" *v1.RoleBinding mtq-lock"] = false
	match[normalCreateSuccess+" *v1.Role mtq-controller"] = false
	match[normalCreateSuccess+" *v1.RoleBinding mtq-controller"] = false
	match[normalCreateSuccess+" *v1.ServiceAccount mtq-controller"] = false
	match[normalCreateSuccess+" *v1.ServiceAccount mtq-lock"] = false
	match[normalCreateSuccess+" *v1.Service mtq-lock"] = false
	match[normalCreateSuccess+" *v1.Deployment mtq-lock"] = false
	match[normalCreateSuccess+" *v1.Deployment mtq-controller"] = false
	match[normalCreateSuccess+" *v1.CustomResourceDefinition virtualmachinemigrationresourcequotas.mtq.kubevirt.io"] = false
	match[normalCreateSuccess+" *v1.Secret mtq-lock"] = false
	match[normalCreateSuccess+" *v1.ConfigMap mtq-lock-signer-bundle"] = false
	match[normalCreateSuccess+" *v1.Secret mtq-lock-server-cert"] = false

	return match
}

const testCertData = "test"

type fakeCertManager struct {
	client    client.Client
	namespace string
}

func (tcm *fakeCertManager) Sync(certs []cert.CertificateDefinition) error {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: tcm.namespace, Name: "mtq-lock-signer-bundle"}
	err := tcm.client.Get(context.TODO(), key, cm)
	// should exist
	if err != nil {
		return err
	}
	cm.Data = map[string]string{
		"ca-bundle.crt": testCertData,
	}
	return tcm.client.Update(context.TODO(), cm)
}

// creating certs is really CPU intensive so mocking out a CertManager to just create what we need
func newFakeCertManager(crClient client.Client, namespace string) CertManager {
	return &fakeCertManager{client: crClient, namespace: namespace}
}
