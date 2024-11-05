package main

import "github.com/carsonoid/talk-half-the-mesh-half-the-mess/internal/demo"

const basePath = `./`
const script = `
# START RUN OMIT
cloc --by-file --include-lang Go cmd/control-plane
END RUN OMIT
`

func main() {
	err := demo.RunShellScript(basePath, script)
	if err != nil {
		panic(err)
	}
}
