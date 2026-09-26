package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/film/violin/internal/laya"
)

func main() {
	root := flag.String("root", "", "staging directory containing checkpoints/ and runtime/")
	output := flag.String("out", "", "directory for release assets and manifest")
	goos := flag.String("goos", "", "target operating system")
	goarch := flag.String("goarch", "", "target architecture")
	flag.Parse()
	if *root == "" || *output == "" || *goos == "" || *goarch == "" {
		fmt.Fprintln(os.Stderr, "usage: laya-bundle -root STAGING -out OUTPUT -goos GOOS -goarch GOARCH")
		os.Exit(2)
	}
	if err := laya.PackageUpstreamBundle(*root, *output, *goos, *goarch); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
