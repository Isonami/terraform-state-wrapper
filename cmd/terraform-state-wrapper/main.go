package main

import (
	"context"
	"github.com/Isonami/terraform-state-wrapper/pkg/backends/file"
	"github.com/Isonami/terraform-state-wrapper/pkg/wrapper"
	"os"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := new(file.Backend)

	wrapper.Wrap(ctx, backend, os.Args[1:])
}
