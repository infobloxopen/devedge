package platform

import "fmt"

// UnsupportedAdapter is returned for platforms without a native service adapter.
type UnsupportedAdapter struct {
	OS string
}

func (u *UnsupportedAdapter) Name() string {
	return fmt.Sprintf("%s (unsupported)", u.OS)
}

func (u *UnsupportedAdapter) Install() error {
	return fmt.Errorf("platform %q does not have a service adapter yet; run devedged manually", u.OS)
}

func (u *UnsupportedAdapter) Uninstall() error {
	return fmt.Errorf("platform %q does not have a service adapter", u.OS)
}

func (u *UnsupportedAdapter) Start() error {
	return fmt.Errorf("platform %q does not have a service adapter; run devedged manually", u.OS)
}

func (u *UnsupportedAdapter) Stop() error {
	return fmt.Errorf("platform %q does not have a service adapter", u.OS)
}

func (u *UnsupportedAdapter) IsRunning() (bool, error) {
	return false, fmt.Errorf("platform %q does not have a service adapter", u.OS)
}
