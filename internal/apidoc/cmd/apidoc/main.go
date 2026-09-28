// Command apidoc writes apidoc_gen.go for a package: go generate runs it in
// internal/catalog, and a test there fails when the file is out of date.
package main

import (
	"daddy-cull/next/internal/apidoc"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: apidoc <import path of the package in this directory>")
	}
	source, err := apidoc.Generate(".", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(apidoc.OutputName, source, 0o644); err != nil {
		log.Fatal(err)
	}
}
