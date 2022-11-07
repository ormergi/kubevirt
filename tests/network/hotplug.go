/*
 * This file is part of the kubevirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2022 Red Hat, Inc.
 *
 */

package network

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	expect "github.com/google/goexpect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/libvmi"
	"kubevirt.io/kubevirt/tests/util"
)

var _ = SIGDescribe("nic-hotplug", func() {
	var virtClient kubecli.KubevirtClient

	BeforeEach(func() {
		var err error

		virtClient, err = kubecli.GetKubevirtClient()
		util.PanicOnError(err)
	})

	Context("a running VMI", func() {
		const (
			bridgeName  = "supadupabr"
			ifaceName   = "iface1"
			networkName = "skynet"
			vmIfaceName = "eth1"
		)

		var vmi *v1.VirtualMachineInstance

		BeforeEach(func() {
			vmi = setupVMI(virtClient, libvmi.NewAlpineWithTestTooling(libvmi.WithMasqueradeNetworking()...))
			Expect(
				createBridgeNetworkAttachmentDefinition(virtClient, util.NamespaceTestDefault, networkName, bridgeName),
			).To(Succeed())
			Expect(assertHotpluggedIfaceDoesNotExist(vmi, vmIfaceName)).To(Succeed())

			Expect(
				virtClient.VirtualMachineInstance(vmi.GetNamespace()).AddInterface(
					vmi.GetName(),
					addIfaceOptions(networkName, ifaceName),
				),
			).To(Succeed())

			Eventually(func() []v1.VirtualMachineInstanceNetworkInterface {
				return vmiCurrentInterfaces(virtClient, vmi.GetNamespace(), vmi.GetName())
			}, 30*time.Second).Should(
				WithTransform(
					CleanMACAddressesFromStatus,
					ConsistOf(interfaceStatusFromInterfaceNames(ifaceName))))
		})

		It("can be hotplugged a network interface", func() {
			Expect(assertHotpluggedIfaceExists(vmi, vmIfaceName)).To(Succeed())
		})
	})
})

func vmiCurrentInterfaces(virtClient kubecli.KubevirtClient, vmiNamespace, vmiName string) []v1.VirtualMachineInstanceNetworkInterface {
	vmi, err := virtClient.VirtualMachineInstance(vmiNamespace).Get(vmiName, &metav1.GetOptions{})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return secondaryInterfaces(vmi)
}

func assertHotpluggedIfaceExists(vmi *v1.VirtualMachineInstance, ifaceName string) error {
	return runSafeCommand(vmi, fmt.Sprintf("ip addr show %s\n", ifaceName))
}

func assertHotpluggedIfaceDoesNotExist(vmi *v1.VirtualMachineInstance, ifaceName string) error {
	return console.SafeExpectBatch(vmi, []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: console.PromptExpression},
		&expect.BSnd{S: fmt.Sprintf("ip addr show %s | wc -l\n", ifaceName)},
		&expect.BExp{R: console.RetValue("0")},
	}, 15)
}

func addIfaceOptions(networkName, ifaceName string) *v1.AddInterfaceOptions {
	return &v1.AddInterfaceOptions{
		NetworkName:   networkName,
		InterfaceName: ifaceName,
	}
}

func createBridgeNetworkAttachmentDefinition(virtClient kubecli.KubevirtClient, namespace, networkName string, bridgeName string) error {
	return createNetworkAttachmentDefinition(
		virtClient,
		networkName,
		namespace,
		fmt.Sprintf(linuxBridgeNAD, networkName, namespace, bridgeCNIType, bridgeName),
	)
}

func secondaryInterfaces(vmi *v1.VirtualMachineInstance) []v1.VirtualMachineInstanceNetworkInterface {
	indexedSecondaryNetworks := indexVMsSecondaryNetworks(vmi)

	var nonDefaultInterfaces []v1.VirtualMachineInstanceNetworkInterface
	for _, iface := range vmi.Status.Interfaces {
		if _, isNonDefaultPodNetwork := indexedSecondaryNetworks[iface.Name]; isNonDefaultPodNetwork {
			nonDefaultInterfaces = append(nonDefaultInterfaces, iface)
		}
	}
	return nonDefaultInterfaces
}

func indexVMsSecondaryNetworks(vmi *v1.VirtualMachineInstance) map[string]v1.Network {
	indexedSecondaryNetworks := map[string]v1.Network{}
	for _, network := range vmi.Spec.Networks {
		if network.Multus != nil && !network.Multus.Default {
			indexedSecondaryNetworks[network.Name] = network
		}
	}
	return indexedSecondaryNetworks
}

func CleanMACAddressesFromStatus(status []v1.VirtualMachineInstanceNetworkInterface) []v1.VirtualMachineInstanceNetworkInterface {
	for i := range status {
		status[i].MAC = ""
	}
	return status
}

func interfaceStatusFromInterfaceNames(ifaceNames ...string) []v1.VirtualMachineInstanceNetworkInterface {
	const initialIfacesInVMI = 1
	var ifaceStatus []v1.VirtualMachineInstanceNetworkInterface
	for i, ifaceName := range ifaceNames {
		ifaceStatus = append(ifaceStatus, v1.VirtualMachineInstanceNetworkInterface{
			Name:          ifaceName,
			InterfaceName: fmt.Sprintf("eth%d", i+initialIfacesInVMI),
			InfoSource:    "domain, guest-agent",
			QueueCount:    1,
			Ready:         true,
		})
	}
	return ifaceStatus
}
