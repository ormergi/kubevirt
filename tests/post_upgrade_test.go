package tests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cniv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	k8scorev1 "k8s.io/api/core/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	kvv1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/network/vmispec"
	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/flags"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	kvmatcher "kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libvmi"
	"kubevirt.io/kubevirt/tests/libwait"
	utils "kubevirt.io/kubevirt/tests/util"
)

// This test suite expects the deployed Kubevirt version to be from previous version, given by test suite flag 'previous-release-tag'.
// It spin-up a VM with secondary NICs, upgrade KubeVirt to the target version, given by the test suite flag 'container tag', and verifies:
// - VM can migrate, w and w/o 'Migrate' WorkloadStrategy.
// - VM can migrate upon user request.
var _ = Describe("Kubevirt from previous version", func() {
	BeforeEach(func() {
		assertKubevirtIsPreviousVersion()
	})

	Context("with 'Migrate' workload update strategy",
		Label("PostUpgradeMigrateStrategy"),
		// enable using BeforeAll/AfterAll
		Ordered,
		// prevent from running tests in parallel because setup/teardown should occur once
		Serial,
		func() {
			var migrateKubeVirtWorkloadUpdateStrategy = kvv1.KubeVirtWorkloadUpdateStrategy{
				WorkloadUpdateMethods: []kvv1.WorkloadUpdateMethod{kvv1.WorkloadUpdateMethodLiveMigrate},
			}

			testVMSurviveKubevirtUpgrade(migrateKubeVirtWorkloadUpdateStrategy)
		},
	)

	Context("no workload update strategy",
		Label("PostUpgrade"),
		// enable using BeforeAll/AfterAll
		Ordered,
		// prevent from running tests in parallel because setup/teardown should occur once
		Serial,
		func() {
			var emptyKubeVirtWorkloadUpdateStrategy = kvv1.KubeVirtWorkloadUpdateStrategy{}

			testVMSurviveKubevirtUpgrade(emptyKubeVirtWorkloadUpdateStrategy)
		},
	)
})

