#!/usr/bin/env bash
#
# This script should be executed through the makefile via `make conformance`.

set -eExuo pipefail

kubectl="./cluster-up/kubectl.sh"

function wait_for_sonobuoy_job_to_start() {
  local -r sonobuoy_job_label="sonobuoy-plugin=kubevirt-conformance"
  local -r ns="sonobuoy"
  local pod=""
  ! timeout 1m bash -ex -c "until ${kubectl} get po -n ${ns} -l ${sonobuoy_job_label} --no-headers | grep .; do sleep 5; done" && \
    echo "FATAL: sonobouy job did not start on time" >&2 && return 1
  ${kubectl} wait --timeout="10m" --for="condition=Ready" -n ${ns} -l ${sonobuoy_job_label} pods
}

function follow_sonobouy_job_logs() {
  local -r sonobuoy_job_pod=$(${kubectl} get po -n sonobuoy -l sonobuoy-plugin=kubevirt-conformance --no-headers  | awk '{print $1}')
  ${kubectl} logs -n sonobuoy "${sonobuoy_job_pod}" -c plugin -f
}

echo 'Preparing directory for artifacts'
export ARTIFACTS=_out/artifacts/conformance
mkdir -p ${ARTIFACTS}

echo 'Obtaining KUBECONFIG of the development cluster'
export KUBECONFIG=$(./cluster-up/kubeconfig.sh)

sonobuoy_args="--wait --plugin _out/manifests/release/conformance.yaml"

if [[ ! -z "$DOCKER_PREFIX" ]]; then
    sonobuoy_args="${sonobuoy_args} --plugin-env kubevirt-conformance.CONTAINER_PREFIX=${DOCKER_PREFIX}"
fi

if [[ ! -z "$DOCKER_TAG" ]]; then
    sonobuoy_args="${sonobuoy_args} --plugin-env kubevirt-conformance.CONTAINER_TAG=${DOCKER_TAG}"
fi

if [[ ! -z "$KUBEVIRT_E2E_FOCUS" ]]; then
    sonobuoy_args="${sonobuoy_args} --plugin-env kubevirt-conformance.E2E_FOCUS=${KUBEVIRT_E2E_FOCUS}"
fi

if [[ ! -z "$SKIP_OUTSIDE_CONN_TESTS" ]]; then
    sonobuoy_args="${sonobuoy_args} --plugin-env kubevirt-conformance.E2E_SKIP=\[outside_connectivity\]"
fi

echo 'Executing conformance tests and wait for them to finish'
sonobuoy run ${sonobuoy_args} &
sonobuoy_exec_pid="$!"

wait_for_sonobuoy_job_to_start
follow_sonobouy_job_logs 2>&1 | tee "${ARTIFACTS}/kubevirt_tests_suite.log" &
sonobuoy_job_log_pid="$!"

wait $sonobuoy_exec_pid
wait $sonobuoy_job_log_pid

trap "{ echo 'Cleaning up after the test execution'; sonobuoy delete --wait; }" EXIT SIGINT SIGTERM SIGQUIT

echo 'Downloading report about the test execution'
results_archive=${ARTIFACTS}/$(cd ${ARTIFACTS} && sonobuoy retrieve)

echo 'Results:'
sonobuoy results ${results_archive}

echo "Extracting the full report and keep it under ${ARTIFACTS}"
tar xf ${results_archive} -C ${ARTIFACTS}
cp ${ARTIFACTS}/plugins/kubevirt-conformance/results/global/junit.xml ${ARTIFACTS}/junit.conformance.xml

echo 'Evaluating success of the test run'
sonobuoy results ${results_archive} | grep -q 'Status: passed' || {
    echo 'Conformance suite has failed'
    exit 1
}
echo 'Conformance suite has successfully completed'
