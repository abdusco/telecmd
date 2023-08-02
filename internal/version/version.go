package version

import (
	"fmt"
	"runtime/debug"
)

type Version struct {
	Commit     string
	CommitTime string
}

func (v Version) String() string {
	return fmt.Sprintf("%s.%s", v.CommitTime, v.Commit)
}

func GitVersion() Version {
	var info Version
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.Commit = setting.Value[:7]
			case "vcs.time":
				info.CommitTime = setting.Value
			}
		}
	}

	return info
}
