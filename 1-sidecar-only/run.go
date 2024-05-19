package main

import "github.com/carsonoid/talk-half-the-mesh-half-the-mess/internal/demo"

const basePath = `./1-sidecar-only/`
const script = `
# START RUN OMIT
k3d cluster create sidecar-cluster --wait \
  --k3s-arg '--disable=metrics-server@all' \
  --k3s-arg '--disable=traefik@all'

k3d image import -c sidecar-cluster ../app.tar

kubectl apply -f k8s-clients.yaml
kubectl apply -f k8s-services.yaml

kubectl wait --timeout=60s --for=condition=Available=True \
  deployment/client-1 \
  deployment/client-2 \
  deployment/service-1 \
  deployment/service-2

kubetail --follow -k false -l purpose=client
END RUN OMIT
`

func main() {
	err := demo.RunShellScript(basePath, script)
	if err != nil {
		panic(err)
	}
}
