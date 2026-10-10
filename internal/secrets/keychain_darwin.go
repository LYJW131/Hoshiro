package secrets

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Keychain access through Security.framework, loaded with purego (no cgo).
var (
	secOnce sync.Once
	secErr  error

	secFind   func(kc uintptr, sl uint32, s *byte, al uint32, a *byte, pl *uint32, pd *unsafe.Pointer, item *uintptr) int32
	secAdd    func(kc uintptr, sl uint32, s *byte, al uint32, a *byte, pl uint32, pd *byte, item *uintptr) int32
	secModify func(item uintptr, attrs uintptr, l uint32, d *byte) int32
	secDelete func(item uintptr) int32
	secFree   func(attrs uintptr, d unsafe.Pointer) int32
	cfRelease func(uintptr)
)

const errSecItemNotFound = -25300

func loadSecurity() error {
	secOnce.Do(func() {
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_GLOBAL)
		if err != nil {
			secErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
		if err != nil {
			secErr = err
			return
		}
		purego.RegisterLibFunc(&secFind, sec, "SecKeychainFindGenericPassword")
		purego.RegisterLibFunc(&secAdd, sec, "SecKeychainAddGenericPassword")
		purego.RegisterLibFunc(&secModify, sec, "SecKeychainItemModifyAttributesAndData")
		purego.RegisterLibFunc(&secDelete, sec, "SecKeychainItemDelete")
		purego.RegisterLibFunc(&secFree, sec, "SecKeychainItemFreeContent")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	})
	return secErr
}

// Keychain is a Store for one generic password item of the login keychain.
type Keychain struct{ Service string }

func System() Store { return Keychain{Service} }
func Legacy() Store { return Keychain{LegacyService} }

func bytesOf(s string) (*byte, uint32) {
	if s == "" {
		return nil, 0
	}
	b := []byte(s)
	return &b[0], uint32(len(b))
}

func (k Keychain) find(withData bool) (string, uintptr, error) {
	if err := loadSecurity(); err != nil {
		return "", 0, err
	}
	sp, sl := bytesOf(k.Service)
	ap, al := bytesOf(Account)
	var n uint32
	var data unsafe.Pointer
	var item uintptr
	var pl *uint32
	var pd *unsafe.Pointer
	if withData {
		pl, pd = &n, &data
	}
	st := secFind(0, sl, sp, al, ap, pl, pd, &item)
	if st == errSecItemNotFound {
		return "", 0, ErrNotFound
	}
	if st != 0 {
		return "", 0, fmt.Errorf("keychain error %d", st)
	}
	var out string
	if withData && data != nil {
		out = string(unsafe.Slice((*byte)(data), n))
		secFree(0, data)
	}
	return out, item, nil
}

func (k Keychain) Get() (string, error) {
	s, item, err := k.find(true)
	if item != 0 {
		cfRelease(item)
	}
	return s, err
}

func (k Keychain) Set(blob string) error {
	_, item, err := k.find(false)
	dp, dl := bytesOf(blob)
	if err == nil {
		defer cfRelease(item)
		if st := secModify(item, 0, dl, dp); st != 0 {
			return fmt.Errorf("keychain error %d", st)
		}
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	sp, sl := bytesOf(k.Service)
	ap, al := bytesOf(Account)
	if st := secAdd(0, sl, sp, al, ap, dl, dp, nil); st != 0 {
		return fmt.Errorf("keychain error %d", st)
	}
	return nil
}

func (k Keychain) Delete() error {
	_, item, err := k.find(false)
	if err != nil {
		return err
	}
	defer cfRelease(item)
	if st := secDelete(item); st != 0 {
		return fmt.Errorf("keychain error %d", st)
	}
	return nil
}
