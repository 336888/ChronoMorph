package main

import "fmt"

var (
	AppVersion = "v1.4.1"
	BuildTime  = "dev"
)

func VersionString() string {
	return fmt.Sprintf("ChronoMorph %s (built %s)", AppVersion, BuildTime)
}
