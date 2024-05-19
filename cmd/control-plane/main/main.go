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
	"strconv"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	router "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"

	// START STEP1_IMPORT OMIT
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"

	// END STEP1_IMPORT OMIT

	"github.com/fsnotify/fsnotify"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"sigs.k8s.io/yaml"

	controlplane "github.com/carsonoid/talk-half-the-mesh-half-the-mess/cmd/control-plane"
)

var (
	l               controlplane.Logger
	port            uint
	nodeID          string
	configFilePath  string
	snapshotVersion = 1
)

func init() {
	l = controlplane.Logger{}

	flag.BoolVar(&l.Debug, "debug", false, "Enable xDS server debug logging")
	flag.UintVar(&port, "port", 18000, "xDS management server port")

	// the is the node ID that consumers of the control plane need to use to get the snapshot we generate
	flag.StringVar(&nodeID, "nodeID", "test-id", "Node ID")
}

func main() {
	flag.Parse()

	if len(flag.Args()) != 1 {
		fmt.Println("Usage: control-plane <config file path>")
		os.Exit(1)
	}

	configFilePath = flag.Args()[0]

	// START STEP1 OMIT
	cache := cache.NewSnapshotCache(false, cache.IDHash{}, l)
	// END STEP1 OMIT

	// START STEP3 OMIT
	snapshot, err := GetSnapshot() // calls GetResources()
	if err != nil {
		l.Errorf("snapshot error %q", err)
		os.Exit(1)
	}

	if err := cache.SetSnapshot(context.Background(), nodeID, snapshot); err != nil {
		l.Errorf("snapshot error %q for %+v", err, snapshot)
		os.Exit(1)
	}
	// END STEP3 OMIT

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// START STEP4A OMIT
	go func() {
		err = WatchConfig(ctx, cache)
		if err != nil {
			panic(err)
		}
	}()
	// END STEP4A OMIT

	// START STEP5 OMIT
	srv := server.NewServer(ctx, cache, &test.Callbacks{Debug: l.Debug})
	controlplane.RunServer(srv, port)
	// END STEP5 OMIT
}

// WatchConfig watches the config file for changes and updates the snapshot
func WatchConfig(ctx context.Context, cache cache.SnapshotCache) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(configFilePath)
	if err != nil {
		return err
	}

	// START WATCH_LOOP OMIT
	// ...
	// END WATCH OMIT
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-watcher.Events:
			fmt.Println("event:", event)
			renamed := false
			if event.Op == fsnotify.Remove {
				// we have to remove and re-add the watch because the file was symlinked
				// this is required due to the way k8s configmap mounts work
				// ref: https://www.martensson.io/go-fsnotify-and-kubernetes-configmaps/
				err = watcher.Remove(configFilePath)
				if err != nil {
					return err
				}

				err = watcher.Add(configFilePath)
				if err != nil {
					return err
				}
			}
			// START WATCH_LOOP_2 OMIT
			// ...
			// END WATCH_LOOP OMIT

			// START STEP4B OMIT
			// *not shown: fsnotify file watch handling loop*
			if event.Op == fsnotify.Write || renamed {
				fmt.Println("Config file, updating")
				snapshot, err := GetSnapshot()
				if err != nil {
					return err
				}

				if err := cache.SetSnapshot(ctx, nodeID, snapshot); err != nil {
					return err
				}
			}
			// END STEP4B OMIT
		case err := <-watcher.Errors:
			return err
		}
		// END WATCH_LOOP_2 OMIT
	}
}

func GetSnapshot() (*cache.Snapshot, error) {
	resources, err := GetResources()
	if err != nil {
		return nil, err
	}

	// use the k8s yaml package to marshal the resources into a yaml string
	// because it respects the json struct tags in all the envoy proto messages
	// which results in a much more streamlined yaml output
	yamlBytes, err := yaml.Marshal(resources)
	if err != nil {
		return nil, err
	}
	fmt.Println("Resources:\n" + string(yamlBytes))

	snap, err := cache.NewSnapshot(strconv.Itoa(snapshotVersion), resources)
	if err != nil {
		return nil, err
	}
	snapshotVersion++

	return snap, nil
}

type Config struct {
	Services []MeshService `json:"services"`
}

type MeshService struct {
	Name string
	Host string
	Port uint32
}

// START GET_RESOURCES OMIT
func GetResources() (map[resource.Type][]types.Resource, error) {
	configBytes, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		return nil, err
	}

	resources := make(map[resource.Type][]types.Resource)
	for _, service := range config.Services {
		serviceResources := GetServiceResources(service)
		for k, v := range serviceResources {
			resources[k] = append(resources[k], v...)
		}
	}

	return resources, nil
}

// END GET_RESOURCES OMIT

// START SERVICE_RESOURCES OMIT
func GetServiceResources(service MeshService) map[resource.Type][]types.Resource {
	return map[resource.Type][]types.Resource{
		resource.ClusterType: {
			&cluster.Cluster{
				Name:                 service.Name,
				ConnectTimeout:       durationpb.New(5 * time.Second),
				ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_LOGICAL_DNS},
				LbPolicy:             cluster.Cluster_ROUND_ROBIN,
				DnsLookupFamily:      cluster.Cluster_V4_ONLY,
				LoadAssignment: &endpoint.ClusterLoadAssignment{
					ClusterName: service.Name,
					Endpoints: []*endpoint.LocalityLbEndpoints{{
						LbEndpoints: []*endpoint.LbEndpoint{{
							HostIdentifier: &endpoint.LbEndpoint_Endpoint{
								Endpoint: &endpoint.Endpoint{
									Address: &core.Address{
										Address: &core.Address_SocketAddress{
											SocketAddress: &core.SocketAddress{
												Protocol: core.SocketAddress_TCP,
												Address:  service.Host,
												PortSpecifier: &core.SocketAddress_PortValue{
													PortValue: uint32(service.Port),
												},
											},
										},
									},
								},
							},
						}},
					}},
				},
			},
		},

		// normally, you would have a FilterChains and FilterChainMatch in listeners but
		// that is not used for sidecar-less grpc clients, instead we set ApiListener
		// since we don't have them, snapshot consistency checks will fail so we disable them
		// START LISTENER_RESOURCES OMIT
		resource.ListenerType: {
			&listener.Listener{
				Name: service.Name,
				ApiListener: &listener.ApiListener{
					ApiListener: mustAnypb(&http_connection_managerv3.HttpConnectionManager{
						CodecType: http_connection_managerv3.HttpConnectionManager_AUTO,
						RouteSpecifier: &http_connection_managerv3.HttpConnectionManager_Rds{
							Rds: &http_connection_managerv3.Rds{
								RouteConfigName: service.Name,
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
				Name:             service.Name,
				ValidateClusters: &wrapperspb.BoolValue{Value: true},
				VirtualHosts: []*route.VirtualHost{{
					Name: service.Name,
					Domains: []string{ // xds:///<domain>
						service.Name,                  // used by grpc-go clients <  1.62.0
						url.QueryEscape(service.Name), // used by grpc-go clients >= 1.62.0
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
										Cluster: service.Name,
									},
								},
							},
						},
					},
				}},
			},
		},
		// END ROUTE_RESOURCES OMIT
	}
}

// END SERVICE_RESOURCES OMIT

func mustAnypb(m protoreflect.ProtoMessage) *anypb.Any {
	a, err := anypb.New(m)
	if err != nil {
		panic(err)
	}
	return a
}
