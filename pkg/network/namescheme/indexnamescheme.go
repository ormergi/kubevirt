/*
 * This file is part of the KubeVirt project
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

package namescheme

import (
	"fmt"
	"strconv"
	"strings"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/network/vmispec"
)

const (
	defaultInterfaceNamePrefix = "eth"
	interfaceNamePrefix        = "net"
)

func IndexedInterfaceName(name string) string {
	for _, prefix := range []string{interfaceNamePrefix, defaultInterfaceNamePrefix} {
		index := strings.TrimPrefix(name, prefix)
		if _, err := strconv.Atoi(index); err == nil {
			return index
		}
	}
	return ""
}

// createIndexedNetworkNameScheme iterates over the VMI's Networks, and creates for each a pod interface name.
// The returned map associates between the network name and the generated pod interface name.
// Primary network will use "eth0" and the secondary ones will use "net<id>" format, where id is an enumeration
// from 1 to n.
func createIndexedNetworkNameScheme(vmiNetworks []v1.Network) map[string]string {
	indexedNetworkNameSchemeMap := mapMultusNonDefaultNetworksToPodInterfaceIndexedName(vmiNetworks)
	if multusDefaultNetwork := vmispec.LookUpDefaultNetwork(vmiNetworks); multusDefaultNetwork != nil {
		indexedNetworkNameSchemeMap[multusDefaultNetwork.Name] = PrimaryPodInterfaceName
	}
	return indexedNetworkNameSchemeMap
}
func mapMultusNonDefaultNetworksToPodInterfaceIndexedName(networks []v1.Network) map[string]string {
	indexedNetworkNameSchemeMap := map[string]string{}
	for i, network := range vmispec.FilterMultusNonDefaultNetworks(networks) {
		indexedNetworkNameSchemeMap[network.Name] = secondaryInterfaceIndexedName(i + 1)
	}
	return indexedNetworkNameSchemeMap
}

func secondaryInterfaceIndexedName(idx int) string {
	return fmt.Sprintf("%s%d", interfaceNamePrefix, idx)
}
