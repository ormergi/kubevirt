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
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
)

var (
	RetainVirtualMachineInstances      = ginkgo.Label("retain-vmi")
	RetainNetworkAttachmentDefinitions = ginkgo.Label("retain-net-attach-def")
	RetainPods                         = ginkgo.Label("retain-pods")
)

const RetainPodsWithLabelKey = "retain-pods-with-label"

func RetainPodsWithLabel(key, value string) ginkgo.Labels {
	return ginkgo.Label(strings.Join([]string{RetainPodsWithLabelKey, key, value}, "="))
}

func RetainPodsLabelSelector(specReport ginkgo.SpecReport) (string, string, error) {
	var label string
	for _, specLabel := range specReport.Labels() {
		if strings.HasPrefix(specLabel, RetainPodsWithLabelKey) {
			label = specLabel
			break
		}
	}
	if label == "" {
		return "", "", fmt.Errorf("spec-report %q label %q not found", specReport.ContainerHierarchyTexts, RetainPodsWithLabelKey)
	}

	return specReportLabelsSelector(specReport, RetainPodsWithLabelKey, label)
}

func specReportLabelsSelector(specReport ginkgo.SpecReport, prefix, label string) (key string, value string, err error) {
	specReportName := specReport.ContainerHierarchyTexts

	labelSelector := strings.TrimPrefix(label, prefix)
	labelSelector = strings.TrimPrefix(labelSelector, "=")
	if labelSelector == "" {
		return "", "", fmt.Errorf("spec-report %q label %q has no label selector value", specReportName, label)
	}

	labelSelectorKeyValue := strings.Split(labelSelector, "=")
	if len(labelSelectorKeyValue) != 2 {
		return "", "", fmt.Errorf("spec-report %q label %q value is not valid:\n\tExpects %q\nto be in a form of a label selector '<key>=<value>' ", specReportName, label, labelSelector)
	}

	return labelSelectorKeyValue[0], labelSelectorKeyValue[1], nil
}

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

func HasLabelWithPrefix(specReport ginkgo.SpecReport, prefix string) bool {
	for _, specReportLabel := range specReport.Labels() {
		if strings.HasPrefix(specReportLabel, prefix) {
			return true
		}
	}

	return false
}
