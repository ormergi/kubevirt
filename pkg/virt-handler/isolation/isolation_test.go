package isolation

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/client-go/api/v1"
	"kubevirt.io/kubevirt/pkg/virt-handler/cgroup"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
)

var (
	mountInfoTestData = map[string]string{
		"overlay":      "/proc/1/root/var/lib/docker/overlay2/f15d9ce07df72e80d809aa99ab4a171f2f3636f65f0653e75db8ca0befd8ae02/merged",
		"devicemapper": "/proc/1/root/var/lib/docker/devicemapper/mnt/d0990551ba8254871a449b2ff0d9063061ae96a2c195d7a850b62f030eae1710/rootfs",
		"btrfs":        "/proc/1/root/var/lib/containers/storage/btrfs/subvolumes/e9a94e2cde75c54834378d4835d4eda6bebb56b02068b9254780de6f9344ad0e",
	}
)

var _ = Describe("Isolation", func() {

	Context("With an existing socket", func() {

		var socket net.Listener
		var tmpDir string
		var podsDir string
		var podUID string
		var finished chan struct{} = nil
		var ctrl *gomock.Controller
		var cgroupParser *cgroup.MockParser

		podUID = "pid-uid-1234"
		vm := v1.NewMinimalVMIWithNS("default", "testvm")
		vm.UID = "1234"
		vm.Status = v1.VirtualMachineInstanceStatus{
			ActivePods: map[types.UID]string{
				types.UID(podUID): "myhost",
			},
		}
		vm.Status.NodeName = "myhost"

		BeforeEach(func() {
			var err error
			tmpDir, err = ioutil.TempDir("", "kubevirt")
			Expect(err).ToNot(HaveOccurred())
			podsDir, err = ioutil.TempDir("", "pods")
			Expect(err).ToNot(HaveOccurred())

			cmdclient.SetLegacyBaseDir(tmpDir)
			cmdclient.SetPodsBaseDir(tmpDir)

			os.MkdirAll(tmpDir+"/sockets/", os.ModePerm)
			socketFile := cmdclient.SocketFilePathOnHost(podUID)
			os.MkdirAll(filepath.Dir(socketFile), os.ModePerm)
			socket, err = net.Listen("unix", socketFile)
			Expect(err).ToNot(HaveOccurred())
			finished = make(chan struct{})
			go func() {
				for {
					conn, err := socket.Accept()
					if err != nil {
						close(finished)
						// closes when socket listener is closed
						return
					}
					conn.Close()
				}
			}()

			ctrl = gomock.NewController(GinkgoT())
			cgroupParser = cgroup.NewMockParser(ctrl)
			cgroupParser.
				EXPECT().
				Parse(gomock.Eq(os.Getpid())).
				Return(map[string]string{"devices": "/"}, nil).
				AnyTimes()
		})

		It("Should detect the PID of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Pid()).To(Equal(os.Getpid()))
		})

		It("Should detect the Parent PID of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.PPid()).To(Equal(os.Getppid()))
		})

		It("Should not detect any slice if there is no matching controller", func() {
			_, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"not_existing_slice"}).Detect(vm)
			Expect(err).To(HaveOccurred())
		})

		It("Should detect the 'devices' controller slice of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Slice()).To(HavePrefix("/"))
		})

		It("Should detect the PID namespace of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.PIDNamespace()).To(Equal(fmt.Sprintf("/proc/%d/ns/pid", os.Getpid())))
		})

		It("Should detect the Mount root of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.MountRoot()).To(Equal(fmt.Sprintf("/proc/%d/root", os.Getpid())))
		})

		It("Should detect the Network namespace of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.NetNamespace()).To(Equal(fmt.Sprintf("/proc/%d/ns/net", os.Getpid())))
		})

		It("Should detect the root mount info of the test suite", func() {
			result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
			Expect(err).ToNot(HaveOccurred())
			mountInfo, err := result.MountInfoRoot()
			Expect(err).ToNot(HaveOccurred())
			Expect(mountInfo.MountPoint).To(Equal("/"))
		})

		It("Should detect the full path of the mount on a node", func() {
			// Restore the overwritten function
			defer func(f func(int) string) { mountInfoFunc = f }(mountInfoFunc)

			for testCase, want := range mountInfoTestData {
				mountInfoFunc = func(pid int) string {
					Expect(pid).To(SatisfyAny(Equal(1), Equal(os.Getpid())))
					base := filepath.Join("testdata", "mountinfo")
					if pid == 1 {
						return filepath.Join(base, fmt.Sprintf("%s_host", testCase))
					}
					return filepath.Join(base, fmt.Sprintf("%s_launcher", testCase))
				}

				mounted, err := NodeIsolationResult().IsMounted("/")
				Expect(err).ToNot(HaveOccurred())
				Expect(mounted).To(BeTrue())
				mounted, err = NodeIsolationResult().IsMounted("???")
				Expect(err).ToNot(HaveOccurred())
				Expect(mounted).To(BeFalse())

				result, err := NewSocketBasedIsolationDetector(tmpDir, cgroupParser).Whitelist([]string{"devices"}).Detect(vm)
				Expect(err).ToNot(HaveOccurred())
				mountInfo, err := result.MountInfoRoot()
				Expect(err).ToNot(HaveOccurred())
				fullPath, err := NodeIsolationResult().FullPath(mountInfo)
				Expect(err).ToNot(HaveOccurred())
				Expect(fullPath).To(Equal(want))
			}
		})

		AfterEach(func() {
			socket.Close()
			os.RemoveAll(tmpDir)
			os.RemoveAll(podsDir)
			if finished != nil {
				<-finished
			}

			ctrl.Finish()
		})
	})

	Context("VM with VFIO device", func() {
		vmi := newMinimalVMIWithSingleSRIOVIface("testvmi", "sriov1")

		Context("Should set QMEU process memory limits successfully", func() {
			var ctrl *gomock.Controller
			var podIsoDetector *MockPodIsolationDetector
			var isolationResult *MockIsolationResult

			virtLauncherForkedPid := 26
			virtLauncherForkedPPid := 1
			forkedVirtLauncherProcesses := newPsGoProcess([]ProcessStub{
				{pid: virtLauncherForkedPPid, ppid: 0, binary: "virt-launcher --no-fork false"},
				{pid: virtLauncherForkedPid, ppid: virtLauncherForkedPPid, binary: "virt-launcher --no-fork true"},
				{pid: 226, ppid: virtLauncherForkedPid, binary: libvirtProcessExecutable},
				{pid: 101, ppid: virtLauncherForkedPPid, binary: qemuProcessExecutable},
			})

			virtLauncherPid := 1
			virtLauncherPPid := 0
			virtLauncherProcesses := newPsGoProcess([]ProcessStub{
				{pid: virtLauncherPid, ppid: virtLauncherPPid, binary: "virt-launcher --no-fork false"},
				{pid: 101, ppid: virtLauncherPid, binary: libvirtProcessExecutable},
				{pid: 102, ppid: virtLauncherPid, binary: qemuProcessExecutable},
			})

			BeforeEach(func() {
				ctrl = gomock.NewController(GinkgoT())
				podIsoDetector = NewMockPodIsolationDetector(ctrl)
				isolationResult = NewMockIsolationResult(ctrl)
			})

			It("when virt-launcher process is forked (runs with --no-fork=false)", func() {
				isolationResult.EXPECT().Pid().Return(virtLauncherForkedPid).AnyTimes()
				isolationResult.EXPECT().PPid().Return(virtLauncherForkedPPid).AnyTimes()
				podIsoDetector.EXPECT().Detect(vmi).Return(isolationResult, nil).AnyTimes()

				processes := filterIsolatedQemuProcess(forkedVirtLauncherProcesses, isolationResult)

				Expect(adjustIsolatedProcessMemoryLimits(vmi, processes, setProcessMemoryLimitsStub)).To(Succeed())
			})

			It("when virt-launcher process is not forked (runs with --no-fork=true)", func() {
				isolationResult.EXPECT().Pid().Return(virtLauncherPid).AnyTimes()
				isolationResult.EXPECT().PPid().Return(virtLauncherPPid).AnyTimes()
				podIsoDetector.EXPECT().Detect(vmi).Return(isolationResult, nil).AnyTimes()

				processes := filterIsolatedQemuProcess(virtLauncherProcesses, isolationResult)

				Expect(adjustIsolatedProcessMemoryLimits(vmi, processes, setProcessMemoryLimitsStub)).To(Succeed())
			})
		})
	})
})

