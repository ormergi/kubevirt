package tests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cniv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	k8scorev1 "k8s.io/api/core/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	kvv1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/flags"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	kvmatcher "kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libvmi"
	"kubevirt.io/kubevirt/tests/libwait"
	utils "kubevirt.io/kubevirt/tests/util"
)

// This test suite expects the deployed Kubevirt version to be as the given test suite '--previous-release-tag' flag.
// It spin-up a VM with secondary NIC, upgrade KubeVirt to the target tested code, and verify the VM can be migrated
// successfully following Kubevirt upgrade or by a user request.
var _ = Describe("previous version Kubevirt with 'Migrate' workload update strategy",
	// enable using BeforeAll/AfterAll
	Ordered,
	// prevent from running these tests in parallel as setup/cleanup should occur once
	Serial,
	func() {
		BeforeAll(func() {
			kv := utils.GetCurrentKv(kubevirt.Client())
			By("Verify kubevirt is from previous version")
			Expect(kv.Status.ObservedKubeVirtVersion).To(Equal(flags.PreviousReleaseTag),
				"Kubevirt version is not be equal to the version given by the tests suite parameter '--previous-release-tag'")
			Expect(kv.Status.ObservedKubeVirtRegistry).To(Equal(flags.PreviousReleaseRegistry),
				"Kubevirt images should be used from the registry given by the tests suite parameter '--previous-release-registry'")

			By("Ensure kubevirt workload update strategy is 'Migrate'")
			ensureKubeVirtMigrateWorkloadUpdateStrategy(kv)
		})

		BeforeEach(func() {
			kv := utils.GetCurrentKv(kubevirt.Client())

			By("Verify Kubevirt is deployed and ready")
			assertKubeVirtIsReady(kv)

			By("Verify Kubevirt workload update strategy is 'Migrate'")
			Expect(kubevirtWorkloadUpdateStrategyIsMigrate(kv)).To(BeTrue())
		})

		Context("running virtual machine with multiple interfaces", func() {
			// VM and net-attach-def is created once and reused in this context tests
			const testNetAttachDefName = "bridge-network"
			var testNetAttachDef *cniv1.NetworkAttachmentDefinition
			var testVMI *kvv1.VirtualMachineInstance
			BeforeAll(func() {

				By("Creating bridge NetworkAttachmentDefinition")
				testNetAttachDef = newBridgeNetworkAttachmentDefinition(testNetAttachDefName)
				Expect(createBridgeNetAttachDef(utils.NamespaceTestDefault, testNetAttachDef)).To(Succeed())
				DeferCleanup(func() {
					By("Deleting NetworkAttachmentDefinition")
					Expect(deleteNetAttachDef(utils.NamespaceTestDefault, testNetAttachDef.Name)).To(Succeed())
				})

				By("Starting VM with secondary NICs before updating Kubevirt")
				var err error
				testVMI = newCirrosVMIWithMultusNetwork(testNetAttachDefName)
				testVMI, err = kubevirt.Client().VirtualMachineInstance(utils.NamespaceTestDefault).Create(context.Background(), testVMI)
				Expect(err).ToNot(HaveOccurred())
				libwait.WaitUntilVMIReady(testVMI, console.LoginToCirros)
				DeferCleanup(func() {
					By("Deleting VM")
					Expect(kubevirt.Client().VirtualMachineInstance(testVMI.Namespace).Delete(context.Background(), testVMI.Name, &k8smetav1.DeleteOptions{})).To(Succeed())
				})
			})

			When("kubevirt updates", func() {
				targetKubevirtVersion := flags.KubeVirtVersionTag
				targetKubevirtRegistry := flags.KubeVirtRepoPrefix

				BeforeAll(func() {
					kv := utils.GetCurrentKv(kubevirt.Client())
					By(fmt.Sprintf("Updating KubeVirt from [%q] to [%q] version", kv.Status.ObservedKubeVirtVersion, targetKubevirtVersion))
					setKubeVirtVersionAndRegistry(kv.Name, targetKubevirtVersion, targetKubevirtRegistry)
				})

				BeforeEach(func() {
					By("Verify Kubevirt has been updated and runs the correct version given by the test suite parameters '--container-prefix' & '--container-tag'")
					kv := utils.GetCurrentKv(kubevirt.Client())
					Expect(kv.Status.ObservedKubeVirtVersion).To(Equal(targetKubevirtVersion),
						"Kubevirt version is not be equal to the version given by the tests suite parameter '--container-tag'")
					Expect(kv.Status.ObservedKubeVirtRegistry).To(Equal(targetKubevirtRegistry),
						"Kubevirt images should be used from the registry given by the tests suite parameter '--container-prefix'")
				})

				It("VM should migrate successfully (following 'Migrate' workloads-update-strategy)", func() {
					const migrationTrials = 3
					verifyVMIUpdated(testVMI.Namespace, testVMI.Name, migrationTrials)

					By("VM should be migrated successfully again by user request")
					virtClient := kubevirt.Client()
					migration, err := virtClient.VirtualMachineInstanceMigration(testVMI.Namespace).Create(tests.NewRandomMigration(testVMI.Name, testVMI.Namespace), &k8smetav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					Eventually(kvmatcher.ThisMigration(migration), 180*time.Second, 1*time.Second).Should(kvmatcher.HaveSucceeded())
				})
			})
		})
	})

func assertKubeVirtIsReady(kv *kvv1.KubeVirt) {
	Expect(kv).To(SatisfyAll(
		Not(BeNil()),
		kvmatcher.HaveConditionTrue(kvv1.KubeVirtConditionAvailable),
		kvmatcher.HaveConditionTrue(kvv1.KubeVirtConditionCreated),
		kvmatcher.HaveConditionFalse(kvv1.KubeVirtConditionProgressing),
		kvmatcher.HaveConditionFalse(kvv1.KubeVirtConditionDegraded),
	))

	pods, err := kubevirt.Client().CoreV1().Pods(flags.KubeVirtInstallNamespace).List(context.Background(), k8smetav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for _, pod := range pods.Items {
		if isManagedByOperator(pod.Labels) {
			Expect(pod).To(kvmatcher.HavePhase(k8scorev1.PodRunning))
			for _, containerStatus := range pod.Status.ContainerStatuses {
				Expect(containerStatus.Ready).To(BeTrue())
			}
		}
	}
}

func kubevirtWorkloadUpdateStrategyIsMigrate(currentKv *kvv1.KubeVirt) bool {
	return len(currentKv.Spec.WorkloadUpdateStrategy.WorkloadUpdateMethods) == 1 &&
		currentKv.Spec.WorkloadUpdateStrategy.WorkloadUpdateMethods[0] == kvv1.WorkloadUpdateMethodLiveMigrate
}
func ensureKubeVirtMigrateWorkloadUpdateStrategy(currentKv *kvv1.KubeVirt) {
	if kubevirtWorkloadUpdateStrategyIsMigrate(currentKv) {
		return
	}

	By("Patch Kubevirt workload update strategy to 'Migrate'")
	v := kvv1.KubeVirtWorkloadUpdateStrategy{
		WorkloadUpdateMethods: []kvv1.WorkloadUpdateMethod{kvv1.WorkloadUpdateMethodLiveMigrate},
	}
	raw, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())

	jsonPatch := []byte(fmt.Sprintf(`[{ "op": "add", "path": "/spec/workloadUpdateStrategy", "value": %s}]`, string(raw)))
	patchAndWaitForKubeVirtReady(currentKv.Name, jsonPatch)
}

func newBridgeNetworkAttachmentDefinition(networkName string) *cniv1.NetworkAttachmentDefinition {
	config := fmt.Sprintf(`{"cniVersion": "0.3.1", "name": %q, "type": "cnv-bridge", "bridge": %q}`, networkName, networkName)
	return &cniv1.NetworkAttachmentDefinition{
		ObjectMeta: k8smetav1.ObjectMeta{
			Name: networkName,
		},
		Spec: cniv1.NetworkAttachmentDefinitionSpec{Config: config},
	}
}
func createBridgeNetAttachDef(namespace string, netAttachNef *cniv1.NetworkAttachmentDefinition) error {
	virtClient := kubevirt.Client()
	_, err := virtClient.NetworkClient().K8sCniCncfIoV1().NetworkAttachmentDefinitions(namespace).Create(
		context.Background(),
		netAttachNef,
		k8smetav1.CreateOptions{},
	)
	return err
}
func deleteNetAttachDef(namespace, name string) error {
	virtClient := kubevirt.Client()
	err := virtClient.NetworkClient().K8sCniCncfIoV1().NetworkAttachmentDefinitions(namespace).Delete(
		context.Background(),
		name,
		k8smetav1.DeleteOptions{},
	)
	return err
}

func newCirrosVMIWithMultusNetwork(netAttachDefName string) *kvv1.VirtualMachineInstance {
	defaultNetwork := &kvv1.Network{
		Name:          libvmi.DefaultInterfaceName,
		NetworkSource: kvv1.NetworkSource{Pod: &kvv1.PodNetwork{}},
	}
	bridgeNetwork1 := &kvv1.Network{
		Name: "brnet1",
		NetworkSource: kvv1.NetworkSource{Multus: &kvv1.MultusNetwork{
			NetworkName: netAttachDefName,
		}},
	}
	bridgeNetwork2 := &kvv1.Network{
		Name: "brnet2",
		NetworkSource: kvv1.NetworkSource{Multus: &kvv1.MultusNetwork{
			NetworkName: netAttachDefName,
		}},
	}
	return libvmi.NewCirros(
		libvmi.WithCloudInitNoCloudUserData("#!/bin/bash\necho 'hello'\n", false),
		libvmi.WithNetwork(defaultNetwork),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
		libvmi.WithNetwork(bridgeNetwork1),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(bridgeNetwork1.Name)),
		libvmi.WithNetwork(bridgeNetwork2),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(bridgeNetwork2.Name)),
	)
}

