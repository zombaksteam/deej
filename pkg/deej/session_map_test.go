package deej

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spf13/viper"
	"go.uber.org/zap"
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
	master bool
	system bool
}

func (m *mockSession) Key() string {
	return m.key
}

func (m *mockSession) Path() string {
	return m.path
}

func (m *mockSession) Master() bool {
	return m.master
}

func (m *mockSession) System() bool {
	return m.system
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
			session:        &mockSession{key: "master", master: true},
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

func TestStreamPCMode_MaximizeMappedAppVolumesOnly(t *testing.T) {
	sm := newSliderMap()
	sm.set(2, []string{"discord.exe"})
	sm.set(3, []string{"chrome.exe", `C:\Games\*`})
	sm.set(4, []string{"music.exe"})

	masterSess := &mockSession{key: "master", master: true, volume: 0.50}
	discordSess := &mockSession{key: "discord.exe", volume: 0.40}
	chromeSess := &mockSession{key: "chrome.exe", volume: 0.30}
	gameSess := &mockSession{key: "speed.exe", path: `C:\Games\NFS\speed.exe`, volume: 0.25}
	unrelatedSess := &mockSession{key: "unrelated.exe", volume: 0.45} // Not in slider_mapping!

	sessionMapInstance := &sessionMap{
		m: map[string][]Session{
			"master":        {masterSess},
			"discord.exe":   {discordSess},
			"chrome.exe":    {chromeSess},
			"speed.exe":     {gameSess},
			"unrelated.exe": {unrelatedSess},
		},
		lock:             &sync.Mutex{},
		sliderValues:     map[int]float32{2: 0.40, 3: 0.85, 4: 0.60},
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: &CanonicalConfig{
				SliderMapping: sm,
				MasterMapping: 3,
				StreamPCMode:  true,
			},
		},
	}

	// Activate Stream PC Mode
	sessionMapInstance.onStreamPCModeChanged(true)

	// Master volume must become slider 3's volume (0.85)
	if masterSess.GetVolume() != 0.85 {
		t.Errorf("masterSess volume = %f; want 0.85", masterSess.GetVolume())
	}

	// Mapped processes (discord, chrome, game in C:\Games\*) must become 1.0 (100%)
	if discordSess.GetVolume() != 1.0 {
		t.Errorf("discordSess volume = %f; want 1.0", discordSess.GetVolume())
	}
	if chromeSess.GetVolume() != 1.0 {
		t.Errorf("chromeSess volume = %f; want 1.0", chromeSess.GetVolume())
	}
	if gameSess.GetVolume() != 1.0 {
		t.Errorf("gameSess volume = %f; want 1.0", gameSess.GetVolume())
	}

	// Unrelated process NOT in slider_mapping must NOT be touched
	if unrelatedSess.GetVolume() != 0.45 {
		t.Errorf("unrelatedSess volume = %f; want 0.45 (should not be modified)", unrelatedSess.GetVolume())
	}
}

func TestStreamPCMode_SpawnNewProcess(t *testing.T) {
	sm := newSliderMap()
	sm.set(2, []string{"discord.exe"})
	sm.set(3, []string{"chrome.exe", `C:\Games\*`})

	sessionMapInstance := &sessionMap{
		m:                make(map[string][]Session),
		lock:             &sync.Mutex{},
		sliderValues:     map[int]float32{2: 0.40, 3: 0.70},
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: &CanonicalConfig{
				SliderMapping: sm,
				MasterMapping: 3,
				StreamPCMode:  true,
			},
		},
	}

	// 1. Newly spawned mapped process (e.g. game launched with default 0.30 volume)
	newGame := &mockSession{key: "game.exe", path: `C:\Games\Sub\game.exe`, volume: 0.30}
	sessionMapInstance.applyStoredSliderVolume(newGame)
	if newGame.GetVolume() != 1.0 {
		t.Errorf("newGame volume = %f; want 1.0", newGame.GetVolume())
	}

	// 2. Newly spawned unmapped process (e.g. calculator launched with 0.20 volume)
	calc := &mockSession{key: "calc.exe", path: `C:\Windows\calc.exe`, volume: 0.20}
	sessionMapInstance.applyStoredSliderVolume(calc)
	if calc.GetVolume() != 0.20 {
		t.Errorf("calc volume = %f; want 0.20 (untouched)", calc.GetVolume())
	}

	// 3. Newly detected master endpoint in Stream PC Mode
	masterEndpoint := &mockSession{key: "master", master: true, volume: 0.10}
	sessionMapInstance.applyStoredSliderVolume(masterEndpoint)
	if masterEndpoint.GetVolume() != 0.70 {
		t.Errorf("masterEndpoint volume = %f; want 0.70 (slider 3 value)", masterEndpoint.GetVolume())
	}
}

