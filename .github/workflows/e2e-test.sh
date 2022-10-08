#! /bin/bash

# Copyright 2022 The OpenFunction Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

function verify_bindings_http() {
  kubectl apply -f test/http/bindings/manifests.yaml

  data_expected="hello: world"
  plugin_expected="sum: 2"
  while /bin/true; do
    data_result=$(kubectl logs --tail=2 -l app="bindings-target" -c target | grep Data | awk '{ print $8 }' | yq -P '.' -)
    plugin_result=$(kubectl logs --tail=2 -l app="bindings-target" -c target | grep plugin | awk '{ print $8 }' | yq -P '.' -)
    if [ "${data_result}" != "${data_expected}" ] || [ "${plugin_result}" != "${plugin_expected}" ]; then
      sleep 1
      continue
    else
      echo "bindings http tested successfully!"
      kubectl delete -f test/http/bindings/manifests.yaml
      break
    fi
  done
}

function verify_pubsub_http() {
  kubectl apply -f test/http/pubsub/manifests.yaml

  data_expected="hello: world"
  plugin_expected="sum: 2"
  while /bin/true; do
    data_result=$(kubectl logs --tail=2 -l app="pubsub-subscriber" -c sub | grep Data | awk '{ print $8 }' | yq -P '.' -)
    plugin_result=$(kubectl logs --tail=2 -l app="pubsub-subscriber" -c sub | grep plugin | awk '{ print $8 }' | yq -P '.' -)
    if [ "${data_result}" != "${data_expected}" ] || [ "${plugin_result}" != "${plugin_expected}" ]; then
      sleep 1
      continue
    else
      echo "bindings http tested successfully!"
      kubectl delete -f test/http/pubsub/manifests.yaml
      break
    fi
  done
}

function verify_bindings_grpc() {
  kubectl apply -f test/grpc/bindings/manifests.yaml

  data_expected="hello: world"
  plugin_expected="sum: 2"
  while /bin/true; do
    data_result=$(kubectl logs --tail=2 -l app="bindings-target" -c target | grep Data | awk '{ print $8 }' | yq -P '.' -)
    plugin_result=$(kubectl logs --tail=2 -l app="bindings-target" -c target | grep plugin | awk '{ print $8 }' | yq -P '.' -)
    if [ "${data_result}" != "${data_expected}" ] || [ "${plugin_result}" != "${plugin_expected}" ]; then
      sleep 1
      continue
    else
      echo "bindings http tested successfully!"
      kubectl delete -f test/grpc/bindings/manifests.yaml
      break
    fi
  done
}

function verify_pubsub_grpc() {
  kubectl apply -f test/grpc/pubsub/manifests.yaml

  data_expected="hello: world"
  plugin_expected="sum: 2"
  while /bin/true; do
    data_result=$(kubectl logs --tail=2 -l app="pubsub-subscriber" -c sub | grep Data | awk '{ print $8 }' | yq -P '.' -)
    plugin_result=$(kubectl logs --tail=2 -l app="pubsub-subscriber" -c sub | grep plugin | awk '{ print $8 }' | yq -P '.' -)
    if [ "${data_result}" != "${data_expected}" ] || [ "${plugin_result}" != "${plugin_expected}" ]; then
      sleep 1
      continue
    else
      echo "bindings http tested successfully!"
      kubectl delete -f test/grpc/pubsub/manifests.yaml
      break
    fi
  done
}

case $1 in

  bindings_http)
    verify_bindings_http
    ;;

  pubsub_http)
    verify_pubsub_http
    ;;

  bindings_grpc)
    verify_bindings_grpc
    ;;

  pubsub_grpc)
    verify_pubsub_grpc
    ;;

esac