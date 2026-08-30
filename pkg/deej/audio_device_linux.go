// +build !windows

package deej

import "go.uber.org/zap"

// SetDefaultAudioOutputDevice is a no-op on non-Windows platforms
func SetDefaultAudioOutputDevice(logger *zap.SugaredLogger, targetName string) error {
	if targetName == "" {
		return nil
	}
	if logger != nil {
		logger.Debugw("Audio output device switching is not supported on this platform", "target", targetName)
	}
	return nil
}