var testVMSurviveKubevirtUpgrade = func(testKVWorkloadUpdateStrategy kvv1.KubeVirtWorkloadUpdateStrategy) {
	Context(fmt.Sprintf("with workload strategy [%v]", testKVWorkloadUpdateStrategy),
		// enable using BeforeAll/AfterAll
		Ordered,
		// prevent from running tests in parallel because setup/teardown should occur once
		Serial,
		func() {
			BeforeAll(func() {
				setKubeVirtMigrateWorkloadUpdateStrategy(testKVWorkloadUpdateStrategy)
			})

			BeforeEach(func() {
				By(fmt.Sprintf("Verify Kubevirt workload update strategy is [%v]", testKVWorkloadUpdateStrategy))
				kv := utils.GetCurrentKv(kubevirt.Client())
				Expect(kv.Spec.WorkloadUpdateStrategy).To(Equal(testKVWorkloadUpdateStrategy))
			})

			Context("running virtual machine with multiple interfaces", func() {
				const (
					testBridgeNetAttachDefName  = "bridge-network"
					testMacvtapNetAttachDefName = "macvtap-network"
					nodeMacvtapIfaceName        = "eth0"
					legacyVMIName               = "testvmi-legacy"
					newVMIName                  = "testvmi-new"
				)

				var (
					testBridgeNetAttachDef  *cniv1.NetworkAttachmentDefinition
					testMacvtapNetAttachDef *cniv1.NetworkAttachmentDefinition
					testLegacyVMI           *kvv1.VirtualMachineInstance
				)

				BeforeAll(func() {
					testBridgeNetAttachDef = newBridgeNetworkAttachmentDefinition(testBridgeNetAttachDefName)
					Expect(createNetAttachDef(utils.NamespaceTestDefault, testBridgeNetAttachDef)).To(Succeed())
					DeferCleanup(func() {
						_ = deleteNetAttachDef(utils.NamespaceTestDefault, testBridgeNetAttachDef.Name)
					})
					testMacvtapNetAttachDef = newMacvtapNetworkAttachmentDefinition(testMacvtapNetAttachDefName, nodeMacvtapIfaceName)
					Expect(createNetAttachDef(utils.NamespaceTestDefault, testMacvtapNetAttachDef)).To(Succeed())
					DeferCleanup(func() {
						_ = deleteNetAttachDef(utils.NamespaceTestDefault, testMacvtapNetAttachDef.Name)
					})
					testLegacyVMI = startVMI(legacyVMIName, testBridgeNetAttachDef.Name, testMacvtapNetAttachDef.Name)
					DeferCleanup(func() {
						deleteVMI(testLegacyVMI)
					})
				})

				When("upgrade KubeVirt to target version", func() {
					targetKubevirtVersion := flags.KubeVirtVersionTag
					targetKubevirtRegistry := flags.KubeVirtRepoPrefix

					BeforeAll(func() {
						kv := utils.GetCurrentKv(kubevirt.Client())
						By(fmt.Sprintf("Updating KubeVirt from [%q] to [%q] version", kv.Status.ObservedKubeVirtVersion, targetKubevirtVersion))
						setKubeVirtVersionAndRegistry(kv.Name, targetKubevirtVersion, targetKubevirtRegistry)
					})

					BeforeEach(func() {
						By("Verifying Kubevirt is updated to the version given by 'container-prefix' 'container-tag' test suite parameters")
						kv := utils.GetCurrentKv(kubevirt.Client())
						Expect(kv.Status.ObservedKubeVirtVersion).To(Equal(targetKubevirtVersion),
							"Kubevirt version is not be equal to the version given by the tests suite parameter '--container-tag'")
						Expect(kv.Status.ObservedKubeVirtRegistry).To(Equal(targetKubevirtRegistry),
							"Kubevirt images should be used from the registry given by the tests suite parameter '--container-prefix'")

						assertKubeVirtIsReady()
					})

					It("VM should migrate successfully", func() {
						const migrationTrials = 3

						if kubevirtWorkloadStrategyMethodsContainsMigrate(testKVWorkloadUpdateStrategy.WorkloadUpdateMethods) {
							By("Verify LEGACY VMI migrated following Kubevirt upgrade with Migrate workload strategy")
							assertVMIUpdated(testLegacyVMI.Namespace, testLegacyVMI.Name, migrationTrials)
						} else {
							By("migrate LEGACY VM after Kubevirt upgrade")
							migrateVMI(testLegacyVMI)
						}

						By("migrating LEGACY VM again (after Kubevirt upgrade)")
						migrateVMI(testLegacyVMI)

						addIfaceOpt := kvv1.AddInterfaceOptions{NetworkAttachmentDefinitionName: testBridgeNetAttachDef.Name, Name: "blue"}
						By(fmt.Sprintf("hotplug interface %q to test LEGACY VM '%s/%s'", addIfaceOpt.Name, testLegacyVMI.Namespace, testLegacyVMI.Name))
						Expect(kubevirt.Client().VirtualMachineInstance(testLegacyVMI.Namespace).AddInterface(context.Background(), testLegacyVMI.Name, &addIfaceOpt)).To(Succeed())

						By("migrating LEGACY VM for hotplug interface to take place")
						migrateVMI(testLegacyVMI)

						By("assert LEGACY VM status reports the new interface")
						assertVMIHasPluggedIface(testLegacyVMI, addIfaceOpt.Name)

						By("assert LEGACY VM plugged pod iface name is in ordinal form")
						expectedLegacyVMPodIfaceName := fmt.Sprintf("net%d", len(vmispec.FilterMultusNonDefaultNetworks(testLegacyVMI.Spec.Networks)))
						Expect(vmiPodInterfaceExistByName(testLegacyVMI, expectedLegacyVMPodIfaceName)).To(BeTrue(),
							"hotpluged iface should exist in pod")

						removeIfaceOpt := kvv1.RemoveInterfaceOptions{Name: "blue"}
						By(fmt.Sprintf("unplug interface %q from test LEGACY VM '%s/%s'", removeIfaceOpt.Name, testLegacyVMI.Namespace, testLegacyVMI.Name))
						Expect(kubevirt.Client().VirtualMachineInstance(testLegacyVMI.Namespace).RemoveInterface(context.Background(), testLegacyVMI.Name, &removeIfaceOpt)).To(Succeed())

						By("migrating LEGACY VM for UNPLUG interface to take place")
						migrateVMI(testLegacyVMI)

						By("assert LEGACY VM unplug fails and status include the unplugged the interface")
						assertVMIHasPluggedIface(testLegacyVMI, removeIfaceOpt.Name)

						By("assert LEGACY VM unplug fails and interface still exist in pod")
						Expect(vmiPodInterfaceExistByName(testLegacyVMI, expectedLegacyVMPodIfaceName)).To(BeTrue(),
							"unpluged iface should fail for legacy VM and the corresponding interface should exist in pod")

						// new vmi hotplug
						By("creating NEW VMI")
						testNewVMI := startVMI(newVMIName, testBridgeNetAttachDefName, testMacvtapNetAttachDefName)
						DeferCleanup(func() {
							deleteVMI(testNewVMI)
						})

						By("migrating NEW VM")
						migrateVMI(testNewVMI)

						By(fmt.Sprintf("hotplug interface %q to test NEW VM '%s/%s'", addIfaceOpt.Name, testNewVMI.Namespace, testNewVMI.Name))
						Expect(kubevirt.Client().VirtualMachineInstance(testNewVMI.Namespace).AddInterface(context.Background(), testNewVMI.Name, &addIfaceOpt)).To(Succeed())

						By("migrating NEW VM for hotplug interface to take place")
						migrateVMI(testNewVMI)

						By("assert NEW VM status reports the new interface")
						assertVMIHasPluggedIface(testNewVMI, addIfaceOpt.Name)

						By("assert the NEW VM plugged pod iface name is in hashed form")
						expectedNewVMIPodIfaceName := "pod16477688c0e"
						Expect(vmiPodInterfaceExistByName(testNewVMI, expectedNewVMIPodIfaceName)).To(BeTrue(),
							"hotpluged iface should exist in pod")

						By(fmt.Sprintf("unplug interface %q from test NEW VM '%s/%s'", removeIfaceOpt.Name, testNewVMI.Namespace, testNewVMI.Name))
						Expect(kubevirt.Client().VirtualMachineInstance(testNewVMI.Namespace).RemoveInterface(context.Background(), testNewVMI.Name, &removeIfaceOpt)).To(Succeed())

						By("migrating NEW VM for unplug interface to take place")
						migrateVMI(testNewVMI)

						By("assert NEW VM status updated and not include the unpluged the interface")
						assertVMIInterfaceNotExist(testNewVMI, removeIfaceOpt.Name)

						By("assert NEW VM unpluged interface not exist in pod")
						Expect(vmiPodInterfaceExistByName(testNewVMI, expectedNewVMIPodIfaceName)).To(BeFalse(),
							"unpluged iface should not exist in pod")
					})
				})
			})
		},
	)
}

