package common_utils

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	memKey = 7749
	memSize = 1024
	memMode = 01600
)

func SetMemCounters(counters *ShareCounters) error {
	shmid, _, err := syscall.Syscall(syscall.SYS_SHMGET, uintptr(memKey), uintptr(memSize), uintptr(memMode))
	if int(shmid) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}

	shmaddr, _, err := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
	if int(shmaddr) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}
	defer syscall.Syscall(syscall.SYS_SHMDT, shmaddr, 0, 0)

	data := (*ShareCounters)(unsafe.Pointer(uintptr(shmaddr)))
	data.Gnmi_get_cnt = counters.Gnmi_get_cnt
	data.Gnmi_get_fail_cnt = counters.Gnmi_get_fail_cnt
	data.Gnmi_set_cnt = counters.Gnmi_set_cnt
	data.Gnmi_set_fail_cnt = counters.Gnmi_set_fail_cnt
	data.Gnoi_reboot_cnt = counters.Gnoi_reboot_cnt
	data.Dbus_cnt = counters.Dbus_cnt
	data.Dbus_fail_cnt = counters.Dbus_fail_cnt
	return nil
}

func GetMemCounters(counters *ShareCounters) error {
	shmid, _, err := syscall.Syscall(syscall.SYS_SHMGET, uintptr(memKey), uintptr(memSize), uintptr(memMode))
	if int(shmid) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}

	shmaddr, _, err := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
	if int(shmaddr) == -1 {
		return fmt.Errorf("syscall error, err: %v\n", err)
	}
	defer syscall.Syscall(syscall.SYS_SHMDT, shmaddr, 0, 0)

	data := (*ShareCounters)(unsafe.Pointer(uintptr(shmaddr)))
	counters.Gnmi_get_cnt = data.Gnmi_get_cnt
	counters.Gnmi_get_fail_cnt = data.Gnmi_get_fail_cnt
	counters.Gnmi_set_cnt = data.Gnmi_set_cnt
	counters.Gnmi_set_fail_cnt = data.Gnmi_set_fail_cnt
	counters.Gnoi_reboot_cnt = data.Gnoi_reboot_cnt
	counters.Dbus_cnt = data.Dbus_cnt
	counters.Dbus_fail_cnt = data.Dbus_fail_cnt
	return nil
}
