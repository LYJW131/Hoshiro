// Package secrets keeps API credentials in the OS credential store as one
// JSON blob in one item, cached in memory, mirroring the Rust vault: the
// keychain is never touched on the hot dictation path and nothing here
// logs a secret value.
package secrets

import (
	"encoding/json"
	"errors"
	"log"
	"maps"
	"strings"
	"sync"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

const (
	Service = "cn.icybit.hoshiro"
	// LegacyService is the keychain service used before the bundle id changed.
	LegacyService = "dev.hoshiro.app"
	Account       = "credentials"
)

// ErrNotFound is returned by Store.Get when the item does not exist yet.
var ErrNotFound = errors.New("credential not found")

// Secrets maps a settings path to a secret value; only non-empty values are kept.
type Secrets map[string]string

func (s Secrets) Blob() string {
	if s == nil {
		return "{}"
	}
	b, _ := json.Marshal(map[string]string(s))
	return string(b)
}

func FromBlob(blob string) (Secrets, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		return nil, errors.New("keychain blob is not valid JSON")
	}
	out := Secrets{}
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// visit calls f for every secret field of s, the single source of truth for what is secret.
func visit(s *model.Settings, f func(key string, v *string)) {
	f("asr.mimo.api_key", &s.Asr.Mimo.APIKey)
	f("asr.cloudflare.api_key", &s.Asr.Cloudflare.APIKey)
	f("asr.azure.api_key", &s.Asr.Azure.APIKey)
	f("asr.google.api_key", &s.Asr.Google.APIKey)
	f("asr.google.service_account_json", &s.Asr.Google.ServiceAccountJSON)
	f("llm.gemini.api_key", &s.Llm.Gemini.APIKey)
	f("llm.anthropic.api_key", &s.Llm.Anthropic.APIKey)
	f("llm.openai.api_key", &s.Llm.OpenAI.APIKey)
}

// Extract collects the non-empty secrets of s.
func Extract(s model.Settings) Secrets {
	m := Secrets{}
	visit(&s, func(k string, v *string) {
		if strings.TrimSpace(*v) != "" {
			m[k] = *v
		}
	})
	return m
}

// PublicCopy returns s with every secret blanked, safe to write to settings.json.
func PublicCopy(s model.Settings) model.Settings {
	visit(&s, func(_ string, v *string) { *v = "" })
	return s
}

// Overlay fills blank secret fields of s from secrets; a value already in s wins.
func Overlay(s *model.Settings, secrets Secrets) {
	visit(s, func(k string, v *string) {
		if strings.TrimSpace(*v) == "" {
			if val, ok := secrets[k]; ok {
				*v = val
			}
		}
	})
}

// Store is one item of a credential store.
type Store interface {
	Get() (string, error) // ErrNotFound when missing
	Set(blob string) error
	Delete() error
}

type state int

const (
	uninit state = iota
	ready
	unavailable
)

// Vault is a credential store with an in-memory cache.
type Vault struct {
	store  Store
	legacy Store
	mu     sync.Mutex
	st     state
	cur    Secrets
}

func NewVault(store, legacy Store) *Vault { return &Vault{store: store, legacy: legacy} }

func (v *Vault) migrateLegacy() (string, bool) {
	if v.legacy == nil {
		return "", false
	}
	blob, err := v.legacy.Get()
	if err != nil {
		return "", false
	}
	if v.store.Set(blob) != nil {
		log.Print("credential store: migrating legacy item failed; will retry next launch")
		return "", false
	}
	if back, err := v.store.Get(); err != nil || back != blob {
		log.Print("credential store: migrating legacy item failed; will retry next launch")
		return "", false
	}
	if err := v.legacy.Delete(); err != nil {
		log.Printf("credential store: migrated, but could not remove legacy item (%v)", err)
	}
	return blob, true
}

func (v *Vault) init() {
	if v.st != uninit {
		return
	}
	blob, err := v.store.Get()
	if errors.Is(err, ErrNotFound) {
		if b, ok := v.migrateLegacy(); ok {
			blob, err = b, nil
		} else {
			v.st, v.cur = ready, Secrets{}
			return
		}
	}
	if err != nil {
		log.Printf("credential store unavailable (%v); falling back to settings.json", err)
		v.st = unavailable
		return
	}
	s, err := FromBlob(blob)
	if err != nil {
		log.Printf("credential store item unusable (%v); falling back to settings.json", err)
		v.st = unavailable
		return
	}
	v.st, v.cur = ready, s
}

// Stored returns the secrets held in the store (empty when unavailable).
func (v *Vault) Stored() Secrets {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.init()
	if v.st != ready {
		return Secrets{}
	}
	return maps.Clone(v.cur)
}

func (v *Vault) Available() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.init()
	return v.st == ready
}

// Sync makes the store hold exactly want, verified by reading it back.
func (v *Vault) Sync(want Secrets) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.init()
	if v.st != ready {
		return false
	}
	if maps.Equal(v.cur, want) {
		return true
	}
	if err := v.store.Set(want.Blob()); err != nil {
		log.Printf("credential store write failed (%v); keeping secrets in settings.json", err)
		return false
	}
	back, err := v.store.Get()
	if err == nil {
		if got, err := FromBlob(back); err == nil && maps.Equal(got, want) {
			v.cur = maps.Clone(want)
			return true
		}
	}
	log.Print("credential store write could not be verified; keeping secrets in settings.json")
	return false
}

// MemStore is an in-memory Store for tests.
type MemStore struct {
	Blob     *string
	Fail     bool
	SetCalls int
}

func (m *MemStore) Get() (string, error) {
	if m.Fail {
		return "", errors.New("locked")
	}
	if m.Blob == nil {
		return "", ErrNotFound
	}
	return *m.Blob, nil
}
func (m *MemStore) Set(b string) error {
	if m.Fail {
		return errors.New("locked")
	}
	m.SetCalls++
	m.Blob = &b
	return nil
}
func (m *MemStore) Delete() error { m.Blob = nil; return nil }
