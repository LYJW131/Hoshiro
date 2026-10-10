package secrets

import "testing"

func TestLegacyMigration(t *testing.T) {
	blob := `{"llm.openai.api_key":"x"}`
	legacy := &MemStore{Blob: &blob}
	cur := &MemStore{}
	v := NewVault(cur, legacy)
	if v.Stored()["llm.openai.api_key"] != "x" || legacy.Blob != nil {
		t.Fatal("legacy not migrated")
	}
}

func TestSyncSkipsWhenUnchanged(t *testing.T) {
	m := &MemStore{}
	v := NewVault(m, nil)
	want := Secrets{"a": "b"}
	if !v.Sync(want) || !v.Sync(want) || m.SetCalls != 1 {
		t.Fatalf("calls=%d", m.SetCalls)
	}
}

func TestBadBlobMakesUnavailable(t *testing.T) {
	b := "nope"
	v := NewVault(&MemStore{Blob: &b}, nil)
	if v.Available() || v.Sync(Secrets{"a": "b"}) {
		t.Fatal("should be unavailable")
	}
}
