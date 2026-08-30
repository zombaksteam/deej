// +build windows

package deej

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	wca "github.com/moutend/go-wca"
	"go.uber.org/zap"
)

var (
	CLSID_PolicyConfigClient = ole.NewGUID("{870af99c-171d-4f9e-af0d-e63df40c2bc9}")
	IID_IPolicyConfig        = ole.NewGUID("{f8679f50-850a-41cf-9c72-430f290290c8}")
	IID_IPolicyConfigVista   = ole.NewGUID("{568b000d-9a0a-4130-94e4-7d5890070863}")
)


type iPolicyConfig struct {
	ole.IUnknown
}

type iPolicyConfigVtbl struct {
	ole.IUnknownVtbl
	GetMixFormat          uintptr
	GetDeviceFormat       uintptr
	ResetDeviceFormat     uintptr
	SetDeviceFormat       uintptr
	GetProcessingPeriod   uintptr
	SetProcessingPeriod   uintptr
	GetShareMode          uintptr
	SetShareMode          uintptr
	GetPropertyValue      uintptr
	SetPropertyValue      uintptr
	SetDefaultEndpoint    uintptr
	SetEndpointVisibility uintptr
}

type iPolicyConfigVista struct {
	ole.IUnknown
}

type iPolicyConfigVistaVtbl struct {
	ole.IUnknownVtbl
	GetMixFormat          uintptr
	GetDeviceFormat       uintptr
	SetDeviceFormat       uintptr
	GetProcessingPeriod   uintptr
	SetProcessingPeriod   uintptr
	GetShareMode          uintptr
	SetShareMode          uintptr
	GetPropertyValue      uintptr
	SetPropertyValue      uintptr
	SetDefaultEndpoint    uintptr
	SetEndpointVisibility uintptr
}

func (v *iPolicyConfig) VTable() *iPolicyConfigVtbl {
	return (*iPolicyConfigVtbl)(unsafe.Pointer(v.RawVTable))
}

func (v *iPolicyConfig) SetDefaultEndpoint(wszDeviceId string, role uint32) error {
	pID, err := syscall.UTF16PtrFromString(wszDeviceId)
	if err != nil {
		return err
	}

	hr, _, _ := syscall.Syscall(
		v.VTable().SetDefaultEndpoint,
		3,
		uintptr(unsafe.Pointer(v)),
		uintptr(unsafe.Pointer(pID)),
		uintptr(role),
	)

	if hr != 0 {
		return ole.NewError(hr)
	}

	return nil
}

func (v *iPolicyConfigVista) VTable() *iPolicyConfigVistaVtbl {
	return (*iPolicyConfigVistaVtbl)(unsafe.Pointer(v.RawVTable))
}

func (v *iPolicyConfigVista) SetDefaultEndpoint(wszDeviceId string, role uint32) error {
	pID, err := syscall.UTF16PtrFromString(wszDeviceId)
	if err != nil {
		return err
	}

	hr, _, _ := syscall.Syscall(
		v.VTable().SetDefaultEndpoint,
		3,
		uintptr(unsafe.Pointer(v)),
		uintptr(unsafe.Pointer(pID)),
		uintptr(role),
	)

	if hr != 0 {
		return ole.NewError(hr)
	}

	return nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var runes []uint16
	for ptr := p; *ptr != 0; ptr = (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 2)) {
		runes = append(runes, *ptr)
	}
	return string(utf16.Decode(runes))
}

func getDeviceID(endpoint *wca.IMMDevice) (string, error) {
	vtbl := (*[10]uintptr)(unsafe.Pointer(endpoint.RawVTable))
	var strIdPtr *uint16
	hr, _, _ := syscall.Syscall(
		vtbl[5], // GetId is index 5 in IMMDevice vtable
		2,
		uintptr(unsafe.Pointer(endpoint)),
		uintptr(unsafe.Pointer(&strIdPtr)),
		0,
	)
	if hr != 0 {
		return "", ole.NewError(hr)
	}
	if strIdPtr == nil {
		return "", fmt.Errorf("null device id pointer returned from IMMDevice::GetId")
	}
	strId := utf16PtrToString(strIdPtr)
	ole.CoTaskMemFree(uintptr(unsafe.Pointer(strIdPtr)))
	return strId, nil
}

