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

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
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

func (kw *kubeWatcher) getSnapshot() (*cache.Snapshot, error) {
	resources, err := kw.getResources()
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

// START RESOURCES OMIT
func (kw *kubeWatcher) getResources() (map[resource.Type][]types.Resource, error) {
	// get a map of all all kube services serving a "mesh" service
	meshServiceMap := make(map[MeshService][]string) // values are "namespace/name"

	for _, serviceObj := range kw.svcIndexer.List() {
		service := serviceObj.(*v1.Service)

		meshServiceYaml, exists := service.Annotations["mesh-service"]
		if !exists {
			fmt.Println("No mesh-service annotation found for service: " + service.Name)
			continue // keep adding resources for other services
		}

		var meshService MeshService
		err := yaml.Unmarshal([]byte(meshServiceYaml), &meshService)
		if err != nil {
			return nil, err
		}

		fmt.Printf("Found service %s/%s for mesh service %s\n",
			service.Namespace, service.Name, meshService.Name)
		meshServiceMap[meshService] = append(meshServiceMap[meshService],
			service.Namespace+"/"+service.Name,
		)
	}
	// END RESOURCES OMIT

	resources := make(map[resource.Type][]types.Resource)
	for meshService, providerKeys := range meshServiceMap {
		ips, err := kw.GetIPsFromEndpointProviders(providerKeys)
		if err != nil {
			return nil, err
		}

		resourcesForService := GetServiceResources(meshService, ips)
		for k, v := range resourcesForService {
			resources[k] = append(resources[k], v...)
		}
	}

	return resources, nil
}

// START ENDPOINTS OMIT
func (kw *kubeWatcher) GetIPsFromEndpointProviders(providers []string) ([]string, error) {
	var ips []string
	for _, providerKey := range providers {
		endpointsObj, exists, err := kw.epIndexer.GetByKey(providerKey)
		if err != nil {
			return nil, err
		}
		if !exists {
			fmt.Println("No endpoints found for provier: " + providerKey)
			continue // keep adding resources for other services
		}

		endpoints := endpointsObj.(*v1.Endpoints)
		for _, subset := range endpoints.Subsets {
			for _, address := range subset.Addresses {
				ips = append(ips, address.IP)
			}
		}
	}

	return ips, nil
}

// END ENDPOINTS OMIT
