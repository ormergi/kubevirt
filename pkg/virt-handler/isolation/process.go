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
 * Copyright 2020 Red Hat, Inc.
 *
 */

package isolation

import "github.com/mitchellh/go-ps"

func filterProcessByPPID(processes []ps.Process, ppids []int) []ps.Process {
	filterProcessParentPid := map[int]struct{}{}
	for _, ppid := range ppids {
		filterProcessParentPid[ppid] = struct{}{}
	}

	var filteredProcesses []ps.Process
	for _, process := range processes {
		if _, ok := filterProcessParentPid[process.PPid()]; ok {
			filteredProcesses = append(filteredProcesses, process)
		}
	}

	return filteredProcesses
}

func filterProcessByExecutable(processes []ps.Process, exectutable string) []ps.Process {
	var filteredProcesses []ps.Process
	for _, process := range processes {
		if process.Executable() == exectutable {
			filteredProcesses = append(filteredProcesses, process)
		}
	}

	return filteredProcesses
}
