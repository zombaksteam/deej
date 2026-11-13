package deej

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	wca "github.com/moutend/go-wca"
	"go.uber.org/zap"
)

type registeredSessionManager struct {
	manager    *wca.IAudioSessionManager2
	enumerator *wca.IAudioSessionEnumerator
}

type wcaSessionFinder struct {
	logger        *zap.SugaredLogger
	sessionLogger *zap.SugaredLogger

	eventCtx *ole.GUID // needed for some session actions to successfully notify other audio consumers

	// needed for device change notifications
	mmDeviceEnumerator      *wca.IMMDeviceEnumerator
	mmNotificationClient    *wca.IMMNotificationClient
	lastDefaultDeviceChange time.Time

	// needed for session creation notifications
	sessionNotification       *wca.IAudioSessionNotification
	sessionNotificationVtbl   *wca.IAudioSessionNotificationVtbl
	registeredSessionManagers []registeredSessionManager
	sessionEventsChannel      chan struct{}

	// state tracking for lightweight change detection without leaking COM resources
	stateLock         sync.Mutex
	devicesStale      bool
	lastDeviceCount   uint32
	lastSessionCounts map[uint32]int
	mtaStopChan       chan struct{}

	// cached COM callback pointers (allocated at most once per finder)
	cbQueryInterface       uintptr
	cbAddRef               uintptr
	cbRelease              uintptr
	cbNoop                 uintptr
	cbDefaultDeviceChanged uintptr
	cbDeviceStateChanged   uintptr
	cbDeviceAdded          uintptr
	cbDeviceRemoved        uintptr
	cbSessionCreated       uintptr

	// our master input and output sessions
	masterOut *masterSession
	masterIn  *masterSession
}

const (

	// there's no real mystery here, it's just a random GUID
	myteriousGUID = "{1ec920a1-7db8-44ba-9779-e5d28ed9f330}"

	// the notification client will call this multiple times in quick succession based on the
	// default device's assigned media roles, so we need to filter out the extraneous calls
	minDefaultDeviceChangeThreshold = 100 * time.Millisecond

	// prefix for device sessions in logger
	deviceSessionFormat = "device.%s"
)

func newSessionFinder(logger *zap.SugaredLogger) (SessionFinder, error) {
	sf := &wcaSessionFinder{
		logger:               logger.Named("session_finder"),
		sessionLogger:        logger.Named("sessions"),
		eventCtx:             ole.NewGUID(myteriousGUID),
		sessionEventsChannel: make(chan struct{}, 10),
	}

	sf.cbQueryInterface = syscall.NewCallback(sf.queryInterfaceCallback)
	sf.cbAddRef = syscall.NewCallback(sf.addRefCallback)
	sf.cbRelease = syscall.NewCallback(sf.releaseCallback)
	sf.cbNoop = syscall.NewCallback(sf.noopCallback)
	sf.cbDefaultDeviceChanged = syscall.NewCallback(sf.defaultDeviceChangedCallback)
	sf.cbDeviceStateChanged = syscall.NewCallback(sf.deviceStateChangedCallback)
	sf.cbDeviceAdded = syscall.NewCallback(sf.deviceAddedOrRemovedCallback)
	sf.cbDeviceRemoved = syscall.NewCallback(sf.deviceAddedOrRemovedCallback)
	sf.cbSessionCreated = syscall.NewCallback(sf.sessionCreatedCallback)

	// Keep the process-wide Multi-Threaded Apartment (MTA) alive on a pinned thread for the lifetime
	// of the session finder so CoUninitialize on worker threads never tears down the MTA apartment.
	sf.mtaStopChan = make(chan struct{})
	mtaReady := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		uninit, err := sf.coInitialize()
		close(mtaReady)
		if err != nil {
			return
		}
		defer uninit()

		<-sf.mtaStopChan
	}()
	<-mtaReady

	sf.logger.Debug("Created WCA session finder instance")

	return sf, nil
}

