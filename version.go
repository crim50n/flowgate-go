package main

import "fmt"

var version = "dev"

func cmdVersion() error {
	fmt.Printf("Flowgate %s\n", version)
	return nil
}
