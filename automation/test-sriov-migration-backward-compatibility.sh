#!/bin/bash

set -ex

readonly SCRIPT_PATH=$(dirname "$(realpath "$0")")
readonly KUBEVIRT_PATH="$(realpath ${SCRIPT_PATH}/..)"
readonly CLUSTER_UP="${KUBEVIRT_PATH}/cluster-up"

NAMESPACE="${NAMESPACE:-kubevirt}"

function kubectl() {
    ${CLUSTER_UP}/kubectl.sh "$@"
}

readonly PARSE_SHASUMS_SCRIPT="${KUBEVIRT_PATH}/hack/parse-shasums.sh"

readonly KUBEVIRT_COMPONENTS=("virt-operator" "virt-api" "virt-controller" "virt-handler" "virt-launcher")

function print_local_kubevirt_components_images(){
    set +x; source $PARSE_SHASUMS_SCRIPT; set -x;

    local env_var_name
    local env_var_value
    for component in "${KUBEVIRT_COMPONENTS[@]}"; do
        env_var_name="${component^^}_SHA"
        env_var_name="${env_var_name//-/_}"
        env_var_value="$(eval echo \$$env_var_name)"
        echo "$component: $env_var_value"
    done
}

function print_deployed_kubevirt_components_images() {
    local -r label=$1

    label_arg="-l $label"
    if [ "$label" == "" ]; then
        label_arg=""
    fi

    for node in $(kubectl get no --no-headers | awk '{print $1'}); do
        for pod in $(kubectl get po -n $NAMESPACE $label_arg -o wide --no-headers | grep -w $node | awk '{print $1'}); do
            image=$(kubectl get pod $pod -n $NAMESPACE -o yaml | grep -Po ".*image:.*\Kvirt-.*")
            component=$(echo $image | grep -Po ".*(?=@)")
            hash=$(echo $image | grep -Po ".*@\K.*")
            echo "$node:
    $component: $hash"
        done
    done
}

readonly MANAGED_BY_LABEL="app.kubernetes.io/managed-by"
readonly REMOVE_TEMPLATE_LABEL_MANAGED_BY_PATCH="{\"spec\":{\"template\":{\"metadata\":{\"labels\":{\"${MANAGED_BY_LABEL}\":\"\"}}}}}"

function patch_virt_handler_pod_image_on_node(){
    local -r target_node=$1
    local -r virt_handler_sha=$2

    echo "prevent virt-operator to roll-back virt-handler pods state by removing managed-by label"
    
    kubectl label -n kubevirt  ds/virt-handler "$MANAGED_BY_LABEL"-
    kubectl patch -n kubevirt  ds/virt-handler -p "$REMOVE_TEMPLATE_LABEL_MANAGED_BY_PATCH"

    kubectl rollout status ds -n $NAMESPACE virt-handler --timeout=300s

    echo "patch migration target virt-handler pod image to new image built from the feature branch"

    local -r virt_handler_pod=$(kubectl get po -n $NAMESPACE -l kubevirt.io=virt-handler -o wide | grep -w $target_node | awk '{print $1'})
    
    local -r new_virt_handler_image="registry:5000/kubevirt/virt-handler@${virt_handler_sha}"
    local -r patch_replace_template_containers_image="[{\"op\": \"replace\", \"path\": \"/spec/containers/0/image\", \"value\": ${new_virt_handler_image}}]"
    kubectl patch pod -n $NAMESPACE $virt_handler_pod --type json -p="${patch_replace_template_containers_image}"

    until [ "$(kubectl get po -n $NAMESPACE $virt_handler_pod -o jsonpath='{.spec.containers}' | jq .[0].image -r)" == "$new_virt_handler_image" ]; do 
        echo "wait for $virt_handler_pod image to update..."; sleep 1; 
    done

    until [ "$(kubectl get po -n $NAMESPACE $virt_handler_pod -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" != "True" ]; do 
        echo "wait for $virt_handler_pod to restart..."; sleep 1; 
    done

    kubectl wait pod/$virt_handler_pod -n $NAMESPACE --for=condition=ready --timeout=300s
}

function patch_virt_controlers_command_launcher_image() {
    local -r virt_launcher_sha=$1

    echo "prevent virt-operator to roll-back virt-controler Deployment by removing managed-by label"
    
    kubectl label -n kubevirt  deploy/virt-controller "$MANAGED_BY_LABEL"-
    kubectl patch -n kubevirt deploy/virt-controller -p "$REMOVE_TEMPLATE_LABEL_MANAGED_BY_PATCH"

    kubectl rollout status deployment -n $NAMESPACE virt-controller --timeout=240s

    echo "patch virt-controller Deployment pod template command, pass the new virt-launcher image to --launcher-image arg
    This will make virt-controller create virt-launcher pods with the new image"

    local -r new_virt_launcher_sha="${virt_launcher_sha}"
    local -r cmd="[\"virt-controller\", \"--launcher-image\", \"registry:5000/kubevirt/virt-launcher@${new_virt_launcher_sha}\", \"--port\", \"8443\", \"-v\", \"2\" ]"
    local -r patch_replace_template_command="[{\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/command\", \"value\": ${cmd}}]"
    kubectl patch -n $NAMESPACE deploy/virt-controller --type json -p="${patch_replace_template_command}"

    kubectl rollout status deployment -n $NAMESPACE virt-controller --timeout=300s
    kubectl wait --for=condition=ready pod -n $NAMESPACE -l kubevirt.io=virt-controller --timeout=300s
}

