//go:build ladybug_native

package ladybug

import _ "github.com/LadybugDB/go-ladybug"

func NativeStatus() NativeRuntimeStatus {
	return NativeRuntimeStatus{
		Available: true,
		Reason:    "native LadybugDB bindings enabled",
	}
}
