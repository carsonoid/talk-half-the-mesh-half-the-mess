package main

import "github.com/carsonoid/talk-half-the-mesh-half-the-mess/internal/demo"

const basePath = `./0-setup/`
const script = `
# START RUN OMIT
docker buildx build -t carsonoid/go-test-app ..
docker save carsonoid/go-test-app -o ../app.tar
END RUN OMIT
`

func main() {
	err := demo.RunShellScript(basePath, script)
	if err != nil {
		panic(err)
	}
}