func assertVMIHasPluggedIface(vmi *kvv1.VirtualMachineInstance, ifaceName string) *kvv1.VirtualMachineInstance {
	virtClient := kubevirt.Client()
	By(fmt.Sprintf("waiting for vmi [%s/%s] interface [%q] to attach and reflect on status", vmi.Namespace, vmi.Name, ifaceName))
	var updatedVmi *kvv1.VirtualMachineInstance
	Eventually(func() error {
		var err error
		updatedVmi, err = virtClient.VirtualMachineInstance(vmi.Namespace).Get(context.Background(), vmi.Name, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}
		for _, ifaceStatus := range updatedVmi.Status.Interfaces {
			if ifaceStatus.Name == ifaceName &&
				vmispec.ContainsInfoSource(ifaceStatus.InfoSource, vmispec.InfoSourceMultusStatus) &&
				vmispec.ContainsInfoSource(ifaceStatus.InfoSource, vmispec.InfoSourceDomain) {
				return nil
			}
		}
		return fmt.Errorf("ifaceStatus is not ready")
	}, 240*time.Second, 1*time.Second).Should(Succeed())
	return updatedVmi
}

func assertVMIInterfaceNotExist(vmi *kvv1.VirtualMachineInstance, ifaceName string) *kvv1.VirtualMachineInstance {
	virtClient := kubevirt.Client()
	By(fmt.Sprintf("waiting for vmi [%s/%s] interface [%q] to detach and reflect on status", vmi.Namespace, vmi.Name, ifaceName))
	var updatedVmi *kvv1.VirtualMachineInstance
	Eventually(func() error {
		var err error
		updatedVmi, err = virtClient.VirtualMachineInstance(vmi.Namespace).Get(context.Background(), vmi.Name, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}
		for _, ifaceStatus := range updatedVmi.Status.Interfaces {
			if ifaceStatus.Name == ifaceName {
				return fmt.Errorf("iface have not detached yet")
			}
		}
		return nil
	}, 240*time.Second, 1*time.Second).Should(Succeed())
	return updatedVmi
}