func (sf *wcaSessionFinder) coInitialize() (func(), error) {
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	if err != nil {
		const (
			eFalse          = 0x00000001
			rpcEChangedMode = 0x80010106
		)
		oleError := &ole.OleError{}
		if errors.As(err, &oleError) {
			switch oleError.Code() {
			case ole.S_OK, eFalse:
				return ole.CoUninitialize, nil
			case rpcEChangedMode:
				// Already initialized in MTA on this thread; do not call CoUninitialize for this call
				return func() {}, nil
			default:
				sf.logger.Warnw("Failed to call CoInitializeEx",
					"isOleError", true,
					"error", err,
					"oleError", oleError)
				return nil, fmt.Errorf("call CoInitializeEx: %w", err)
			}
		}

		sf.logger.Warnw("Failed to call CoInitializeEx",
			"isOleError", false,
			"error", err,
			"oleError", nil)
		return nil, fmt.Errorf("call CoInitializeEx: %w", err)
	}

	return ole.CoUninitialize, nil
}

func releaseSessionsSlice(sessions []Session) {
	for _, s := range sessions {
		if s != nil {
			s.Release()
		}
	}
}

func (sf *wcaSessionFinder) GetAllSessions() ([]Session, error) {
	sessions := []Session{}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit, err := sf.coInitialize()
	if err != nil {
		return nil, err
	}
	defer uninit()

	// ensure we have a device enumerator
	if err := sf.getDeviceEnumerator(); err != nil {
		sf.logger.Warnw("Failed to get device enumerator", "error", err)
		return nil, fmt.Errorf("get device enumerator: %w", err)
	}

	// get the currently active default output and input devices.
	// please note that this can return a nil defaultInputEndpoint, in case there are no input devices connected.
	// you must check it for non-nil
	defaultOutputEndpoint, defaultInputEndpoint, err := sf.getDefaultAudioEndpoints()
	if err != nil {
		sf.logger.Warnw("Failed to get default audio endpoints", "error", err)
		return nil, fmt.Errorf("get default audio endpoints: %w", err)
	}
	defer defaultOutputEndpoint.Release()

	if defaultInputEndpoint != nil {
		defer defaultInputEndpoint.Release()
	}

	// receive notifications whenever the default device changes (only do this once)
	if sf.mmNotificationClient == nil {
		if err := sf.registerDefaultDeviceChangeCallback(); err != nil {
			sf.logger.Warnw("Failed to register default device change callback", "error", err)
			return nil, fmt.Errorf("register default device change callback: %w", err)
		}
	}

	// get the master output session
	sf.masterOut, err = sf.getMasterSession(defaultOutputEndpoint, masterSessionName, masterSessionName)
	if err != nil {
		sf.logger.Warnw("Failed to get master audio output session", "error", err)
		return nil, fmt.Errorf("get master audio output session: %w", err)
	}

	sessions = append(sessions, sf.masterOut)

	// get the master input session, if a default input device exists
	if defaultInputEndpoint != nil {
		sf.masterIn, err = sf.getMasterSession(defaultInputEndpoint, inputSessionName, inputSessionName)
		if err != nil {
			sf.logger.Warnw("Failed to get master audio input session", "error", err)
			releaseSessionsSlice(sessions)
			return nil, fmt.Errorf("get master audio input session: %w", err)
		}

		sessions = append(sessions, sf.masterIn)
	}

	// enumerate all devices and make their "master" sessions bindable by friendly name;
	// for output devices, this is also where we enumerate process sessions
	if err := sf.enumerateAndAddSessions(&sessions); err != nil {
		sf.logger.Warnw("Failed to enumerate device sessions", "error", err)
		releaseSessionsSlice(sessions)
		return nil, fmt.Errorf("enumerate device sessions: %w", err)
	}

	return sessions, nil
}

func (sf *wcaSessionFinder) unregisterSessionNotifications() {
	for _, item := range sf.registeredSessionManagers {
		if item.enumerator != nil {
			item.enumerator.Release()
		}
		if item.manager != nil {
			if sf.sessionNotification != nil {
				_ = item.manager.UnregisterSessionNotification(sf.sessionNotification)
			}
			item.manager.Release()
		}
	}
	sf.registeredSessionManagers = nil
}

