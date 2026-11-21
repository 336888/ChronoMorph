package main

import (
	"embed"
	"fmt"
	"io"
)

//go:embed assets/licenses/*.txt
var thirdPartyLicenses embed.FS

func printLicenses(w io.Writer) error {
	entries, err := thirdPartyLicenses.ReadDir("assets/licenses")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		data, err := thirdPartyLicenses.ReadFile("assets/licenses/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "%s\n%s\n", entry.Name(), data); err != nil {
			return err
		}
	}
	return nil
}

var (
	AppVersion = "v1.4.4"
	BuildTime  = "dev"
)

func VersionString() string {
	return fmt.Sprintf("ChronoMorph %s (built %s)", AppVersion, BuildTime)
}
