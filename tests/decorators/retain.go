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
 * Copyright 2023 Red Hat, Inc.
 *
 */

package decorators

import (
	"github.com/onsi/ginkgo/v2"
)

const (
	retainVirtualMachineInstancesKey           = "retain-vmis"
	retainVirtualMachineInstanceReplicaSetsKey = "retain-vmirss"
)

var (
	RetainVirtualMachineInstances           = ginkgo.Label(retainVirtualMachineInstancesKey)
	RetainVirtualMachineInstanceReplicaSets = ginkgo.Label(retainVirtualMachineInstanceReplicaSetsKey)
)

func HasLabel(specReport ginkgo.SpecReport, labels ginkgo.Labels) bool {
	lookupLabel := map[string]string{}
	for _, label := range labels {
		lookupLabel[label] = label
	}

	for _, specReportLabel := range specReport.Labels() {
		if _, exists := lookupLabel[specReportLabel]; exists {
			return true
		}
	}

	return false
}

func ShouldRetainVMIs(specReport ginkgo.SpecReport) bool {
	lookupRetainVMIsLabels := map[string]string{
		retainVirtualMachineInstanceReplicaSetsKey: retainVirtualMachineInstanceReplicaSetsKey,
		retainVirtualMachineInstancesKey:           retainVirtualMachineInstancesKey,
	}

	for _, specReportLabel := range specReport.Labels() {
		if _, exists := lookupRetainVMIsLabels[specReportLabel]; exists {
			return true
		}
	}
	return false
}
