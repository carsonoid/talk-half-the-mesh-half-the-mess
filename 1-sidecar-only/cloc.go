package main

import "github.com/carsonoid/talk-half-the-mesh-half-the-mess/internal/demo"

const basePath = `./`
const script = `
# START RUN OMIT
cloc --by-file 1-sidecar-only/*.yaml
END RUN OMIT
`

func main() {
	err := demo.RunShellScript(basePath, script)
	if err != nil {
		panic(err)
	}
}
