package utils

import (
	"fmt"
	"os"
)

func LogErrorAndExit(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
