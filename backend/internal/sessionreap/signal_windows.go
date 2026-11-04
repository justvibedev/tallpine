package sessionreap

import "syscall"

func signalPID(int, syscall.Signal) error { return ErrUnsupported }
