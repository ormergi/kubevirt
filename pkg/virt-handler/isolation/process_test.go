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

import (
	"github.com/mitchellh/go-ps"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter process", func() {
	table.DescribeTable("should return the correct processes set comparing by PPID",
		func(processes []ps.Process, ppids []int, expectedProcesses []ps.Process) {
			Expect(filterProcessByPPID(processes, ppids)).
				To(ConsistOf(expectedProcesses))
		},
		table.Entry("empty set, zero elements to filter",
			[]ps.Process{},
			[]int{},
			// expected
			[]ps.Process{},
		),
		table.Entry("valid set, zero elements to filter",
			newPsGoProcess([]ProcessStub{
				{ppid: 1, pid: 120, binary: "processA"},
				{ppid: 1, pid: 110, binary: "processC"},
				{ppid: 110, pid: 2222, binary: "processB"},
				{ppid: 110, pid: 3333, binary: "processD"},
			}),
			[]int{},
			// expected
			[]ps.Process{},
		),
		table.Entry("empty set, at least one element to filter",
			[]ps.Process{},
			[]int{10, 110},
			// expected
			[]ps.Process{},
		),
		table.Entry("valid set, at least one element to filter",
			newPsGoProcess([]ProcessStub{
				{ppid: 1, pid: 120, binary: "processA"},
				{ppid: 2, pid: 110, binary: "processD"},
				{ppid: 110, pid: 2222, binary: "processB"},
				{ppid: 110, pid: 3333, binary: "processC"},
			}),
			[]int{10, 110, 3},
			// expected
			newPsGoProcess([]ProcessStub{
				{ppid: 110, pid: 2222, binary: "processB"},
				{ppid: 110, pid: 3333, binary: "processC"},
			}),
		),
	)

	table.DescribeTable("should return the correct processes set comparing by Executable",
		func(processes []ps.Process, executable string, expectedProcesses []ps.Process) {
			Expect(filterProcessByExecutable(processes, executable)).
				To(ConsistOf(expectedProcesses))
		},
		table.Entry("empty set, zero elements to filter",
			[]ps.Process{},
			"",
			// expected
			[]ps.Process{},
		),
		table.Entry("empty set, one element to filter",
			[]ps.Process{},
			"processA",
			// expected
			[]ps.Process{},
		),
		table.Entry("valid set, zero elements to filter",
			newPsGoProcess([]ProcessStub{
				{ppid: 1, pid: 120, binary: "processA"},
				{ppid: 1, pid: 110, binary: "processD"},
				{ppid: 110, pid: 2222, binary: "processB"},
				{ppid: 110, pid: 3333, binary: "processC"},
			}),
			"",
			// expected
			[]ps.Process{},
		),
		table.Entry("valid set, one element to filter",
			newPsGoProcess([]ProcessStub{
				{ppid: 1, pid: 120, binary: "processA"},
				{ppid: 2, pid: 110, binary: "processC"},
				{ppid: 110, pid: 2222, binary: "processB"},
				{ppid: 110, pid: 3333, binary: "processD"},
			}),
			"processA",
			// expected
			newPsGoProcess([]ProcessStub{
				{ppid: 1, pid: 120, binary: "processA"},
			}),
		),
	)
})

func newPsGoProcess(processes []ProcessStub) []ps.Process {
	result := []ps.Process{}
	for idx := range processes {
		result = append(result, &processes[idx])
	}

	return result
}

type ProcessStub struct {
	ppid   int
	pid    int
	binary string
}

func (p *ProcessStub) Pid() int {
	return p.pid
}

func (p *ProcessStub) PPid() int {
	return p.ppid
}

func (p *ProcessStub) Executable() string {
	return p.binary
}