readonly SRIOV_MIGRATION_TEST_LABEL="sriov-migration"

function run_sriov_migration_test() {
   KUBEVIRT_MIGRATION_TARGET_NODE=$1 ARTIFACTS=$2 KUBEVIRT_E2E_FOCUS="$SRIOV_MIGRATION_TEST_LABEL" make functest
}

export WORKSPACE="${WORKSPACE:-$PWD}"
readonly start_timestamp=$(date +%d-%m-%Y_%H-%M-%S)
readonly ARTIFACTS_DIR="${ARTIFACTS_DIR:-$WORKSPACE/exported-artifacts/${start_timestamp}}"
mkdir -p "${ARTIFACTS_DIR}"

export KUBEVIRT_PROVIDER="kind-1.22-sriov"
export KUBEVIRT_NUM_NODES=3 
readonly NEW_VIRT_HANDLER_NODE="sriov-worker"
readonly OLD_VIRT_HANDLER_NODE="sriov-worker2"

MAIN_BRANCH="${MAIN_BRANCH:-6c3b8db8194c1717d72ea2215e1984a7c785f83d}"
FEATURE_BRANCH="${FEATURE_BRANCH:-hotplug-sriov-on-reconciler}"
TEST_BRANCH="${TEST_BRANCH:-test-sriov-migration-backward-compatibility}"

trap "git checkout . && git checkout $TEST_BRANCH" EXIT

echo "spin up Kubevirtci SR-IOV cluster with two workers and deploy Kubevirt built from master"

git checkout "$MAIN_BRANCH"
make cluster-up cluster-sync
export KUBECONFIG=$($CLUSTER_UP/kubeconfig.sh)

echo "save initial config of Kubevirt operator and CR"

cp "${KUBEVIRT_PATH}/_out/manifests/release/kubevirt-operator.yaml" "${ARTIFACTS_DIR}/kubevirt-operator.backup.yaml"
cp "${KUBEVIRT_PATH}/_out/manifests/release/kubevirt-cr.yaml"       "${ARTIFACTS_DIR}/kubevirt-cr.backup.yaml"

echo "build new images from feature branch"

git checkout "$FEATURE_BRANCH"
PUSH_TARGETS='virt-handler virt-launcher' make bazel-push-images

print_local_kubevirt_components_images
print_deployed_kubevirt_components_images

## patch virt-handler DaemonSet

set +x; source $PARSE_SHASUMS_SCRIPT; set -x;

patch_virt_handler_pod_image_on_node  "$NEW_VIRT_HANDLER_NODE" "$VIRT_HANDLER_SHA"

print_local_kubevirt_components_images
print_deployed_kubevirt_components_images "kubevirt.io=virt-handler"

## The node $NEW_VIRT_HANDLER_NODE runs new virt-handler pod built from the feautre branch.
## All other pods, including virt-controller, running the old images built from main branch.
## This state emulates Kubevirt upgrade where virt-controller isnt updated yet.

git checkout $TEST_BRANCH

echo "[TEST] old virt-launcher target pod runs on a node with old virt-handler"
run_sriov_migration_test "$OLD_VIRT_HANDLER_NODE" "${ARTIFACTS_DIR}/old-virt-launcher-old-virt-handler" || true

echo "[TEST] old virt-launcher target pod runs on a node with new virt-handler"
run_sriov_migration_test "$NEW_VIRT_HANDLER_NODE" "${ARTIFACTS_DIR}/old-virt-launcher-new-virt-handler" || true

## patch virt-controller Deployment

patch_virt_controlers_command_launcher_image "$VIRT_LAUNCHER_SHA" 

print_local_kubevirt_components_images
print_deployed_kubevirt_components_images

## virt-controller will create virt-launcher pods with new image that built from the feautre branch.
## virt-handler on $NEW_VIRT_HANDLER_NODE node runs new image that built from the feature branch as well.
## This state emulates Kubevirt upgrade where one of the nodes still runs old virt-handler image.

echo "[TEST] new virt-launcher target pod runs on a node with new virt-handler"
run_sriov_migration_test "$NEW_VIRT_HANDLER_NODE" "${ARTIFACTS_DIR}/new-virt-launcher-new-virt-handler" || true

echo "[TEST] new virt-launcher target pod runs on a node with old virt-handler"
run_sriov_migration_test "$OLD_VIRT_HANDLER_NODE" "${ARTIFACTS_DIR}/new-virt-launcher-old-virt-handler" || true

echo "start time:   $start_timestamp"
echo "end time:     $(date +%d-%m-%Y_%H-%M-%S)"