func (sf *wcaSessionFinder) Release() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit, err := sf.coInitialize()
	if err == nil {
		defer uninit()
	}

	sf.unregisterSessionNotifications()

	// skip unregistering the mmnotificationclient, as it's not implemented in go-wca
	if sf.mmDeviceEnumerator != nil {
		sf.mmDeviceEnumerator.Release()
		sf.mmDeviceEnumerator = nil
	}

	if sf.mtaStopChan != nil {
		close(sf.mtaStopChan)
		sf.mtaStopChan = nil
	}

	sf.logger.Debug("Released WCA session finder instance")

	return nil
}

func (sf *wcaSessionFinder) getDeviceEnumerator() error {

	// get the IMMDeviceEnumerator (only once)
	if sf.mmDeviceEnumerator == nil {
		if err := wca.CoCreateInstance(
			wca.CLSID_MMDeviceEnumerator,
			0,
			wca.CLSCTX_ALL,
			wca.IID_IMMDeviceEnumerator,
			&sf.mmDeviceEnumerator,
		); err != nil {
			sf.logger.Warnw("Failed to call CoCreateInstance", "error", err)
			return fmt.Errorf("call CoCreateInstance: %w", err)
		}
	}

	return nil
}

func (sf *wcaSessionFinder) getDefaultAudioEndpoints() (*wca.IMMDevice, *wca.IMMDevice, error) {

	// get the default audio endpoints as IMMDevice instances
	var mmOutDevice *wca.IMMDevice
	var mmInDevice *wca.IMMDevice

	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmOutDevice); err != nil {
		sf.logger.Warnw("Failed to call GetDefaultAudioEndpoint (out)", "error", err)
		return nil, nil, fmt.Errorf("call GetDefaultAudioEndpoint (out): %w", err)
	}

	// allow this call to fail (not all users have a microphone connected)
	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmInDevice); err != nil {
		sf.logger.Debug("No default input device detected, proceeding without it (\"mic\" will not work)")
		mmInDevice = nil
	}

	return mmOutDevice, mmInDevice, nil
}

func (sf *wcaSessionFinder) registerDefaultDeviceChangeCallback() error {
	sf.mmNotificationClient = &wca.IMMNotificationClient{}
	sf.mmNotificationClient.VTable = &wca.IMMNotificationClientVtbl{}

	sf.mmNotificationClient.VTable.QueryInterface = sf.cbQueryInterface
	sf.mmNotificationClient.VTable.AddRef = sf.cbAddRef
	sf.mmNotificationClient.VTable.Release = sf.cbRelease
	sf.mmNotificationClient.VTable.OnDeviceStateChanged = sf.cbDeviceStateChanged
	sf.mmNotificationClient.VTable.OnDeviceAdded = sf.cbDeviceAdded
	sf.mmNotificationClient.VTable.OnDeviceRemoved = sf.cbDeviceRemoved
	sf.mmNotificationClient.VTable.OnPropertyValueChanged = sf.cbNoop
	sf.mmNotificationClient.VTable.OnDefaultDeviceChanged = sf.cbDefaultDeviceChanged

	if err := sf.mmDeviceEnumerator.RegisterEndpointNotificationCallback(sf.mmNotificationClient); err != nil {
		sf.logger.Warnw("Failed to call RegisterEndpointNotificationCallback", "error", err)
		return fmt.Errorf("call RegisterEndpointNotificationCallback: %w", err)
	}

	return nil
}

