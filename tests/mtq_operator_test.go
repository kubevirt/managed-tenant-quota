package tests_test

import (
	"context"
	"encoding/json"
	"fmt"
	schedulev1 "k8s.io/api/scheduling/v1"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
	"kubevirt.io/managed-tenant-quota/tests/utils"
	"reflect"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	conditions "github.com/openshift/custom-resource-status/conditions/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	sdkapi "kubevirt.io/controller-lifecycle-operator-sdk/api"
	"kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk"
	resourcesutils "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	mtqv1 "kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
	"kubevirt.io/managed-tenant-quota/tests/framework"
)

const (
	assertionPollInterval      = 2 * time.Second
	CompletionTimeout          = 270 * time.Second
	MTQControllerLabelSelector = resourcesutils.MTQLabel + "=" + resourcesutils.ControllerPodName
	MTQLockServerLabelSelector = resourcesutils.MTQLabel + "=" + resourcesutils.LockServerPodName

	mtqControllerPodPrefix = "mtq-controller-"
	mtqLockServerPodPrefix = "mtq-lock-"
)

var _ = Describe("ALL Operator tests", func() {
	Context("[Destructive]", func() {
		var _ = Describe("Operator tests", func() {
			f := framework.NewFramework("operator-test")

			// Condition flags can be found here with their meaning https://github.com/kubevirt/hyperconverged-cluster-operator/blob/main/docs/conditions.md
			It("Condition flags on CR should be healthy and operating", func() {
				Eventually(func() error {
					mtqObject := getMTQ(f)
					conditionMap := sdk.GetConditionValues(mtqObject.Status.Conditions)
					if conditionMap[conditions.ConditionAvailable] != corev1.ConditionTrue {
						return fmt.Errorf("ConditionAvailable is false")
					}
					if conditionMap[conditions.ConditionProgressing] != corev1.ConditionFalse {
						return fmt.Errorf("ConditionProgressing is true")
					}
					if conditionMap[conditions.ConditionDegraded] != corev1.ConditionFalse {
						return fmt.Errorf("ConditionDegraded is true")
					}
					return nil
				}, 5*time.Minute, 2*time.Second).Should(BeNil())
			})
		})

		var _ = Describe("Tests that require restore nodes", func() {
			var nodes *corev1.NodeList
			var mtqPods *corev1.PodList
			var err error
			f := framework.NewFramework("operator-delete-mtq-test")

			BeforeEach(func() {
				nodes, err = f.K8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
				Expect(nodes.Items).ToNot(BeEmpty(), "There should be some compute node")
				Expect(err).ToNot(HaveOccurred())
				mtqPods = getMTQPods(f)
			})

			AfterEach(func() {
				var newMtqPods *corev1.PodList
				By("Restoring nodes")
				for _, node := range nodes.Items {
					Eventually(func() error {
						newNode, err := f.K8sClient.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						newNode.Spec = node.Spec
						_, err = f.K8sClient.CoreV1().Nodes().Update(context.TODO(), newNode, metav1.UpdateOptions{})
						return err
					}, 5*time.Minute, 2*time.Second).Should(BeNil())
				}

				By("Waiting for there to be amount of MTQ pods like before")
				Eventually(func() error {
					newMtqPods = getMTQPods(f)
					if len(mtqPods.Items) != len(newMtqPods.Items) {
						return fmt.Errorf("Original number of mtq pods: %d\n is diffrent from the new number of mtq pods: %d\n", len(mtqPods.Items), len(newMtqPods.Items))
					}
					return nil
				}, 5*time.Minute, 2*time.Second).Should(BeNil())

				for _, newMtqPod := range newMtqPods.Items {
					By(fmt.Sprintf("Waiting for MTQ pod %s to be ready", newMtqPod.Name))
					err := utils.WaitTimeoutForPodReady(f.K8sClient, newMtqPod.Name, newMtqPod.Namespace, 2*time.Minute)
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func() error {
					services, err := f.K8sClient.CoreV1().Services(f.MTQInstallNs).List(context.TODO(), metav1.ListOptions{})
					Expect(err).ToNot(HaveOccurred(), "failed getting MTQ services")
					for _, service := range services.Items {
						endpoint, err := f.K8sClient.CoreV1().Endpoints(f.MTQInstallNs).Get(context.TODO(), service.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred(), "failed getting service endpoint")
						for _, subset := range endpoint.Subsets {
							if len(subset.NotReadyAddresses) > 0 {
								return fmt.Errorf("not all endpoints of service %s are ready", service.Name)
							}
						}
					}
					return nil
				}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())
			})

			It("should deploy components that tolerate CriticalAddonsOnly taint", func() {
				mtq := getMTQ(f)
				criticalAddonsToleration := corev1.Toleration{
					Key:      "CriticalAddonsOnly",
					Operator: corev1.TolerationOpExists,
				}

				if !tolerationExists(mtq.Spec.Infra.Tolerations, criticalAddonsToleration) {
					Skip("Unexpected MTQ CR (not from mtq-cr.yaml), doesn't tolerate CriticalAddonsOnly")
				}

				By("adding taints to all nodes")
				criticalPodTaint := corev1.Taint{
					Key:    "CriticalAddonsOnly",
					Value:  "",
					Effect: corev1.TaintEffectNoExecute,
				}

				for _, node := range nodes.Items {
					Eventually(func() error {
						nodeCopy, err := f.K8sClient.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						nodeCopy.Spec.Taints = append(nodeCopy.Spec.Taints, criticalPodTaint)
						_, err = f.K8sClient.CoreV1().Nodes().Update(context.TODO(), nodeCopy, metav1.UpdateOptions{})
						return err
					}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())

					Eventually(func() error {
						nodeCopy, err := f.K8sClient.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						if !nodeHasTaint(*nodeCopy, criticalPodTaint) {
							return fmt.Errorf("node still doesn't have the criticalPodTaint")
						}
						return nil
					}, 5*time.Minute, 2*time.Second).Should(BeNil())
				}

				By("Checking that all pods are running")
				for _, mtqPods := range mtqPods.Items {
					By(fmt.Sprintf("MTQ pod: %s", mtqPods.Name))
					podUpdated, err := f.K8sClient.CoreV1().Pods(mtqPods.Namespace).Get(context.TODO(), mtqPods.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred(), "failed setting taint on node")
					Expect(podUpdated.Status.Phase).To(Equal(corev1.PodRunning))
				}
			})
		})

		var _ = Describe("Operator delete MTQ CR tests", func() {
			var cr *mtqv1.MTQ
			f := framework.NewFramework("operator-delete-mtq-test")
			var mtqPods *corev1.PodList

			BeforeEach(func() {
				cr = getMTQ(f)
				mtqPods = getMTQPods(f)
			})

			AfterEach(func() {
				removeMTQ(f, cr)
				updateOrCreateMTQComponentsAndEnsureReady(f, cr, mtqPods)
			})

			It("should remove/install MTQ a number of times successfully", func() {
				for i := 0; i < 3; i++ {
					err := f.MtqClient.MtqV1alpha1().MTQs().Delete(context.TODO(), cr.Name, metav1.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())
					updateOrCreateMTQComponentsAndEnsureReady(f, cr, mtqPods)
				}
			})

		})

		var _ = Describe("mtq Operator deployment + mtq CR delete tests", func() {
			var mtqBackup *mtqv1.MTQ
			var mtqOperatorDeploymentBackup *appsv1.Deployment
			nodeSelectorTestValue := map[string]string{"kubernetes.io/arch": runtime.GOARCH}
			tolerationTestValue := []corev1.Toleration{{Key: "test", Value: "123"}, {Key: "CriticalAddonsOnly", Value: string(corev1.TolerationOpExists)}}
			f := framework.NewFramework("operator-delete-mtq-test")

			BeforeEach(func() {
				currentCR := getMTQ(f)

				mtqBackup = &mtqv1.MTQ{
					ObjectMeta: metav1.ObjectMeta{
						Name: currentCR.Name,
					},
					Spec: currentCR.Spec,
				}

				currentMTQOperatorDeployment, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), "mtq-operator", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				mtqOperatorDeploymentBackup = &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mtq-operator",
						Namespace: f.MTQInstallNs,
					},
					Spec: currentMTQOperatorDeployment.Spec,
				}

				removeMTQCrAndOperator(f, mtqBackup)
			})

			AfterEach(func() {
				removeMTQCrAndOperator(f, mtqBackup)
				deployMTQOperator(f, mtqBackup, mtqOperatorDeploymentBackup)
			})

			It("Should install MTQ infrastructure pods with node placement", func() {
				By("Creating modified MTQ CR, with infra nodePlacement")
				localSpec := mtqBackup.Spec.DeepCopy()
				localSpec.Infra = f.GetNodePlacementValuesWithRandomNodeAffinity(nodeSelectorTestValue, tolerationTestValue)

				tempMtqCr := &mtqv1.MTQ{
					ObjectMeta: metav1.ObjectMeta{
						Name: mtqBackup.Name,
					},
					Spec: *localSpec,
				}

				deployMTQOperator(f, tempMtqCr, mtqOperatorDeploymentBackup)

				By("Testing all infra deployments have the chosen node placement")
				for _, deploymentName := range []string{"mtq-lock", "mtq-controller"} {
					deployment, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), deploymentName, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					err = f.PodSpecHasTestNodePlacementValues(deployment.Spec.Template.Spec, localSpec.Infra)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})

		var _ = Describe("Strict Reconciliation tests", func() {
			f := framework.NewFramework("strict-reconciliation-test")

			It("mtq-deployment replicas back to original value on attempt to scale", func() {
				By("Overwrite number of replicas with 10")
				deploymentName := "mtq-controller"
				originalReplicaVal := scaleDeployment(f, deploymentName, 10)

				By("Ensuring original value of replicas restored & extra deployment pod was cleaned up")
				Eventually(func() error {
					depl, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), deploymentName, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					if *depl.Spec.Replicas != originalReplicaVal {
						return fmt.Errorf("original replicas value in deployment should be restore by mtq-operator")
					}
					return nil
				}, 5*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())
			})

			It("Service spec.selector restored on overwrite attempt", func() {
				service, err := f.K8sClient.CoreV1().Services(f.MTQInstallNs).Get(context.TODO(), "mtq-lock", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				originalSelectorVal := service.Spec.Selector[resourcesutils.MTQLabel]

				By("Overwrite spec.selector with empty string")
				service.Spec.Selector[resourcesutils.MTQLabel] = ""
				_, err = f.K8sClient.CoreV1().Services(f.MTQInstallNs).Update(context.TODO(), service, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() error {
					svc, err := f.K8sClient.CoreV1().Services(f.MTQInstallNs).Get(context.TODO(), "mtq-lock", metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					if svc.Spec.Selector[resourcesutils.MTQLabel] != originalSelectorVal {
						return fmt.Errorf("original spec.selector value: %s\n should Matches current: %s\n", originalSelectorVal, svc.Spec.Selector[resourcesutils.MTQLabel])
					}
					return nil
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())
			})

			It("ServiceAccount values restored on update attempt", func() {
				serviceAccount, err := f.K8sClient.CoreV1().ServiceAccounts(f.MTQInstallNs).Get(context.TODO(), resourcesutils.ControllerServiceAccountName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Change one of ServiceAccount labels")
				serviceAccount.Labels[resourcesutils.MTQLabel] = "somebadvalue"

				_, err = f.K8sClient.CoreV1().ServiceAccounts(f.MTQInstallNs).Update(context.TODO(), serviceAccount, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Waiting until label value restored")
				Eventually(func() error {
					sa, err := f.K8sClient.CoreV1().ServiceAccounts(f.MTQInstallNs).Get(context.TODO(), resourcesutils.ControllerServiceAccountName, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					if sa.Labels[resourcesutils.MTQLabel] != "" {
						return fmt.Errorf("mtq label should be restores if modifed")
					}
					return nil
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())
			})

			It("Certificate restored to ConfigMap on deletion attempt", func() {
				configMap, err := f.K8sClient.CoreV1().ConfigMaps(f.MTQInstallNs).Get(context.TODO(), "mtq-lock-signer-bundle", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Empty ConfigMap's data")
				configMap.Data = map[string]string{}

				_, err = f.K8sClient.CoreV1().ConfigMaps(f.MTQInstallNs).Update(context.TODO(), configMap, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Waiting until ConfigMap's data is not empty")
				Eventually(func() error {
					cm, err := f.K8sClient.CoreV1().ConfigMaps(f.MTQInstallNs).Get(context.TODO(), "mtq-lock-signer-bundle", metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					if len(cm.Data) == 0 {
						return fmt.Errorf("mtq-lock-signer-bundle data should be filled by mtq-operator if modified")
					}
					return nil
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())
			})
		})

		var _ = Describe("Operator cert config tests", func() {
			var mtqBackup *mtqv1.MTQ
			f := framework.NewFramework("operator-cert-config-test")

			BeforeEach(func() {
				mtqBackup = getMTQ(f)
			})

			AfterEach(func() {
				if mtqBackup == nil {
					return
				}

				cr, err := f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), mtqBackup.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				cr.Spec.CertConfig = mtqBackup.Spec.CertConfig

				_, err = f.MtqClient.MtqV1alpha1().MTQs().Update(context.TODO(), cr, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())
			})

			validateCertConfig := func(obj metav1.Object, lifetime, refresh string) {
				cca, ok := obj.GetAnnotations()["operator.mtq.kubevirt.io/certConfig"]
				Expect(ok).To(BeTrue())
				certConfig := make(map[string]interface{})
				err := json.Unmarshal([]byte(cca), &certConfig)
				Expect(err).ToNot(HaveOccurred())
				l, ok := certConfig["lifetime"]
				Expect(ok).To(BeTrue())
				Expect(l.(string)).To(Equal(lifetime))
				r, ok := certConfig["refresh"]
				Expect(ok).To(BeTrue())
				Expect(r.(string)).To(Equal(refresh))
			}

			It("should allow update", func() {
				origNotAfterTime := map[string]time.Time{}
				caSecret, err := f.K8sClient.CoreV1().Secrets(f.MTQInstallNs).Get(context.TODO(), "mtq-lock", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				serverSecret, err := f.K8sClient.CoreV1().Secrets(f.MTQInstallNs).Get(context.TODO(), namespaced.SecretResourceName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				for _, s := range []*corev1.Secret{caSecret, serverSecret} {
					notAfterAnn := s.Annotations["auth.openshift.io/certificate-not-after"]
					notAfterTIme, err := time.Parse(time.RFC3339, notAfterAnn)
					Expect(err).ToNot(HaveOccurred())
					origNotAfterTime[s.Name] = notAfterTIme
				}

				Eventually(func() error {
					cr := getMTQ(f)
					cr.Spec.CertConfig = &mtqv1.MTQCertConfig{
						CA: &mtqv1.CertConfig{
							Duration:    &metav1.Duration{Duration: time.Minute * 20},
							RenewBefore: &metav1.Duration{Duration: time.Minute * 5},
						},
						Server: &mtqv1.CertConfig{
							Duration:    &metav1.Duration{Duration: time.Minute * 5},
							RenewBefore: &metav1.Duration{Duration: time.Minute * 2},
						},
					}
					newCR, err := f.MtqClient.MtqV1alpha1().MTQs().Update(context.TODO(), cr, metav1.UpdateOptions{})
					if err != nil {
						return err
					}
					Expect(newCR.Spec.CertConfig).To(Equal(cr.Spec.CertConfig))
					By("Cert config update complete")
					return nil
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())

				By("Verify secrets have been modified")
				Eventually(func() error {
					for _, secret := range []*corev1.Secret{caSecret, serverSecret} {
						currSecret, err := f.K8sClient.CoreV1().Secrets(f.MTQInstallNs).Get(context.TODO(), secret.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						notAfterAnn := currSecret.Annotations["auth.openshift.io/certificate-not-after"]
						notAfterTime, err := time.Parse(time.RFC3339, notAfterAnn)
						Expect(err).ToNot(HaveOccurred())
						if notAfterTime.Equal(origNotAfterTime[currSecret.Name]) {
							return fmt.Errorf("not-before-time annotation should change")
						}
					}
					return nil
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())

				By("Verify secrets have the right values")
				Eventually(func() error {
					caSecret, err := f.K8sClient.CoreV1().Secrets(f.MTQInstallNs).Get(context.TODO(), "mtq-lock", metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())

					serverSecret, err := f.K8sClient.CoreV1().Secrets(f.MTQInstallNs).Get(context.TODO(), namespaced.SecretResourceName, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())

					currCaSecretNotBeforeAnn := caSecret.Annotations["auth.openshift.io/certificate-not-before"]
					caNotBeforeTime, err := time.Parse(time.RFC3339, currCaSecretNotBeforeAnn)
					Expect(err).ToNot(HaveOccurred())
					currCaSecretNotAnn := caSecret.Annotations["auth.openshift.io/certificate-not-after"]
					caNotAfterTime, err := time.Parse(time.RFC3339, currCaSecretNotAnn)
					Expect(err).ToNot(HaveOccurred())
					if caNotAfterTime.Sub(caNotBeforeTime) < time.Minute*20 {
						return fmt.Errorf("Not-Before (%s) should be 20 minutes before Not-After (%s)\n", currCaSecretNotBeforeAnn, currCaSecretNotAnn)
					}
					if caNotAfterTime.Sub(caNotBeforeTime)-(time.Minute*20) > time.Second {
						return fmt.Errorf("Not-Before (%s) should be 20 minutes before Not-After (%s) with 1 second toleration\n", currCaSecretNotBeforeAnn, currCaSecretNotAnn)
					}
					// 20m - 5m = 15m
					validateCertConfig(caSecret, "20m0s", "15m0s")

					currServerSecretNotBeforeAnn := serverSecret.Annotations["auth.openshift.io/certificate-not-before"]
					serverNotBeforeTime, err := time.Parse(time.RFC3339, currServerSecretNotBeforeAnn)
					Expect(err).ToNot(HaveOccurred())
					currServerNotAfterAnn := serverSecret.Annotations["auth.openshift.io/certificate-not-after"]
					serverNotAfterTime, err := time.Parse(time.RFC3339, currServerNotAfterAnn)
					Expect(err).ToNot(HaveOccurred())
					if serverNotAfterTime.Sub(serverNotBeforeTime) < time.Minute*5 {
						return fmt.Errorf("Not-Before (%s) should be 5 minutes before Not-After (%s)\n", currServerSecretNotBeforeAnn, currServerNotAfterAnn)
					}
					if serverNotAfterTime.Sub(serverNotBeforeTime)-(time.Minute*5) > time.Second {
						return fmt.Errorf("Not-Before (%s) should be 5 minutes before Not-After (%s) with 1 second toleration\n", currServerSecretNotBeforeAnn, currServerNotAfterAnn)
					}
					// 5m - 2m = 3m
					validateCertConfig(serverSecret, "5m0s", "3m0s")
					return nil
				}, 2*time.Minute, 1*time.Second).Should(BeNil())
			})
		})

		var _ = Describe("Priority class tests", func() {
			var (
				mtq                   *mtqv1.MTQ
				systemClusterCritical = mtqv1.MTQPriorityClass("system-cluster-critical")
				osUserCrit            = &schedulev1.PriorityClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourcesutils.MTQPriorityClass,
					},
					Value: 10000,
				}
			)
			f := framework.NewFramework("operator-priority-class-test")
			verifyPodPriorityClass := func(prefix, priorityClassName, labelSelector string) {
				Eventually(func() error {
					controllerPod, err := utils.FindPodByPrefix(f.K8sClient, f.MTQInstallNs, prefix, labelSelector)
					if err != nil {
						return err
					}
					if controllerPod.Spec.PriorityClassName != priorityClassName {
						return fmt.Errorf("controllerPod.Spec.PriorityClassName: %v is not equal to PriorityClassName:%v", controllerPod.Spec.PriorityClassName, priorityClassName)
					}
					return nil
				}, 30*time.Second, 1*time.Second).Should(BeNil())
			}

			BeforeEach(func() {
				list, err := f.VirtClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
				if err != nil {
					return
				}
				if len(list.Items) < 3 {
					Skip("This test require at least 3 nodes")
				}
				mtq = getMTQ(f)
				if mtq.Spec.PriorityClass != nil {
					By(fmt.Sprintf("Current priority class is: [%s]", *mtq.Spec.PriorityClass))
				}

			})

			AfterEach(func() {
				var cr *mtqv1.MTQ
				Eventually(func() error {
					cr = getMTQ(f)
					cr.Spec.PriorityClass = mtq.Spec.PriorityClass
					_, err := f.MtqClient.MtqV1alpha1().MTQs().Update(context.TODO(), cr, metav1.UpdateOptions{})
					return err
				}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())

				if !utils.IsOpenshift(f.K8sClient) {
					Eventually(func() error {
						if !errors.IsNotFound(f.K8sClient.SchedulingV1().PriorityClasses().Delete(context.TODO(), osUserCrit.Name, metav1.DeleteOptions{})) {
							return fmt.Errorf("Priority class " + osUserCrit.Name + " should not exsist ")
						}
						return nil
					}, 2*time.Minute, 1*time.Second).ShouldNot(HaveOccurred())
				}
				By("Ensuring the MTQ priority class is restored")
				prioClass := ""
				if cr.Spec.PriorityClass != nil {
					prioClass = string(*cr.Spec.PriorityClass)
				} else if utils.IsOpenshift(f.K8sClient) {
					prioClass = osUserCrit.Name
				}
				podToSelector := map[string]string{mtqControllerPodPrefix: MTQControllerLabelSelector, mtqLockServerPodPrefix: MTQLockServerLabelSelector}
				verifyPodPriorityClass(mtqControllerPodPrefix, prioClass, MTQControllerLabelSelector)
				verifyPodPriorityClass(mtqLockServerPodPrefix, prioClass, "")

				for prefix := range podToSelector {
					Eventually(func() error {
						pod, err := utils.FindPodByPrefix(f.K8sClient, f.MTQInstallNs, prefix, podToSelector[prefix])
						if err != nil {
							return err
						}
						if pod.Status.Phase != corev1.PodRunning {
							return fmt.Errorf(pod.Name + " is not running")
						}
						return nil
					}, 3*time.Minute, 1*time.Second).Should(BeNil())
				}

			})

			It("should use kubernetes priority class if set", func() {
				cr := getMTQ(f)
				By("Setting the priority class to system cluster critical, which is known to exist")
				cr.Spec.PriorityClass = &systemClusterCritical
				_, err := f.MtqClient.MtqV1alpha1().MTQs().Update(context.TODO(), cr, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())
				By("Verifying the MTQ deployment is updated")
				verifyPodPriorityClass(mtqControllerPodPrefix, string(systemClusterCritical), MTQControllerLabelSelector)
				By("Verifying the MTQ api server is updated")
				verifyPodPriorityClass(mtqLockServerPodPrefix, string(systemClusterCritical), MTQLockServerLabelSelector)

			})

			It("should use openshift priority class if not set and available", func() {
				if utils.IsOpenshift(f.K8sClient) {
					Skip("This test is not needed in OpenShift")
				}
				getMTQ(f)
				_, err := f.K8sClient.SchedulingV1().PriorityClasses().Create(context.TODO(), osUserCrit, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())
				By("Verifying the MTQ control plane is updated")
				verifyPodPriorityClass(mtqControllerPodPrefix, osUserCrit.Name, MTQControllerLabelSelector)
				verifyPodPriorityClass(mtqLockServerPodPrefix, osUserCrit.Name, MTQLockServerLabelSelector)
			})
		})
	})
})

func getMTQPods(f *framework.Framework) *corev1.PodList {
	By("Getting MTQ pods")
	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": "multi-tenant"}}
	mtqPods, err := f.K8sClient.CoreV1().Pods(f.MTQInstallNs).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed listing mtq pods")
	ExpectWithOffset(1, mtqPods.Items).ToNot(BeEmpty(), "no mtq pods found")
	return mtqPods
}

func getMTQ(f *framework.Framework) *mtqv1.MTQ {
	By("Getting MTQ resource")
	mtqs, err := f.MtqClient.MtqV1alpha1().MTQs().List(context.TODO(), metav1.ListOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, mtqs.Items).To(HaveLen(1))
	return &mtqs.Items[0]
}
func removeMTQCrAndOperator(f *framework.Framework, mtq *mtqv1.MTQ) {
	removeMTQ(f, mtq)
	By("Deleting MTQ operator")
	err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Delete(context.TODO(), "mtq-operator", metav1.DeleteOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	By("Waiting for MTQ operator deployment to be deleted")
	EventuallyWithOffset(1, func() error {
		if mtqOperatorDeploymentGone(f) == false {
			return fmt.Errorf("operator deployment should be deleted")
		}
		return nil
	}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())
}
func removeMTQ(f *framework.Framework, cr *mtqv1.MTQ) {
	By("Deleting MTQ CR if exists")
	_ = f.MtqClient.MtqV1alpha1().MTQs().Delete(context.TODO(), cr.Name, metav1.DeleteOptions{})

	By("Waiting for MTQ CR and infra deployments to be gone now that we are sure there's no MTQ CR")

	EventuallyWithOffset(1, func() error {
		for _, deploymentName := range []string{"mtq-lock", "mtq-controller"} {
			_, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), deploymentName, metav1.GetOptions{})
			if !errors.IsNotFound(err) {
				return fmt.Errorf(deploymentName + " should be deleted")
			}
		}
		_, err := f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), cr.Name, metav1.GetOptions{})
		if !errors.IsNotFound(err) {
			return fmt.Errorf("mtq should be deleted")
		}
		return nil
	}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())
}
func deployMTQOperator(f *framework.Framework, mtq *mtqv1.MTQ, mtqOperatorDeployment *appsv1.Deployment) {
	By("Re-creating MTQ (CR and deployment)")
	_, err := f.MtqClient.MtqV1alpha1().MTQs().Create(context.TODO(), mtq, metav1.CreateOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	By("Recreating MTQ operator")
	_, err = f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Create(context.TODO(), mtqOperatorDeployment, metav1.CreateOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	By("Verifying MTQ lock-server, controller exist, before continuing")
	EventuallyWithOffset(1, func() error {
		if !infraDeploymentAvailable(f, mtq) {
			return fmt.Errorf("mtq operator should be available")
		}
		return nil
	}, CompletionTimeout, assertionPollInterval).ShouldNot(HaveOccurred())
}
func updateOrCreateMTQComponentsAndEnsureReady(f *framework.Framework, updatedMtq *mtqv1.MTQ, mtqPods *corev1.PodList) {
	By("Check if MTQ CR exists")
	mtq, err := f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), updatedMtq.Name, metav1.GetOptions{})
	if err == nil {
		if mtq.DeletionTimestamp == nil {
			By("MTQ CR exists")
			mtq.Spec = updatedMtq.Spec
			_, err = f.MtqClient.MtqV1alpha1().MTQs().Update(context.TODO(), mtq, metav1.UpdateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return
		}

		By("Waiting for MTQ CR deletion")
		EventuallyWithOffset(1, func() error {
			_, err = f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), updatedMtq.Name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return nil
			}
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return fmt.Errorf("MTQ with deletion timestemp is not being deleted")
		}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())
	} else {
		ExpectWithOffset(1, errors.IsNotFound(err)).To(BeTrue())
	}

	mtq = &mtqv1.MTQ{
		ObjectMeta: metav1.ObjectMeta{
			Name: updatedMtq.Name,
		},
		Spec: updatedMtq.Spec,
	}

	By("Create MTQ CR")
	_, err = f.MtqClient.MtqV1alpha1().MTQs().Create(context.TODO(), mtq, metav1.CreateOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	waitForMTQToBeReady(f, updatedMtq, mtqPods)
}

func waitForMTQToBeReady(f *framework.Framework, cr *mtqv1.MTQ, mtqPods *corev1.PodList) {
	var newMtqPods *corev1.PodList

	By("Waiting for MTQ CR to be Available")
	EventuallyWithOffset(2, func() error {
		mtq, err := f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), cr.Name, metav1.GetOptions{})
		ExpectWithOffset(2, err).ToNot(HaveOccurred())
		ExpectWithOffset(2, mtq.Status.Phase).ShouldNot(Equal(sdkapi.PhaseError))
		if !conditions.IsStatusConditionTrue(mtq.Status.Conditions, conditions.ConditionAvailable) {
			return fmt.Errorf("MTQ CR should be Available")
		}
		return nil
	}, 10*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())

	By("Verifying MTQ lock-server and controller exist, before continuing")
	EventuallyWithOffset(2, func() error {
		if !infraDeploymentAvailable(f, cr) {
			return fmt.Errorf("MTQ deploymets should be Available")
		}
		return nil
	}, CompletionTimeout, assertionPollInterval).ShouldNot(HaveOccurred(), "Timeout reading MTQ deployments")

	By("Waiting for there to be as many MTQ pods as before")
	EventuallyWithOffset(2, func() error {
		newMtqPods = getMTQPods(f)
		if len(mtqPods.Items) != len(newMtqPods.Items) {
			return fmt.Errorf("number of mtq pods: %d\n should be equal to new number of mtq pods: %d\n", len(mtqPods.Items), len(newMtqPods.Items))
		}
		return nil
	}, 5*time.Minute, 2*time.Second).ShouldNot(HaveOccurred())

	for _, newMtqPod := range newMtqPods.Items {
		By(fmt.Sprintf("Waiting for MTQ pod %s to be ready", newMtqPod.Name))
		err := utils.WaitTimeoutForPodReady(f.K8sClient, newMtqPod.Name, newMtqPod.Namespace, 20*time.Minute)
		ExpectWithOffset(2, err).ToNot(HaveOccurred())
	}
}

func tolerationExists(tolerations []corev1.Toleration, testValue corev1.Toleration) bool {
	for _, toleration := range tolerations {
		if reflect.DeepEqual(toleration, testValue) {
			return true
		}
	}
	return false
}

func nodeHasTaint(node corev1.Node, testedTaint corev1.Taint) bool {
	for _, taint := range node.Spec.Taints {
		if reflect.DeepEqual(taint, testedTaint) {
			return true
		}
	}
	return false
}

func infraDeploymentAvailable(f *framework.Framework, cr *mtqv1.MTQ) bool {
	mtq, _ := f.MtqClient.MtqV1alpha1().MTQs().Get(context.TODO(), cr.Name, metav1.GetOptions{})
	if !conditions.IsStatusConditionTrue(mtq.Status.Conditions, conditions.ConditionAvailable) {
		return false
	}

	for _, deploymentName := range []string{"mtq-lock", "mtq-controller"} {
		_, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), deploymentName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false
		}
	}

	return true
}

func mtqOperatorDeploymentGone(f *framework.Framework) bool {
	_, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), "mtq-operator", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return true
	}
	ExpectWithOffset(2, err).ToNot(HaveOccurred())
	return false
}

func scaleDeployment(f *framework.Framework, deploymentName string, replicas int32) int32 {
	operatorDeployment, err := f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Get(context.TODO(), deploymentName, metav1.GetOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	originalReplicas := *operatorDeployment.Spec.Replicas
	patch := fmt.Sprintf(`[{"op": "replace", "path": "/spec/replicas", "value": %d}]`, replicas)
	_, err = f.K8sClient.AppsV1().Deployments(f.MTQInstallNs).Patch(context.TODO(), deploymentName, types.JSONPatchType, []byte(patch), metav1.PatchOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return originalReplicas
}
