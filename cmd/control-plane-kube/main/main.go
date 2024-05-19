// Copyright 2020 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	router "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	controlplane "github.com/carsonoid/talk-half-the-mesh-half-the-mess/cmd/control-plane"
)

var (
	l               controlplane.Logger
	port            uint
	nodeID          string
	kubeconfig      string
	snapshotVersion = 1
)

func init() {
	l = controlplane.Logger{}

	flag.BoolVar(&l.Debug, "debug", false, "Enable xDS server debug logging")
	flag.UintVar(&port, "port", 18000, "xDS management server port")

	// the is the node ID that consumers of the control plane need to use to get the snapshot we generate
	flag.StringVar(&nodeID, "nodeID", "test-id", "Node ID")

	flag.StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig file")
}

func main() {
	flag.Parse()

	if len(flag.Args()) != 0 {
		fmt.Println("Usage: control-plane-kube")
		os.Exit(1)
	}

	cache := cache.NewSnapshotCache(false, cache.IDHash{}, l)

	// sidecar-less grpc configs don't pass snapshot consistency checks
	// so they are disabled here, configs can still work even if they fail the consistency check anyway
	// if err := snapshot.Consistent(); err != nil {
	// 	l.Errorf("snapshot inconsistency: %+v\n%+v", snapshot, err)
	// 	os.Exit(1)
	// }

	go runKubeWatcher(context.Background(), cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cb := &test.Callbacks{Debug: l.Debug}
	srv := server.NewServer(ctx, cache, cb)
	controlplane.RunServer(srv, port)
}

type MeshService struct {
	Name string `json:"name"`
	Port uint32 `json:"port"`
}

func GetServiceResources(meshService MeshService, ips []string) map[resource.Type][]types.Resource {
	// START GET_ENDPOINTS_SLICE OMIT
	var endpoints []*endpoint.LbEndpoint
	for _, ip := range ips {
		ep := &endpoint.LbEndpoint{
			HostIdentifier: &endpoint.LbEndpoint_Endpoint{
				Endpoint: &endpoint.Endpoint{
					Address: &core.Address{
						Address: &core.Address_SocketAddress{
							SocketAddress: &core.SocketAddress{
								Protocol: core.SocketAddress_TCP,
								Address:  ip,
								PortSpecifier: &core.SocketAddress_PortValue{
									PortValue: meshService.Port,
								},
							},
						},
					},
				},
			},
		}
		endpoints = append(endpoints, ep)
	}
	// END GET_ENDPOINTS_SLICE OMIT

	// START CLUSTER_RESOURCES OMIT
	resources := map[resource.Type][]types.Resource{
		resource.ClusterType: {
			&cluster.Cluster{
				Name:                 meshService.Name,
				LbPolicy:             cluster.Cluster_ROUND_ROBIN,
				ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
				EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
					EdsConfig: &core.ConfigSource{
						ConfigSourceSpecifier: &core.ConfigSource_Ads{},
					},
				},
			},
		},
		// END CLUSTER_RESOURCES OMIT

		// normally, you would have a FilterChains and FilterChainMatch here but
		// that is not used for sidecar-less grpc clients, instead we set ApiListener
		// since we don't have them, snapshot consistency checks will fail so we disable them
		// START LISTENER_RESOURCES OMIT
		resource.ListenerType: {
			&listener.Listener{
				Name: meshService.Name,
				ApiListener: &listener.ApiListener{
					ApiListener: mustAnypb(&http_connection_managerv3.HttpConnectionManager{
						CodecType: http_connection_managerv3.HttpConnectionManager_AUTO,
						RouteSpecifier: &http_connection_managerv3.HttpConnectionManager_Rds{
							Rds: &http_connection_managerv3.Rds{
								RouteConfigName: meshService.Name,
								ConfigSource: &core.ConfigSource{
									ResourceApiVersion: core.ApiVersion_V3,
									ConfigSourceSpecifier: &core.ConfigSource_Ads{
										Ads: &core.AggregatedConfigSource{},
									},
								},
							},
						},
						HttpFilters: []*http_connection_managerv3.HttpFilter{{
							Name: wellknown.Router,
							ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{
								TypedConfig: mustAnypb(&router.Router{}),
							},
						}},
					}),
				},
			},
		},
		// END LISTENER_RESOURCES OMIT

		// START ROUTE_RESOURCES OMIT
		resource.RouteType: {
			&route.RouteConfiguration{
				Name:             meshService.Name,
				ValidateClusters: &wrapperspb.BoolValue{Value: true},
				VirtualHosts: []*route.VirtualHost{{
					Name: meshService.Name,
					Domains: []string{
						meshService.Name,                  // used by grpc-go clients <  1.62.0
						url.QueryEscape(meshService.Name), // used by grpc-go clients >= 1.62.0
					},
					Routes: []*route.Route{
						{
							Match: &route.RouteMatch{
								PathSpecifier: &route.RouteMatch_Prefix{
									Prefix: "/",
								},
							},
							Action: &route.Route_Route{
								Route: &route.RouteAction{
									ClusterSpecifier: &route.RouteAction_Cluster{
										Cluster: meshService.Name,
									},
								},
							},
						},
					},
				}},
			},
		},
		// END ROUTE_RESOURCES OMIT

		// START ENDPOINT_RESOURCES OMIT
		resource.EndpointType: {
			&endpoint.ClusterLoadAssignment{
				ClusterName: meshService.Name,
				Endpoints: []*endpoint.LocalityLbEndpoints{
					{
						Locality:            &core.Locality{},
						LoadBalancingWeight: wrapperspb.UInt32(1), // must be > 0 to not be ignored
						LbEndpoints:         endpoints,
					},
				},
			},
		},
		// END ENDPOINT_RESOURCES OMIT
	}

	return resources
}

func mustAnypb(m protoreflect.ProtoMessage) *anypb.Any {
	a, err := anypb.New(m)
	if err != nil {
		panic(err)
	}
	return a
}
