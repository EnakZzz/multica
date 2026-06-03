//go:build windows

package browserauth

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	crypt32            = windows.NewLazySystemDLL("Crypt32.dll")
	kernel32           = windows.NewLazySystemDLL("Kernel32.dll")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func decryptDPAPI(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	in := dataBlob{
		cbData: uint32(len(ciphertext)),
		pbData: &ciphertext[0],
	}
	var out dataBlob
	r1, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	plain := unsafe.Slice(out.pbData, out.cbData)
	return append([]byte(nil), plain...), nil
}

func setUserEnvVar(name, value string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(name, value); err != nil {
		return fmt.Errorf("write HKCU\\Environment: %w", err)
	}
	return nil
}
