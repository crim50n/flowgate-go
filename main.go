package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Println(`Flowgate - Network Flow Controller (Angie + Blocky)

Usage:
  flowgate init
  flowgate doctor [-v|--verbose]
  flowgate add DOMAIN [DOMAIN...]
  flowgate service DOMAIN PORT [--ip IP]
  flowgate dns DOMAIN
  flowgate remove DOMAIN [DOMAIN...]
  flowgate status
  flowgate sync

Commands add, service, dns and remove automatically run sync.`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit()
	case "doctor":
		err = cmdDoctor(hasVerbose(os.Args[2:]))
	case "add":
		err = cmdAdd(os.Args[2:])
	case "service":
		err = cmdService(os.Args[2:])
	case "dns":
		err = cmdDNS(os.Args[2:])
	case "remove":
		err = cmdRemove(os.Args[2:])
	case "status":
		err = cmdStatus()
	case "sync":
		err = syncAll()
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}

	if err != nil {
		fail("%v", err)
		os.Exit(1)
	}
}
