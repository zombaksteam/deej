package deej

import (
	"fmt"

	"github.com/getlantern/systray"

	"github.com/omriharel/deej/pkg/deej/icon"
	"github.com/omriharel/deej/pkg/deej/util"
)

func (d *Deej) initializeTray(onDone func()) {
	logger := d.logger.Named("tray")

	onReady := func() {
		logger.Debug("Tray instance ready")

		systray.SetTemplateIcon(icon.DeejLogo, icon.DeejLogo)
		systray.SetTitle("Deej")
		systray.SetTooltip("Deej")

		versionText := d.version
		if versionText == "" {
			versionText = fmt.Sprintf("Version %s", Version)
		}
		versionItem := systray.AddMenuItem(versionText, fmt.Sprintf("Open GitHub repository (%s)", RepoURL))
		versionItem.SetIcon(icon.DeejLogo)

		systray.AddSeparator()

		streamPCModeItem := systray.AddMenuItem("Stream PC Mode", "Lock master volume to single slider and set mapped apps to 100%")
		if d.config.EnableStreamPCSwitching {
			if d.config.StreamPCMode {
				streamPCModeItem.Check()
			} else {
				streamPCModeItem.Uncheck()
			}
		} else {
			streamPCModeItem.Hide()
		}

		editConfig := systray.AddMenuItem("Edit configuration", "Open config file with notepad")
		editConfig.SetIcon(icon.EditConfig)

		refreshSessions := systray.AddMenuItem("Re-scan audio sessions", "Manually refresh audio sessions if something's stuck")
		refreshSessions.SetIcon(icon.RefreshSessions)

		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Stop deej and quit")

		configReloadedCh := d.config.SubscribeToChanges()

		// wait on things to happen
		go func() {
			for {
				select {

				// version info / repo link
				case <-versionItem.ClickedCh:
					logger.Info("Version menu item clicked, opening GitHub repository in browser")

					if err := util.OpenURL(logger, RepoURL); err != nil {
						logger.Warnw("Failed to open repository URL", "error", err)
					}

				// stream pc mode toggle
				case <-streamPCModeItem.ClickedCh:
					if !d.config.EnableStreamPCSwitching {
						continue
					}
					newVal := !d.config.StreamPCMode
					logger.Infow("Stream PC Mode menu item clicked", "newVal", newVal)

					if newVal {
						streamPCModeItem.Check()
					} else {
						streamPCModeItem.Uncheck()
					}

					if err := d.SetStreamPCMode(newVal); err != nil {
						logger.Warnw("Failed to update Stream PC Mode", "error", err)
					}

				// config reloaded externally
				case <-configReloadedCh:
					if d.config.EnableStreamPCSwitching {
						streamPCModeItem.Show()
						if d.config.StreamPCMode {
							streamPCModeItem.Check()
						} else {
							streamPCModeItem.Uncheck()
						}
					} else {
						streamPCModeItem.Hide()
					}


				// quit
				case <-quit.ClickedCh:
					logger.Info("Quit menu item clicked, stopping")

					d.signalStop()

				// edit config
				case <-editConfig.ClickedCh:
					logger.Info("Edit config menu item clicked, opening config for editing")

					editor := "notepad.exe"
					if util.Linux() {
						editor = "gedit"
					}

					if err := util.OpenExternal(logger, editor, userConfigFilepath); err != nil {
						logger.Warnw("Failed to open config file for editing", "error", err)
					}

				// refresh sessions
				case <-refreshSessions.ClickedCh:
					logger.Info("Refresh sessions menu item clicked, triggering session map refresh")

					// performance: the reason that forcing a refresh here is okay is that users can't spam the
					// right-click -> select-this-option sequence at a rate that's meaningful to performance
					d.sessions.refreshSessions(true)
				}
			}
		}()


		// actually start the main runtime
		onDone()
	}

	onExit := func() {
		logger.Debug("Tray exited")
	}

	// start the tray icon
	logger.Debug("Running in tray")
	systray.Run(onReady, onExit)
}

func (d *Deej) stopTray() {
	d.logger.Debug("Quitting tray")
	systray.Quit()
}
