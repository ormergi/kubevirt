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

package exec_pool

import (
	"math"
	"time"

	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"

	"kubevirt.io/client-go/log"
)

const (
	DefaultInitialBackoff = 3 * time.Second
	DefaultMaxBackoff     = 10 * time.Minute
	DefaultLimit          = 5 * time.Hour
	DefaultFactor         = 1.8
	DefaultJitter         = 0
)

type limitedBackoff struct {
	backoff      wait.Backoff
	limit        time.Duration
	clock        clock.Clock
	backoffLimit time.Time
	stepEnd      time.Time
}

func (l *limitedBackoff) Ready() bool {
	now := l.clock.Now()

	if now.Before(l.stepEnd) {
		log.Log.Infof("DEBUG: limitedBackoff: Ready: backoff step end time not passed yet: (%v) now: (%v)",
			l.stepEnd.Format(time.Stamp), now.Format(time.Stamp))
		return false
	}

	if now.After(l.backoffLimit) {
		log.Log.Infof("DEBUG: limitedBackoff: Ready: backoff limit time reached: (%v) now: (%v)",
			l.backoffLimit.Format(time.Stamp), now.Format(time.Stamp))
		return false
	}

	return true
}

func (l *limitedBackoff) Step() {
	if !l.Ready() {
		log.Log.Info("DEBUG: limitedBackoff: Ready returned FALSE")
		return
	}
	log.Log.Info("DEBUG: limitedBackoff: Ready returned TRUE")

	beforeStepEnd := l.stepEnd
	now := l.clock.Now()
	l.stepEnd = now.Add(l.backoff.Step())
	log.Log.Infof("DEBUG: limitedBackoff: Step: new step end: (%v), now: (%v), previous: (%v)",
		l.stepEnd.Format(time.Stamp), now.Format(time.Stamp), beforeStepEnd.Format(time.Stamp))
}

func (l *limitedBackoff) StepEnd() time.Time {
	return l.stepEnd
}

func (l *limitedBackoff) init() {
	now := l.clock.Now()
	l.stepEnd = now
	l.backoffLimit = now.Add(l.limit)
}

func newLimitedBackoffWithClock(backoff wait.Backoff, backoffLimit time.Duration, clk clock.Clock) *limitedBackoff {
	instance := &limitedBackoff{
		backoff: backoff,
		limit:   backoffLimit,
		clock:   clk,
	}
	instance.init()
	return instance
}

func NewFixedExponentialLimitedBackoffWithClock(clk clock.Clock) *limitedBackoff {
	backoff := wait.Backoff{
		Duration: DefaultInitialBackoff,
		Cap:      DefaultMaxBackoff,
		// the underlying wait.Backoff will stop to calculate the next duration once Steps reaches zero,
		// thus for an exponential rateLimiter, Steps should approach infinity.
		Steps:  math.MaxInt64,
		Factor: DefaultFactor,
		Jitter: DefaultJitter,
	}
	return newLimitedBackoffWithClock(backoff, DefaultLimit, clk)
}

type limitedBackoffCreator struct {
	template limitedBackoff
}

func newLimitedBackoffCreator(backoff limitedBackoff) *limitedBackoffCreator {
	return &limitedBackoffCreator{template: backoff}
}

func newFixedExponentialLimitedBackoffCreatorWithClock(clk clock.Clock) *limitedBackoffCreator {
	return newLimitedBackoffCreator(
		*NewFixedExponentialLimitedBackoffWithClock(clk),
	)
}

func NewFixedExponentialLimitedBackoffCreator() *limitedBackoffCreator {
	return newFixedExponentialLimitedBackoffCreatorWithClock(clock.RealClock{})
}

func (l *limitedBackoffCreator) New() limitedBackoff {
	instance := limitedBackoff{
		backoff: l.template.backoff,
		limit:   l.template.limit,
		clock:   l.template.clock,
	}
	instance.init()
	return instance
}