var _ = Describe("getMemlockSize", func() {
	vm := v1.NewMinimalVMIWithNS("default", "testvm")

	It("Should return correct number of bytes for memlock limit", func() {
		bytes_, err := getMemlockSize(vm)
		Expect(err).ToNot(HaveOccurred())
		// 1Gb (static part for vfio VMs) + 256Mb (estimated overhead) + 8 Mb (VM)
		Expect(int(bytes_)).To(Equal(1264389000))
	})
})

func newMinimalVMIWithSingleSRIOVIface(name, sriovDeviceName string) *v1.VirtualMachineInstance {
	sriovNetwork := v1.Network{NetworkSource: v1.NetworkSource{Multus: &v1.MultusNetwork{NetworkName: sriovDeviceName}}}
	sriovIface := v1.Interface{Name: sriovDeviceName, InterfaceBindingMethod: v1.InterfaceBindingMethod{SRIOV: &v1.InterfaceSRIOV{}}}

	vmi := v1.NewMinimalVMIWithNS("default", name)
	vmi.Spec.Networks = append(vmi.Spec.Networks, sriovNetwork)
	vmi.Spec.Domain.Devices.Interfaces = append(vmi.Spec.Domain.Devices.Interfaces, sriovIface)

	return vmi
}

func setProcessMemoryLimitsStub(pid int, max, current uint64) error {
	return nil
}
