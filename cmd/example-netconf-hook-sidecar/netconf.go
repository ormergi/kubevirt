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

package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	vmSchema "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/hooks"
	hooksInfo "kubevirt.io/kubevirt/pkg/hooks/info"
	hooksV1alpha2 "kubevirt.io/kubevirt/pkg/hooks/v1alpha2"
	domainSchema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

const (
	macAddressAnnotation = "netconf.kubevirt.io/macAddress"
	netconfSocket        = "netconf.sock"
)

type infoServer struct {
	Version string
}

func (s infoServer) Info(_ context.Context, _ *hooksInfo.InfoParams) (*hooksInfo.InfoResult, error) {
	log.Log.Info("Hook's Info method has been called")

	return &hooksInfo.InfoResult{
		Name: "mac-address",
		Versions: []string{
			s.Version,
		},
		HookPoints: []*hooksInfo.HookPoint{
			{
				Name:     hooksInfo.OnDefineDomainHookPointName,
				Priority: 0,
			},
		},
	}, nil
}

type v1alpha2Server struct{}

func (s v1alpha2Server) PreCloudInitIso(_ context.Context, _ *hooksV1alpha2.PreCloudInitIsoParams) (*hooksV1alpha2.PreCloudInitIsoResult, error) {
	log.Log.Info("Hook's PreCloudInitIso method has been called")

	return &hooksV1alpha2.PreCloudInitIsoResult{}, nil
}

func (s v1alpha2Server) OnDefineDomain(ctx context.Context, params *hooksV1alpha2.OnDefineDomainParams) (*hooksV1alpha2.OnDefineDomainResult, error) {
	log.Log.Info("Hook's OnDefineDomain callback method has been called")
	newDomainXML, err := onDefineDomain(params.GetVmi(), params.GetDomainXML())
	if err != nil {
		return nil, err
	}
	return &hooksV1alpha2.OnDefineDomainResult{
		DomainXML: newDomainXML,
	}, nil
}

func onDefineDomain(vmiJSON []byte, domainXML []byte) ([]byte, error) {
	vmi := vmSchema.VirtualMachineInstance{}
	err := json.Unmarshal(vmiJSON, &vmi)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given VMI spec: %s", vmiJSON)
		panic(err)
	}

	annotations := vmi.GetAnnotations()

	macAddressRequestJSON, found := annotations[macAddressAnnotation]
	if !found {
		log.Log.Info("netconf hook sidecar requested, but no attributes provided. Returning original domain spec")
		return domainXML, nil
	}

	var ifaceNetConf []struct {
		Name       string `json:"name"`
		MacAddress string `json:"macAddress"`
	}
	if err := json.Unmarshal([]byte(macAddressRequestJSON), &ifaceNetConf); err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal annotation %q: %v", macAddressRequestJSON, err)
		panic(err)
	}

	domainSpec := domainSchema.DomainSpec{}
	err = xml.Unmarshal(domainXML, &domainSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given domain spec: %v", domainXML)
		panic(err)
	}

	for _, netconf := range ifaceNetConf {
		for i := range domainSpec.Devices.Interfaces {
			log.Log.Infof("iface: %+v", domainSpec.Devices.Interfaces[i])
			if domainSpec.Devices.Interfaces[i].Alias != nil &&
				domainSpec.Devices.Interfaces[i].Alias.GetName() == netconf.Name {
				log.Log.Infof("setting VMI '%s/%s' interface %q with MAC address %q",
					vmi.Namespace, vmi.Name, netconf.Name, netconf.MacAddress)
				domainSpec.Devices.Interfaces[i].MAC = &domainSchema.MAC{MAC: netconf.MacAddress}
			}
		}
	}

	updatedDomainSpec, err := xml.Marshal(domainSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to marshal updated domain spec: %+v", domainSpec)
		panic(err)
	}

	log.Log.Info("Successfully updated interfaces with desired settings")

	return updatedDomainSpec, nil
}

func main() {
	log.InitializeLogging("netconf-hook-sidecar")

	socketPath := filepath.Join(hooks.HookSocketsSharedDirectory, netconfSocket)
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to initialized socket on path: %q", socket)
		log.Log.Error("Check whether given directory exists and socket name is not already taken by other file")
		panic(err)
	}
	defer os.Remove(socketPath)

	server := grpc.NewServer([]grpc.ServerOption{}...)
	hooksInfo.RegisterInfoServer(server, infoServer{Version: "v1alpha2"})
	hooksV1alpha2.RegisterCallbacksServer(server, v1alpha2Server{})

	log.Log.Infof("Starting hook server exposing 'info' and 'v1alpha2' services on socket %q", socketPath)
	server.Serve(socket)
}
