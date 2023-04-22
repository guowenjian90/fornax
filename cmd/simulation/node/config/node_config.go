/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package config

import (
	"fmt"
	"os"

	nconfig "centaurusinfra.io/fornax-serverless/pkg/nodeagent/config"
	"centaurusinfra.io/fornax-serverless/pkg/nodeagent/network"
	"github.com/spf13/pflag"
)

type SimulationNodeConfiguration struct {
	NodeConfig     nconfig.NodeConfiguration
	NodeIP         string
	FornaxCoreUrls []string
	NumOfNode      int
	PodConcurrency int
	NodeNamePrefix string
}

func AddConfigFlags(flagSet *pflag.FlagSet, nodeConfig *SimulationNodeConfiguration) {
	flagSet.StringVar(&nodeConfig.NodeIP, "node-ip", nodeConfig.NodeIP, "IPv4 addresses of the node. If unset, use the node's default IPv4 address")

	flagSet.StringVar(&nodeConfig.NodeNamePrefix, "node-name-prefix", nodeConfig.NodeNamePrefix, "Simulation node's name prefix")

	flagSet.StringArrayVar(&nodeConfig.FornaxCoreUrls, "fornaxcore-ip", nodeConfig.FornaxCoreUrls, "IPv4 addresses of the fornaxcores. must provided")

	flagSet.IntVar(&nodeConfig.NumOfNode, "num-of-node", nodeConfig.NumOfNode, "How many nodes are simulated")

	flagSet.IntVar(&nodeConfig.PodConcurrency, "concurrency-of-pod-operation", nodeConfig.PodConcurrency, "How many pods are allowed to create or terminated in parallel")
}

func DefaultNodeConfiguration() (*SimulationNodeConfiguration, error) {
	ips, err := network.GetLocalV4IP()
	if err != nil {
		return nil, err
	}
	nodeIp := ips[0].To4().String()
	nodeConfig, _ := nconfig.DefaultNodeConfiguration()
	namePrefix, _ := os.Hostname()

	return &SimulationNodeConfiguration{
		NodeConfig:     *nodeConfig,
		NodeIP:         nodeIp,
		FornaxCoreUrls: []string{fmt.Sprintf("%s:18001", nodeIp)},
		NumOfNode:      1,
		PodConcurrency: 5,
		NodeNamePrefix: namePrefix,
	}, nil
}

func ValidateNodeConfiguration(*SimulationNodeConfiguration) []error {
	return nil
}

// // ReservedMemoryVar is used for validating a command line option that represents a reserved memory. It implements the pflag.Value interface
// type ReservedMemoryVar struct {
//   Value       *[]kubeletconfig.MemoryReservation
//   initialized bool // set to true after the first Set call
// }
//
// // Set sets the flag value
// func (v *ReservedMemoryVar) Set(s string) error {
//   if v.Value == nil {
//     return fmt.Errorf("no target (nil pointer to *[]MemoryReservation")
//   }
//
//   if s == "" {
//     v.Value = nil
//     return nil
//   }
//
//   if !v.initialized || *v.Value == nil {
//     *v.Value = make([]kubeletconfig.MemoryReservation, 0)
//     v.initialized = true
//   }
//
//   if s == "" {
//     return nil
//   }
//
//   numaNodeReservations := strings.Split(s, ";")
//   for _, reservation := range numaNodeReservations {
//     numaNodeReservation := strings.Split(reservation, ":")
//     if len(numaNodeReservation) != 2 {
//       return fmt.Errorf("the reserved memory has incorrect format, expected numaNodeID:type=quantity[,type=quantity...], got %s", reservation)
//     }
//     memoryTypeReservations := strings.Split(numaNodeReservation[1], ",")
//     if len(memoryTypeReservations) < 1 {
//       return fmt.Errorf("the reserved memory has incorrect format, expected numaNodeID:type=quantity[,type=quantity...], got %s", reservation)
//     }
//     numaNodeID, err := strconv.Atoi(numaNodeReservation[0])
//     if err != nil {
//       return fmt.Errorf("failed to convert the NUMA node ID, exptected integer, got %s", numaNodeReservation[0])
//     }
//
//     memoryReservation := kubeletconfig.MemoryReservation{
//       NumaNode: int32(numaNodeID),
//       Limits:   map[v1.ResourceName]resource.Quantity{},
//     }
//
//     for _, memoryTypeReservation := range memoryTypeReservations {
//       limit := strings.Split(memoryTypeReservation, "=")
//       if len(limit) != 2 {
//         return fmt.Errorf("the reserved limit has incorrect value, expected type=quantatity, got %s", memoryTypeReservation)
