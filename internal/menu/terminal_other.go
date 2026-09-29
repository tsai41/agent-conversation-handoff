//go:build !darwin && !linux

package menu

func ioctlWidth() int { return 0 }