// SetDefaultAudioOutputDevice finds the output device matching targetName and sets it as the default playback endpoint
func SetDefaultAudioOutputDevice(logger *zap.SugaredLogger, targetName string) error {
	if targetName == "" {
		return nil
	}

	if err := ole.CoInitialize(0); err != nil {
		oleCode := err.(*ole.OleError).Code()
		// RPC_E_CHANGED_MODE or already initialized is okay
		if oleCode != ole.S_OK && oleCode != 0x00000001 && oleCode != 0x80010106 {
			return fmt.Errorf("CoInitialize: %w", err)
		}
	}
	defer ole.CoUninitialize()

	var mmDeviceEnumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator,
		0,
		wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator,
		&mmDeviceEnumerator,
	); err != nil {
		return fmt.Errorf("create MMDeviceEnumerator: %w", err)
	}
	defer mmDeviceEnumerator.Release()

	var deviceCollection *wca.IMMDeviceCollection
	if err := mmDeviceEnumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		return fmt.Errorf("enum audio endpoints: %w", err)
	}
	defer deviceCollection.Release()

	var deviceCount uint32
	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		return fmt.Errorf("get device count: %w", err)
	}

	var matchedDeviceID string
	var matchedDeviceName string

	normalizedTarget := strings.ToLower(strings.TrimSpace(targetName))

	for deviceIdx := uint32(0); deviceIdx < deviceCount; deviceIdx++ {
		var endpoint *wca.IMMDevice
		if err := deviceCollection.Item(deviceIdx, &endpoint); err != nil {
			continue
		}

		var propertyStore *wca.IPropertyStore
		if err := endpoint.OpenPropertyStore(wca.STGM_READ, &propertyStore); err != nil {
			endpoint.Release()
			continue
		}

		value := &wca.PROPVARIANT{}
		friendlyName := ""
		if err := propertyStore.GetValue(&wca.PKEY_Device_FriendlyName, value); err == nil {
			friendlyName = value.String()
		}

		desc := ""
		if err := propertyStore.GetValue(&wca.PKEY_Device_DeviceDesc, value); err == nil {
			desc = value.String()
		}

		propertyStore.Release()

		normFriendly := strings.ToLower(strings.TrimSpace(friendlyName))
		normDesc := strings.ToLower(strings.TrimSpace(desc))

		// Check exact or substring match
		if normFriendly == normalizedTarget || normDesc == normalizedTarget ||
			strings.Contains(normFriendly, normalizedTarget) || strings.Contains(normDesc, normalizedTarget) {
			if strId, err := getDeviceID(endpoint); err == nil {
				matchedDeviceID = strId
				matchedDeviceName = friendlyName
				endpoint.Release()
				break
			}
		}

		endpoint.Release()
	}

	if matchedDeviceID == "" {
		if logger != nil {
			logger.Warnw("No matching active audio output device found to switch to", "target", targetName)
		}
		return fmt.Errorf("audio device not found: %s", targetName)
	}

	// Instantiate PolicyConfigClient
	var policyConfigUnk *ole.IUnknown
	if err := wca.CoCreateInstance(
		CLSID_PolicyConfigClient,
		0,
		wca.CLSCTX_ALL,
		ole.IID_IUnknown,
		&policyConfigUnk,
	); err != nil {
		return fmt.Errorf("create PolicyConfigClient: %w", err)
	}
	defer policyConfigUnk.Release()

	// Try IPolicyConfig first (Win 7/8/10/11)
	dispatch, err := policyConfigUnk.QueryInterface(IID_IPolicyConfig)
	if err == nil && dispatch != nil {
		policyConfig := (*iPolicyConfig)(unsafe.Pointer(dispatch))
		defer policyConfig.Release()

		// eConsole = 0, eMultimedia = 1, eCommunications = 2
		for role := uint32(0); role <= 2; role++ {
			if err := policyConfig.SetDefaultEndpoint(matchedDeviceID, role); err != nil {
				if logger != nil {
					logger.Warnw("Failed to set default endpoint for role", "role", role, "error", err)
				}
			}
		}
	} else {
		// Fallback to IPolicyConfigVista
		dispatchVista, errVista := policyConfigUnk.QueryInterface(IID_IPolicyConfigVista)
		if errVista != nil || dispatchVista == nil {
			return fmt.Errorf("query IPolicyConfig and IPolicyConfigVista failed: %v / %v", err, errVista)
		}
		policyConfigVista := (*iPolicyConfigVista)(unsafe.Pointer(dispatchVista))
		defer policyConfigVista.Release()

		for role := uint32(0); role <= 2; role++ {
			if err := policyConfigVista.SetDefaultEndpoint(matchedDeviceID, role); err != nil {
				if logger != nil {
					logger.Warnw("Failed to set default endpoint (Vista) for role", "role", role, "error", err)
				}
			}
		}
	}

	if logger != nil {
		logger.Infow("Successfully switched default audio output device",
			"target", targetName,
			"matchedName", matchedDeviceName,
			"deviceId", matchedDeviceID)
	}

	// Give Windows a brief moment to process the device change
	time.Sleep(50 * time.Millisecond)

	return nil
}
