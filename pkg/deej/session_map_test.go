package deej

import (
	"sync"
	"testing"
)

func TestIsPathTarget(t *testing.T) {
	tests := []struct {
		target   string
		expected bool
	}{
		{"chrome.exe", false},
		{"master", false},
		{"system", false},
		{"mic", false},
		{"deej.unmapped", false},
		{"deej.current", false},
		{"Speakers (Realtek High Definition Audio)", false},
		{`C:\Program Files\Steam\*`, true},
		{`c:/games/*`, true},
		{`*.exe`, true},
		{`path/to/app.exe`, true},
		{`C:\app.exe`, true},
	}

	for _, tt := range tests {
		result := isPathTarget(tt.target)
		if result != tt.expected {
			t.Errorf("isPathTarget(%q) = %v; want %v", tt.target, result, tt.expected)
		}
	}
}

func TestMatchPathTarget(t *testing.T) {
	tests := []struct {
		name        string
		sessionPath string
		target      string
		expected    bool
	}{
		{
			name:        "Steam game 1 with backslashes",
			sessionPath: `C:\Program Files (x86)\Steam\steamapps\common\Path of Exile\PathOfExile_x64.exe`,
			target:      `C:\Program Files (x86)\Steam\steamapps\common\*`,
			expected:    true,
		},
		{
			name:        "Steam game 2 from another folder in same common root",
			sessionPath: `C:\Program Files (x86)\Steam\steamapps\common\Rocket League\Binaries\Win64\RocketLeague.exe`,
			target:      `C:\Program Files (x86)\Steam\steamapps\common\*`,
			expected:    true,
		},
		{
			name:        "Config with forward slashes matching Windows backslash path",
			sessionPath: `C:\Games\MyGame\game.exe`,
			target:      `c:/games/*`,
			expected:    true,
		},
		{
			name:        "Long path prefix \\\\?\\ on Windows",
			sessionPath: `\\?\C:\Program Files (x86)\Steam\steamapps\common\Dota 2\dota2.exe`,
			target:      `C:\Program Files (x86)\Steam\steamapps\common\*`,
			expected:    true,
		},
		{
			name:        "Case insensitivity",
			sessionPath: `c:\program files\games\GAME.EXE`,
			target:      `C:\PROGRAM FILES\GAMES\*`,
			expected:    true,
		},
		{
			name:        "Different directory should not match",
			sessionPath: `C:\Windows\System32\audiodg.exe`,
			target:      `C:\Program Files (x86)\Steam\steamapps\common\*`,
			expected:    false,
		},
		{
			name:        "Empty session path should not match",
			sessionPath: ``,
			target:      `C:\Games\*`,
			expected:    false,
		},
		{
			name:        "Exact path match",
			sessionPath: `C:\Games\MyGame\game.exe`,
			target:      `C:\Games\MyGame\game.exe`,
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPathTarget(tt.sessionPath, tt.target)
			if result != tt.expected {
				t.Errorf("matchPathTarget(%q, %q) = %v; want %v", tt.sessionPath, tt.target, result, tt.expected)
			}
		})
	}
}

type mockSession struct {
	key    string
	path   string
	volume float32
}

func (m *mockSession) Key() string {
	return m.key
}

func (m *mockSession) Path() string {
	return m.path
}

func (m *mockSession) GetVolume() float32 {
	return m.volume
}

func (m *mockSession) SetVolume(v float32) error {
	m.volume = v
	return nil
}

func (m *mockSession) Release() {}

func TestFindSliderForSession(t *testing.T) {
	sm := newSliderMap()
	sm.set(0, []string{"master"})
	sm.set(1, []string{"spotify.exe", "chrome.exe"})
	sm.set(2, []string{`C:/Games/*`})
	sm.set(3, []string{"mic"})
	sm.set(4, []string{"deej.unmapped"})

	sessionMapInstance := &sessionMap{
		m:                make(map[string][]Session),
		lock:             &sync.Mutex{},
		sliderValues:     make(map[int]float32),
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: &CanonicalConfig{
				SliderMapping: sm,
			},
		},
	}

	tests := []struct {
		name           string
		session        Session
		expectedSlider int
		expectedFound  bool
	}{
		{
			name:           "Master session",
			session:        &mockSession{key: "master"},
			expectedSlider: 0,
			expectedFound:  true,
		},
		{
			name:           "Spotify exe direct mapping",
			session:        &mockSession{key: "spotify.exe", path: `C:\Users\User\AppData\Local\Spotify\Spotify.exe`},
			expectedSlider: 1,
			expectedFound:  true,
		},
		{
			name:           "Steam game in games directory",
			session:        &mockSession{key: "cs2.exe", path: `C:\Games\Steam\steamapps\common\Counter-Strike\game.exe`},
			expectedSlider: 2,
			expectedFound:  true,
		},
		{
			name:           "Mic input session",
			session:        &mockSession{key: "mic"},
			expectedSlider: 3,
			expectedFound:  true,
		},
		{
			name:           "Unmapped process (discord.exe)",
			session:        &mockSession{key: "discord.exe", path: `C:\Users\User\AppData\Local\Discord\Discord.exe`},
			expectedSlider: 4,
			expectedFound:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slider, found := sessionMapInstance.findSliderForSession(tt.session)
			if found != tt.expectedFound {
				t.Fatalf("findSliderForSession(%s) found = %v; want %v", tt.name, found, tt.expectedFound)
			}
			if found && slider != tt.expectedSlider {
				t.Errorf("findSliderForSession(%s) slider = %d; want %d", tt.name, slider, tt.expectedSlider)
			}
		})
	}
}

func TestApplyStoredSliderVolume(t *testing.T) {
	sm := newSliderMap()
	sm.set(0, []string{"master"})
	sm.set(1, []string{"spotify.exe"})
	sm.set(2, []string{`C:/Games/*`})
	sm.set(3, []string{"deej.unmapped"})

	sessionMapInstance := &sessionMap{
		m:                make(map[string][]Session),
		lock:             &sync.Mutex{},
		sliderValues:     map[int]float32{0: 0.80, 1: 0.50, 2: 0.35, 3: 0.10},
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: &CanonicalConfig{
				SliderMapping: sm,
			},
		},
	}

	// 1. Newly spawned game process in C:/Games/
	gameSession := &mockSession{key: "game.exe", path: `C:\Games\SubFolder\game.exe`, volume: 1.0}
	sessionMapInstance.applyStoredSliderVolume(gameSession)
	if gameSession.GetVolume() != 0.35 {
		t.Errorf("gameSession volume = %f; want 0.35", gameSession.GetVolume())
	}

	// 2. Newly spawned unmapped app
	unmappedSession := &mockSession{key: "notepad.exe", volume: 1.0}
	sessionMapInstance.applyStoredSliderVolume(unmappedSession)
	if unmappedSession.GetVolume() != 0.10 {
		t.Errorf("unmappedSession volume = %f; want 0.10", unmappedSession.GetVolume())
	}

	// 3. Spotify
	spotifySession := &mockSession{key: "spotify.exe", volume: 0.9}
	sessionMapInstance.applyStoredSliderVolume(spotifySession)
	if spotifySession.GetVolume() != 0.50 {
		t.Errorf("spotifySession volume = %f; want 0.50", spotifySession.GetVolume())
	}
}
