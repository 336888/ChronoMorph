package main

import "fmt"

var (
	AppVersion = "v1.0.2"
	BuildTime  = "dev"
)

func VersionString() string {
	return fmt.Sprintf("ChronoMorph %s (built %s)", AppVersion, BuildTime)
}
