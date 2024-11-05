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
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

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
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	kubecache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

func runKubeWatcher(ctx context.Context, cache cache.SnapshotCache) {
	kw := &kubeWatcher{
		cache: cache,
	}

	if err := kw.Start(ctx); err != nil {
		panic(err)
	}

	<-ctx.Done()
}

type kubeWatcher struct {
	cache      cache.SnapshotCache
	svcIndexer kubecache.Indexer
	epIndexer  kubecache.Indexer
}

func (kw *kubeWatcher) Start(ctx context.Context) error {
	updateChan := make(chan struct{}, 1)

	ready := &atomic.Bool{}
	updateFunc := func() {
		if ready.Load() {
			select {
			case updateChan <- struct{}{}:
			default:
			}
		}
	}

	err := kw.startKubeWatchers(ctx, updateFunc)
	if err != nil {
		return err
	}

	// tell the cache we're ready to start updating
	ready.Store(true)

	// update the cache once to get the initial snapshot
	err = kw.updateCache()
	if err != nil {
		return err
	}

	// start a goroutine to listen for updates and update the cache
	go func() {
		defer close(updateChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-updateChan:
				err := kw.updateCache()
				if err != nil {
					fmt.Println("Error updating cache: " + err.Error())
				}
			}
		}
	}()

	return nil
}

// START WATCH OMIT
func (kw *kubeWatcher) startKubeWatchers(ctx context.Context, queueUpdate func()) error {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	handlerFuncs := kubecache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { queueUpdate() },
		UpdateFunc: func(_, _ interface{}) { queueUpdate() },
		DeleteFunc: func(_ interface{}) { queueUpdate() },
	}

	// START SVCWATCH OMIT
	// ...
	// END WATCH OMIT
	svcWatcher := kubecache.NewListWatchFromClient(
		kubeClient.CoreV1().RESTClient(), "services", v1.NamespaceAll, fields.Everything(),
	)
	svcIndexer, svcInformer := kubecache.NewIndexerInformer(
		svcWatcher, &v1.Service{}, time.Hour, handlerFuncs, kubecache.Indexers{},
	)
	go svcInformer.Run(ctx.Done())

	if !kubecache.WaitForCacheSync(ctx.Done(), svcInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	kw.svcIndexer = svcIndexer
	// START EPWATCH OMIT
	// ...
	// END SVCWATCH OMIT
	endpointsWatcher := kubecache.NewListWatchFromClient(
		kubeClient.CoreV1().RESTClient(), "endpoints", v1.NamespaceAll, fields.Everything(),
	)
	epIndexer, endpointsInformer := kubecache.NewIndexerInformer(
		endpointsWatcher, &v1.Endpoints{}, time.Hour, handlerFuncs, kubecache.Indexers{},
	)
	go endpointsInformer.Run(ctx.Done())

	if !kubecache.WaitForCacheSync(ctx.Done(), endpointsInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	kw.epIndexer = epIndexer
	// END EPWATCH OMIT

	return nil
}

// START UPDATE CACHE OMIT
func (kw *kubeWatcher) updateCache() error {
	snap, err := kw.getSnapshot()
	if err != nil {
		return err
	}

	err = kw.cache.SetSnapshot(context.Background(), nodeID, snap)
	if err != nil {
		return err
	}

	return nil
}

// END UPDATE CACHE OMIT

// START GET SNAPSHOT OMIT
func (kw *kubeWatcher) getSnapshot() (*cache.Snapshot, error) {
	resources, err := kw.getResources() // <-- make xds resources
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

// END GET SNAPSHOT OMIT

type MeshServiceConfig struct {
	Provides string `json:"provides"`
	Port     uint32 `json:"port"`
	Weight   uint32 `json:"weight"`
}

type MeshServices map[string]*meshService // key is mesh service name

func (mss MeshServices) Register(svc *v1.Service, cfg MeshServiceConfig, ips []string) {
	if _, exists := mss[cfg.Provides]; !exists {
		mss[cfg.Provides] = &meshService{
			name:      cfg.Provides,
			providers: make(map[string]*meshServiceProvider),
		}
	}

	clusterName := svc.GetNamespace() + "/" + svc.GetName()

	mss[cfg.Provides].providers[clusterName] = &meshServiceProvider{
		cluster: &route.WeightedCluster_ClusterWeight{
			Name:   clusterName,
			Weight: &wrapperspb.UInt32Value{Value: cfg.Weight},
		},
		endpoints: getEndpointsForIPs(cfg, ips),
	}
}

type meshService struct {
	name      string
	providers map[string]*meshServiceProvider
}

type meshServiceProvider struct {
	cluster   *route.WeightedCluster_ClusterWeight
	endpoints *endpoint.LocalityLbEndpoints
}

// START RESOURCES OMIT
func (kw *kubeWatcher) getResources() (map[resource.Type][]types.Resource, error) {
	var meshServices MeshServices = make(map[string]*meshService) // verbose init for clarity
	for _, serviceObj := range kw.svcIndexer.List() {
		service := serviceObj.(*v1.Service)
		meshServiceYaml, exists := service.Annotations["mesh-service"]
		if !exists {
			fmt.Println("No mesh-service annotation found for service: " + service.Name)
			continue // keep adding resources for other services
		}

		var meshConfig MeshServiceConfig
		err := yaml.Unmarshal([]byte(meshServiceYaml), &meshConfig)
		if err != nil {
			fmt.Println("Error unmarshalling mesh-service annotation: " + err.Error())
			continue
		}

		ips, err := kw.getIPsForService(service.Namespace, service.Name)
		if err != nil {
			fmt.Println("Error getting IPs for service: " + err.Error())
			continue
		}

		meshServices.Register(service, meshConfig, ips)
	}

	// END RESOURCES OMIT

	// START RESOURCES2 OMIT
	resources := make(map[resource.Type][]types.Resource)
	for _, meshService := range meshServices {
		resources[resource.ListenerType] = append(
			resources[resource.ListenerType], getListener(meshService),
		)
		resources[resource.RouteType] = append(
			resources[resource.RouteType], getRoute(meshService),
		)
		resources[resource.ClusterType] = append(
			resources[resource.ClusterType], getClusters(meshService)...,
		)
		resources[resource.EndpointType] = append(
			resources[resource.EndpointType], getEndpoints(meshService)...,
		)
	}

	return resources, nil
	// END RESOURCES2 OMIT
}

func (kw *kubeWatcher) getIPsForService(namespace, name string) ([]string, error) {
	key := namespace + "/" + name
	endpointsObj, exists, err := kw.epIndexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		fmt.Println("No endpoints found for: " + key)
	}

	var ips []string
	endpoints := endpointsObj.(*v1.Endpoints)
	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			ips = append(ips, address.IP)
		}
	}

	return ips, nil
}

