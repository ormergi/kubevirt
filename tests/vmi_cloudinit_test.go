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
 * Copyright 2017 Red Hat, Inc.
 *
 */

package tests_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kubevirt.io/kubevirt/pkg/libvmi"
	libvmici "kubevirt.io/kubevirt/pkg/libvmi/cloudinit"
	"kubevirt.io/kubevirt/pkg/pointer"

	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/libvmops"

	expect "github.com/google/goexpect"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubevirt.io/kubevirt/tests/exec"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/libpod"
	"kubevirt.io/kubevirt/tests/libsecret"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	cloudinit "kubevirt.io/kubevirt/pkg/cloud-init"
	libcloudinit "kubevirt.io/kubevirt/pkg/libvmi/cloudinit"
	"kubevirt.io/kubevirt/pkg/util/net/dns"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/console"
)

const (
	sshAuthorizedKey     = "ssh-rsa AAAAB3NzaC1yc2EAAAABIwAAAQEA6NF8iallvQVp22WDkT test-ssh-key"
	fedoraPassword       = "fedora"
	expectedUserDataFile = "cloud-init-userdata-executed"
	testNetworkData      = "#Test networkData"
	testUserData         = "#cloud-config"
)

var _ = Describe("[rfe_id:151][crit:high][vendor:cnv-qe@redhat.com][level:component][sig-compute]CloudInit UserData", decorators.SigCompute, func() {

	var virtClient kubecli.KubevirtClient

	var (
		LaunchVMI                 func(*v1.VirtualMachineInstance) *v1.VirtualMachineInstance
		VerifyUserDataVMI         func(*v1.VirtualMachineInstance, []expect.Batcher, time.Duration)
		MountCloudInitNoCloud     func(*v1.VirtualMachineInstance)
		MountCloudInitConfigDrive func(*v1.VirtualMachineInstance)
		CheckCloudInitFile        func(*v1.VirtualMachineInstance, string, string)
		CheckCloudInitIsoSize     func(vmi *v1.VirtualMachineInstance, source cloudinit.DataSourceType)
	)

	BeforeEach(func() {
		virtClient = kubevirt.Client()

		// from default virt-launcher flag: do we need to make this configurable in some cases?
		cloudinit.SetLocalDirectoryOnly("/var/run/kubevirt-ephemeral-disks/cloud-init-data")
		MountCloudInitNoCloud = tests.MountCloudInitFunc("cidata")
		MountCloudInitConfigDrive = tests.MountCloudInitFunc("config-2")
	})

	LaunchVMI = func(vmi *v1.VirtualMachineInstance) *v1.VirtualMachineInstance {
		By("Starting a VirtualMachineInstance")
		obj, err := virtClient.RestClient().Post().Resource("virtualmachineinstances").Namespace(testsuite.GetTestNamespace(vmi)).Body(vmi).Do(context.Background()).Get()
		Expect(err).ToNot(HaveOccurred())

		By("Waiting the VirtualMachineInstance start")
		vmi, ok := obj.(*v1.VirtualMachineInstance)
		Expect(ok).To(BeTrue(), "Object is not of type *v1.VirtualMachineInstance")
		Expect(libwait.WaitForSuccessfulVMIStart(vmi).Status.NodeName).ToNot(BeEmpty())
		return vmi
	}

	VerifyUserDataVMI = func(vmi *v1.VirtualMachineInstance, commands []expect.Batcher, timeout time.Duration) {
		By("Checking that the VirtualMachineInstance serial console output equals to expected one")
		Expect(console.SafeExpectBatch(vmi, commands, int(timeout.Seconds()))).To(Succeed())
	}

	CheckCloudInitFile = func(vmi *v1.VirtualMachineInstance, testFile, testData string) {
		cmdCheck := "cat " + filepath.Join("/mnt", testFile) + "\n"
		err := console.SafeExpectBatch(vmi, []expect.Batcher{
			&expect.BSnd{S: "sudo su -\n"},
			&expect.BExp{R: console.PromptExpression},
			&expect.BSnd{S: cmdCheck},
			&expect.BExp{R: testData},
		}, 15)
		Expect(err).ToNot(HaveOccurred())
	}

	CheckCloudInitIsoSize = func(vmi *v1.VirtualMachineInstance, source cloudinit.DataSourceType) {
		pod, err := libpod.GetPodByVirtualMachineInstance(vmi, vmi.Namespace)
		Expect(err).NotTo(HaveOccurred())

		path := cloudinit.GetIsoFilePath(source, vmi.Name, vmi.Namespace)

		By(fmt.Sprintf("Checking cloud init ISO at '%s' is 4k-block fs compatible", path))
		cmdCheck := []string{"stat", "--printf='%s'", path}

		out, err := exec.ExecuteCommandOnPod(pod, "compute", cmdCheck)
		Expect(err).NotTo(HaveOccurred())
		size, err := strconv.Atoi(strings.Trim(out, "'"))
		Expect(err).NotTo(HaveOccurred())
		Expect(size % 4096).To(Equal(0))
	}

	Describe("[rfe_id:151][crit:medium][vendor:cnv-qe@redhat.com][level:component]A new VirtualMachineInstance", func() {
		Context("with cloudInitNoCloud userDataBase64 source", func() {
			It("[test_id:1615]should have cloud-init data", func() {
				userData := fmt.Sprintf("#!/bin/sh\n\ntouch /%s\n", expectedUserDataFile)
				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudEncodedUserData(userData)),
				)

				vmi = libvmops.RunVMIAndExpectLaunch(vmi, 60)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)
				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceNoCloud)

				By("Checking whether the user-data script had created the file")
				Expect(console.RunCommand(vmi, fmt.Sprintf("cat /%s\n", expectedUserDataFile), time.Second*120)).To(Succeed())
			})

			Context("with injected ssh-key", func() {
				It("[test_id:1616]should have ssh-key under authorized keys", func() {
					userData := fmt.Sprintf(
						"#cloud-config\npassword: %s\nchpasswd: { expire: False }\nssh_authorized_keys:\n  - %s",
						fedoraPassword,
						sshAuthorizedKey,
					)
					vmi := libvmifact.NewFedora(libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudUserData(userData)))

					vmi = LaunchVMI(vmi)
					CheckCloudInitIsoSize(vmi, cloudinit.DataSourceNoCloud)

					VerifyUserDataVMI(vmi, []expect.Batcher{
						&expect.BSnd{S: "\n"},
						&expect.BExp{R: "login:"},
						&expect.BSnd{S: "fedora\n"},
						&expect.BExp{R: "Password:"},
						&expect.BSnd{S: fedoraPassword + "\n"},
						&expect.BExp{R: console.PromptExpression},
						&expect.BSnd{S: "cat /home/fedora/.ssh/authorized_keys\n"},
						&expect.BExp{R: "test-ssh-key"},
					}, time.Second*300)
				})
			})
		})

		Context("with cloudInitConfigDrive userDataBase64 source", func() {
			It("[test_id:3178]should have cloud-init data", func() {
				userData := fmt.Sprintf("#!/bin/sh\n\ntouch /%s\n", expectedUserDataFile)
				vmi := libvmifact.NewCirros(libvmi.WithCloudInitConfigDrive(libcloudinit.WithConfigDriveUserData(userData)))

				vmi = libvmops.RunVMIAndExpectLaunch(vmi, 60)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)
				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

				By("Checking whether the user-data script had created the file")
				Expect(console.RunCommand(vmi, fmt.Sprintf("cat /%s\n", expectedUserDataFile), time.Second*120)).To(Succeed())
			})

			Context("with injected ssh-key", func() {
				It("[test_id:3178]should have ssh-key under authorized keys", func() {
					userData := fmt.Sprintf(
						"#cloud-config\npassword: %s\nchpasswd: { expire: False }\nssh_authorized_keys:\n  - %s",
						fedoraPassword,
						sshAuthorizedKey,
					)
					vmi := libvmifact.NewFedora(
						libvmi.WithCloudInitConfigDrive(libvmici.WithConfigDriveUserData(userData)),
					)

					vmi = LaunchVMI(vmi)
					CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

					VerifyUserDataVMI(vmi, []expect.Batcher{
						&expect.BSnd{S: "\n"},
						&expect.BExp{R: "login:"},
						&expect.BSnd{S: "fedora\n"},
						&expect.BExp{R: "Password:"},
						&expect.BSnd{S: fedoraPassword + "\n"},
						&expect.BExp{R: console.PromptExpression},
						&expect.BSnd{S: "cat /home/fedora/.ssh/authorized_keys\n"},
						&expect.BExp{R: "test-ssh-key"},
					}, time.Second*300)
				})
			})

			It("cloud-init instance-id should be stable", func() {
				getInstanceId := func(vmi *v1.VirtualMachineInstance) (string, error) {
					cmd := "cat /var/lib/cloud/data/instance-id"
					instanceId, err := console.RunCommandAndStoreOutput(vmi, cmd, time.Second*30)
					return instanceId, err
				}

				userData := fmt.Sprintf(
					"#cloud-config\npassword: %s\nchpasswd: { expire: False }",
					fedoraPassword,
				)
				vmi := libvmifact.NewFedora(libvmi.WithCloudInitConfigDrive(libvmici.WithConfigDriveUserData(userData)))
				// runStrategy := v1.RunStrategyManual
				vm := &v1.VirtualMachine{
					ObjectMeta: vmi.ObjectMeta,
					Spec: v1.VirtualMachineSpec{
						Running: pointer.P(false),
						Template: &v1.VirtualMachineInstanceTemplateSpec{
							Spec: vmi.Spec,
						},
					},
				}

				By("Start VM")
				vm, err := virtClient.VirtualMachine(testsuite.GetTestNamespace(vmi)).Create(context.Background(), vm, metav1.CreateOptions{})
				Expect(vm.Namespace).ToNot(BeEmpty())
				Expect(err).ToNot(HaveOccurred())
				vm = libvmops.StartVirtualMachine(vm)
				vmi, err = virtClient.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

				By("Get VM cloud-init instance-id")
				instanceId, err := getInstanceId(vmi)
				Expect(err).ToNot(HaveOccurred())
				Expect(instanceId).ToNot(BeEmpty())

				By("Restart VM")
				vm = libvmops.StopVirtualMachine(vm)
				libvmops.StartVirtualMachine(vm)
				vmi, err = virtClient.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

				By("Get VM cloud-init instance-id after restart")
				newInstanceId, err := getInstanceId(vmi)
				Expect(err).ToNot(HaveOccurred())

				By("Make sure the instance-ids match")
				Expect(instanceId).To(Equal(newInstanceId))
			})
		})

		Context("should process provided cloud-init data", func() {
			userData := fmt.Sprintf("#!/bin/sh\n\ntouch /%s\n", expectedUserDataFile)

			runTest := func(vmi *v1.VirtualMachineInstance, dsType cloudinit.DataSourceType) {
				vmi = libvmops.RunVMIAndExpectLaunch(vmi, 60)

				By("waiting until login appears")
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				By("validating cloud-init disk is 4k aligned")
				CheckCloudInitIsoSize(vmi, dsType)

				By("Checking whether the user-data script had created the file")
				Expect(console.RunCommand(vmi, fmt.Sprintf("cat /%s\n", expectedUserDataFile), time.Second*120)).To(Succeed())

				By("validating the hostname matches meta-data")
				Expect(console.SafeExpectBatch(vmi, []expect.Batcher{
					&expect.BSnd{S: "hostname\n"},
					&expect.BExp{R: dns.SanitizeHostname(vmi)},
				}, 10)).To(Succeed())
			}

			It("[test_id:1617] with cloudInitNoCloud userData source", func() {
				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudUserData(userData)),
				)

				runTest(vmi, cloudinit.DataSourceNoCloud)
			})
			It("[test_id:3180] with cloudInitConfigDrive userData source", func() {
				vmi := libvmifact.NewCirros(libvmi.WithCloudInitConfigDrive(libcloudinit.WithConfigDriveUserData(userData)))
				runTest(vmi, cloudinit.DataSourceConfigDrive)
			})
		})

		It("[test_id:1618]should take user-data from k8s secret", func() {
			userData := fmt.Sprintf("#!/bin/sh\n\ntouch /%s\n", expectedUserDataFile)
			secretID := fmt.Sprintf("%s-test-secret", uuid.NewString())

			vmi := libvmifact.NewCirros(
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudUserDataSecretName(secretID)),
			)

			// Store userdata as k8s secret
			By("Creating a user-data secret")
			secret := libsecret.New(secretID, libsecret.DataString{"userdata": userData})
			_, err := virtClient.CoreV1().Secrets(testsuite.GetTestNamespace(vmi)).Create(context.Background(), secret, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			runningVMI := libvmops.RunVMIAndExpectLaunch(vmi, 60)
			runningVMI = libwait.WaitUntilVMIReady(runningVMI, console.LoginToCirros)

			CheckCloudInitIsoSize(runningVMI, cloudinit.DataSourceNoCloud)

			By("Checking whether the user-data script had created the file")
			Expect(console.RunCommand(runningVMI, fmt.Sprintf("cat /%s\n", expectedUserDataFile), time.Second*120)).To(Succeed())

			// Expect that the secret is not present on the vmi itself
			runningVMI, err = virtClient.VirtualMachineInstance(testsuite.GetTestNamespace(runningVMI)).Get(context.Background(), runningVMI.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			runningCloudInitVolume := lookupCloudInitNoCloudVolume(runningVMI.Spec.Volumes)
			origCloudInitVolume := lookupCloudInitNoCloudVolume(vmi.Spec.Volumes)

			Expect(origCloudInitVolume).To(Equal(runningCloudInitVolume), "volume must not be changed when running the vmi, to prevent secret leaking")
		})

		Context("with cloudInitNoCloud networkData", func() {
			It("[test_id:3181]should have cloud-init network-config with NetworkData source", func() {
				vmi := libvmifact.NewCirros(
					libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
					libvmi.WithNetwork(v1.DefaultPodNetwork()),
					libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(testNetworkData)),
				)
				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceNoCloud)

				By("mouting cloudinit iso")
				MountCloudInitNoCloud(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "network-config", testNetworkData)

			})
			It("[test_id:3182]should have cloud-init network-config with NetworkDataBase64 source", func() {
				vmi := libvmifact.NewCirros(
					libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
					libvmi.WithNetwork(v1.DefaultPodNetwork()),
					libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudEncodedNetworkData(testNetworkData)),
				)
				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceNoCloud)

				By("mouting cloudinit iso")
				MountCloudInitNoCloud(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "network-config", testNetworkData)

			})
			It("[test_id:3183]should have cloud-init network-config from k8s secret", func() {
				secretID := fmt.Sprintf("%s-test-secret", uuid.NewString())

				vmi := libvmifact.NewCirros(
					libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
					libvmi.WithNetwork(v1.DefaultPodNetwork()),
					libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkDataSecretName(secretID)),
				)

				By("Creating a secret with network data")
				secret := libsecret.New(secretID, libsecret.DataString{"networkdata": testNetworkData})
				_, err := virtClient.CoreV1().Secrets(testsuite.GetTestNamespace(vmi)).Create(context.Background(), secret, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceNoCloud)

				By("mouting cloudinit iso")
				MountCloudInitNoCloud(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "network-config", testNetworkData)

				// Expect that the secret is not present on the vmi itself
				vmi, err = virtClient.VirtualMachineInstance(testsuite.GetTestNamespace(vmi)).Get(context.Background(), vmi.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				testVolume := lookupCloudInitNoCloudVolume(vmi.Spec.Volumes)
				Expect(testVolume).ToNot(BeNil(), "should find cloud-init volume in vmi spec")
				Expect(testVolume.CloudInitNoCloud.UserData).To(BeEmpty())
				Expect(testVolume.CloudInitNoCloud.NetworkData).To(BeEmpty())
				Expect(testVolume.CloudInitNoCloud.NetworkDataBase64).To(BeEmpty())
			})

		})

		Context("with cloudInitConfigDrive networkData", func() {
			It("[test_id:3184]should have cloud-init network-config with NetworkData source", func() {
				vmi := libvmifact.NewCirros(libvmi.WithCloudInitConfigDrive(libcloudinit.WithConfigDriveNetworkData(testNetworkData)))
				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

				By("mouting cloudinit iso")
				MountCloudInitConfigDrive(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "openstack/latest/network_data.json", testNetworkData)
			})
			It("[test_id:4622]should have cloud-init meta_data with tagged devices", func() {
				testInstancetype := "testInstancetype"
				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitConfigDrive(libcloudinit.WithConfigDriveNetworkData(testNetworkData)),
					libvmi.WithInterface(v1.Interface{
						Name: "default",
						Tag:  "specialNet",
						InterfaceBindingMethod: v1.InterfaceBindingMethod{
							Masquerade: &v1.InterfaceMasquerade{},
						},
					}),
					libvmi.WithNetwork(v1.DefaultPodNetwork()),
					libvmi.WithAnnotation(v1.InstancetypeAnnotation, testInstancetype),
				)
				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)
				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

				domXml, err := tests.GetRunningVirtualMachineInstanceDomainXML(virtClient, vmi)
				Expect(err).ToNot(HaveOccurred())

				domSpec := &api.DomainSpec{}
				Expect(xml.Unmarshal([]byte(domXml), domSpec)).To(Succeed())
				nic := domSpec.Devices.Interfaces[0]
				address := nic.Address
				pciAddrStr := fmt.Sprintf("%s:%s:%s.%s", address.Domain[2:], address.Bus[2:], address.Slot[2:], address.Function[2:])
				deviceData := []cloudinit.DeviceData{
					{
						Type:    cloudinit.NICMetadataType,
						Bus:     nic.Address.Type,
						Address: pciAddrStr,
						MAC:     nic.MAC.MAC,
						Tags:    []string{"specialNet"},
					},
				}
				vmi, err = virtClient.VirtualMachineInstance(testsuite.GetTestNamespace(vmi)).Get(context.Background(), vmi.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				metadataStruct := cloudinit.ConfigDriveMetadata{
					InstanceID:   fmt.Sprintf("%s.%s", vmi.Name, vmi.Namespace),
					InstanceType: testInstancetype,
					Hostname:     dns.SanitizeHostname(vmi),
					UUID:         string(vmi.Spec.Domain.Firmware.UUID),
					Devices:      &deviceData,
				}

				buf, err := json.Marshal(metadataStruct)
				Expect(err).ToNot(HaveOccurred())
				By("mouting cloudinit iso")
				MountCloudInitConfigDrive(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "openstack/latest/network_data.json", testNetworkData)

				By("checking cloudinit meta-data")
				tests.CheckCloudInitMetaData(vmi, "openstack/latest/meta_data.json", string(buf))
			})
			It("[test_id:3185]should have cloud-init network-config with NetworkDataBase64 source", func() {
				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitConfigDrive(libcloudinit.WithConfigDriveEncodedNetworkData(testNetworkData)),
				)
				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)
				By("mouting cloudinit iso")
				MountCloudInitConfigDrive(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "openstack/latest/network_data.json", testNetworkData)

			})
			It("[test_id:3186]should have cloud-init network-config from k8s secret", func() {
				secretID := fmt.Sprintf("%s-test-secret", uuid.NewString())
				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitConfigDrive(
						libcloudinit.WithConfigDriveUserDataSecretName(secretID),
						libcloudinit.WithConfigDriveNetworkDataSecretName(secretID),
					),
				)

				// Store cloudinit data as k8s secret
				By("Creating a secret with user and network data")
				secret := libsecret.New(secretID, libsecret.DataString{
					"userdata":    testUserData,
					"networkdata": testNetworkData,
				})

				_, err := virtClient.CoreV1().Secrets(testsuite.GetTestNamespace(vmi)).Create(context.Background(), secret, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

				By("mouting cloudinit iso")
				MountCloudInitConfigDrive(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "openstack/latest/network_data.json", testNetworkData)
				CheckCloudInitFile(vmi, "openstack/latest/user_data", testUserData)

				// Expect that the secret is not present on the vmi itself
				vmi, err = virtClient.VirtualMachineInstance(testsuite.GetTestNamespace(vmi)).Get(context.Background(), vmi.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				for _, volume := range vmi.Spec.Volumes {
					if volume.CloudInitConfigDrive != nil {
						Expect(volume.CloudInitConfigDrive.UserData).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.UserDataBase64).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.UserDataSecretRef).ToNot(BeNil())

						Expect(volume.CloudInitConfigDrive.NetworkData).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.NetworkDataBase64).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.NetworkDataSecretRef).ToNot(BeNil())
						break
					}
				}
			})

			DescribeTable("[test_id:3187]should have cloud-init userdata and network-config from separate k8s secrets", func(userDataLabel string, networkDataLabel string) {
				uSecretID := fmt.Sprintf("%s-test-secret", uuid.NewString())
				nSecretID := fmt.Sprintf("%s-test-secret", uuid.NewString())

				vmi := libvmifact.NewCirros(
					libvmi.WithCloudInitConfigDrive(
						libcloudinit.WithConfigDriveUserDataSecretName(uSecretID),
						libcloudinit.WithConfigDriveNetworkDataSecretName(nSecretID),
					),
				)

				ns := testsuite.GetTestNamespace(vmi)

				By("Creating a secret with userdata")
				uSecret := libsecret.New(uSecretID, libsecret.DataString{userDataLabel: testUserData})

				_, err := virtClient.CoreV1().Secrets(ns).Create(context.Background(), uSecret, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Creating a secret with network data")
				nSecret := libsecret.New(nSecretID, libsecret.DataString{networkDataLabel: testNetworkData})

				_, err = virtClient.CoreV1().Secrets(ns).Create(context.Background(), nSecret, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				vmi = LaunchVMI(vmi)
				vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToCirros)

				CheckCloudInitIsoSize(vmi, cloudinit.DataSourceConfigDrive)

				By("mounting cloudinit iso")
				MountCloudInitConfigDrive(vmi)

				By("checking cloudinit network-config")
				CheckCloudInitFile(vmi, "openstack/latest/network_data.json", testNetworkData)

				By("checking cloudinit user-data")
				CheckCloudInitFile(vmi, "openstack/latest/user_data", testUserData)

				// Expect that the secret is not present on the vmi itself
				vmi, err = virtClient.VirtualMachineInstance(testsuite.GetTestNamespace(vmi)).Get(context.Background(), vmi.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				for _, volume := range vmi.Spec.Volumes {
					if volume.CloudInitConfigDrive != nil {
						Expect(volume.CloudInitConfigDrive.UserData).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.UserDataBase64).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.UserDataSecretRef).ToNot(BeNil())

						Expect(volume.CloudInitConfigDrive.NetworkData).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.NetworkDataBase64).To(BeEmpty())
						Expect(volume.CloudInitConfigDrive.NetworkDataSecretRef).ToNot(BeNil())
						break
					}
				}
			},
				Entry("with lowercase labels", "userdata", "networkdata"),
				Entry("with camelCase labels", "userData", "networkData"),
			)
		})

	})
})

func lookupCloudInitNoCloudVolume(volumes []v1.Volume) *v1.Volume {
	for i, volume := range volumes {
		if volume.CloudInitNoCloud != nil {
			return &volumes[i]
		}
	}
	return nil
}
