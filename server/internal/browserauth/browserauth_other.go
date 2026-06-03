//go:build !windows

package browserauth

import "errors"

func decryptDPAPI(_ []byte) ([]byte, error) {
	return nil, errors.New("browser auth extraction is only supported on Windows")
}

func setUserEnvVar(_, _ string) error {
	return errors.New("browser auth extraction is only supported on Windows")
}
