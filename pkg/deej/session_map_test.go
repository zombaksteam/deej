package deej

import (
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
