// Package main provides a standalone entry point for the types-server.
package main

import (
	"log"

	"veil/internal/config"
	"veil/internal/typesserver"
	"veil/pkg/decoder"
	"veil/pkg/types"
)

func main() {
	registry := decoder.NewRegistry()
	types.RegisterDecoders(registry)

	s := typesserver.New(registry)
	log.Fatal(s.ListenAndServe(config.TypesServerPort))
}
