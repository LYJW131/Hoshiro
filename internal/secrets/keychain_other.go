//go:build !darwin

package secrets

import "errors"

type unsupported struct{}

func (unsupported) Get() (string, error) {
	return "", errors.New("no credential store on this platform")
}
func (unsupported) Set(string) error { return errors.New("no credential store on this platform") }
func (unsupported) Delete() error    { return errors.New("no credential store on this platform") }

// System returns the platform credential store; on this platform there is none,
// so secrets stay in settings.json (0600), as the Rust app falls back to.
func System() Store { return unsupported{} }
func Legacy() Store { return nil }
