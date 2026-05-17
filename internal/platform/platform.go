package platform

import "runtime"

func OS() string { return runtime.GOOS }