func vmiPodInterfaceExistByName(vmi *kvv1.VirtualMachineInstance, expectedIfaceName string) bool {
	pod := getVMIRunningPod(vmi)
	Expect(pod).ToNot(BeNil(), "should get '%s/%s' VMI running pod", vmi.Namespace, vmi.Name)

	podAnnotations := pod.GetAnnotations()
	Expect(podAnnotations).To(HaveKey(cniv1.NetworkStatusAnnot))

	networkStatusAnnotationJSON := pod.ObjectMeta.Annotations[cniv1.NetworkStatusAnnot]
	var podNetworkStatus []cniv1.NetworkStatus
	Expect(json.Unmarshal([]byte(networkStatusAnnotationJSON), &podNetworkStatus)).To(Succeed())

	foundExpectedIface := false
	for _, networkStatus := range podNetworkStatus {
		if networkStatus.Interface == expectedIfaceName {
			foundExpectedIface = true
			break
		}
	}

	return foundExpectedIface
}

func getVMIRunningPod(vmi *kvv1.VirtualMachineInstance) *k8scorev1.Pod {
	newLegacyVMIPods, err := getPodsByLabel(string(vmi.GetUID()), kvv1.CreatedByLabel, vmi.Namespace)
	Expect(err).ToNot(HaveOccurred())
	for i := 0; i < len(newLegacyVMIPods.Items); i++ {
		if newLegacyVMIPods.Items[i].Status.Phase == k8scorev1.PodRunning {
			return &newLegacyVMIPods.Items[i]
		}
	}
	return nil
}

func getPodsByLabel(label, labelType, namespace string) (*k8scorev1.PodList, error) {
	virtCli := kubevirt.Client()
	labelSelector := fmt.Sprintf("%s=%s", labelType, label)
	pods, err := virtCli.CoreV1().Pods(namespace).List(context.Background(),
		k8smetav1.ListOptions{LabelSelector: labelSelector},
	)
	if err != nil {
		return nil, err
	}

	return pods, nil
}

func kubevirtWorkloadStrategyMethodsContainsMigrate(methods []kvv1.WorkloadUpdateMethod) bool {
	for _, method := range methods {
		if method == kvv1.WorkloadUpdateMethodLiveMigrate {
			return true
		}
	}
	return false
}

func startVMI(vmiName, testNetAttachDefName, testMacvtapNetAttachDefName string) *kvv1.VirtualMachineInstance {
	opts := testVMIOptions(testNetAttachDefName, testMacvtapNetAttachDefName)
	vmi := libvmi.NewFedora(opts...)
	vmi.Name = vmiName
	By(fmt.Sprintf("Starting [%s/%s] VM with two secondary interfaces (before upgrading Kubevirt)", utils.NamespaceTestDefault, vmi.Name))
	vmi, err := kubevirt.Client().VirtualMachineInstance(utils.NamespaceTestDefault).Create(context.Background(), vmi)
	Expect(err).ToNot(HaveOccurred())
	libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
	return vmi
}

