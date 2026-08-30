// +build windows

package deej

import (
	"testing"

	"go.uber.org/zap"
)

func TestSetDefaultAudioOutputDevice_Execution(t *testing.T) {
	logger := zap.NewExample().Sugar()

	err := SetDefaultAudioOutputDevice(logger, "Headphones (Shure MVX2U)")
	t.Logf("SetDefaultAudioOutputDevice(Headphones) returned: %v", err)
	if err != nil {
		t.Fatalf("SetDefaultAudioOutputDevice failed: %v", err)
	}
}
