//go:build darwin && cgo

package main

/*
#include <pthread.h>
#include <pthread/qos.h>
static void high_qos(void) { pthread_set_qos_class_self_np(QOS_CLASS_USER_INTERACTIVE, 0); }
*/
import "C"

// highQoS asks macOS to keep the current (locked) OS thread on P-cores:
// macOS has no pinning (BENCH.md §2.1). Callers hold runtime.LockOSThread.
func highQoS() { C.high_qos() }
