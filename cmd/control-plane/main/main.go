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
	"os"

	// START STEP1_IMPORT OMIT
	controlplane "github.com/carsonoid/talk-half-the-mesh-half-the-mess/cmd/control-plane"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"

	// END STEP1_IMPORT OMIT

	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"
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

	// START STEP1 OMIT
	cache := cache.NewSnapshotCache(false, cache.IDHash{}, l)

	// watch kube resources and update the cache
	go runKubeWatcher(context.Background(), cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cb := &test.Callbacks{Debug: l.Debug}
	srv := server.NewServer(ctx, cache, cb)
	controlplane.RunServer(srv, port)
	// END STEP1 OMIT
}
