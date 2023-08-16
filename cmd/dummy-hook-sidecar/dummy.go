/*
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
 * Copyright 2023 StackPath, LLC
 *
 */

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"kubevirt.io/client-go/log"

	hooks "kubevirt.io/kubevirt/pkg/hooks"
	hooksInfo "kubevirt.io/kubevirt/pkg/hooks/info"
	hooksV1alpha2 "kubevirt.io/kubevirt/pkg/hooks/v1alpha2"
)

type infoServer struct{}

func (s infoServer) Info(_ context.Context, _ *hooksInfo.InfoParams) (*hooksInfo.InfoResult, error) {
	log.Log.Info("Info method has been called")

	return &hooksInfo.InfoResult{
		Name: "dummy",
		Versions: []string{
			hooksV1alpha2.Version,
		},
		HookPoints: []*hooksInfo.HookPoint{
			{
				Name:     hooksInfo.PreCloudInitIsoHookPointName,
				Priority: 0,
			},
			{
				Name:     hooksInfo.OnDefineDomainHookPointName,
				Priority: 0,
			},
		},
	}, nil
}

type v1alpha2Server struct{}

func (s v1alpha2Server) OnDefineDomain(_ context.Context, params *hooksV1alpha2.OnDefineDomainParams) (*hooksV1alpha2.OnDefineDomainResult, error) {
	log.Log.Warning("OnDefineDomain callback method has been called")

	return &hooksV1alpha2.OnDefineDomainResult{
		DomainXML: params.GetDomainXML(),
	}, nil
}

func (s v1alpha2Server) PreCloudInitIso(_ context.Context, params *hooksV1alpha2.PreCloudInitIsoParams) (*hooksV1alpha2.PreCloudInitIsoResult, error) {
	log.Log.Info("PreCloudInitIso callback method has been called")

	return &hooksV1alpha2.PreCloudInitIsoResult{
		CloudInitData:          params.GetCloudInitData(),
		CloudInitNoCloudSource: params.GetCloudInitNoCloudSource(),
	}, nil
}

func main() {
	log.InitializeLogging("dummy-hook-sidecar")

	socketPath := filepath.Join(hooks.HookSocketsSharedDirectory, "dummy.sock")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to initialized socket on path: %s", socket)
		log.Log.Error("Check whether given directory exists and socket name is not already taken by other file")
		panic(err)
	}
	defer os.Remove(socketPath)

	server := grpc.NewServer([]grpc.ServerOption{}...)
	hooksInfo.RegisterInfoServer(server, infoServer{})
	hooksV1alpha2.RegisterCallbacksServer(server, v1alpha2Server{})
	log.Log.Infof("Starting hook server exposing 'info' and 'v1alpha2' services on socket %s", socketPath)
	server.Serve(socket)
}
