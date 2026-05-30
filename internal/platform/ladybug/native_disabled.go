//go:build !ladybug_native

package ladybug

func NativeStatus() NativeRuntimeStatus {
	return NativeRuntimeStatus{
		Available: false,
		Reason:    "build tag ladybug_native is not enabled; run scripts/ladybug-libs.sh before native LadybugDB builds",
	}
}