func setKubeVirtVersionAndRegistry(name, targetVersion, targetVersionRegistry string) {
	currentKv := utils.GetCurrentKv(kubevirt.Client())
	if currentKv.Status.ObservedKubeVirtVersion == targetVersion {
		By("Kubevirt version is already updated!")
		return
	}

	By(fmt.Sprintf("Patching KubeVirt version: [%q] and registry: [%q]", targetVersion, targetVersionRegistry))
	jsonPatch := []byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/imageTag", "value": "%s"},{ "op": "replace", "path": "/spec/imageRegistry", "value": "%s"}]`, targetVersion, targetVersionRegistry))
	patchAndWaitForKubeVirtReady(name, jsonPatch)
}

func verifyVMIUpdated(vmiNamespace, vmiName string, trials int) {
	// expects a VMI that been migrated following kubevirt updated and asserts:
	// - VMI isn't labeled with "kubevirt.io/outdatedLauncherImage".
	// - VMI status reflect successful migration (status.migrationsState.completed == True).
	// - The corresponding 'VirtualMachineInstanceMigration' object reflects successful migration (status.phase == Succeeded).
	virtClient := kubevirt.Client()
	Eventually(func() error {
		vmi, err := virtClient.VirtualMachineInstance(vmiNamespace).Get(context.Background(), vmiName, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}
		if vmi.Status.MigrationState == nil {
			return fmt.Errorf("VMI '%s/%s' migration did not start yet", vmi.Namespace, vmi.Name)
		}
		if len(vmi.Status.ActivePods) > trials {
			return fmt.Errorf("VMI '%s/%s' migration failed  after 3 trials", vmi.Namespace, vmi.Name)
		}

		if !vmi.Status.MigrationState.Completed {
			var startTime time.Time
			var endTime time.Time
			now := time.Now()
			if vmi.Status.MigrationState.StartTimestamp != nil {
				startTime = vmi.Status.MigrationState.StartTimestamp.Time
			}
			if vmi.Status.MigrationState.EndTimestamp != nil {
				endTime = vmi.Status.MigrationState.EndTimestamp.Time
			}
			return fmt.Errorf("VMI '%s/%s' migration UID=%q did not finished:\n\tSource Node [%s]\n\tTarget Node [%s]\n\tStart Time [%s]\n\tEnd Time [%s]\n\tNow [%s]\n\tFailed: %t",
				vmi.Namespace, vmi.Name, string(vmi.Status.MigrationState.MigrationUID),
				vmi.Status.MigrationState.SourceNode, vmi.Status.MigrationState.TargetNode,
				startTime.String(), endTime.String(),
				now.String(),
				vmi.Status.MigrationState.Failed,
			)
		}

		if _, exist := vmi.Labels[kvv1.OutdatedLauncherImageLabel]; exist {
			return fmt.Errorf("VMI '%s/%s' launcher pod did is outdated", vmi.Namespace, vmi.Name)
		}

		return nil
	}, time.Second*500, 1*time.Second).Should(Succeed(), "VMI should migrate successfully")

	// this is put in an eventually loop because it's possible for the VMI to complete
	// migrating and for the migration object to briefly lag behind in reporting
	// the results
	Eventually(func() error {
		By("Verifying only a single successful migration took place")
		migrationList, err := virtClient.VirtualMachineInstanceMigration(utils.NamespaceTestDefault).List(&k8smetav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred(), "retrieving migrations")
		count := 0
		for _, migration := range migrationList.Items {
			if migration.Spec.VMIName == vmiName && migration.Status.Phase == kvv1.MigrationSucceeded {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("vmi [%s] returned %d successful migrations", vmiName, count)
		}
		return nil
	}, 10, 1).Should(BeNil(), "Expects only a single successful migration per workload update")
}

func patchAndWaitForKubeVirtReady(name string, patchBytes []byte) {
	const (
		patchTimeout      = 10 * time.Second
		patchPollInterval = 1 * time.Second
	)
	virtClient := kubevirt.Client()
	Eventually(func() error {
		_, err := virtClient.KubeVirt(flags.KubeVirtInstallNamespace).Patch(name, k8stypes.JSONPatchType, patchBytes, &k8smetav1.PatchOptions{})
		return err
	}, patchTimeout, patchPollInterval).Should(Succeed())

	By("Wait for KubeVirt update conditions")
	waitForKubeVirtUpdateConditions(name)

	By("Waiting for KubeVirt to stabilize")
	waitForKubeVirtReady(name)

	By("Verifying infrastructure Is Updated")
	waitForKubevirtSystemPodsReady(name)
}
func waitForKubeVirtUpdateConditions(name string) {
	const (
		kubevirtIsUpdatingTimeout = 120 * time.Second
		pollingInterval           = 1 * time.Second
	)
	virtClient := kubevirt.Client()
	Eventually(func() *kvv1.KubeVirt {
		kv, _ := virtClient.KubeVirt(flags.KubeVirtInstallNamespace).Get(name, &k8smetav1.GetOptions{})
		return kv
	}, kubevirtIsUpdatingTimeout, pollingInterval).Should(
		SatisfyAll(
			Not(BeNil()),
			kvmatcher.HaveConditionTrue(kvv1.KubeVirtConditionAvailable),
			kvmatcher.HaveConditionTrue(kvv1.KubeVirtConditionProgressing),
			kvmatcher.HaveConditionTrue(kvv1.KubeVirtConditionDegraded),
		))
}
func waitForKubeVirtReady(name string) {
	const (
		kubevirtReadyTimeout = 420 * time.Second
		pollingInterval      = 1 * time.Second
	)
	virtClient := kubevirt.Client()
	Eventually(func() error {
		kv, err := virtClient.KubeVirt(flags.KubeVirtInstallNamespace).Get(name, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}

		if kv.Status.Phase != kvv1.KubeVirtPhaseDeployed {
			return fmt.Errorf("Waiting for phase to be deployed (current phase: %+v)", kv.Status.Phase)
		}

		available := false
		progressing := true
		degraded := true
		created := false
		for _, condition := range kv.Status.Conditions {
			if condition.Type == kvv1.KubeVirtConditionAvailable && condition.Status == k8scorev1.ConditionTrue {
				available = true
			} else if condition.Type == kvv1.KubeVirtConditionProgressing && condition.Status == k8scorev1.ConditionFalse {
				progressing = false
			} else if condition.Type == kvv1.KubeVirtConditionDegraded && condition.Status == k8scorev1.ConditionFalse {
				degraded = false
			} else if condition.Type == kvv1.KubeVirtConditionCreated && condition.Status == k8scorev1.ConditionTrue {
				created = true
			}
		}

		if !available || progressing || degraded || !created {
			if kv.Status.ObservedGeneration != nil {
				if *kv.Status.ObservedGeneration == kv.ObjectMeta.Generation {
					return fmt.Errorf("observed generation must not match the current configuration")
				}
			}
			return fmt.Errorf("Waiting for conditions to indicate deployment (conditions: %+v)", kv.Status.Conditions)
		}

		if kv.Status.ObservedGeneration != nil {
			if *kv.Status.ObservedGeneration != kv.ObjectMeta.Generation {
				return fmt.Errorf("the observed generation must match the current generation")
			}
		}

		return nil
	}, kubevirtReadyTimeout, pollingInterval).Should(Succeed())
}
func waitForKubevirtSystemPodsReady(kvName string) {
	const (
		kubevirtPodsReadyTimeout = 300 * time.Second
		pollingInterval          = 1 * time.Second
	)
	virtClient := kubevirt.Client()
	Eventually(func() error {
		curKv, err := virtClient.KubeVirt(flags.KubeVirtInstallNamespace).Get(kvName, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}
		if curKv.Status.TargetDeploymentID != curKv.Status.ObservedDeploymentID {
			return fmt.Errorf("Target and obeserved id don't match")
		}

		podsReadyAndOwned := 0
		pods, err := virtClient.CoreV1().Pods(curKv.Namespace).List(context.Background(), k8smetav1.ListOptions{LabelSelector: "kubevirt.io"})
		if err != nil {
			return err
		}
		for _, pod := range pods.Items {
			if !isManagedByOperator(pod.Labels) {
				continue
			}

			if pod.Status.Phase != k8scorev1.PodRunning {
				return fmt.Errorf("Waiting for pod %s with phase %s to reach Running phase", pod.Name, pod.Status.Phase)
			}

			for _, containerStatus := range pod.Status.ContainerStatuses {
				if !containerStatus.Ready {
					return fmt.Errorf("Waiting for pod %s to have all containers in Ready state", pod.Name)
				}
			}

			id, ok := pod.Annotations[kvv1.InstallStrategyIdentifierAnnotation]
			if !ok {
				return fmt.Errorf("Pod %s is owned by operator but has no id annotation", pod.Name)
			}

			expectedID := curKv.Status.ObservedDeploymentID
			if id != expectedID {
				return fmt.Errorf("Pod %s is of version %s when we expected id %s", pod.Name, id, expectedID)
			}
			podsReadyAndOwned++
		}

		// this just sanity checks that at least one pod was found and verified.
		// 0 would indicate our labeling was incorrect.
		Expect(podsReadyAndOwned).To(BeNumerically(">", 0))

		return nil
	}, kubevirtPodsReadyTimeout, pollingInterval).Should(Succeed())
}
func isManagedByOperator(labels map[string]string) bool {
	if v, ok := labels[kvv1.ManagedByLabel]; ok && (v == kvv1.ManagedByLabelOperatorValue || v == kvv1.ManagedByLabelOperatorOldValue) {
		return true
	}
	return false
}
