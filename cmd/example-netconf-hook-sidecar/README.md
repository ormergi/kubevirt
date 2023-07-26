# KubeVirt Netconf hook sidecar

## Summary

The Netconf hook sidecar enables overriding VMs interfaces MAC addresses.

## How to use

Set the following annotations:

```yaml
annotations:
  # Request the hook sidecar
  hooks.kubevirt.io/hookSidecars: '[{"image": "registry:5000/kubevirt/netconf-hook-sidecar:latest"}]'
  # Overwrite interface "blue" MAC address
  netconf.kubevirt.io/macAddress: '[{"name": "blue", "macAddress": "02:02:02:02:02:02"}]'
```

## Example

Create NetworkAttachmentDefinition for a secondary interface

```shell
cat <<EOF | kubectl apply -f -
--- 
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: supersecretnet
spec:
  config: |
    {
      "cniVersion":"0.3.1",
      "name": "supersecretnet",
      "plugins": [
          {   
              "type": "bridge",
              "bridge": "cryptbr"
          }
      ]
    }
EOF
```

Create VM with secondary interface, and netconf sidecar hook annotations

```shell
cat <<EOF | kubectl apply -f -
---
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstance
metadata:
  annotations:
    hooks.kubevirt.io/hookSidecars: '[{"image": "registry:5000/kubevirt/netconf-hook-sidecar:latest"}]'
    netconf.kubevirt.io/macAddress: '[{"name": "blue", "macAddress": "02:02:02:02:02:02"}]'
  labels:
  name: vmi-with-netconf-sidecar-hook
spec:
  domain:
    devices:
      interfaces:
      - name: pods
        masquerade: {}
      - name: blue
        bridge: {}
      disks:
      - disk:
          bus: virtio
        name: containerdisk
      - disk:
          bus: virtio
        name: cloudinitdisk
      rng: {}
    resources:
      requests:
        memory: 1024M
  terminationGracePeriodSeconds: 0
  networks:
  - name: pods
    pod: {}
  - name: blue
    multus:
      networkName: supersecretnet
  volumes:
  - containerDisk:
      image: registry:5000/kubevirt/fedora-with-test-tooling-container-disk:devel
      imagePullPolicy: Always
    name: containerdisk
  - cloudInitNoCloud:
      userData: |-
        #cloud-config
        password: fedora
        chpasswd: { expire: False }
    name: cloudinitdisk
EOF
```

Once the VM is ready, check that VMI interfaces status reports the desired MAC address is set:

```shell
kubectl get vmi vmi-with-sidecar-hook -o jsonpath='{.status.interfaces}'  | jq '.[] | select(.name == "blue")'
{
  "infoSource": "domain, guest-agent, multus-status",
  "interfaceName": "eth1",
  "mac": "02:02:02:02:02:02",
  "name": "blue",
  "queueCount": 1
}
```
