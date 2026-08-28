package util

import (
	"errors"
	"os/exec"
)

func getCurrentWindowProcessNames() ([]string, error) {
	return nil, errors.New("Not implemented")
}

func openURLPlatform(url string) error {
	cmd := exec.Command("xdg-open", url)
	return cmd.Run()
}
