// +build gnmi_memcheck

package test_utils

// int __lsan_do_recoverable_leak_check(void);
import "C"
import "fmt"

func MemLeakCheck() {
	ret := int(C.__lsan_do_recoverable_leak_check())
	if ret != 0 {
		panic(fmt.Errorf("Detect memory leak!"))
	}
}