func deleteVMI(vmi *kvv1.VirtualMachineInstance) {
	var getErr error
	By(fmt.Sprintf("Deleting VMI '%s/%s'", vmi.Namespace, vmi.Name))
	_ = kubevirt.Client().VirtualMachineInstance(vmi.Namespace).Delete(context.Background(), vmi.Name, &k8smetav1.DeleteOptions{})
	Eventually(func() error {
		_, getErr = kubevirt.Client().VirtualMachineInstance(vmi.Namespace).Get(context.Background(), vmi.Name, &k8smetav1.GetOptions{})
		if errors.IsNotFound(getErr) {
			return nil
		}
		return getErr
	}, 30*time.Second, 1*time.Second).Should(Succeed())
}

func migrateVMI(vmi *kvv1.VirtualMachineInstance) {
	vmim := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
	vmim, err := kubevirt.Client().VirtualMachineInstanceMigration(vmi.Namespace).Create(vmim, &k8smetav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())
	Eventually(kvmatcher.ThisMigration(vmim), 180*time.Second, 1*time.Second).Should(kvmatcher.HaveSucceeded())
}

func assertKubevirtIsPreviousVersion() {
	kv := utils.GetCurrentKv(kubevirt.Client())
	By("Verify kubevirt is from previous version")
	Expect(kv.Status.ObservedKubeVirtVersion).To(Equal(flags.PreviousReleaseTag),
		"Kubevirt version is not be equal to the version given by the tests suite parameter '--previous-release-tag'")
	Expect(kv.Status.ObservedKubeVirtRegistry).To(Equal(flags.PreviousReleaseRegistry),
		"Kubevirt images should be used from the registry given by the tests suite parameter '--previous-release-registry'")
}