func (sf *wcaSessionFinder) getMasterSession(mmDevice *wca.IMMDevice, key string, loggerKey string) (*masterSession, error) {

	var audioEndpointVolume *wca.IAudioEndpointVolume

	if err := mmDevice.Activate(wca.IID_IAudioEndpointVolume, wca.CLSCTX_ALL, nil, &audioEndpointVolume); err != nil {
		sf.logger.Warnw("Failed to activate AudioEndpointVolume for master session", "error", err)
		return nil, fmt.Errorf("activate master session: %w", err)
	}

	// create the master session
	master, err := newMasterSession(sf.sessionLogger, audioEndpointVolume, sf.eventCtx, key, loggerKey)
	if err != nil {
		audioEndpointVolume.Release()
		sf.logger.Warnw("Failed to create master session instance", "error", err)
		return nil, fmt.Errorf("create master session: %w", err)
	}

	return master, nil
}

func (sf *wcaSessionFinder) enumerateAndAddSessions(sessions *[]Session) error {

	// get list of devices
	var deviceCollection *wca.IMMDeviceCollection

	if err := sf.mmDeviceEnumerator.EnumAudioEndpoints(wca.EAll, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		sf.logger.Warnw("Failed to enumerate active audio endpoints", "error", err)
		return fmt.Errorf("enumerate active audio endpoints: %w", err)
	}
	defer deviceCollection.Release()

	// check how many devices there are
	var deviceCount uint32

	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		sf.logger.Warnw("Failed to get device count from device collection", "error", err)
		return fmt.Errorf("get device count from device collection: %w", err)
	}

	sf.stateLock.Lock()
	sf.devicesStale = false
	sf.stateLock.Unlock()

	// Always unregister and release previous session notification managers before re-registering
	sf.unregisterSessionNotifications()

	currentSessionCounts := make(map[uint32]int)

	// for each device:
	for deviceIdx := uint32(0); deviceIdx < deviceCount; deviceIdx++ {
		if err := sf.enumerateSingleDevice(
			deviceCollection,
			deviceIdx,
			true,
			sessions,
			currentSessionCounts,
		); err != nil {
			return err
		}
	}

	sf.stateLock.Lock()
	sf.lastDeviceCount = deviceCount
	sf.lastSessionCounts = currentSessionCounts
	sf.stateLock.Unlock()

	return nil
}

