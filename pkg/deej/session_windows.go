package deej

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	ps "github.com/keybase/go-ps"
	wca "github.com/moutend/go-wca"
	"go.uber.org/zap"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procGetExitCodeProcess         = kernel32.NewProc("GetExitCodeProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorInvalidParameter          = syscall.Errno(87)
)

func processExists(pid uint32) bool {
	if pid == 0 {
		return true
	}

	handle, _, _ := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return false
	}
	defer procCloseHandle.Call(handle)

	var exitCode uint32
	ret, _, _ := procGetExitCodeProcess.Call(
		handle,
		uintptr(unsafe.Pointer(&exitCode)),
	)
	if ret != 0 && exitCode != stillActive {
		return false
	}

	return true
}

func getProcessPathByPID(pid uint32) (string, error) {
	handle, _, err := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		if errors.Is(err, errorInvalidParameter) {
			return "", errNoSuchProcess
		}
		return "", err
	}
	defer procCloseHandle.Call(handle)

	var exitCode uint32
	retExit, _, _ := procGetExitCodeProcess.Call(
		handle,
		uintptr(unsafe.Pointer(&exitCode)),
	)
	if retExit != 0 && exitCode != stillActive {
		return "", errNoSuchProcess
	}

	var buf [1024]uint16
	size := uint32(len(buf))

	ret, _, err := procQueryFullProcessImageNameW.Call(
		handle,
		uintptr(0),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", err
	}

	rawPath := syscall.UTF16ToString(buf[:size])
	rawPath = strings.TrimPrefix(rawPath, `\\?\`)
	return rawPath, nil
}

var errNoSuchProcess = errors.New("No such process")
var errRefreshSessions = errors.New("Trigger session refresh")

type wcaSession struct {
	baseSession

	pid         uint32
	processName string

	control *wca.IAudioSessionControl2
	volume  *wca.ISimpleAudioVolume

	eventCtx *ole.GUID
}

type masterSession struct {
	baseSession

	volume *wca.IAudioEndpointVolume

	eventCtx *ole.GUID

	stale bool // when set to true, we should refresh sessions on the next call to SetVolume
}

func newWCASession(
	logger *zap.SugaredLogger,
	control *wca.IAudioSessionControl2,
	volume *wca.ISimpleAudioVolume,
	pid uint32,
	eventCtx *ole.GUID,
) (*wcaSession, error) {

	s := &wcaSession{
		control:  control,
		volume:   volume,
		pid:      pid,
		eventCtx: eventCtx,
	}

	// special treatment for system sounds session
	if pid == 0 {
		s.system = true
		s.name = systemSessionName
		s.humanReadableDesc = "system sounds"
	} else {
		path, err := getProcessPathByPID(pid)
		if errors.Is(err, errNoSuchProcess) {
			logger.Debugw("Process already exited, not creating audio session", "pid", pid)
			return nil, errNoSuchProcess
		}

		if err == nil && path != "" {
			cleanPath := filepath.Clean(strings.TrimPrefix(path, `\\?\`))
			s.baseSession.path = cleanPath
			s.processName = filepath.Base(cleanPath)
		} else {
			// Fallback to toolhelp snapshot if OpenProcess was denied
			process, findErr := ps.FindProcess(int(pid))
			if findErr != nil {
				logger.Warnw("Failed to find process name by ID", "pid", pid, "error", findErr)
				return nil, fmt.Errorf("find process name by pid: %w", findErr)
			}

			if process == nil {
				logger.Debugw("Process already exited, not creating audio session", "pid", pid)
				return nil, errNoSuchProcess
			}

			s.processName = process.Executable()
			if procPath, pathErr := process.Path(); pathErr == nil && procPath != "" {
				s.baseSession.path = filepath.Clean(strings.TrimPrefix(procPath, `\\?\`))
			}
		}

		s.name = s.processName
		s.humanReadableDesc = fmt.Sprintf("%s (pid %d)", s.processName, s.pid)
	}

	// use a self-identifying session name e.g. deej.sessions.chrome
	s.logger = logger.Named(strings.TrimSuffix(s.Key(), ".exe"))
	s.logger.Debugw(sessionCreationLogMessage, "session", s)

	return s, nil
}

func newMasterSession(
	logger *zap.SugaredLogger,
	volume *wca.IAudioEndpointVolume,
	eventCtx *ole.GUID,
	key string,
	loggerKey string,
) (*masterSession, error) {

	s := &masterSession{
		volume:   volume,
		eventCtx: eventCtx,
	}

	s.logger = logger.Named(loggerKey)
	s.master = (key == masterSessionName)
	s.name = key
	s.humanReadableDesc = key

	s.logger.Debugw(sessionCreationLogMessage, "session", s)

	return s, nil
}

func (s *wcaSession) GetVolume() float32 {
	var level float32

	if err := s.volume.GetMasterVolume(&level); err != nil {
		s.logger.Warnw("Failed to get session volume", "error", err)
	}

	return level
}

func (s *wcaSession) SetVolume(v float32) error {
	if err := s.volume.SetMasterVolume(v, s.eventCtx); err != nil {
		s.logger.Warnw("Failed to set session volume", "error", err)
		return fmt.Errorf("adjust session volume: %w", err)
	}

	// mitigate expired sessions by checking the state whenever we change volumes
	var state uint32

	if err := s.control.GetState(&state); err != nil {
		s.logger.Warnw("Failed to get session state while setting volume", "error", err)
		return fmt.Errorf("get session state: %w", err)
	}

	if state == wca.AudioSessionStateExpired {
		s.logger.Warnw("Audio session expired, triggering session refresh")
		return errRefreshSessions
	}

	s.logger.Debugw("Adjusting session volume", "to", fmt.Sprintf("%.2f", v))

	return nil
}

func (s *wcaSession) Release() {
	s.logger.Debug("Releasing audio session")

	s.volume.Release()
	s.control.Release()
}

func (s *wcaSession) String() string {
	return fmt.Sprintf(sessionStringFormat, s.humanReadableDesc, s.GetVolume(), s.Path())
}

func (s *masterSession) GetVolume() float32 {
	var level float32

	if err := s.volume.GetMasterVolumeLevelScalar(&level); err != nil {
		s.logger.Warnw("Failed to get session volume", "error", err)
	}

	return level
}

func (s *masterSession) SetVolume(v float32) error {
	if s.stale {
		s.logger.Warnw("Session expired because default device has changed, triggering session refresh")
		return errRefreshSessions
	}

	if err := s.volume.SetMasterVolumeLevelScalar(v, s.eventCtx); err != nil {
		s.logger.Warnw("Failed to set session volume",
			"error", err,
			"volume", v)

		return fmt.Errorf("adjust session volume: %w", err)
	}

	s.logger.Debugw("Adjusting session volume", "to", fmt.Sprintf("%.2f", v))

	return nil
}

func (s *masterSession) Release() {
	s.logger.Debug("Releasing audio session")

	s.volume.Release()
}

func (s *masterSession) String() string {
	return fmt.Sprintf(sessionStringFormat, s.humanReadableDesc, s.GetVolume(), s.Path())
}

func (s *masterSession) markAsStale() {
	s.stale = true
}