func assertKubeVirtIsReady() {
	kv := utils.GetCurrentKv(kubevirt.Client())

	By("Verify Kubevirt is deployed and ready")
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

func setKubeVirtMigrateWorkloadUpdateStrategy(workloadStrategy kvv1.KubeVirtWorkloadUpdateStrategy) {
	currentKv := utils.GetCurrentKv(kubevirt.Client())

	if len(workloadStrategy.WorkloadUpdateMethods) == 0 {
		By("no workload updated is set")
		return
	}

	By(fmt.Sprintf("Patch Kubevirt workload update strategy to %v", workloadStrategy))
	workloadStrategyJSON, err := json.Marshal(workloadStrategy)
	Expect(err).ToNot(HaveOccurred())

	jsonPatch := []byte(fmt.Sprintf(`[{ "op": "add", "path": "/spec/workloadUpdateStrategy", "value": %s}]`, string(workloadStrategyJSON)))
	patchAndWaitForKubeVirtReady(currentKv.Name, jsonPatch)
}

func newBridgeNetworkAttachmentDefinition(networkName string) *cniv1.NetworkAttachmentDefinition {
	config := fmt.Sprintf(`{
		"cniVersion": "0.3.1", 
		"name": %q, 
		"type": "cnv-bridge", 
		"bridge": %q,
		"ipam": {
			"type": "host-local",
			"subnet": "10.10.0.0/24",
			"gateway": "10.10.0.254"
		}
	}`,
		networkName, networkName)

	return &cniv1.NetworkAttachmentDefinition{
		ObjectMeta: k8smetav1.ObjectMeta{
			Name: networkName,
		},
		Spec: cniv1.NetworkAttachmentDefinitionSpec{Config: config},
	}
}

func newMacvtapNetworkAttachmentDefinition(networkName, ifaceName string) *cniv1.NetworkAttachmentDefinition {
	config := fmt.Sprintf(`{"cniVersion": "0.3.1", "name": %q, "type": "macvtap"}`, networkName)
	return &cniv1.NetworkAttachmentDefinition{
		ObjectMeta: k8smetav1.ObjectMeta{
			Name: networkName,
			Annotations: map[string]string{
				"k8s.v1.cni.cncf.io/resourceName": fmt.Sprintf("macvtap.network.kubevirt.io/%s", ifaceName),
			},
		},
		Spec: cniv1.NetworkAttachmentDefinitionSpec{Config: config},
	}
}

func createNetAttachDef(namespace string, netAttachNef *cniv1.NetworkAttachmentDefinition) error {
	By(fmt.Sprintf("creating NetworkAttachmentDefinition '%s/%s'", namespace, netAttachNef.Name))
	virtClient := kubevirt.Client()
	_, err := virtClient.NetworkClient().K8sCniCncfIoV1().NetworkAttachmentDefinitions(namespace).Create(
		context.Background(),
		netAttachNef,
		k8smetav1.CreateOptions{},
	)
	return err
}

func deleteNetAttachDef(namespace, name string) error {
	By(fmt.Sprintf("deleting NetworkAttachmentDefinition '%s/%s'", namespace, name))
	virtClient := kubevirt.Client()
	err := virtClient.NetworkClient().K8sCniCncfIoV1().NetworkAttachmentDefinitions(namespace).Delete(
		context.Background(),
		name,
		k8smetav1.DeleteOptions{},
	)
	return err
}

func testVMIOptions(bridgeNetAttachDefName, macvtapNetAttachDefName string) []libvmi.Option {
	defaultNetwork := &kvv1.Network{
		Name:          libvmi.DefaultInterfaceName,
		NetworkSource: kvv1.NetworkSource{Pod: &kvv1.PodNetwork{}},
	}
	bridgeNetwork1 := &kvv1.Network{
		Name: "brnatnet1",
		NetworkSource: kvv1.NetworkSource{Multus: &kvv1.MultusNetwork{
			NetworkName: bridgeNetAttachDefName,
		}},
	}
	bridgeNetwork2 := &kvv1.Network{
		Name: "brnet2",
		NetworkSource: kvv1.NetworkSource{Multus: &kvv1.MultusNetwork{
			NetworkName: bridgeNetAttachDefName,
		}},
	}
	mavtapNetwork := &kvv1.Network{
		Name: "macvtapnet1",
		NetworkSource: kvv1.NetworkSource{Multus: &kvv1.MultusNetwork{
			NetworkName: macvtapNetAttachDefName,
		}},
	}
	mavtapIface := kvv1.Interface{
		Name: "macvtapnet1",
		InterfaceBindingMethod: kvv1.InterfaceBindingMethod{
			Macvtap: &kvv1.InterfaceMacvtap{},
		},
	}
	return []libvmi.Option{
		libvmi.WithCloudInitNoCloudUserData("#!/bin/bash\necho 'hello'\n", false),
		libvmi.WithNetwork(defaultNetwork),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
		libvmi.WithNetwork(bridgeNetwork1),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(bridgeNetwork1.Name)),
		libvmi.WithNetwork(bridgeNetwork2),
		libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(bridgeNetwork2.Name)),
		libvmi.WithNetwork(mavtapNetwork),
		libvmi.WithInterface(mavtapIface),
	}
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

// assertVMIUpdated wait until the given VMI migration completes.
// Migration failures are tolerated up to the given trials count.
// The VMI is asserted as follows:
// - VMI isn't labeled with "kubevirt.io/outdatedLauncherImage".
// - VMI status reflect successful migration (status.migrationsState.completed == True).
// - The corresponding 'VirtualMachineInstanceMigration' object reflects successful migration (status.phase == Succeeded).
func assertVMIUpdated(vmiNamespace, vmiName string, trials int) {
	virtClient := kubevirt.Client()
	maxTrialsExceeded := false
	Eventually(func() error {
		vmi, err := virtClient.VirtualMachineInstance(vmiNamespace).Get(context.Background(), vmiName, &k8smetav1.GetOptions{})
		if err != nil {
			return err
		}
		if vmi.Status.MigrationState == nil {
			return fmt.Errorf("VMI '%s/%s' migration did not start yet", vmi.Namespace, vmi.Name)
		}
		if len(vmi.Status.ActivePods) > trials {
			maxTrialsExceeded = true
			return nil
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
	Expect(maxTrialsExceeded).To(BeFalse(), "VMI '%s/%s' migration failed  after 3 trials", vmiNamespace, vmiName)

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
