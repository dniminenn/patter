package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	cfg := loadConfig()
	cmd := "daemon"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "daemon":
		d := newDaemon(cfg)
		if err := d.acquire(); err != nil {
			if errors.Is(err, errDaemonRunning) {
				_, _ = sendCmd("show")
				os.Exit(0)
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if showWindow() {
			go d.run()
			runStatus(d)
			return
		}
		logf("headless")
		d.run()
	case "status":
		fmt.Println(ensureDaemon())
	case "toggle", "down":
		ensureDaemon()
	case "check":
		runCheck(cfg)
	default:
		fmt.Fprintln(os.Stderr, "usage: patter [daemon|status|check]")
		os.Exit(2)
	}
}

func runCheck(cfg Config) {
	r := &remote{cfg: cfg}
	if err := r.health(); err != nil {
		fmt.Fprintf(os.Stderr, "health: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("health ok")
	res, err := sanitizeRaw(r, "um meet on tuesday no wednesday")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sanitize: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s\n%s\n", res.Action, strings.TrimRight(res.Text, "\n"))
}
