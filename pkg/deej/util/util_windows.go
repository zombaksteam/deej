package util

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"github.com/mitchellh/go-ps"
)

const (
	getCurrentWindowInternalCooldown = time.Millisecond * 350
)

var (
	lastGetCurrentWindowResult []string
	lastGetCurrentWindowCall   = time.Now()
)

type enumChildWindowsContext struct {
	ownerPID uint32
	result   []string
}

var enumChildWindowsCallbackPtr = syscall.NewCallback(func(childHWND uintptr, lParam uintptr) uintptr {
	ctx := (*enumChildWindowsContext)(unsafe.Pointer(lParam))
	if ctx == nil {
		return 1
	}

	var childPID uint32
	win.GetWindowThreadProcessId(win.HWND(childHWND), &childPID)

	if childPID != 0 && childPID != ctx.ownerPID {
		actualProcess, err := ps.FindProcess(int(childPID))
		if err == nil && actualProcess != nil {
			ctx.result = append(ctx.result, actualProcess.Executable())
		}
	}

	return 1
})

func getCurrentWindowProcessNames() ([]string, error) {

	// apply an internal cooldown on this function to avoid calling windows API functions too frequently.
	// return a cached value during that cooldown
	now := time.Now()
	if lastGetCurrentWindowCall.Add(getCurrentWindowInternalCooldown).After(now) {
		return lastGetCurrentWindowResult, nil
	}

	lastGetCurrentWindowCall = now

	// get the current foreground window
	hwnd := win.GetForegroundWindow()
	var ownerPID uint32

	// get its PID and put it in our window info struct
	win.GetWindowThreadProcessId(hwnd, &ownerPID)

	// check for system PID (0)
	if ownerPID == 0 {
		return nil, nil
	}

	// find the process name corresponding to the parent PID
	process, err := ps.FindProcess(int(ownerPID))
	if err != nil {
		return nil, fmt.Errorf("get parent process for pid %d: %w", ownerPID, err)
	}
	if process == nil {
		return nil, nil
	}

	ctx := enumChildWindowsContext{
		ownerPID: ownerPID,
		result:   []string{process.Executable()},
	}

	// iterate its child windows, adding their names too
	win.EnumChildWindows(hwnd, enumChildWindowsCallbackPtr, uintptr(unsafe.Pointer(&ctx)))

	// cache & return whichever executable names we ended up with
	lastGetCurrentWindowResult = ctx.result
	return ctx.result, nil
}

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

func openURLPlatform(url string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	urlPtr, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}

	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(urlPtr)),
		0,
		0,
		uintptr(1), // SW_SHOWNORMAL
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW failed: %w (ret code %d)", err, ret)
	}

	return nil
}
