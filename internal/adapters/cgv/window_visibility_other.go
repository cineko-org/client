//go:build !darwin

package cgv

func hideBrowserApplication(int) error { return nil }

func showBrowserApplication(int) error { return nil }

func browserApplicationHidden(int) (bool, error) { return true, nil }
