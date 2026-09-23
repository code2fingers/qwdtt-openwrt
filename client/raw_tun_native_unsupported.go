//go:build !linux || android

package main

import (
	"fmt"
	"os"
)

type nativeRawTUN struct {
	file *os.File
	name string
}

func createNativeRawTUN(_ string) (*nativeRawTUN, error) {
	return nil, fmt.Errorf("native RAW TUN is supported only on Linux/OpenWrt")
}

func (t *nativeRawTUN) configure(_ string, _ int) error { return nil }

func (t *nativeRawTUN) cleanup() {}

func (t *nativeRawTUN) destroy() {}

func runNativeCommandEnv(_ []string, _ ...string) error {
	return fmt.Errorf("running system commands is supported only on Linux/OpenWrt")
}
