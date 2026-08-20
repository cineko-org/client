package cgv

// ProcessPID returns the Playwright driver process that owns this adapter.
func (adapter *Adapter) ProcessPID() int {
	if adapter == nil {
		return 0
	}
	return adapter.processPID
}

// ProcessProfileDir returns the persistent profile assigned to this adapter.
func (adapter *Adapter) ProcessProfileDir() string {
	if adapter == nil {
		return ""
	}
	return adapter.profileDir
}

// ProcessPageCount reports the number of pages currently owned by the adapter.
func (adapter *Adapter) ProcessPageCount() int {
	if adapter == nil || adapter.browserContext == nil {
		return 0
	}
	return len(adapter.browserContext.Pages())
}

// ProcessReady reports whether the adapter completed its single-page setup.
func (adapter *Adapter) ProcessReady() bool {
	return adapter != nil && adapter.processPID > 0 && adapter.ProcessPageCount() == 1 &&
		adapter.profileDir != "" && adapter.page != nil
}

// ProcessCrashed signals an unexpected browser-context close.
func (adapter *Adapter) ProcessCrashed() <-chan error {
	if adapter == nil {
		return nil
	}
	return adapter.processCrashed
}

// ProcessNeedsForcedReap reports whether transport shutdown returned before
// its driver wait. Callers must kill the tree before WaitProcess in that case.
func (adapter *Adapter) ProcessNeedsForcedReap() bool {
	if adapter == nil {
		return false
	}
	adapter.lifecycleMu.Lock()
	defer adapter.lifecycleMu.Unlock()
	return adapter.fallbackWait
}

// WaitProcess waits for the Playwright driver to stop after Close or a crash.
// Playwright's transport owns the normal cmd.Wait call. Only when transport
// shutdown returned before that wait can this method perform the single
// explicit root-process reap after KillProcessTree signals the fallback.
func (adapter *Adapter) WaitProcess() error {
	if adapter == nil || adapter.processDone == nil {
		return nil
	}
	adapter.processWaitOnce.Do(func() {
		defer close(adapter.processWaitDone)
		<-adapter.closeAttemptDone
		adapter.lifecycleMu.Lock()
		fallbackWait := adapter.fallbackWait
		adapter.lifecycleMu.Unlock()
		var waitErr error
		if fallbackWait {
			<-adapter.forceWait
			waitErr = waitRootProcess(adapter.processPID)
		} else {
			<-adapter.processDone
		}
		adapter.lifecycleMu.Lock()
		adapter.processWaitErr = waitErr
		adapter.lifecycleMu.Unlock()
		adapter.markProcessReaped(waitErr)
	})
	<-adapter.processWaitDone
	adapter.lifecycleMu.Lock()
	defer adapter.lifecycleMu.Unlock()
	return adapter.processWaitErr
}

// KillProcessTree forcefully stops the driver and all of its browser children.
// It deliberately does not reap the root: the Playwright transport owns the
// normal wait, while WaitProcess owns the fallback wait after this signal.
func (adapter *Adapter) KillProcessTree() error {
	if adapter == nil {
		return nil
	}
	err := killProcessTree(adapter.processPID)
	adapter.forceWaitOnce.Do(func() { close(adapter.forceWait) })
	return err
}