func (sf *wcaSessionFinder) enumerateSingleDevice(
	deviceCollection *wca.IMMDeviceCollection,
	deviceIdx uint32,
	needRegisterNotifications bool,
	sessions *[]Session,
	currentSessionCounts map[uint32]int,
) error {
	// get its IMMDevice instance
	var endpoint *wca.IMMDevice

	if err := deviceCollection.Item(deviceIdx, &endpoint); err != nil {
		sf.logger.Warnw("Failed to get device from device collection",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("get device %d from device collection: %w", deviceIdx, err)
	}
	defer endpoint.Release()

	// get its IMMEndpoint instance to figure out if it's an output device (and we need to enumerate its process sessions later)
	dispatch, err := endpoint.QueryInterface(wca.IID_IMMEndpoint)
	if err != nil {
		sf.logger.Warnw("Failed to query IMMEndpoint for device",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("query device %d IMMEndpoint: %w", deviceIdx, err)
	}
	endpointType := (*wca.IMMEndpoint)(unsafe.Pointer(dispatch))
	defer endpointType.Release()

	// get the device's property store
	var propertyStore *wca.IPropertyStore

	if err := endpoint.OpenPropertyStore(wca.STGM_READ, &propertyStore); err != nil {
		sf.logger.Warnw("Failed to open property store for endpoint",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("open endpoint %d property store: %w", deviceIdx, err)
	}
	defer propertyStore.Release()

	// query the property store for the device's description and friendly name
	value := &wca.PROPVARIANT{}

	if err := propertyStore.GetValue(&wca.PKEY_Device_DeviceDesc, value); err != nil {
		sf.logger.Warnw("Failed to get description for device",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("get device %d description: %w", deviceIdx, err)
	}

	// device description i.e. "Headphones" (note: value.String() frees the underlying CoTaskMem pointer)
	endpointDescription := strings.ToLower(value.String())

	*value = wca.PROPVARIANT{}
	if err := propertyStore.GetValue(&wca.PKEY_Device_FriendlyName, value); err != nil {
		sf.logger.Warnw("Failed to get friendly name for device",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("get device %d friendly name: %w", deviceIdx, err)
	}

	// device friendly name i.e. "Headphones (Realtek Audio)"
	endpointFriendlyName := value.String()

	var dataFlow uint32
	if err := endpointType.GetDataFlow(&dataFlow); err != nil {
		sf.logger.Warnw("Failed to get data flow for endpoint",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("get device %d data flow: %w", deviceIdx, err)
	}

	sf.logger.Debugw("Enumerated device info",
		"deviceIdx", deviceIdx,
		"deviceDescription", endpointDescription,
		"deviceFriendlyName", endpointFriendlyName,
		"dataFlow", dataFlow)

	// if the device is an output device, enumerate and add its per-process audio sessions
	if dataFlow == wca.ERender {
		if err := sf.enumerateAndAddProcessSessions(
			endpoint,
			endpointFriendlyName,
			needRegisterNotifications,
			sessions,
			currentSessionCounts,
		); err != nil {
			sf.logger.Warnw("Failed to enumerate and add process sessions for device",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("enumerate and add device %d process sessions: %w", deviceIdx, err)
		}
	}

	// for all devices (both input and output), add a named "master" session that can be addressed
	// by using the device's friendly name (as appears when the user left-clicks the speaker icon in the tray)
	newSession, err := sf.getMasterSession(endpoint,
		endpointFriendlyName,
		fmt.Sprintf(deviceSessionFormat, endpointDescription))

	if err != nil {
		sf.logger.Warnw("Failed to get master session for device",
			"deviceIdx", deviceIdx,
			"error", err)

		return fmt.Errorf("get device %d master session: %w", deviceIdx, err)
	}

	// add it to our slice
	*sessions = append(*sessions, newSession)
	return nil
}

func (sf *wcaSessionFinder) enumerateAndAddProcessSessions(
	endpoint *wca.IMMDevice,
	endpointFriendlyName string,
	needRegisterNotifications bool,
	sessions *[]Session,
	currentSessionCounts map[uint32]int,
) error {

	sf.logger.Debugw("Enumerating and adding process sessions for audio output device",
		"deviceFriendlyName", endpointFriendlyName)

	// query the given IMMDevice's IAudioSessionManager2 interface
	var audioSessionManager2 *wca.IAudioSessionManager2

	if err := endpoint.Activate(
		wca.IID_IAudioSessionManager2,
		wca.CLSCTX_ALL,
		nil,
		&audioSessionManager2,
	); err != nil {

		sf.logger.Warnw("Failed to activate endpoint as IAudioSessionManager2", "error", err)
		return fmt.Errorf("activate endpoint: %w", err)
	}

	// Only register session notification once per active device lifecycle, and keep the manager + enumerator alive
	// until unregistered (releasing IAudioSessionManager2 without UnregisterSessionNotification leaks in AudioSes.dll).
	keepManagerAlive := false
	if needRegisterNotifications {
		if err := sf.registerSessionNotification(audioSessionManager2); err != nil {
			sf.logger.Debugw("Failed to register session notification for audio output device", "error", err)
		} else {
			keepManagerAlive = true
		}
	}
	if !keepManagerAlive {
		defer audioSessionManager2.Release()
	}

	// get its IAudioSessionEnumerator
	var sessionEnumerator *wca.IAudioSessionEnumerator

	if err := audioSessionManager2.GetSessionEnumerator(&sessionEnumerator); err != nil {
		if keepManagerAlive {
			_ = audioSessionManager2.UnregisterSessionNotification(sf.sessionNotification)
			audioSessionManager2.Release()
		}
		return err
	}
	if keepManagerAlive {
		sf.registeredSessionManagers = append(sf.registeredSessionManagers, registeredSessionManager{
			manager:    audioSessionManager2,
			enumerator: sessionEnumerator,
		})
	} else {
		defer sessionEnumerator.Release()
	}

	// check how many audio sessions there are
	var sessionCount int

	if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
		sf.logger.Warnw("Failed to get session count from session enumerator", "error", err)
		return fmt.Errorf("get session count: %w", err)
	}

	sf.logger.Debugw("Got session count from session enumerator", "count", sessionCount)

	// for each session:
	for sessionIdx := 0; sessionIdx < sessionCount; sessionIdx++ {

		// get the IAudioSessionControl
		var audioSessionControl *wca.IAudioSessionControl
		if err := sessionEnumerator.GetSession(sessionIdx, &audioSessionControl); err != nil {
			sf.logger.Warnw("Failed to get session from session enumerator",
				"error", err,
				"sessionIdx", sessionIdx)

			return fmt.Errorf("get session %d from enumerator: %w", sessionIdx, err)
		}

		// query its IAudioSessionControl2
		dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
		// we no longer need the IAudioSessionControl, release it unconditionally
		audioSessionControl.Release()

		if err != nil {
			sf.logger.Warnw("Failed to query session's IAudioSessionControl2",
				"error", err,
				"sessionIdx", sessionIdx)

			return fmt.Errorf("query session %d IAudioSessionControl2: %w", sessionIdx, err)
		}

		// receive a useful object instead of our dispatch
		audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))

		// skip expired sessions so we don't hold dead COM handles
		var state uint32
		if err := audioSessionControl2.GetState(&state); err == nil && state == wca.AudioSessionStateExpired {
			audioSessionControl2.Release()
			continue
		}

		var pid uint32

		// get the session's PID
		if err := audioSessionControl2.GetProcessId(&pid); err != nil {

			// if this is the system sounds session, GetProcessId will error with an undocumented
			// AUDCLNT_S_NO_CURRENT_PROCESS (0x889000D) - this is fine, we actually want to treat it a bit differently
			isSystemSoundsErr := audioSessionControl2.IsSystemSoundsSession()
			if isSystemSoundsErr != nil && !strings.Contains(err.Error(), "143196173") {

				// of course, if it's not the system sounds session, we got a problem
				sf.logger.Warnw("Failed to query session's pid",
					"error", err,
					"isSystemSoundsError", isSystemSoundsErr,
					"sessionIdx", sessionIdx)

				audioSessionControl2.Release()
				return fmt.Errorf("query session %d pid: %w", sessionIdx, err)
			}

			// update 2020/08/31: this is also the exact case for UWP applications, so we should no longer override the PID.
			// it will successfully update whenever we call GetProcessId for e.g. Video.UI.exe, despite the error being non-nil.
		}

		// get its ISimpleAudioVolume
		dispatch, err = audioSessionControl2.QueryInterface(wca.IID_ISimpleAudioVolume)
		if err != nil {
			sf.logger.Warnw("Failed to query session's ISimpleAudioVolume",
				"error", err,
				"sessionIdx", sessionIdx)

			audioSessionControl2.Release()
			return fmt.Errorf("query session %d ISimpleAudioVolume: %w", sessionIdx, err)
		}

		// make it useful, again
		simpleAudioVolume := (*wca.ISimpleAudioVolume)(unsafe.Pointer(dispatch))

		// create the deej session object
		newSession, err := newWCASession(sf.sessionLogger, audioSessionControl2, simpleAudioVolume, pid, sf.eventCtx)
		if err != nil {
			audioSessionControl2.Release()
			simpleAudioVolume.Release()

			// this could just mean this process is already closed by now, and the session will be cleaned up later by the OS
			if !errors.Is(err, errNoSuchProcess) {
				sf.logger.Warnw("Failed to create new WCA session instance",
					"error", err,
					"sessionIdx", sessionIdx)

				return fmt.Errorf("create wca session for session %d: %w", sessionIdx, err)
			}

			// in this case, log it and skip to the next one
			sf.logger.Debugw("Process already exited, skipping session and releasing handles", "pid", pid)
			continue
		}

		currentSessionCounts[pid]++

		// add it to our slice
		*sessions = append(*sessions, newSession)
	}

	return nil
}

// HasSessionChanges performs a fast, allocation-free COM check to determine whether any new audio sessions
// have spawned, any tracked sessions have expired/closed, or audio devices have changed since the last refresh.
func (sf *wcaSessionFinder) HasSessionChanges() bool {
	if (sf.masterOut != nil && sf.masterOut.stale) || (sf.masterIn != nil && sf.masterIn.stale) {
		return true
	}

	sf.stateLock.Lock()
	devicesStale := sf.devicesStale
	lastDeviceCount := sf.lastDeviceCount
	lastCounts := sf.lastSessionCounts
	sf.stateLock.Unlock()

	if devicesStale || lastCounts == nil {
		return true
	}

	if sf.mmDeviceEnumerator == nil {
		return true
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit, err := sf.coInitialize()
	if err != nil {
		return false
	}
	defer uninit()

	var deviceCollection *wca.IMMDeviceCollection
	if err := sf.mmDeviceEnumerator.EnumAudioEndpoints(wca.EAll, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		return false
	}
	defer deviceCollection.Release()

	var deviceCount uint32
	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		return false
	}

	if deviceCount != lastDeviceCount {
		return true
	}

	currentCounts := make(map[uint32]int, len(lastCounts))

	for deviceIdx := uint32(0); deviceIdx < deviceCount; deviceIdx++ {
		var endpoint *wca.IMMDevice
		if err := deviceCollection.Item(deviceIdx, &endpoint); err != nil {
			return true
		}

		dispatch, err := endpoint.QueryInterface(wca.IID_IMMEndpoint)
		if err != nil {
			endpoint.Release()
			return true
		}
		endpointType := (*wca.IMMEndpoint)(unsafe.Pointer(dispatch))

		var dataFlow uint32
		err = endpointType.GetDataFlow(&dataFlow)
		endpointType.Release()
		if err != nil {
			endpoint.Release()
			return true
		}

		if dataFlow != wca.ERender {
			endpoint.Release()
			continue
		}

		var mgr *wca.IAudioSessionManager2
		if err := endpoint.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &mgr); err != nil {
			endpoint.Release()
			return true
		}
		endpoint.Release()

		var sessionEnumerator *wca.IAudioSessionEnumerator
		if err := mgr.GetSessionEnumerator(&sessionEnumerator); err != nil {
			mgr.Release()
			return true
		}

		var sessionCount int
		if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
			sessionEnumerator.Release()
			mgr.Release()
			return true
		}

		for sessionIdx := 0; sessionIdx < sessionCount; sessionIdx++ {
			var audioSessionControl *wca.IAudioSessionControl
			if err := sessionEnumerator.GetSession(sessionIdx, &audioSessionControl); err != nil {
				sessionEnumerator.Release()
				mgr.Release()
				return true
			}

			dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
			audioSessionControl.Release()
			if err != nil {
				sessionEnumerator.Release()
				mgr.Release()
				return true
			}

			audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))

			var state uint32
			if err := audioSessionControl2.GetState(&state); err == nil && state == wca.AudioSessionStateExpired {
				audioSessionControl2.Release()
				continue
			}

			var pid uint32
			if err := audioSessionControl2.GetProcessId(&pid); err != nil {
				isSystemSoundsErr := audioSessionControl2.IsSystemSoundsSession()
				if isSystemSoundsErr != nil && !strings.Contains(err.Error(), "143196173") {
					audioSessionControl2.Release()
					continue
				}
			}
			audioSessionControl2.Release()

			if pid != 0 && !processExists(pid) {
				continue
			}

			currentCounts[pid]++
		}

		sessionEnumerator.Release()
		mgr.Release()
	}

	if len(currentCounts) != len(lastCounts) {
		return true
	}

	for pid, count := range currentCounts {
		if lastCounts[pid] != count {
			return true
		}
	}

	return false
}

