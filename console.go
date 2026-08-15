package main

import "fmt"

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

func info(s string, a ...any)    { fmt.Printf(blue+"ℹ "+reset+s+"\n", a...) }
func success(s string, a ...any) { fmt.Printf(green+"✔ "+reset+s+"\n", a...) }
func warn(s string, a ...any)    { fmt.Printf(yellow+"⚠ "+reset+s+"\n", a...) }
func fail(s string, a ...any)    { fmt.Printf(red+"✖ "+reset+s+"\n", a...) }
func header(s string, a ...any)  { fmt.Printf("\n"+bold+magenta+":: "+reset+s+"\n", a...) }