func TestStreamPCMode_MasterSliderMove(t *testing.T) {
	sm := newSliderMap()
	sm.set(2, []string{"discord.exe"})
	sm.set(3, []string{"chrome.exe"})

	masterSess := &mockSession{key: "master", master: true, volume: 0.50}
	chromeSess := &mockSession{key: "chrome.exe", volume: 1.0}
	discordSess := &mockSession{key: "discord.exe", volume: 1.0}

	sessionMapInstance := &sessionMap{
		m: map[string][]Session{
			"master":      {masterSess},
			"chrome.exe":  {chromeSess},
			"discord.exe": {discordSess},
		},
		lock:             &sync.Mutex{},
		sliderValues:     map[int]float32{2: 0.50, 3: 0.50},
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: &CanonicalConfig{
				SliderMapping: sm,
				MasterMapping: 3,
				StreamPCMode:  true,
			},
		},
	}

	// Move slider 2 (mapped to discord in normal mode) -> should be ignored in Stream PC Mode
	sessionMapInstance.handleSliderMoveEvent(SliderMoveEvent{SliderID: 2, PercentValue: 0.20})
	if discordSess.GetVolume() != 1.0 {
		t.Errorf("discordSess volume = %f; want 1.0 (slider 2 should be ignored in Stream PC Mode)", discordSess.GetVolume())
	}
	if masterSess.GetVolume() != 0.50 {
		t.Errorf("masterSess volume = %f; want 0.50", masterSess.GetVolume())
	}

	// Move slider 3 (master_mapping) -> should adjust master volume exclusively
	sessionMapInstance.handleSliderMoveEvent(SliderMoveEvent{SliderID: 3, PercentValue: 0.95})
	if masterSess.GetVolume() != 0.95 {
		t.Errorf("masterSess volume = %f; want 0.95", masterSess.GetVolume())
	}
	if chromeSess.GetVolume() != 1.0 {
		t.Errorf("chromeSess volume = %f; want 1.0 (chrome should stay 100%%)", chromeSess.GetVolume())
	}
}

func TestStreamPCMode_ToggleBackToNormal(t *testing.T) {
	sm := newSliderMap()
	sm.set(2, []string{"discord.exe"})
	sm.set(3, []string{"chrome.exe"})

	discordSess := &mockSession{key: "discord.exe", volume: 1.0}
	chromeSess := &mockSession{key: "chrome.exe", volume: 1.0}

	cfg := &CanonicalConfig{
		SliderMapping: sm,
		MasterMapping: 3,
		StreamPCMode:  true,
	}

	sessionMapInstance := &sessionMap{
		m: map[string][]Session{
			"discord.exe": {discordSess},
			"chrome.exe":  {chromeSess},
		},
		lock:             &sync.Mutex{},
		sliderValues:     map[int]float32{2: 0.40, 3: 0.65},
		sliderValuesLock: &sync.RWMutex{},
		deej: &Deej{
			config: cfg,
		},
	}

	// Turn off Stream PC Mode
	cfg.StreamPCMode = false
	sessionMapInstance.onStreamPCModeChanged(false)

	// Volumes should be restored to their stored slider positions
	if discordSess.GetVolume() != 0.40 {
		t.Errorf("discordSess volume = %f; want 0.40", discordSess.GetVolume())
	}
	if chromeSess.GetVolume() != 0.65 {
		t.Errorf("chromeSess volume = %f; want 0.65", chromeSess.GetVolume())
	}
}

func TestConfig_MasterMappingAndPreferences(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "deej-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	userCfgPath := filepath.Join(tmpDir, "config.yaml")
	prefCfgPath := filepath.Join(tmpDir, "preferences.yaml")

	userCfgContent := `
slider_mapping:
  2: discord.exe
  3: chrome.exe
master_mapping: 3
`
	if err := os.WriteFile(userCfgPath, []byte(userCfgContent), 0644); err != nil {
		t.Fatalf("Failed to write test config.yaml: %v", err)
	}

	prefCfgContent := `
stream_pc_mode: true
`
	if err := os.WriteFile(prefCfgPath, []byte(prefCfgContent), 0644); err != nil {
		t.Fatalf("Failed to write test preferences.yaml: %v", err)
	}

	uViper := viper.New()
	uViper.SetConfigFile(userCfgPath)
	if err := uViper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig user: %v", err)
	}

	iViper := viper.New()
	iViper.SetConfigFile(prefCfgPath)
	if err := iViper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig pref: %v", err)
	}

	cfg := &CanonicalConfig{
		userConfig:     uViper,
		internalConfig: iViper,
		logger:         zap.NewNop().Sugar(),
	}

	if err := cfg.populateFromVipers(); err != nil {
		t.Fatalf("populateFromVipers: %v", err)
	}

	if cfg.MasterMapping != 3 {
		t.Errorf("cfg.MasterMapping = %d; want 3", cfg.MasterMapping)
	}

	if !cfg.StreamPCMode {
		t.Errorf("cfg.StreamPCMode = %v; want true", cfg.StreamPCMode)
	}

	// Test updating preference
	cfg.StreamPCMode = false
	cfg.internalConfig.Set(configKeyStreamPCMode, false)
	if err := cfg.internalConfig.WriteConfig(); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	// Re-read
	iViper2 := viper.New()
	iViper2.SetConfigFile(prefCfgPath)
	if err := iViper2.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig pref 2: %v", err)
	}
	if iViper2.GetBool("stream_pc_mode") != false {
		t.Errorf("saved stream_pc_mode = %v; want false", iViper2.GetBool("stream_pc_mode"))
	}
}