func (sf *wcaSessionFinder) defaultDeviceChangedCallback(
	this *wca.IMMNotificationClient,
	EDataFlow, eRole uint32,
	lpcwstr uintptr,
) (hResult uintptr) {

	// filter out calls that happen in rapid succession
	now := time.Now()

	if sf.lastDefaultDeviceChange.Add(minDefaultDeviceChangeThreshold).After(now) {
		return 0
	}

	sf.lastDefaultDeviceChange = now

	sf.logger.Debug("Default audio device changed, marking master sessions as stale")
	if sf.masterOut != nil {
		sf.masterOut.markAsStale()
	}

	if sf.masterIn != nil {
		sf.masterIn.markAsStale()
	}

	sf.stateLock.Lock()
	sf.devicesStale = true
	sf.stateLock.Unlock()

	sf.notifySessionEvent()

	return 0
}

func (sf *wcaSessionFinder) deviceStateChangedCallback(
	this *wca.IMMNotificationClient,
	pwstrDeviceId uintptr,
	dwNewState uint32,
) (hResult uintptr) {
	sf.stateLock.Lock()
	sf.devicesStale = true
	sf.stateLock.Unlock()

	sf.notifySessionEvent()
	return 0
}

func (sf *wcaSessionFinder) deviceAddedOrRemovedCallback(
	this *wca.IMMNotificationClient,
	pwstrDeviceId uintptr,
) (hResult uintptr) {
	sf.stateLock.Lock()
	sf.devicesStale = true
	sf.stateLock.Unlock()

	sf.notifySessionEvent()
	return 0
}

