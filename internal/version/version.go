package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	Version   = "dev"
	Commit    = ""
	BuildTime = "unknown"
	BuiltBy   = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	BuiltBy   string `json:"builtBy"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	goVersion := runtime.Version()
	if ok {
		goVersion = bi.GoVersion
	}

	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		BuiltBy:   BuiltBy,
		GoVersion: goVersion,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func VersionString() string {
	v := Get()
	if v.Commit != "" {
		return fmt.Sprintf("%s (%s)", v.Version, v.Commit)
	}
	return v.Version
}
