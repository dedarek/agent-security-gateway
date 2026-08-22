package shellcontrol

import (
	"encoding/json"
	"runtime"
	"time"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func nowUnixNano() int64 { return time.Now().UnixNano() }

// shellBin / shellFlag pick the platform shell.
func shellBin() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "/bin/bash"
}

func shellFlag() string {
	if runtime.GOOS == "windows" {
		return "-Command"
	}
	return "-c"
}