func (sf *wcaSessionFinder) registerSessionNotification(manager *wca.IAudioSessionManager2) error {
	if sf.sessionNotification == nil {
		sf.sessionNotificationVtbl = &wca.IAudioSessionNotificationVtbl{
			QueryInterface:   sf.cbQueryInterface,
			AddRef:           sf.cbAddRef,
			Release:          sf.cbRelease,
			OnSessionCreated: sf.cbSessionCreated,
		}
		sf.sessionNotification = &wca.IAudioSessionNotification{
			VTable: (*wca.IAudioSessionEventsVtbl)(unsafe.Pointer(sf.sessionNotificationVtbl)),
		}
	}

	if err := manager.RegisterSessionNotification(sf.sessionNotification); err != nil {
		return fmt.Errorf("register session notification: %w", err)
	}

	return nil
}

func (sf *wcaSessionFinder) sessionCreatedCallback(
	this *wca.IAudioSessionNotification,
	newSession *wca.IAudioSessionControl,
) (hResult uintptr) {
	sf.logger.Debug("New audio session created notification received")
	sf.notifySessionEvent()
	return 0
}

func (sf *wcaSessionFinder) notifySessionEvent() {
	select {
	case sf.sessionEventsChannel <- struct{}{}:
	default:
	}
}

// SubscribeToSessionEvents returns a channel that receives signals when sessions are created or modified
func (sf *wcaSessionFinder) SubscribeToSessionEvents() <-chan struct{} {
	return sf.sessionEventsChannel
}

func (sf *wcaSessionFinder) queryInterfaceCallback(
	this uintptr,
	riid *ole.GUID,
	ppvObject *uintptr,
) (hResult uintptr) {
	if ppvObject == nil {
		return 0x80004003 // E_POINTER
	}

	if riid != nil && (ole.IsEqualGUID(riid, ole.IID_IUnknown) ||
		ole.IsEqualGUID(riid, wca.IID_IAudioSessionNotification) ||
		ole.IsEqualGUID(riid, wca.IID_IMMNotificationClient)) {
		*ppvObject = this
		return uintptr(ole.S_OK)
	}

	*ppvObject = 0
	return uintptr(ole.E_NOINTERFACE)
}

func (sf *wcaSessionFinder) addRefCallback(this uintptr) (hResult uintptr) {
	return 1
}

func (sf *wcaSessionFinder) releaseCallback(this uintptr) (hResult uintptr) {
	return 1
}

func (sf *wcaSessionFinder) noopCallback() (hResult uintptr) {
	return 0
}