func getListener(meshService *meshService) types.Resource {
	return &listener.Listener{
		Name: meshService.name,
		ApiListener: &listener.ApiListener{
			ApiListener: mustAnypb(&http_connection_managerv3.HttpConnectionManager{
				CodecType: http_connection_managerv3.HttpConnectionManager_AUTO,
				RouteSpecifier: &http_connection_managerv3.HttpConnectionManager_Rds{
					Rds: &http_connection_managerv3.Rds{
						RouteConfigName: meshService.name,
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
	}
}

func getRoute(meshService *meshService) types.Resource {
	return &route.RouteConfiguration{
		Name:             meshService.name,
		ValidateClusters: &wrapperspb.BoolValue{Value: true},
		VirtualHosts: []*route.VirtualHost{{
			Name: meshService.name,
			Domains: []string{
				meshService.name, // from 'provides' in service yaml
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
							ClusterSpecifier: getWeightedClusters(meshService),
						},
					},
				},
			},
		}},
	}
}

func getWeightedClusters(ms *meshService) *route.RouteAction_WeightedClusters {
	ret := &route.RouteAction_WeightedClusters{
		WeightedClusters: &route.WeightedCluster{},
	}
	for _, provider := range ms.providers {
		ret.WeightedClusters.Clusters = append(ret.WeightedClusters.Clusters, provider.cluster)
	}
	return ret
}

func getClusters(meshService *meshService) []types.Resource {
	var clusters []types.Resource

	for clusterName := range meshService.providers {
		clusters = append(clusters,
			&cluster.Cluster{
				Name:                 clusterName,
				LbPolicy:             cluster.Cluster_ROUND_ROBIN,
				ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
				EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
					EdsConfig: &core.ConfigSource{
						ConfigSourceSpecifier: &core.ConfigSource_Ads{},
					},
				},
			},
		)
	}

	return clusters
}

func getEndpoints(meshService *meshService) []types.Resource {
	var endpoints []types.Resource

	for clusterName, provider := range meshService.providers {
		endpoints = append(endpoints,
			&endpoint.ClusterLoadAssignment{
				ClusterName: clusterName,
				Endpoints:   []*endpoint.LocalityLbEndpoints{provider.endpoints},
			},
		)
	}

	return endpoints
}

func getEndpointsForIPs(cfg MeshServiceConfig, ips []string) *endpoint.LocalityLbEndpoints {
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
									PortValue: cfg.Port,
								},
							},
						},
					},
				},
			},
		}
		endpoints = append(endpoints, ep)
	}

	return &endpoint.LocalityLbEndpoints{
		Locality:            &core.Locality{},
		LoadBalancingWeight: &wrapperspb.UInt32Value{Value: cfg.Weight},
		LbEndpoints:         endpoints,
	}
}

func mustAnypb(m protoreflect.ProtoMessage) *anypb.Any {
	a, err := anypb.New(m)
	if err != nil {
		panic(err)
	}
	return a
}
