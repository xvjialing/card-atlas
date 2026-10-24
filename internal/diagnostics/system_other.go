//go:build !darwin

package diagnostics

import "runtime"

func systemVersion() string { return runtime.GOOS }
