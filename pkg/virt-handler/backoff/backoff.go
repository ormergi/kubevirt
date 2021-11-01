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
 * Copyright 2021 Red Hat, Inc.
 *
 */

package backoff

import (
	"fmt"
	"time"
)

type BackoffExecutor struct {
	command              func() error
	schedule             []time.Duration
	retries              int
	trial                int
	lastError            error
	executedSuccessfully bool
	nextExecTimestamp    time.Time
}

func NewBackoffExecutor(fn func() error, backoffSchedule []time.Duration) *BackoffExecutor {
	backoff := BackoffExecutor{
		command:              fn,
		schedule:             backoffSchedule,
		retries:              len(backoffSchedule),
		trial:                0,
		lastError:            nil,
		executedSuccessfully: false,
		nextExecTimestamp:    time.Now(),
	}

	return &backoff
}

func (e *BackoffExecutor) Exec() error {
	if e.executedSuccessfully {
		return nil
	}

	if e.trial > e.retries {
		return fmt.Errorf("faild after retried for %v with error: %v", e.schedule, e.lastError)
	}

	if time.Now().After(e.nextExecTimestamp) {
		err := e.command()
		if err != nil {
			e.executedSuccessfully = false
		}
		e.executedSuccessfully = true

		e.lastError = err
		e.trial++

		e.nextExecTimestamp = time.Now().Add(e.schedule[e.trial])
	}

	return nil
}
