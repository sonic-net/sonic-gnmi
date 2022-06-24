package common_utils

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Use share memory to dump GNMI internal counters,
// GNMI server and gnmi_dump should use memKey to access the share memory,
// memSize is 1024 bytes, so we can support 128 counters
var (
	memKey = 7749
	memSize = 1024
	memMode = 0x380
)

func SetMemCounters(counters *[len(CountersName)]uint64) error {
	shmid, _, err := syscall.Syscall(syscall.SYS_SHMGET, uintptr(memKey), uintptr(memSize), uintptr(memMode))
	if int(shmid) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}

	shmaddr, _, err := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
	if int(shmaddr) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}
	defer syscall.Syscall(syscall.SYS_SHMDT, shmaddr, 0, 0)

	const size = unsafe.Sizeof(uint64(0))
	addr := uintptr(unsafe.Pointer(shmaddr))

	for i := 0; i < len(counters); i++ {
		*(*uint64)(unsafe.Pointer(addr)) = counters[i]
		addr += size
	}
	return nil
}

func GetMemCounters(counters *[len(CountersName)]uint64) error {
	shmid, _, err := syscall.Syscall(syscall.SYS_SHMGET, uintptr(memKey), uintptr(memSize), uintptr(memMode))
	if int(shmid) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}

	shmaddr, _, err := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
	if int(shmaddr) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}
	defer syscall.Syscall(syscall.SYS_SHMDT, shmaddr, 0, 0)

	const size = unsafe.Sizeof(uint64(0))
	addr := uintptr(unsafe.Pointer(shmaddr))

	for i := 0; i < len(counters); i++ {
		counters[i] = *(*uint64)(unsafe.Pointer(addr))
		addr += size
	}
	return nil
}

